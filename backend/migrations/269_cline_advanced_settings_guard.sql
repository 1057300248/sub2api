-- Additive validation for Cline administrator configuration only. Reuse quota
-- accounting fields; no new ledger, provider reset or historical data rewrite.
CREATE OR REPLACE FUNCTION cline_advanced_settings_guard() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
  e JSONB := COALESCE(NEW.extra,'{}'::jsonb);
  c JSONB := NEW.credentials;
  k TEXT;
  v JSONB;
  n NUMERIC;
  item JSONB;
  kw JSONB;
  text_value TEXT;
  seen TEXT[];
  changed BOOLEAN := TG_OP='INSERT';
  reset_changed BOOLEAN := TG_OP='INSERT';
  reset_keys TEXT[] := ARRAY['quota_daily_reset_mode','quota_daily_reset_hour','quota_weekly_reset_mode','quota_weekly_reset_day','quota_weekly_reset_hour','quota_reset_timezone'];
  policy_keys TEXT[] := ARRAY['custom_error_codes_enabled','custom_error_codes','temp_unschedulable_enabled','temp_unschedulable_rules'];
  dim TEXT;
  zone TEXT;
  boundary TIMESTAMP;
  last_boundary TIMESTAMPTZ;
  period_days INTEGER;
  delay_days INTEGER;
  settings TEXT[] := ARRAY['quota_limit','quota_daily_limit','quota_weekly_limit',
    'quota_daily_reset_mode','quota_daily_reset_hour','quota_weekly_reset_mode','quota_weekly_reset_day','quota_weekly_reset_hour','quota_reset_timezone',
    'quota_notify_total_enabled','quota_notify_total_threshold','quota_notify_total_threshold_type',
    'quota_notify_daily_enabled','quota_notify_daily_threshold','quota_notify_daily_threshold_type',
    'quota_notify_weekly_enabled','quota_notify_weekly_threshold','quota_notify_weekly_threshold_type'];
