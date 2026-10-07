// Package clinemigration implements an explicitly approved, offline migration.
// It never starts/stops workers, sends inference, edits API-key permissions,
// changes pricing or rewrites history. One account is migrated per transaction.
package clinemigration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const MaintenanceAcknowledgement = "ALL_WORKERS_STOPPED"
const PlanWarning = "Offline only. Account stays unschedulable. Existing API-key bindings and user permissions are not changed; confirm access to target groups separately. Account IDs, prices and usage history are preserved."

var ErrIneligible = errors.New("account is not an eligible explicit-host legacy Cline account")
var ErrSpecification = errors.New("migration specification is incomplete or incompatible")
var ErrConflict = errors.New("migration plan or rollback configuration changed; preview again")
var ErrMaintenance = errors.New("disable the account, drain requests and stop all workers before writing")
var ErrApproval = errors.New("exact plan approval and maintenance acknowledgement are required")

// GroupMove preserves each original binding's priority and allowed_models.
// It never copies a group's users/API keys or implicitly grants new access.
type GroupMove struct {
	From int64 `json:"from_group_id"`
	To   int64 `json:"to_group_id"`
}
type Spec struct {
	AccountID      int64             `json:"account_id"`
	Mode           string            `json:"mode"`
	AuthType       string            `json:"auth_type"`
	ModelMapping   map[string]string `json:"model_mapping"`
	ExcludedModels []string          `json:"excluded_models,omitempty"`
	GroupMoves     []GroupMove       `json:"group_moves"`
}
type Plan struct {
	Version      int    `json:"version"`
	Spec         Spec   `json:"spec"`
	BeforeDigest string `json:"before_digest"`
	Warning      string `json:"warning"`
	Approval     string `json:"approval"`
}
type binding struct {
	GroupID       int64           `json:"group_id"`
	Priority      int             `json:"priority"`
	AllowedModels json.RawMessage `json:"allowed_models"`
	CreatedAt     time.Time       `json:"created_at"`
}
type snapshot struct {
	AccountID               int64           `json:"account_id"`
	Platform                string          `json:"platform"`
	Type                    string          `json:"type"`
	Credentials             map[string]any  `json:"credentials"`
	Extra                   map[string]any  `json:"extra"`
	Schedulable             bool            `json:"schedulable"`
	RateLimitResetAt        *time.Time      `json:"rate_limit_reset_at"`
	ConfigurationDigest     string          `json:"configuration_digest"`
	Bindings                []binding       `json:"bindings"`
	Groups                  json.RawMessage `json:"groups"`
	APIKeyPermissionsDigest string          `json:"api_key_permissions_digest"`
	UserPermissionsDigest   string          `json:"user_permissions_digest"`
}

