-- Extend the existing positive list to Cline documented application headers.
-- Old migration checksums and stored accounts are unchanged.
-- Additive guard: do not change the checksum of historical credential migrations.
-- Covers raw SQL, bulk edits and imports, not just the administrator service.
-- No existing account is rewritten; other platforms retain their own contracts.
CREATE OR REPLACE FUNCTION cline_header_settings_guard() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    c JSONB;
    headers JSONB;
    pair RECORD;
    header_value TEXT;
    folded TEXT;
    seen TEXT[] := ARRAY[]::TEXT[];
    combined_bytes INTEGER := 0;
    encoded_bytes INTEGER := 2;
    field_text TEXT;
BEGIN
    IF NEW.platform IS DISTINCT FROM 'cline' THEN RETURN NEW; END IF;
    c := NEW.credentials;
    IF jsonb_typeof(c) IS DISTINCT FROM 'object' THEN
        RAISE EXCEPTION 'invalid Cline header configuration' USING ERRCODE='23514';
    END IF;
    IF c ? 'header_override_enabled' AND jsonb_typeof(c->'header_override_enabled') IS DISTINCT FROM 'boolean' THEN
        RAISE EXCEPTION 'invalid Cline header configuration' USING ERRCODE='23514';
    END IF;
    IF NOT (c ? 'header_overrides') THEN RETURN NEW; END IF;
    headers := c->'header_overrides';
    IF jsonb_typeof(headers) IS DISTINCT FROM 'object' THEN
        RAISE EXCEPTION 'invalid Cline header configuration' USING ERRCODE='23514';
    END IF;
    FOR pair IN SELECT key,value FROM jsonb_each(headers) LOOP
        IF cardinality(seen) >= 16 OR octet_length(pair.key) NOT BETWEEN 1 AND 128
           OR pair.key COLLATE "C" !~ '^[!#$%&''*+.^_`|~0-9A-Za-z-]+$'
           OR jsonb_typeof(pair.value) IS DISTINCT FROM 'string' THEN
            RAISE EXCEPTION 'invalid Cline header configuration' USING ERRCODE='23514';
        END IF;
        folded := lower(pair.key COLLATE "C");
        IF folded = ANY(seen) OR NOT (folded IN ('user-agent','accept-language','x-request-id','x-client-name','x-client-version','http-referer','x-title')
            OR starts_with(folded,'x-metadata-')) THEN
            RAISE EXCEPTION 'invalid Cline header configuration' USING ERRCODE='23514';
        END IF;
        header_value := pair.value#>>'{}';
        IF octet_length(header_value)>2048 OR EXISTS (
            SELECT 1 FROM generate_series(1,length(header_value)) AS chars(i)
            WHERE ascii(substr(header_value,i,1))<32 OR ascii(substr(header_value,i,1))=127
        ) THEN
            RAISE EXCEPTION 'invalid Cline header configuration' USING ERRCODE='23514';
        END IF;
        combined_bytes := combined_bytes + octet_length(pair.key) + octet_length(header_value);
        -- Go's encoding/json uses compact separators and escapes HTML and U+2028/9.
        -- Count that representation, not PostgreSQL's whitespace-expanded object.
        encoded_bytes := encoded_bytes + CASE WHEN cardinality(seen)>0 THEN 1 ELSE 0 END
            + octet_length(to_json(pair.key)::text) + 1 + octet_length(pair.value::text);
        FOREACH field_text IN ARRAY ARRAY[pair.key,header_value] LOOP
            encoded_bytes := encoded_bytes
                + 5*(length(field_text)-length(translate(field_text,'<>&','')))
                + 3*(length(field_text)-length(translate(field_text,chr(8232)||chr(8233),'')));
        END LOOP;
        IF combined_bytes>8192 OR encoded_bytes>16384 THEN
            RAISE EXCEPTION 'invalid Cline header configuration' USING ERRCODE='23514';
        END IF;
        seen := array_append(seen,folded);
    END LOOP;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS cline_guard_header_settings ON accounts;
CREATE TRIGGER cline_guard_header_settings
BEFORE INSERT OR UPDATE OF credentials,platform,type ON accounts
FOR EACH ROW EXECUTE FUNCTION cline_header_settings_guard();
