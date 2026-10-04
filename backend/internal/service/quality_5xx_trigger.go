package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const quality5xxSource = "upstream_5xx"
const quality5xxPendingKey = "quality:5xx:pending"
const quality5xxTempUnschedReason = "quality_5xx"
const quality5xxTempUnschedDuration = 10 * time.Minute

type qualityProbeContextKey struct{}

// Redis bridges request-serving replicas and the existing singleton test worker.
// Only account IDs enter this bounded, coalescing queue; never upstream bodies.
type quality5xxTrigger struct {
	redis      *redis.Client
	ownsClient bool
}

func newQuality5xxTrigger(shared *redis.Client) *quality5xxTrigger {
	if shared == nil {
		return nil
	}
	// Same Redis database and credentials, separate tiny pool so stalled signal
	// delivery cannot hold a user request behind an unrelated shared-pool wait.
	opts := *shared.Options()
	opts.ContextTimeoutEnabled = true
	opts.ReadTimeout = 150 * time.Millisecond
	opts.WriteTimeout = 150 * time.Millisecond
	opts.DialTimeout = 150 * time.Millisecond
	opts.PoolTimeout = 150 * time.Millisecond
	opts.MaxRetries = -1
	opts.PoolSize = 4
	opts.MinIdleConns = 0
	return &quality5xxTrigger{redis: redis.NewClient(&opts), ownsClient: true}
}

var queueQuality5xx = redis.NewScript(`
 -- Every 5xx starts a newer episode. Replacing the score fences an older
 -- probe's completion cleanup and guarantees the signal is retried after it.
 redis.call('ZADD', KEYS[1], ARGV[2], ARGV[1])
 redis.call('EXPIRE', KEYS[1], 1200)
 return 1`)

func (s *quality5xxTrigger) Observe(ctx context.Context, account *Account, status int) {
	if s == nil || s.redis == nil || account == nil || account.ID <= 0 || account.Type != AccountTypeOAuth || status < 500 || status > 599 || ctx.Value(qualityProbeContextKey{}) != nil {
		return
	}
	enqueueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 150*time.Millisecond)
	defer cancel()
	id := strconv.FormatInt(account.ID, 10)
	if _, err := queueQuality5xx.Run(enqueueCtx, s.redis, []string{quality5xxPendingKey}, id, time.Now().UnixMilli()).Result(); err != nil {
		slog.Warn("quality 5xx trigger could not be queued", "account_id", account.ID)
	}
}

func (s *ScheduledTestRunnerService) startQualityTriggers() {
	if s.qualityTrigger == nil || s.qualityTrigger.redis == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.triggerCancel = cancel
	s.triggerWG.Add(1)
	go func() {
		defer s.triggerWG.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var active sync.Map
		slots := make(chan struct{}, scheduledTestDefaultMaxWorkers)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			scanCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			rdb := s.qualityTrigger.redis
			_ = rdb.ZRemRangeByScore(scanCtx, quality5xxPendingKey, "-inf", strconv.FormatInt(time.Now().Add(-20*time.Minute).UnixMilli(), 10)).Err()
			pending, err := rdb.ZRangeWithScores(scanCtx, quality5xxPendingKey, 0, 99).Result()
			stop()
			if err != nil {
				continue
			}
			for _, entry := range pending {
				id, err := strconv.ParseInt(fmt.Sprint(entry.Member), 10, 64)
				if err != nil {
					continue
				}
				if _, loaded := active.LoadOrStore(id, true); loaded {
					continue
				}
				select {
				case slots <- struct{}{}:
				default:
					active.Delete(id)
					continue
				}
				s.triggerWG.Add(1)
				go func(id int64, entry redis.Z) {
					defer s.triggerWG.Done()
					defer func() { <-slots; active.Delete(id) }()
					runCtx, cancel := context.WithTimeout(ctx, 12*time.Minute)
					defer cancel()
					if err := s.runQualityTriggeredAccount(runCtx, id, time.UnixMilli(int64(entry.Score))); err != nil {
						return
					}
					if runCtx.Err() != nil {
						return
					}
					// Do not remove a newer signal that arrived after queue expiry.
					_ = rdb.Eval(runCtx, `if tonumber(redis.call('ZSCORE',KEYS[1],ARGV[1])) == tonumber(ARGV[2]) then return redis.call('ZREM',KEYS[1],ARGV[1]) end return 0`, []string{quality5xxPendingKey}, entry.Member, entry.Score).Err()
				}(id, entry)
			}
		}
	}()
}

