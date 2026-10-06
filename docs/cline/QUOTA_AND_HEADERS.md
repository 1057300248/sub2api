# Automatic Pass quota recovery and request header overrides

## Scope and rollout

Only the independent Cline platform gets automatic metadata maintenance. Legacy
OpenAI/DeepSeek accounts pointing to the official Cline host retain request-level
header/body compatibility, but are not silently converted or enrolled in polling.
No NewAPI change, production deploy, main/stable merge, live provider test, paid
fallback or Free API entitlement is part of this change.

The server starts one bounded metadata worker with the gateway and joins it on
shutdown. Active, schedulable, unexpired official-origin Pass API-key accounts are
enrolled automatically. Disabled, Free/PAYG/unknown-mode and custom-origin accounts
are not polled. Cooldown is deliberately NOT a candidate exclusion. A composition
option disables the worker in deterministic fixtures before construction.

## Timing and recovery

- Healthy quota observations target five minutes, long exhaustion thirty minutes,
  plus up to sixteen seconds of per-account jitter. Known resets schedule a near-
  boundary recheck. These are target cadences, not latency guarantees under load.
- Keyset pages contain at most fifty accounts, with four simultaneous refreshes
  per process. The existing thirty-second PostgreSQL lease deduplicates each
  credential across instances/manual refresh. Metadata errors back off from
  thirty seconds to thirty minutes. Metadata Retry-After can extend this further
  and extends the metadata lease, never an inference quota.
- The three windows are five_hour, weekly, monthly. An exhausted window remains
  a durable block after display freshness expires, a query fails, a partial
  response omits it, or the server restarts. The reset boundary alone does not
  authorize inference. A later successful observation of that window is required.
- Query failures and absent fields are unknown, never zero. Unknown reset times
  remain null; a non-authoritative retry boundary is not displayed as a reset.
  Five-hour/week/month names are not a new duration starting at the error time.
- Inference Retry-After and duration text still create independent monotonic
  limits. 5hr/5h/5 hours/five-hour aliases are recognized in the window label,
  without mistaking a retry duration for the quota window.
- Metadata exhaustion is projected to the verified active-account subject's
  shared table in the same transaction as state and the scheduler outbox.
  Fresh healthy metadata can clear only metadata-origin evidence. It cannot
  shorten a future inference limit or overwrite a newer conflicting observation.
  Every still-blocking window is checked before sending, including other keys'
  shared evidence. Identity failures cannot attribute a quota response to the old
  subject. Credential rotation invalidates old snapshots and poll schedules.
- Where the optional official usage endpoint cannot be read, an exhausted
  official Pass account stays pending recheck; this implementation does not issue
  billable background probes or claim recovery without evidence. A custom-origin
  account retains its existing inference-only Retry-After expiration semantics.
- The UI polls LOCAL state every thirty seconds while visible. It never turns a
  page visit into a provider POST/inference. It shows authoritative resets,
  retry-only boundaries, sources and pending-recheck status separately.

Normal quota failure, admission-unavailable and header-validation responses do
not become provider-health failures or invent usage/charges. Existing downstream
billing, subscription pricing and token accounting are unchanged.

## Request-scoped header overrides

The top-level JSON extension `header_overrides` works with Chat Completions,
Responses, Messages and the Cline Chat-backed compaction path. It is consumed at
the shared outbound boundary, removed from the model's JSON request, and is not
persisted in the account configuration. Existing `providerOptions` remain intact.

```json
{
  "model": "your-configured-public-model",
  "messages": [{"role": "user", "content": "Hello"}],
  "providerOptions": {"gateway": {"only": ["deepseek"]}},
  "header_overrides": {
    "User-Agent": "MyClient/1.0",
    "X-Request-ID": "request-example",
    "X-Metadata-Trace": "diagnostic-example"
  }
}
```

Allowed names are `User-Agent`, `Accept-Language`, `X-Request-ID`, `X-Client-Name`,
`X-Client-Version`, and valid `X-Metadata-*` names, matched case-insensitively.
This is intentionally a safe positive list, not unrestricted header injection.
Authorization, API keys, cookies, Host, forwarding/tenant/organization selectors,
Content-Type/Accept and transport/framing/hop-by-hop headers cannot be overridden.
Metadata GETs never inherit these request overrides.

Up to sixteen unique headers, 128 bytes per name, 2,048 bytes per string value,
8,192 combined name/value bytes, and 16,384 encoded object bytes are accepted.
Null, arrays, numbers, control characters, duplicate decoded names/case aliases,
invalid names and noncanonical/duplicate top-level extension fields are rejected.
An invalid request receives 400 before upstream work; do not put secrets in
optional diagnostic headers or in request bodies captured by operator logging.
A later request without the extension does not inherit earlier overrides.

## Validation

The Cline workflow requires the new quota/header tests by name. Coverage includes
scope aliases, staleness/errors/expiry, partial recovery, unknown reset handling,
metadata Retry-After, credential binding, bounded worker shutdown/concurrency,
three protocols with native and legacy accounts, real PostgreSQL shared-state
recovery and inference-limit preservation, plus UI polling/countdown behavior.
The existing handler/ledger parameter cases now assert final headers and body
stripping, including streaming, non-streaming and compaction. Tests use synthetic
credentials/transports and disposable PostgreSQL only. CI results must be read
for the exact delivered commit; passing an earlier revision is not final evidence.

## Optional account defaults and account settings

The native Cline account editor now also exposes the existing administrator
account-default header fields, `credentials.header_override_enabled` and
`credentials.header_overrides`. They obey the same stricter Cline positive list
and byte limits as the request extension. They are optional and disabled by
default; no account-level body/providerOptions override has been introduced.

Precedence: gateway defaults < enabled account header defaults < request-level
`header_overrides`. All existing case variants are removed before replacement.
Only configured account defaults persist; a request override never changes them
or leaks to later requests. Metadata GETs inherit neither form of override.
Clearing the account toggle sends false plus an empty header object. Native
create/edit, mixed bulk updates and repository credential writes validate the
settings before persistence; non-Cline header behavior remains unchanged.

See `ACCOUNT_SETTINGS_AUDIT.md` for the visible create entry, shared account
settings, credential-preserving edits, saved quota list projection and tests.
