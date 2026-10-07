package service

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrClineLocalQuotaExceeded = infraerrors.New(http.StatusTooManyRequests, "CLINE_LOCAL_QUOTA_EXCEEDED", "The local Cline account budget is exhausted")

var ErrClineAdvancedSettings = infraerrors.BadRequest("INVALID_CLINE_ADVANCED_SETTINGS", "Invalid Cline local quota or error policy configuration")

var clineQuotaConfigKeys = []string{
	"quota_limit", "quota_daily_limit", "quota_weekly_limit",
	"quota_daily_reset_mode", "quota_daily_reset_hour", "quota_weekly_reset_mode", "quota_weekly_reset_day", "quota_weekly_reset_hour", "quota_reset_timezone",
	"quota_notify_total_enabled", "quota_notify_total_threshold", "quota_notify_total_threshold_type",
	"quota_notify_daily_enabled", "quota_notify_daily_threshold", "quota_notify_daily_threshold_type",
	"quota_notify_weekly_enabled", "quota_notify_weekly_threshold", "quota_notify_weekly_threshold_type",
}

// Null is a deletion sentinel only for explicit administrator-owned quota keys.
func IsClineQuotaConfigKey(key string) bool {
	for _, k := range clineQuotaConfigKeys {
		if key == k {
			return true
		}
	}
	return false
}

func HasClineQuotaConfig(extra map[string]any) bool {
	for key := range extra {
		if IsClineQuotaConfigKey(key) {
			return true
		}
	}
	return false
}