func (s *ScheduledTestRunnerService) runQualityTriggeredAccount(ctx context.Context, accountID int64, observedAt time.Time) error {
	plans, err := s.planRepo.ListByAccountID(ctx, accountID)
	if err != nil {
		return err
	}
	qualityPlan := false
	for _, plan := range plans {
		if plan != nil && plan.Enabled && plan.PelicanConfig != nil && plan.PelicanConfig.Quality != nil && plan.PelicanConfig.Quality.TriggerOnUpstream5xx {
			qualityPlan = true
			break
		}
	}
	if qualityPlan && s.rateLimitSvc != nil && s.accountTestSvc != nil {
		account, lookupErr := s.accountTestSvc.accountRepo.GetByID(ctx, accountID)
		if lookupErr != nil || account == nil {
			return fmt.Errorf("quality 5xx account %d could not be loaded", accountID)
		}
		// The quality rule owns the quarantine state. Do not apply the generic
		// account-wide ten-minute temporary unschedule here: it would delay the
		// probe and make an opted-in 5xx signal ineffective.
	}
	for _, plan := range plans {
		if !plan.Enabled || plan.PelicanConfig == nil || plan.PelicanConfig.Quality == nil || !plan.PelicanConfig.Quality.TriggerOnUpstream5xx {
			continue
		}
		plan.TriggerSource = quality5xxSource
		plan.TriggerObservedAt = &observedAt
		if plan.LastRunAt != nil && !plan.LastRunAt.Before(observedAt) {
			continue // An overlapping scheduled/event test already covered this signal.
		}
		if plan.RunningUntil != nil && plan.RunningUntil.After(time.Now()) {
			return fmt.Errorf("quality plan %d is still running", plan.ID)
		}
		if !s.runOnePlan(ctx, plan) {
			// A lost claim can mean an actual running test OR only a transient
			// advisory-lock conflict. Re-read before acknowledging this signal.
			current, err := s.planRepo.GetByID(ctx, plan.ID)
			if err != nil {
				return err
			}
			if current == nil || !current.Enabled || current.PelicanConfig == nil || current.PelicanConfig.Quality == nil || !current.PelicanConfig.Quality.TriggerOnUpstream5xx {
				continue
			}
			if current.RunningUntil != nil && current.RunningUntil.After(time.Now()) {
				return fmt.Errorf("quality plan %d is still running", plan.ID)
			}
			if current.LastRunAt != nil && !current.LastRunAt.Before(observedAt) {
				continue
			}
			return fmt.Errorf("quality plan %d could not be claimed", plan.ID)
		}
	}
	return nil
}

// Called immediately after real transport responses, before protocol retry or
// status mapping. Stream failures also report their classified status separately.
func (s *RateLimitService) observeQualityResponse(ctx context.Context, account *Account, response *http.Response, err error) {
	if s == nil || err != nil || response == nil {
		return
	}
	s.observeQualityStatus(ctx, account, response.StatusCode)
}

// Both transport status and semantic failure status feed the same coalescing
// queue. Probe context is preserved to prevent recursive tests.
func (s *RateLimitService) observeQualityStatus(ctx context.Context, account *Account, status int) {
	if s != nil {
		s.qualityTrigger.Observe(ctx, account, status)
	}
}

// temporarilyUnscheduleQuality5xx follows the native account runtime-state
// path. It removes the account from new selection immediately while preserving
// the lease and context of requests that are already in flight. The queued
// quality run remains pending and is allowed to execute after the cooldown.
func (s *RateLimitService) temporarilyUnscheduleQuality5xx(ctx context.Context, account *Account) bool {
	if s == nil || s.accountRepo == nil || account == nil || account.ID <= 0 || account.Type != AccountTypeOAuth {
		return false
	}
	now := time.Now()
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(now) {
		return true
	}
	if account.TempUnschedulableReason == quality5xxTempUnschedReason {
		return false
	}
	until := now.Add(quality5xxTempUnschedDuration)
	account.TempUnschedulableUntil = &until
	account.TempUnschedulableReason = quality5xxTempUnschedReason
	s.notifyAccountSchedulingBlocked(account, until, quality5xxTempUnschedReason)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.accountRepo.SetTempUnschedulable(persistCtx, account.ID, until, quality5xxTempUnschedReason); err != nil {
		slog.Warn("quality_5xx_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		return true
	}
	slog.Warn("quality_5xx_temp_unschedulable", "account_id", account.ID, "until", until)
	return true
}

// Select an execution-only test for the account's actual model route. Never
// bypass BPS to test direct ticket state when diagnosing the BPS route itself.
func quality5xxTestConfig(account *Account, model string, cfg PelicanTestConfig) (PelicanTestConfig, bool) {
	cfg.ParallelCount = 1
	if len(cfg.ModelIDs) <= 1 && openAICodexStateProbeUnsupportedReason(account, model, false) == "" {
		cfg.QuestionKind = OpenAICodexStateProbeQuestionKind
		return cfg, true
	}
	cfg.QuestionKind = "candy"
	cfg.Prompt = CandyPrompt
	applyOutcome := true
	if cfg.Quality != nil {
		policy := *cfg.Quality
		policy.ExpectedAnswer = "21"
		policy.Judge = nil
		cfg.Quality = &policy
		// Only a direct state probe may advance an owned BPS recovery rule.
		// A correct BPS candy answer cannot prove direct routing has recovered.
		applyOutcome = policy.Action != QualityActionEnableBPS && !cfg.BPSRecoveryPending
	}
	return cfg, applyOutcome
}
