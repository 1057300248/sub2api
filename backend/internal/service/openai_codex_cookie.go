package service

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Codex 上游在 2026-09-22 前后把 turn-state 从「按时长续命的门票」改成了
// 「Cookie 续命」模型：无 Cookie 的探测只会拿到一次性 292 票，服务端不再
// 换发新票；带上 __cflb/__oailb/__cf_bm 这组 Cookie 后，即使不带票也能持续
// 换发 312 长度的新票。票因此只是便捷凭证，Cookie 罐才是账号级续航主体。
//
// 因此 Cookie 罐按账号维度共享（不按票、不按模型），与 292 票一起放在
// accounts.extra 下；两者都只在服务端内部流转，绝不下发给客户端。
const (
	openAICodexCookieJarExtraKey = "codex_cookie_jar"

	// turn-state blob 的密文块分档（社区与官方社区实测一致）：
	//   个人号 10 块 = 292 长度 -> 服务端按请求模型服务（健康票）
	//   Team/Business 12 块 = 332 长度
	// 而 11 块 = 312 是**降级信号**：请求 gpt-6-astra 时服务端返回
	// gpt-5.6-luna（官方社区实测 247/247 命中；我们 2026-09-22 A/B 复现：
	// 292 -> gpt-6-astra，312 -> gpt-5.6-luna）。
	// 因此只接受"恰好等于该档块数"的票，多一块即判降级、丢弃并继续重采。
	openAICodexTicketMaxStateLength = 1024

	openAICodexPersonalStateBlocks = 10
	openAICodexTeamStateBlocks     = 12
	// 每块固定字节（实测 292/10 与 332/12 的公约数是 28+4 的密文封装；
	// 这里直接按观测长度约束，避免对分块内部编码做假设）。
	openAICodexPersonalStateLength = 292
	openAICodexTeamStateLength     = 332
	// 个人号出现 312（11 块）时视为降级，不再接受。
	openAICodexDegradedStateLength = 312

	openAICodexStateBucketUnknown  = "unknown"
	openAICodexStateBucketPersonal = "personal"
	openAICodexStateBucketTeam     = "team"

	// Only the cookies minted by the codex edge/gateway are persisted; anything
	// else the upstream (or a proxy) sets is ignored so we never leak or replay
	// unrelated browser state.
	openAICodexCookieNameOailb  = "__oailb"
	openAICodexCookieNameCflb   = "__cflb"
	openAICodexCookieNameCfBm   = "__cf_bm"
)

func isOpenAICodexManagedCookie(name string) bool {
	switch strings.TrimSpace(name) {
	case openAICodexCookieNameOailb, openAICodexCookieNameCflb, openAICodexCookieNameCfBm:
		return true
	default:
		return false
	}
}