func digest(v any) (string, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
func planDigest(plan Plan) (string, error) { plan.Approval = ""; return digest(plan) }

func validateSpec(spec Spec) error {
	if spec.AccountID <= 0 || len(spec.ModelMapping) == 0 || len(spec.ModelMapping) > 1000 || len(spec.ExcludedModels) > 1000 || len(spec.GroupMoves) > 1000 {
		return ErrSpecification
	}
	if spec.Mode != cline.ModePass && spec.Mode != cline.ModePayG && spec.Mode != cline.ModeFree {
		return ErrSpecification
	}
	if spec.AuthType != cline.AuthAPIKey && spec.AuthType != cline.AuthAccountToken {
		return ErrSpecification
	}
	for public, upstream := range spec.ModelMapping {
		if !cline.ValidModelID(public) || !cline.ValidModelID(upstream) {
			return ErrSpecification
		}
		if spec.Mode != cline.ModeFree && cline.ValidateUpstreamModel(spec.Mode, upstream) != nil {
			return ErrSpecification
		}
	}
	excluded := map[string]bool{}
	for _, public := range spec.ExcludedModels {
		if spec.Mode != cline.ModePass || !cline.ValidModelID(public) || excluded[public] {
			return ErrSpecification
		}
		if _, kept := spec.ModelMapping[public]; kept {
			return ErrSpecification
		}
		excluded[public] = true
	}
	from, to := map[int64]bool{}, map[int64]bool{}
	for _, move := range spec.GroupMoves {
		if move.From <= 0 || move.To <= 0 || from[move.From] || to[move.To] {
			return ErrSpecification
		}
		from[move.From], to[move.To] = true, true
	}
	return nil
}

// Empty/default DeepSeek base URLs must not be interpreted as Cline origins.
func eligible(before *snapshot) bool {
	if before.Platform != service.PlatformDeepseek || before.Type != service.AccountTypeAPIKey {
		return false
	}
	raw, ok := before.Credentials["base_url"].(string)
	return ok && strings.TrimSpace(raw) != "" && cline.IsOfficialBase(raw)
}

func targetCredentials(before *snapshot, spec Spec) (map[string]any, error) {
	if !eligible(before) {
		return nil, ErrIneligible
	}
	body, err := json.Marshal(before.Credentials["model_mapping"])
	if err != nil {
		return nil, ErrSpecification
	}
	var original map[string]string
	if json.Unmarshal(body, &original) != nil || len(original) != len(spec.ModelMapping)+len(spec.ExcludedModels) {
		return nil, ErrSpecification
	}
	// Exclusions are explicit in the approved plan. Only incompatible legacy
	// aliases may be removed from Pass; the journal retains the full original.
	excluded := map[string]bool{}
	for _, key := range spec.ExcludedModels {
		upstream, exists := original[key]
		if !exists || spec.Mode != cline.ModePass || excluded[key] || !cline.ValidModelID(upstream) || cline.ValidateUpstreamModel(cline.ModePass, upstream) == nil {
			return nil, ErrSpecification
		}
		excluded[key] = true
	}
	for key := range original {
		_, kept := spec.ModelMapping[key]
		if !cline.ValidModelID(key) || (kept == excluded[key]) {
			return nil, ErrSpecification
		}
	}
	credentials := make(map[string]any, len(before.Credentials))
	for key, value := range before.Credentials {
		credentials[key] = value
	}
	for _, key := range []string{"pool_mode", "cline_paid_fallback", "cline_free_api_enabled", "openai_passthrough", "api_base_urls"} {
		delete(credentials, key)
	}
	credentials["account_mode"], credentials["cline_auth_type"], credentials["api_protocol"] = spec.Mode, spec.AuthType, "chat_completions"
	credentials["base_url"], credentials["model_mapping"] = cline.BaseURL, spec.ModelMapping
	if service.NormalizeClineCredentials(service.PlatformCline, service.AccountTypeAPIKey, credentials) != nil {
		return nil, ErrSpecification
	}
	return credentials, nil
}

func capture(ctx context.Context, tx *sql.Tx, spec Spec) (*snapshot, error) {
	s := &snapshot{AccountID: spec.AccountID, Bindings: []binding{}}
	var credentials, extra []byte
	var reset sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT platform,type,credentials,COALESCE(extra,'{}'::jsonb),schedulable,rate_limit_reset_at,
 md5((to_jsonb(a)-'updated_at'-'last_used_at')::text)
 FROM accounts a WHERE id=$1 AND deleted_at IS NULL`, spec.AccountID).Scan(&s.Platform, &s.Type, &credentials, &extra, &s.Schedulable, &reset, &s.ConfigurationDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIneligible
	}
	if err != nil {
		return nil, err
	}
	if decodeExact(credentials, &s.Credentials) != nil || decodeExact(extra, &s.Extra) != nil {
		return nil, ErrSpecification
	}
	if reset.Valid {
		s.RateLimitResetAt = &reset.Time
	}
	rows, err := tx.QueryContext(ctx, `SELECT group_id,priority,allowed_models,created_at FROM account_groups WHERE account_id=$1 ORDER BY group_id`, spec.AccountID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b binding
		var models []byte
		if err := rows.Scan(&b.GroupID, &b.Priority, &models, &b.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(models) == 0 {
			models = []byte("null")
		}
		b.AllowedModels = models
		s.Bindings = append(s.Bindings, b)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	groupSet := map[int64]bool{}
	for _, b := range s.Bindings {
		groupSet[b.GroupID] = true
	}
	for _, move := range spec.GroupMoves {
		groupSet[move.From], groupSet[move.To] = true, true
	}
	ids := make([]int64, 0, len(groupSet))
	for id := range groupSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(g) ORDER BY id),'[]'::jsonb) FROM groups g WHERE id=ANY($1) AND deleted_at IS NULL`, pq.Array(ids)).Scan(&s.Groups)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `SELECT md5(COALESCE(jsonb_agg(to_jsonb(k) ORDER BY id),'[]'::jsonb)::text) FROM api_keys k WHERE group_id=ANY($1)`, pq.Array(ids)).Scan(&s.APIKeyPermissionsDigest)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `SELECT md5(COALESCE(jsonb_agg(to_jsonb(g) ORDER BY user_id,group_id),'[]'::jsonb)::text) FROM user_allowed_groups g WHERE group_id=ANY($1)`, pq.Array(ids)).Scan(&s.UserPermissionsDigest)
	return s, err
}

