package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/robfig/cron/v3"
)

func cooldownEntryEqual(current any, applied json.RawMessage) bool {
	raw, err := json.Marshal(current)
	if err != nil {
		return false
	}
	var decoded any
	if json.Unmarshal(applied, &decoded) != nil {
		return false
	}
	expected, _ := json.Marshal(decoded)
	return bytes.Equal(raw, expected)
}

func cooldownEntryUntil(entry any) time.Time {
	value, ok := entry.(map[string]any)
	if !ok {
		return time.Time{}
	}
	raw, _ := value["rate_limit_reset_at"].(string)
	until, _ := time.Parse(time.RFC3339, raw)
	return until
}

// Keep ownership at entry granularity: a newer native error or manual edit wins.
func transitionQualityModels(account *service.Account, state *qualityState, planID int64, models []string, until time.Time, outcome string, restore bool, now time.Time) (string, error) {
	if outcome == "inconclusive" {
		return "inconclusive", nil
	}
	if outcome == "passed" && (state.Action == "" || !restore) {
		return "passed", nil
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	limits, _ := account.Extra["model_rate_limits"].(map[string]any)
	if limits == nil {
		limits = map[string]any{}
	}
	if outcome == "failed" {
		first := state.Action == ""
		if first {
			state.Action = service.QualityActionRemoveModel
			state.ModelRateLimits = map[string]json.RawMessage{}
			state.ModelApplied = map[string]json.RawMessage{}
			for _, model := range models {
				key := account.GetMappedModel(model)
				previous, err := json.Marshal(limits[key])
				if err != nil {
					return "", err
				}
				state.ModelRateLimits[key] = previous
			}
		} else {
			for key, applied := range state.ModelApplied {
				if !cooldownEntryEqual(limits[key], applied) {
					return "action_conflict", nil
				}
			}
		}
		for key := range state.ModelRateLimits {
			reset := until
			if previous := cooldownEntryUntil(limits[key]); previous.After(reset) {
				reset = previous
			}
			entry := map[string]any{"rate_limited_at": now.UTC().Format(time.RFC3339), "rate_limit_reset_at": reset.UTC().Format(time.RFC3339), "reason": fmt.Sprintf("quality_rule:%d", planID)}
			limits[key] = entry
			raw, err := json.Marshal(entry)
			if err != nil {
				return "", err
			}
			state.ModelApplied[key] = raw
		}
		account.Extra["model_rate_limits"] = limits
		if first && account.Type == service.AccountTypeOAuth && account.Concurrency > 5 {
			previous, applied := account.Concurrency, 5
			state.PreviousConcurrency, state.AppliedConcurrency = &previous, &applied
			account.Concurrency = applied
		}
		if first {
			return "models_cooled", nil
		}
		return "model_cooldown_refreshed", nil
	}
	if outcome != "passed" {
		return "no_change", nil
	}
	conflict := false
	for key, previous := range state.ModelRateLimits {
		if !cooldownEntryEqual(limits[key], state.ModelApplied[key]) {
			conflict = true
			continue
		}
		var original any
		if err := json.Unmarshal(previous, &original); err != nil {
			return "", err
		}
		if cooldownEntryUntil(original).After(now) {
			limits[key] = original
		} else {
			delete(limits, key)
		}
		delete(state.ModelRateLimits, key)
		delete(state.ModelApplied, key)
	}
	if len(limits) == 0 {
		delete(account.Extra, "model_rate_limits")
	} else {
		account.Extra["model_rate_limits"] = limits
	}
	if state.PreviousConcurrency != nil && state.AppliedConcurrency != nil {
		if account.Concurrency == *state.AppliedConcurrency {
			account.Concurrency = *state.PreviousConcurrency
			state.PreviousConcurrency, state.AppliedConcurrency = nil, nil
		} else {
			conflict = true
		}
	}
	if conflict {
		return "restore_conflict", nil
	}
	*state = qualityState{}
	return "restored", nil
}

func applyQualityModelOutcome(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan, outcome, status string, state qualityState) (string, error) {
	if outcome == "inconclusive" {
		return "inconclusive", nil
	}
	if outcome == "passed" && state.Action != "" && plan.PelicanConfig.Quality.AutoRestore && status != "active" {
		return "restore_conflict", nil
	}
	account := &service.Account{}
	var credentials, extra []byte
	if err := tx.QueryRowContext(ctx, `SELECT platform,type,concurrency,COALESCE(credentials,'{}'::jsonb),COALESCE(extra,'{}'::jsonb) FROM accounts WHERE id=$1`, plan.AccountID).Scan(&account.Platform, &account.Type, &account.Concurrency, &credentials, &extra); err != nil {
		return "", err
	}
	if err := json.Unmarshal(credentials, &account.Credentials); err != nil {
		return "", err
	}
	if err := json.Unmarshal(extra, &account.Extra); err != nil {
		return "", err
	}
	now := time.Now()
	schedule, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(plan.CronExpression)
	if err != nil {
		return "", err
	}
	beforeConcurrency := account.Concurrency
	action, err := transitionQualityModels(account, &state, plan.ID, plan.PelicanConfig.Quality.RemoveModels, schedule.Next(now), outcome, plan.PelicanConfig.Quality.AutoRestore, now)
	if err != nil {
		return "", err
	}
	updated, err := json.Marshal(account.Extra)
	if err != nil {
		return "", err
	}
	changed := !bytes.Equal(updated, extra) // Database JSON order differs; compare decoded content below.
	var original any
	var current any
	if err := json.Unmarshal(extra, &original); err != nil {
		return "", err
	}
	if err := json.Unmarshal(updated, &current); err != nil {
		return "", err
	}
	originalRaw, _ := json.Marshal(original)
	currentRaw, _ := json.Marshal(current)
	changed = !bytes.Equal(originalRaw, currentRaw) || beforeConcurrency != account.Concurrency
	if changed {
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb,concurrency=$3,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE id=$1`, plan.AccountID, string(updated), account.Concurrency); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,payload) VALUES('account_changed',$1,'{}')`, plan.AccountID); err != nil {
			return "", err
		}
	}
	if state.Action == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM account_quality_states WHERE plan_id=$1`, plan.ID)
	} else {
		err = qualityUpsertState(ctx, tx, plan.ID, state)
	}
	return action, err
}