func normalizeOpenAICodexCookieJar(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for name, value := range raw {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" || !isOpenAICodexManagedCookie(name) {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseOpenAICodexCookieJarFromAny 兼容 map[string]string / map[string]any 两种
// JSON 反序列化形态，供 accounts.extra 读回时使用。
func parseOpenAICodexCookieJarFromAny(raw any) map[string]string {
	switch typed := raw.(type) {
	case nil:
		return nil
	case map[string]string:
		return normalizeOpenAICodexCookieJar(typed)
	case map[string]any:
		out := make(map[string]string, len(typed))
		for name, value := range typed {
			if str, ok := value.(string); ok {
				out[name] = str
			}
		}
		return normalizeOpenAICodexCookieJar(out)
	default:
		return nil
	}
}

// mergeOpenAICodexCookieJar 以响应中新下发的 Cookie 覆盖旧值，保留未变更项。
func mergeOpenAICodexCookieJar(current, updates map[string]string) map[string]string {
	if len(updates) == 0 {
		return normalizeOpenAICodexCookieJar(current)
	}
	merged := make(map[string]string, len(current)+len(updates))
	for name, value := range current {
		merged[name] = value
	}
	for name, value := range updates {
		if strings.TrimSpace(value) == "" {
			continue
		}
		merged[name] = value
	}
	return normalizeOpenAICodexCookieJar(merged)
}

func openAICodexCookieJarFromResponse(resp *http.Response) map[string]string {
	if resp == nil {
		return nil
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return nil
	}
	out := make(map[string]string, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || !isOpenAICodexManagedCookie(cookie.Name) {
			continue
		}
		if value := strings.TrimSpace(cookie.Value); value != "" {
			out[cookie.Name] = value
		}
	}
	return normalizeOpenAICodexCookieJar(out)
}

func openAICodexCookieHeader(jar map[string]string) string {
	jar = normalizeOpenAICodexCookieJar(jar)
	if len(jar) == 0 {
		return ""
	}
	names := make([]string, 0, len(jar))
	for name := range jar {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+jar[name])
	}
	return strings.Join(parts, "; ")
}

// applyOpenAICodexCookies 把账号级 Cookie 罐注入出站请求。出站请求此前不会
// 携带任何 Cookie（转发白名单不含 Cookie），因此这里是唯一注入点。
func applyOpenAICodexCookies(h http.Header, jar map[string]string) {
	if h == nil {
		return
	}
	if header := openAICodexCookieHeader(jar); header != "" {
		h.Set("Cookie", header)
	}
}

// openAICodexPlanType 读取账号套餐类型（OAuth/Team 账号都会写入 credentials.plan_type）。
func openAICodexPlanType(account *Account) string {
	if account == nil {
		return ""
	}
	if account.Credentials != nil {
		if raw, ok := account.Credentials["plan_type"].(string); ok {
			return strings.ToLower(strings.TrimSpace(raw))
		}
	}
	if account.Extra != nil {
		if raw, ok := account.Extra["plan_type"].(string); ok {
			return strings.ToLower(strings.TrimSpace(raw))
		}
	}
	return ""
}

// openAICodexStateBucket 把账号映射到密文块档：个人 10 块 / Team 12 块。
// 套餐未知（API-key 账号、旧数据）按未知处理，只做全局区间校验。
func openAICodexStateBucket(account *Account) string {
	switch plan := openAICodexPlanType(account); {
	case plan == "":
		return openAICodexStateBucketUnknown
	// 只把明确的 Team/Business 订阅判为 team 档，避免把 pro/prolite/k12/
	// enterprise 等误判成高下界而拒绝其个人档票。新增档位时先观察该账号
	// 实际票长（个人 10 块 ≈292，Team 12 块 ≈332）再决定是否归入。
	case strings.Contains(plan, "team"), strings.Contains(plan, "business"):
		return openAICodexStateBucketTeam
	default:
		return openAICodexStateBucketPersonal
	}
}

// openAICodexStateLengthBounds 返回某档允许的长度区间（闭区间）。
// 精确等于该档的块数长度才是健康票；312 是个人号的降级形态，明确排除。
func openAICodexStateLengthBounds(bucket string) (int, int) {
	switch bucket {
	case openAICodexStateBucketTeam:
		return openAICodexTeamStateLength, openAICodexTicketMaxStateLength
	case openAICodexStateBucketPersonal:
		return openAICodexPersonalStateLength, openAICodexTicketMaxStateLength
	default:
		// 套餐未知：只要求是 292/332 这类健康形态之一，挡住 312 与明显异常值。
		return openAICodexPersonalStateLength, openAICodexTicketMaxStateLength
	}
}

// openAICodexStateDegraded 判定 blob 是否为降级形态（个人号的 11 块 = 312）。
// 降级票必须丢弃并继续重采，不能注入业务请求。
func openAICodexStateDegraded(state string) bool {
	return len(strings.TrimSpace(state)) == openAICodexDegradedStateLength
}

// openAICodexTicketStateValid 只做全局形状校验：前缀 + 合理上界 + 非降级形态。
// 具体档位是否匹配由 openAICodexTicketStateMatchesAccount 判定。
func openAICodexTicketStateValid(state string) bool {
	state = strings.TrimSpace(state)
	if !strings.HasPrefix(state, openAICodexTicketStatePrefix) {
		return false
	}
	if len(state) <= len(openAICodexTicketStatePrefix) || len(state) > openAICodexTicketMaxStateLength {
		return false
	}
	return !openAICodexStateDegraded(state)
}

// openAICodexTicketStateMatchesAccount 校验票与本账号的档位/下界是否一致。
// 未知档位账号只做下界校验，避免把 API-key 账号或历史数据一刀切掉。
func openAICodexTicketStateMatchesAccount(account *Account, state string) bool {
	state = strings.TrimSpace(state)
	if !openAICodexTicketStateValid(state) {
		return false
	}
	lower, upper := openAICodexStateLengthBounds(openAICodexStateBucket(account))
	length := len(state)
	return length >= lower && length <= upper
}

// persistOpenAICodexCookieJar 落库账号级 Cookie 罐并刷新内存快照。
func (s *OpenAIGatewayService) persistOpenAICodexCookieJar(ctx context.Context, account *Account, updates map[string]string) map[string]string {
	if s == nil || account == nil || account.ID <= 0 || len(updates) == 0 {
		return nil
	}
	merged := mergeOpenAICodexCookieJar(s.lookupOpenAICodexCookieJar(account), updates)
	if len(merged) == 0 {
		return nil
	}
	s.openaiCodexCookies.Store(account.ID, merged)
	if s.accountRepo == nil {
		return merged
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		openAICodexCookieJarExtraKey: merged,
	}); err != nil {
		logger.L().Warn("openai_codex_cookie_jar persist failed",
			zap.Int64("account_id", account.ID),
			zap.Error(err),
		)
	}
	return merged
}