func targetBindings(before *snapshot, spec Spec) ([]binding, error) {
	if len(before.Bindings) != len(spec.GroupMoves) {
		return nil, ErrSpecification
	}
	var groups []struct {
		ID       int64  `json:"id"`
		Platform string `json:"platform"`
	}
	if json.Unmarshal(before.Groups, &groups) != nil {
		return nil, ErrSpecification
	}
	platforms := map[int64]string{}
	for _, g := range groups {
		platforms[g.ID] = g.Platform
	}
	moves := map[int64]int64{}
	for _, m := range spec.GroupMoves {
		moves[m.From] = m.To
	}
	out := make([]binding, 0, len(before.Bindings))
	for _, b := range before.Bindings {
		target, ok := moves[b.GroupID]
		if !ok || (platforms[target] != service.PlatformCline && platforms[target] != service.PlatformComposite) {
			return nil, ErrSpecification
		}
		b.GroupID = target
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GroupID < out[j].GroupID })
	return out, nil
}

// Preview uses a consistent read-only transaction and returns no credentials.
func Preview(ctx context.Context, db *sql.DB, spec Spec) (*Plan, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	before, err := capture(ctx, tx, spec)
	if err != nil {
		return nil, err
	}
	if _, err = targetCredentials(before, spec); err != nil {
		return nil, err
	}
	if _, err = targetBindings(before, spec); err != nil {
		return nil, err
	}
	beforeDigest, err := digest(before)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Version: 1, Spec: spec, BeforeDigest: beforeDigest, Warning: PlanWarning}
	plan.Approval, err = planDigest(*plan)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return plan, nil
}

func beginMaintenance(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*sql.Tx, error) { _ = tx.Rollback(); return nil, err }
	if _, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '2s'"); err != nil {
		return fail(err)
	}
	var locked bool
	if err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(731104426)").Scan(&locked); err != nil {
		return fail(err)
	}
	if !locked {
		return fail(ErrMaintenance)
	}
	var others int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend'`).Scan(&others); err != nil {
		return fail(err)
	}
	if others != 0 {
		return fail(ErrMaintenance)
	}
	_, err = tx.ExecContext(ctx, `LOCK TABLE accounts,groups,account_groups,api_keys,user_allowed_groups,cline_migration_journal IN SHARE ROW EXCLUSIVE MODE NOWAIT`)
	if err != nil {
		return fail(ErrMaintenance)
	}
	return tx, nil
}

func verifyApproval(plan Plan, approval, maintenance string) error {
	if plan.Version != 1 || plan.Warning != PlanWarning || validateSpec(plan.Spec) != nil {
		return ErrApproval
	}
	want, err := planDigest(plan)
	if err != nil || want != plan.Approval || approval != want || maintenance != MaintenanceAcknowledgement {
		return ErrApproval
	}
	return nil
}

func replaceBindings(ctx context.Context, tx *sql.Tx, id int64, bindings []binding) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM account_groups WHERE account_id=$1", id); err != nil {
		return err
	}
	for _, b := range bindings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority,allowed_models,created_at) VALUES($1,$2,$3,$4::jsonb,$5)`, id, b.GroupID, b.Priority, string(b.AllowedModels), b.CreatedAt); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id) VALUES($1,$2)`, service.SchedulerOutboxEventAccountChanged, id)
	return err
}

// carryCooldown retains the longest well-formed legacy account/model cooldown.
// It never treats migration as a reset. Malformed time data requires operator
// correction instead of quietly dropping a potentially active restriction.
func carryCooldown(before *snapshot) (*time.Time, error) {
	latest := before.RateLimitResetAt
	raw, exists := before.Extra["model_rate_limits"]
	if !exists || raw == nil {
		return latest, nil
	}
	limits, ok := raw.(map[string]any)
	if !ok {
		return nil, ErrSpecification
	}
	for _, rawLimit := range limits {
		limit, ok := rawLimit.(map[string]any)
		if !ok {
			return nil, ErrSpecification
		}
		if value, exists := limit["reset_unix"]; exists {
			number, ok := value.(json.Number)
			if !ok {
				return nil, ErrSpecification
			}
			seconds, err := number.Int64()
			if err != nil {
				return nil, ErrSpecification
			}
			at := time.Unix(seconds, 0).UTC()
			if latest == nil || at.After(*latest) {
				latest = &at
			}
		}
		if value, exists := limit["rate_limit_reset_at"]; exists {
			text, ok := value.(string)
			if !ok {
				return nil, ErrSpecification
			}
			at, err := time.Parse(time.RFC3339, text)
			if err != nil {
				return nil, ErrSpecification
			}
			if latest == nil || at.After(*latest) {
				latest = &at
			}
		}
	}
	return latest, nil
}