func clineNumber(value any, maximum float64, integer bool) bool {
	if value == nil {
		return false
	}
	switch value.(type) {
	case float64, float32, int, int64, int32, uint, uint64, json.Number:
	default:
		return false
	}
	raw, err := json.Marshal(value)
	var n float64
	return err == nil && json.Unmarshal(raw, &n) == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= maximum && (!integer || math.Trunc(n) == n)
}
func clineCleanText(s string, maximum int) bool {
	if len(s) > maximum {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func ValidateClineLocalQuotaSettings(platform string, extra map[string]any) error {
	if platform != PlatformCline {
		return nil
	}
	for _, key := range clineQuotaConfigKeys {
		v, exists := extra[key]
		if !exists || v == nil {
			continue
		}
		valid := true
		switch {
		case strings.HasSuffix(key, "_limit"):
			valid = clineNumber(v, 1e12, false)
		case strings.HasSuffix(key, "_enabled"):
			_, valid = v.(bool)
		case strings.HasSuffix(key, "_threshold_type"):
			valid = v == "fixed" || v == "percentage"
		case strings.HasSuffix(key, "_threshold"):
			maximum := 1e12
			if extra[key+"_type"] == "percentage" {
				maximum = 100
			}
			valid = clineNumber(v, maximum, false)
		case strings.HasSuffix(key, "_mode"):
			valid = v == "rolling" || v == "fixed"
		case strings.HasSuffix(key, "_hour"):
			valid = clineNumber(v, 23, true)
		case strings.HasSuffix(key, "_day"):
			valid = clineNumber(v, 6, true)
		case key == "quota_reset_timezone":
			tz, ok := v.(string)
			valid = ok && len(tz) > 0 && len(tz) <= 100 && tz != "Local"
			if valid {
				_, err := time.LoadLocation(tz)
				valid = err == nil
			}
		}
		if !valid {
			return ErrClineAdvancedSettings
		}
	}
	return nil
}

func validateClineErrorSettings(platform string, credentials map[string]any) error {
	if platform != PlatformCline {
		return nil
	}
	for _, key := range []string{"custom_error_codes_enabled", "temp_unschedulable_enabled"} {
		if value, exists := credentials[key]; exists {
			if _, ok := value.(bool); !ok {
				return ErrClineAdvancedSettings
			}
		}
	}
	for _, pair := range [][2]string{{"custom_error_codes_enabled", "custom_error_codes"}, {"temp_unschedulable_enabled", "temp_unschedulable_rules"}} {
		if enabled, _ := credentials[pair[0]].(bool); enabled {
			raw, err := json.Marshal(credentials[pair[1]])
			var list []any
			if err != nil || json.Unmarshal(raw, &list) != nil || len(list) == 0 {
				return ErrClineAdvancedSettings
			}
		}
	}
	for _, key := range []string{"custom_error_codes", "temp_unschedulable_rules"} {
		raw, exists := credentials[key]
		if !exists {
			continue
		}
		data, err := json.Marshal(raw)
		var list []any
		if err != nil || json.Unmarshal(data, &list) != nil || list == nil {
			return ErrClineAdvancedSettings
		}
		if key == "custom_error_codes" {
			if len(list) > 64 {
				return ErrClineAdvancedSettings
			}
			seen := map[int]bool{}
			for _, value := range list {
				number, ok := value.(float64)
				if !ok || !clineNumber(number, 599, true) || number < 400 {
					return ErrClineAdvancedSettings
				}
				code := int(number)
				if seen[code] {
					return ErrClineAdvancedSettings
				}
				seen[code] = true
			}
		} else {
			if len(list) > 32 {
				return ErrClineAdvancedSettings
			}
			for _, value := range list {
				r, ok := value.(map[string]any)
				if !ok {
					return ErrClineAdvancedSettings
				}
				code, codeOK := r["error_code"].(float64)
				minutes, minutesOK := r["duration_minutes"].(float64)
				if !codeOK || !minutesOK || !clineNumber(code, 599, true) || code < 400 || !clineNumber(minutes, 10080, true) || minutes < 1 {
					return ErrClineAdvancedSettings
				}
				keywords, ok := r["keywords"].([]any)
				if !ok || len(keywords) == 0 || len(keywords) > 20 {
					return ErrClineAdvancedSettings
				}
				seen := map[string]bool{}
				for _, v := range keywords {
					k, ok := v.(string)
					if !ok || strings.TrimSpace(k) == "" || !clineCleanText(k, 256) || seen[strings.ToLower(k)] {
						return ErrClineAdvancedSettings
					}
					seen[strings.ToLower(k)] = true
				}
				if d, exists := r["description"]; exists {
					s, ok := d.(string)
					if !ok || !clineCleanText(s, 512) {
						return ErrClineAdvancedSettings
					}
				}
			}
		}
	}
	return nil
}

// Cline-specific hard failures always use built-in handling. An operator's
// custom allowlist or short temporary rule cannot suppress these protections.
func clineProtectedError(account *Account, status int, body []byte) bool {
	return account.IsCline() && (status == http.StatusUnauthorized || status == http.StatusPaymentRequired || status == http.StatusForbidden || status == http.StatusTooManyRequests || isClineScopedError(account, status, body) || isUpstreamModelNotFoundError(status, body) || (status == http.StatusBadRequest && isOpenAICompatibleModelNotFound400(body)))
}

var clineLocalQuotaRuntimeKeys = []string{"quota_used", "quota_daily_used", "quota_weekly_used", "quota_daily_start", "quota_weekly_start", "quota_daily_reset_at", "quota_weekly_reset_at"}

// Called under the repository's row lock. Never copy local counters from the
// editor snapshot, even when unrelated configuration fields are being changed.
func PreserveClineLocalQuotaRuntime(platform string, current, incoming map[string]any) map[string]any {
	if platform != PlatformCline {
		return incoming
	}
	if incoming == nil {
		incoming = make(map[string]any)
	}
	for _, key := range clineLocalQuotaRuntimeKeys {
		delete(incoming, key)
		if value, exists := current[key]; exists {
			incoming[key] = value
		}
	}
	changed := false
	for _, key := range clineQuotaConfigKeys[3:9] {
		if !reflect.DeepEqual(current[key], incoming[key]) {
			changed = true
		}
	}
	if changed {
		ComputeQuotaResetAt(incoming)
		NormalizeFixedQuotaWindows(incoming)
	}
	return incoming
}
