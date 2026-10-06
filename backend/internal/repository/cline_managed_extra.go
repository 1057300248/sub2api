package repository

// Only trusted Cline observation/CAS methods write these keys. Account edits
// and generic Extra deltas preserve the current database value even when the
// incoming map contains JSON null, forged state or stale model cooldowns.
// placeholder is an internal SQL parameter identifier, never user input.
func clineManagedExtraDeltaSQL(placeholder string) string {
	return "(CASE WHEN platform = 'cline' THEN " + placeholder + "::jsonb - 'cline_state' - 'model_rate_limits' ELSE " + placeholder + "::jsonb END)"
}

// Keep unrelated Extra updates byte-for-byte on their existing SQL path. The
// platform-specific guard is necessary only when a reserved key is present,
// including an explicit JSON null. PostgreSQL still owns the current value.
func clineExtraUpdateSQL(placeholder string, updates map[string]any) string {
	_, state := updates["cline_state"]
	_, limits := updates["model_rate_limits"]
	if state || limits {
		return clineManagedExtraDeltaSQL(placeholder)
	}
	return placeholder + "::jsonb"
}
