package repository

// Only trusted Cline observation/CAS methods write these keys. Account edits
// and generic Extra deltas preserve the current database value even when the
// incoming map contains JSON null, forged state or stale model cooldowns.
// placeholder is an internal SQL parameter identifier, never user input.
func clineManagedExtraDeltaSQL(placeholder string) string {
	return "(CASE WHEN platform = 'cline' THEN " + placeholder + "::jsonb - 'cline_state' - 'cline_route' - 'model_rate_limits' - 'quota_used' - 'quota_daily_used' - 'quota_weekly_used' - 'quota_daily_start' - 'quota_weekly_start' - 'quota_daily_reset_at' - 'quota_weekly_reset_at' ELSE " + placeholder + "::jsonb END)"
}

// Keep unrelated Extra updates byte-for-byte on their existing SQL path. The
// platform-specific guard is necessary only when a reserved key is present,
// including an explicit JSON null. PostgreSQL still owns the current value.
func clineExtraUpdateSQL(placeholder string, updates map[string]any) string {
	_, state := updates["cline_state"]
	_, routing := updates["cline_route"]
	_, limits := updates["model_rate_limits"]
	managedQuota := false
	for key := range updates {
		switch key {
		case "quota_used", "quota_daily_used", "quota_weekly_used", "quota_daily_start", "quota_weekly_start", "quota_daily_reset_at", "quota_weekly_reset_at":
			managedQuota = true
		}
	}
	if state || routing || limits || managedQuota {
		return clineManagedExtraDeltaSQL(placeholder)
	}
	return placeholder + "::jsonb"
}
