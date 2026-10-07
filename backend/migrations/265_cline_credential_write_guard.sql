-- Guard every PostgreSQL credential mutation, including direct and bulk updates.
-- Go remains the full URL/parser authority at the final outbound boundary.
-- This trigger enforces the payment/protocol/model invariants before storage;
-- it neither converts legacy DeepSeek rows nor grants Free API entitlement.
CREATE OR REPLACE FUNCTION wanchuan_cline_credential_guard() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    c JSONB;
    field_name TEXT;
    mode_name TEXT;
    auth_name TEXT;
    raw_base TEXT;
    pair RECORD;
BEGIN
    IF NEW.platform <> 'cline' THEN RETURN NEW; END IF;
    c := NEW.credentials;
    IF NEW.type <> 'apikey' OR jsonb_typeof(c) IS DISTINCT FROM 'object' THEN
        RAISE EXCEPTION 'invalid Cline account credential shape' USING ERRCODE='23514';
    END IF;
    IF jsonb_typeof(c->'api_key') IS DISTINCT FROM 'string' OR length(btrim(c->>'api_key'))=0
       OR length(c->>'api_key')>8192 OR (c->>'api_key') ~ '[[:cntrl:]]' THEN
        RAISE EXCEPTION 'invalid Cline account credential' USING ERRCODE='23514';
    END IF;
    FOREACH field_name IN ARRAY ARRAY['account_mode','cline_auth_type','base_url','api_protocol'] LOOP
        IF c ? field_name AND jsonb_typeof(c->field_name) IS DISTINCT FROM 'string' THEN
            RAISE EXCEPTION 'invalid Cline configuration field type' USING ERRCODE='23514';
        END IF;
    END LOOP;
    mode_name := COALESCE(NULLIF(c->>'account_mode',''),'unknown');
    auth_name := COALESCE(NULLIF(c->>'cline_auth_type',''),'api_key');
    IF mode_name NOT IN ('pass','free','payg','unknown') OR auth_name NOT IN ('api_key','account_token')
       OR COALESCE(c->>'api_protocol','') NOT IN ('','chat_completions') THEN
        RAISE EXCEPTION 'invalid Cline mode or upstream protocol' USING ERRCODE='23514';
    END IF;
    FOREACH field_name IN ARRAY ARRAY['pool_mode','cline_paid_fallback','cline_free_api_enabled','openai_passthrough'] LOOP
        IF c ? field_name AND c->field_name IS DISTINCT FROM 'false'::jsonb THEN
            RAISE EXCEPTION 'Cline fallback and passthrough are disabled' USING ERRCODE='23514';
        END IF;
    END LOOP;
    raw_base := btrim(COALESCE(c->>'base_url',''));
    IF raw_base='' THEN raw_base:='https://api.cline.bot/api/v1'; END IF;
    IF raw_base !~ '^https://(\[[0-9A-Fa-f:.]+\]|[^:/?#@\\[:space:]]+)(:[0-9]+)?(/[^?#\\[:space:]]*)?$' THEN
        RAISE EXCEPTION 'invalid Cline HTTPS base URL' USING ERRCODE='23514';
    END IF;
    IF raw_base ~* '^https://api[.]cline[.]bot(:[0-9]+)?(/|$)' THEN
        IF raw_base !~* '^https://api[.]cline[.]bot(:443)?(/api(/v1)?)?/*$' THEN
            RAISE EXCEPTION 'invalid Cline official API base' USING ERRCODE='23514';
        END IF;
        raw_base:='https://api.cline.bot/api/v1';
    ELSE
        raw_base:=rtrim(raw_base,'/');
        IF raw_base ~ '/(chat/completions|responses|messages)$' THEN
            RAISE EXCEPTION 'use a Cline base URL, not an inference endpoint' USING ERRCODE='23514';
        END IF;
    END IF;
    IF c ? 'model_mapping' THEN
        IF jsonb_typeof(c->'model_mapping') IS DISTINCT FROM 'object' THEN
            RAISE EXCEPTION 'Cline model mapping must be an explicit object' USING ERRCODE='23514';
        END IF;
        FOR pair IN SELECT key,value FROM jsonb_each(c->'model_mapping') LOOP
            IF jsonb_typeof(pair.value) IS DISTINCT FROM 'string'
               OR length(pair.key) NOT BETWEEN 1 AND 256 OR length(pair.value#>>'{}') NOT BETWEEN 1 AND 256
               OR pair.key ~ '[[:space:]]' OR (pair.value#>>'{}') ~ '[[:space:]]'
               OR position('*' IN pair.key)>0 OR position('*' IN (pair.value#>>'{}'))>0
               OR position(chr(92) IN pair.key)>0 OR position(chr(92) IN (pair.value#>>'{}'))>0 THEN
                RAISE EXCEPTION 'Cline requires complete explicit model IDs' USING ERRCODE='23514';
            END IF;
            IF mode_name='pass' AND ((pair.value#>>'{}') NOT LIKE 'cline-pass/%' OR length(pair.value#>>'{}')<=11)
               OR mode_name='payg' AND (pair.value#>>'{}') LIKE 'cline-pass/%' THEN
                RAISE EXCEPTION 'Cline model does not match the confirmed usage mode' USING ERRCODE='23514';
            END IF;
        END LOOP;
    END IF;
    NEW.credentials := (c-'api_base_urls') || jsonb_build_object('account_mode',mode_name,'cline_auth_type',auth_name,'base_url',raw_base,'api_protocol','chat_completions');
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS wanchuan_cline_guard_credentials ON accounts;
CREATE TRIGGER wanchuan_cline_guard_credentials
BEFORE INSERT OR UPDATE OF credentials,platform,type ON accounts
FOR EACH ROW EXECUTE FUNCTION wanchuan_cline_credential_guard();