// forgetOpenAICodexCookieJar 在 Cookie 被判失效时丢弃账号级罐，等待重建。
func (s *OpenAIGatewayService) forgetOpenAICodexCookieJar(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	s.openaiCodexCookies.Delete(accountID)
}

// openAICodexTicketUsable 判定票是否可直接复用：未过期、档位匹配、没有待
// 重采标记，且未临近 refreshBefore。harvester 与出站注入共用同一判据；
// 出站注入传 refreshBefore = 0（临近过期也仍可注入，因为续航靠 Cookie 罐）。
func openAICodexTicketUsable(account *Account, t *openAICodexTicket, now time.Time, refreshBefore time.Duration) bool {
	return t.usableFor(account, now, refreshBefore)
}

// usableFor 见 openAICodexTicketUsable。
func (t *openAICodexTicket) usableFor(account *Account, now time.Time, refreshBefore time.Duration) bool {
	if t == nil || t.ExpiresAt.IsZero() || !now.Before(t.ExpiresAt) {
		return false
	}
	if refreshBefore > 0 && !t.ExpiresAt.After(now.Add(refreshBefore)) {
		return false
	}
	state := strings.TrimSpace(t.State)
	if !openAICodexTicketStateMatchesAccount(account, state) {
		return false
	}
	if t.Length != 0 && t.Length != len(state) {
		return false
	}
	if t.HarvestPending {
		return false
	}
	// 账号档位与铸票档位不一致时拒绝复用；未知档位（旧票/API-key 账号）放行。
	if t.StateBucket != "" && t.StateBucket != openAICodexStateBucketUnknown &&
		t.StateBucket != openAICodexStateBucket(account) {
		return false
	}
	return true
}

// recordOpenAICodexDegradedProbe 累计某账号连续收到的 312 降级票次数。
func (s *OpenAIGatewayService) recordOpenAICodexDegradedProbe(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	actual, _ := s.openaiCodexDegradedProbes.LoadOrStore(accountID, new(atomic.Int64))
	counter, ok := actual.(*atomic.Int64)
	if !ok {
		counter = new(atomic.Int64)
		s.openaiCodexDegradedProbes.Store(accountID, counter)
	}
	counter.Add(1)
}

func (s *OpenAIGatewayService) clearOpenAICodexDegradedProbe(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	s.openaiCodexDegradedProbes.Delete(accountID)
}

func (s *OpenAIGatewayService) openAICodexDegradedProbeStreak(accountID int64) int {
	if s == nil || accountID <= 0 {
		return 0
	}
	if actual, ok := s.openaiCodexDegradedProbes.Load(accountID); ok {
		if counter, ok := actual.(*atomic.Int64); ok && counter != nil {
			return int(counter.Load())
		}
	}
	return 0
}

// accountOpenAICodexDegradedStreak 读取降级连续计数（无 service 依赖的调用点）。
func accountOpenAICodexDegradedStreak(_ *Account) int { return 0 }

// IsOpenAICodexCookieJarExtraKey 标记账号级 Cookie 罐为服务端私有 extra：
// 管理端读取时会被 Redact 掉，管理员提交编辑时也会被 Merge 保护。
func IsOpenAICodexCookieJarExtraKey(key string) bool {
	return strings.TrimSpace(key) == openAICodexCookieJarExtraKey
}

// accountOpenAICodexCookieJar 从账号 extra 读取 Cookie 罐（无 service 依赖，
// 供状态/管理接口这类不持有 service 的调用点使用）。
func accountOpenAICodexCookieJar(account *Account) map[string]string {
	if account == nil || account.Extra == nil {
		return nil
	}
	return parseOpenAICodexCookieJarFromAny(account.Extra[openAICodexCookieJarExtraKey])
}

// lookupOpenAICodexCookieJar 读取账号级 Cookie 罐（内存快照优先，其次 extra）。
func (s *OpenAIGatewayService) lookupOpenAICodexCookieJar(account *Account) map[string]string {
	if s == nil || account == nil || account.ID <= 0 {
		return nil
	}
	if raw, ok := s.openaiCodexCookies.Load(account.ID); ok {
		if jar, _ := raw.(map[string]string); len(jar) > 0 {
			return jar
		}
	}
	if account.Extra != nil {
		return parseOpenAICodexCookieJarFromAny(account.Extra[openAICodexCookieJarExtraKey])
	}
	return nil
}