BEGIN
  IF NEW.platform IS DISTINCT FROM 'cline' THEN RETURN NEW; END IF;
  IF TG_OP='UPDATE' THEN
    changed := OLD.platform IS DISTINCT FROM 'cline';
    reset_changed := changed;
    FOREACH k IN ARRAY settings LOOP
      changed := changed OR (NEW.extra->k IS DISTINCT FROM OLD.extra->k);
    END LOOP;
    FOREACH k IN ARRAY policy_keys LOOP
      changed := changed OR (NEW.credentials->k IS DISTINCT FROM OLD.credentials->k);
    END LOOP;
    FOREACH k IN ARRAY reset_keys LOOP
      reset_changed := reset_changed OR (NEW.extra->k IS DISTINCT FROM OLD.extra->k);
    END LOOP;
    -- Billing, metadata and lease writes do not scan timezones or revalidate
    -- unchanged historical admin settings on the hot path.
    IF NOT changed THEN RETURN NEW; END IF;
  END IF;
  FOREACH k IN ARRAY settings LOOP
    IF NOT(e ? k) THEN CONTINUE; END IF;
    v := e->k;
    IF v = 'null'::jsonb THEN e := e-k; CONTINUE; END IF;
    IF k LIKE '%_limit' OR k LIKE '%_threshold' OR k LIKE '%_hour' OR k LIKE '%_day' THEN
      IF jsonb_typeof(v) IS DISTINCT FROM 'number' THEN
        RAISE EXCEPTION 'invalid Cline local quota setting' USING ERRCODE='23514';
      END IF;
      n := (v#>>'{}')::NUMERIC;
      IF n NOT BETWEEN 0 AND 1000000000000 THEN
        RAISE EXCEPTION 'invalid Cline local quota number' USING ERRCODE='23514';
      END IF;
      IF (k LIKE '%_hour' AND (n>23 OR n<>trunc(n))) OR (k LIKE '%_day' AND (n>6 OR n<>trunc(n)))
         OR (k LIKE '%_threshold' AND e->>(k||'_type')='percentage' AND n>100) THEN
        RAISE EXCEPTION 'invalid Cline local quota bounds' USING ERRCODE='23514';
      END IF;
    ELSIF k LIKE '%_enabled' THEN
      IF jsonb_typeof(v) IS DISTINCT FROM 'boolean' THEN RAISE EXCEPTION 'invalid Cline notification setting' USING ERRCODE='23514'; END IF;
    ELSIF k LIKE '%_threshold_type' THEN
      IF v NOT IN ('"fixed"'::jsonb,'"percentage"'::jsonb) THEN RAISE EXCEPTION 'invalid Cline threshold type' USING ERRCODE='23514'; END IF;
    ELSIF k LIKE '%_mode' THEN
      IF v NOT IN ('"fixed"'::jsonb,'"rolling"'::jsonb) THEN RAISE EXCEPTION 'invalid Cline reset mode' USING ERRCODE='23514'; END IF;
    ELSIF k='quota_reset_timezone' THEN
      IF jsonb_typeof(v) IS DISTINCT FROM 'string' OR octet_length(v#>>'{}') NOT BETWEEN 1 AND 100
         OR NOT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=(v#>>'{}')) THEN
        RAISE EXCEPTION 'invalid Cline reset timezone' USING ERRCODE='23514';
      END IF;
    END IF;
  END LOOP;
  IF reset_changed THEN
    zone := COALESCE(e->>'quota_reset_timezone','UTC');
    FOREACH dim IN ARRAY ARRAY['daily','weekly'] LOOP
      IF COALESCE(e->>('quota_'||dim||'_reset_mode'),'rolling')='fixed' THEN
        period_days := CASE WHEN dim='daily' THEN 1 ELSE 7 END;
        delay_days := CASE WHEN dim='daily' THEN 0 ELSE
          (COALESCE((e->>'quota_weekly_reset_day')::INTEGER,1)-extract(dow FROM NOW() AT TIME ZONE zone)::INTEGER+7)%7 END;
        boundary := date_trunc('day',NOW() AT TIME ZONE zone)+make_interval(days=>delay_days,hours=>COALESCE((e->>('quota_'||dim||'_reset_hour'))::INTEGER,0));
        IF boundary AT TIME ZONE zone <= NOW() THEN boundary := boundary+make_interval(days=>period_days); END IF;
        last_boundary := (boundary-make_interval(days=>period_days)) AT TIME ZONE zone;
        e := jsonb_set(e,ARRAY['quota_'||dim||'_reset_at'],to_jsonb(to_char((boundary AT TIME ZONE zone) AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')),true);
        IF COALESCE((e->>('quota_'||dim||'_limit'))::NUMERIC,0)>0 AND
           COALESCE((e->>('quota_'||dim||'_start'))::TIMESTAMPTZ,'1970-01-01'::TIMESTAMPTZ)<last_boundary THEN
          e := jsonb_set(e,ARRAY['quota_'||dim||'_used'],'0'::jsonb,true);
          e := jsonb_set(e,ARRAY['quota_'||dim||'_start'],to_jsonb(to_char(last_boundary AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')),true);
        END IF;
      ELSE e := e-('quota_'||dim||'_reset_at'); END IF;
    END LOOP;
  END IF;
  NEW.extra := e;
  FOREACH k IN ARRAY ARRAY['custom_error_codes_enabled','temp_unschedulable_enabled'] LOOP
    IF c ? k AND jsonb_typeof(c->k) IS DISTINCT FROM 'boolean' THEN RAISE EXCEPTION 'invalid Cline error toggle' USING ERRCODE='23514'; END IF;
  END LOOP;
  FOREACH k IN ARRAY ARRAY['custom_error_codes','temp_unschedulable_rules'] LOOP
    IF c->>(CASE WHEN k='custom_error_codes' THEN 'custom_error_codes_enabled' ELSE 'temp_unschedulable_enabled' END)='true' THEN
      IF jsonb_typeof(c->k) IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'enabled Cline policy requires rules' USING ERRCODE='23514'; END IF;
      IF jsonb_array_length(c->k)=0 THEN RAISE EXCEPTION 'enabled Cline policy requires rules' USING ERRCODE='23514'; END IF;
    END IF;
  END LOOP;
  IF c ? 'custom_error_codes' THEN
    IF jsonb_typeof(c->'custom_error_codes') IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'invalid Cline error codes' USING ERRCODE='23514'; END IF;
    IF jsonb_array_length(c->'custom_error_codes')>64 THEN RAISE EXCEPTION 'too many Cline error codes' USING ERRCODE='23514'; END IF;
    seen := ARRAY[]::TEXT[];
    FOR item IN SELECT value FROM jsonb_array_elements(c->'custom_error_codes') LOOP
      IF jsonb_typeof(item) IS DISTINCT FROM 'number' THEN RAISE EXCEPTION 'invalid Cline error code' USING ERRCODE='23514'; END IF;
      n := (item#>>'{}')::NUMERIC;
      IF n NOT BETWEEN 400 AND 599 OR n<>trunc(n) OR n::INTEGER::TEXT=ANY(seen) THEN RAISE EXCEPTION 'invalid Cline error code' USING ERRCODE='23514'; END IF;
      seen := array_append(seen,n::INTEGER::TEXT);
    END LOOP;
  END IF;
  IF c ? 'temp_unschedulable_rules' THEN
    IF jsonb_typeof(c->'temp_unschedulable_rules') IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'invalid Cline temporary rules' USING ERRCODE='23514'; END IF;
    IF jsonb_array_length(c->'temp_unschedulable_rules')>32 THEN RAISE EXCEPTION 'too many Cline temporary rules' USING ERRCODE='23514'; END IF;
    FOR item IN SELECT value FROM jsonb_array_elements(c->'temp_unschedulable_rules') LOOP
      IF jsonb_typeof(item) IS DISTINCT FROM 'object' OR jsonb_typeof(item->'error_code') IS DISTINCT FROM 'number'
         OR jsonb_typeof(item->'duration_minutes') IS DISTINCT FROM 'number' OR jsonb_typeof(item->'keywords') IS DISTINCT FROM 'array' THEN
        RAISE EXCEPTION 'invalid Cline temporary rule' USING ERRCODE='23514';
      END IF;
      n := (item->>'error_code')::NUMERIC;
      IF n NOT BETWEEN 400 AND 599 OR n<>trunc(n) THEN RAISE EXCEPTION 'invalid Cline rule code' USING ERRCODE='23514'; END IF;
      n := (item->>'duration_minutes')::NUMERIC;
      IF n NOT BETWEEN 1 AND 10080 OR n<>trunc(n) OR jsonb_array_length(item->'keywords') NOT BETWEEN 1 AND 20 THEN RAISE EXCEPTION 'invalid Cline rule bounds' USING ERRCODE='23514'; END IF;
      seen := ARRAY[]::TEXT[];
      FOR kw IN SELECT value FROM jsonb_array_elements(item->'keywords') LOOP
        text_value := kw#>>'{}';
        IF jsonb_typeof(kw) IS DISTINCT FROM 'string' OR btrim(text_value)='' OR octet_length(text_value)>256 OR lower(text_value)=ANY(seen)
           OR EXISTS(SELECT 1 FROM generate_series(1,length(text_value)) AS chars(i) WHERE ascii(substr(text_value,i,1))<32 OR ascii(substr(text_value,i,1))=127) THEN
          RAISE EXCEPTION 'invalid Cline rule keyword' USING ERRCODE='23514';
        END IF;
        seen := array_append(seen,lower(text_value));
      END LOOP;
      IF item ? 'description' THEN
        text_value := item->>'description';
        IF jsonb_typeof(item->'description') IS DISTINCT FROM 'string' OR octet_length(text_value)>512
           OR EXISTS(SELECT 1 FROM generate_series(1,length(text_value)) AS chars(i) WHERE ascii(substr(text_value,i,1))<32 OR ascii(substr(text_value,i,1))=127) THEN
          RAISE EXCEPTION 'invalid Cline rule description' USING ERRCODE='23514';
        END IF;
      END IF;
    END LOOP;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS cline_guard_advanced_settings ON accounts;
CREATE TRIGGER cline_guard_advanced_settings BEFORE INSERT OR UPDATE OF extra,credentials,platform ON accounts
FOR EACH ROW EXECUTE FUNCTION cline_advanced_settings_guard();