func Apply(ctx context.Context, db *sql.DB, plan Plan, approval, maintenance string) error {
	if err := verifyApproval(plan, approval, maintenance); err != nil {
		return err
	}
	tx, err := beginMaintenance(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var rolledBack sql.NullTime
	err = tx.QueryRowContext(ctx, "SELECT rolled_back_at FROM cline_migration_journal WHERE plan_id=$1", plan.Approval).Scan(&rolledBack)
	if err == nil {
		if rolledBack.Valid {
			return ErrConflict
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	before, err := capture(ctx, tx, plan.Spec)
	if err != nil {
		return err
	}
	if before.Schedulable {
		return ErrMaintenance
	}
	beforeDigest, err := digest(before)
	if err != nil {
		return err
	}
	if beforeDigest != plan.BeforeDigest {
		return ErrConflict
	}
	credentials, err := targetCredentials(before, plan.Spec)
	if err != nil {
		return err
	}
	bindings, err := targetBindings(before, plan.Spec)
	if err != nil {
		return err
	}
	reset, err := carryCooldown(before)
	if err != nil {
		return err
	}
	extra := make(map[string]any, len(before.Extra))
	for k, v := range before.Extra {
		if k != service.ClineStateExtraKey {
			extra[k] = v
		}
	}
	credentialsJSON, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	extraJSON, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET platform='cline',credentials=$1::jsonb,extra=$2::jsonb,schedulable=false,rate_limit_reset_at=GREATEST(rate_limit_reset_at,$3::timestamptz),updated_at=NOW() WHERE id=$4 AND deleted_at IS NULL`, string(credentialsJSON), string(extraJSON), reset, plan.Spec.AccountID)
	if err != nil {
		return err
	}
	if err = replaceBindings(ctx, tx, plan.Spec.AccountID, bindings); err != nil {
		return err
	}
	after, err := capture(ctx, tx, plan.Spec)
	if err != nil {
		return err
	}
	afterDigest, err := digest(after)
	if err != nil {
		return err
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return err
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cline_migration_journal(plan_id,account_id,plan,before_state,after_digest) VALUES($1,$2,$3::jsonb,$4::jsonb,$5)`, plan.Approval, plan.Spec.AccountID, string(planJSON), string(beforeJSON), afterDigest)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func Rollback(ctx context.Context, db *sql.DB, plan Plan, approval, maintenance string) error {
	if err := verifyApproval(plan, approval, maintenance); err != nil {
		return err
	}
	tx, err := beginMaintenance(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var beforeJSON []byte
	var afterDigest string
	var rolledBack sql.NullTime
	err = tx.QueryRowContext(ctx, "SELECT before_state,after_digest,rolled_back_at FROM cline_migration_journal WHERE plan_id=$1 AND account_id=$2 FOR UPDATE", plan.Approval, plan.Spec.AccountID).Scan(&beforeJSON, &afterDigest, &rolledBack)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if rolledBack.Valid {
		return tx.Commit()
	}
	current, err := capture(ctx, tx, plan.Spec)
	if err != nil {
		return err
	}
	if current.Schedulable {
		return ErrMaintenance
	}
	actual, err := digest(current)
	if err != nil {
		return err
	}
	if actual != afterDigest {
		return ErrConflict
	}
	var before snapshot
	if decodeExact(beforeJSON, &before) != nil || before.AccountID != plan.Spec.AccountID {
		return ErrConflict
	}
	credentials, err := json.Marshal(before.Credentials)
	if err != nil {
		return err
	}
	extra, err := json.Marshal(before.Extra)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET platform=$1,credentials=$2::jsonb,extra=$3::jsonb,schedulable=false,rate_limit_reset_at=GREATEST(rate_limit_reset_at,$4::timestamptz),updated_at=NOW() WHERE id=$5 AND deleted_at IS NULL`, before.Platform, string(credentials), string(extra), before.RateLimitResetAt, plan.Spec.AccountID)
	if err != nil {
		return err
	}
	if err = replaceBindings(ctx, tx, plan.Spec.AccountID, before.Bindings); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE cline_migration_journal SET rolled_back_at=NOW() WHERE plan_id=$1", plan.Approval)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SafeError omits SQL diagnostics, DSNs, credentials and row snapshots.
func SafeError(err error) string {
	for _, known := range []error{ErrIneligible, ErrSpecification, ErrConflict, ErrMaintenance, ErrApproval} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "migration database operation failed; no sensitive diagnostics are printed"
}

func ValidatePlan(plan Plan) error {
	want, err := planDigest(plan)
	if err != nil || plan.Version != 1 || want != plan.Approval || plan.Warning != PlanWarning {
		return ErrApproval
	}
	return validateSpec(plan.Spec)
}

func (plan Plan) Summary() string {
	return fmt.Sprintf("account=%d mode=%s group_moves=%d approval=%s", plan.Spec.AccountID, plan.Spec.Mode, len(plan.Spec.GroupMoves), plan.Approval)
}

func decodeExact(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return decoder.Decode(target)
}
