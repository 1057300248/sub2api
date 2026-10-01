-- Independent Cline platform. Additive schema compatibility only: no account,
-- group, API key, historical usage or credential is migrated automatically.
ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check CHECK (platform IN ('anthropic','openai','gemini','antigravity','grok','kimi','zhipu','deepseek','minimax','opencode_go','cline'));
ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check CHECK (target_platform IN ('anthropic','openai','gemini','antigravity','grok','kimi','zhipu','deepseek','minimax','opencode_go','cline'));
ALTER TABLE channel_monitors DROP CONSTRAINT IF EXISTS channel_monitors_provider_check;
ALTER TABLE channel_monitors ADD CONSTRAINT channel_monitors_provider_check CHECK (provider IN ('anthropic','openai','gemini','antigravity','grok','kimi','zhipu','deepseek','minimax','opencode_go','cline'));
ALTER TABLE channel_monitor_request_templates DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check;
ALTER TABLE channel_monitor_request_templates ADD CONSTRAINT channel_monitor_request_templates_provider_check CHECK (provider IN ('anthropic','openai','gemini','antigravity','grok','kimi','zhipu','deepseek','minimax','opencode_go','cline'));
