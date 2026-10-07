-- Reconcile the independently released Cline and TypeSafe platform CHECKs.
-- 241 and 263 must also accept their union while pending: an appended migration
-- alone cannot rescue existing rows rejected by either earlier CHECK. Their
-- exact historical checksums remain accepted without rewriting migration history.
-- This migration repairs databases where either historical file was already
-- applied. It does not change accounts, credentials, bindings or entitlements.
ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check CHECK (platform IN ('anthropic','openai','gemini','antigravity','grok','kimi','zhipu','deepseek','minimax','opencode_go','cline','typesafe'));
ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check CHECK (target_platform IN ('anthropic','openai','gemini','antigravity','grok','kimi','zhipu','deepseek','minimax','opencode_go','cline','typesafe'));
