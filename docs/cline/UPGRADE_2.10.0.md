# Clean v2.10.0 Cline rebuild

The subsequent [native account flow refactor](NATIVE_ACCOUNT_FLOW.md) removes the parallel Cline account editor. Use the standard **Add account → Cline** tab and standard Edit/Bulk Edit controls; the provider protocol and quota protections below are unchanged.

## Source identity and scope

- Upstream: `ranxi2001/sub2api` tag `v2.10.0`, peeled commit `5ca3cca21eeaf4ca8a694a7f2f8f0ecd9575c549`.
- Fresh branch: `codex/sub2-v2100-cline-rebuild-20261007`, rooted directly at that commit.
- Reviewed Cline source: `c0fd7815108a5dd59b171a3e89b4cbbf61781a7f`, previously based on `e6addfe4aff745c859f9bb50817bc468e0e1f4f7`.
- Only the Cline overlay and the bounded incident diagnostics in `patches.json` are retained. No old Ticket/Cookie/updater overlay.
- `frontend/package.json`, `frontend/pnpm-lock.yaml`, Go dependencies, upstream version, payment fix, WebSocket acceleration and upstream release/updater code remain upstream-owned. The previous `@e965/xlsx` maintenance override is not transplanted; v2.10.0 supplies its own SheetJS fix.
- Ten shared source files were three-way reconciled. The two dependency files overlapping upstream were kept exactly upstream, not merged backward.

This is a clean **code** base, not a new database. Existing API keys, account IDs, users and usage logs must not be recreated. No production deploy, database change or key rotation is performed by this rebuild.

## Confirmed incident correction

Two Cline paths had diverged: the native platform classifier recognized `The limit resets in ...`, while legacy OpenAI/DeepSeek accounts used a separate `Try again in ...` parser. Both now use the same bounded parser/classifier.

A confirmed weekly message ending in `The limit resets in 5d 21h` produces a 141-hour cooldown, not the generic one-minute fallback. Supported input includes compact/spaced units, full English units, HTTP Retry-After seconds/date, fractional seconds rounded upward, weekly/monthly quota classes, and wrapped upstream errors. The latest valid observation wins. A short Retry-After cannot shorten a later body reset. Malformed, negative, overflow, over-bound and partial-unit values are not truncated into a short duration.

When a confirmed Pass quota has no usable reset observation, the retry bound is conservative (five-hour: 5h; weekly: 7d; monthly: 31d; unknown window: 24h). This is marked as an estimate (`ResetAt` remains absent), not an invented authoritative reset. Normal throttles retain their separate classification. A real Retry-After remains an explicit observation.

Legacy limits remain account-local and use the existing monotonic database setter. No subject, entitlement, paid fallback, account re-enable or alternative model is inferred from an error message. Native limits retain credential/subject isolation, durable admission, transaction/outbox behavior and explicit quota scopes.

## 401 investigation without changing authentication

Both authentication middleware variants capture the **actual selected** credential after their existing extraction rules. Access logs add:

`auth_key_source`, `auth_key_length`, `auth_key_sha256_128`, `auth_key_lookup`, `auth_key_id`, and counts of Authorization/x-api-key/x-goog-api-key header values.

`auth_key_sha256_128` is the first 16 bytes of SHA-256, lower-case hex. It can be compared with an independently computed digest on the calling gateway. No plaintext key, header dump or query string is logged. Existing ingress log sampling still applies. These fields do not prove whether an internal cache hit occurred: compare them first to distinguish wrong credentials from failures for the same credential.

Header precedence and whitespace behavior are deliberately unchanged, including Google-specific rules. An absent key remains 401, backend lookup failure remains 500, and auth overload remains 503. The code does not clear caches, delete keys or retry authentication with a different header after a rejected validly selected credential.

## Snapshot / usage persistence evidence

- Snapshot failures now distinguish `account_not_found`, cancellation/deadline and persistence failures, with account IDs and request correlation. Batch ID lists are bounded at 32. A scoped `ACCOUNT_NOT_FOUND` is not reinterpreted as proof that PostgreSQL lost a row or database.
- Usage write/fallback failures identify the exact request/account/API-key/user identity, stage and sanitized SQLSTATE. Asynchronous batch identities are captured alongside immutable SQL arguments.
- SQLSTATE `23503` remains a real foreign-key failure. No foreign key is removed, no account ID is replaced with zero/null, no missing usage is declared persisted, and diagnostic logging does not rebill a request.
- These changes make the unknown reference/auth failures diagnosable; they do **not** establish their production root cause. Check new logs and the live caller configuration in a controlled canary.

## Validation and rollout gate

The Cline workflow requires named incident regressions, native/legacy protocol tests, race tests, PostgreSQL monotonic/transaction tests, all 42 durable HTTP lifecycle scenarios, offline migration apply/rollback, full frontend tests, lint, typecheck and production build. It rejects missing/skipped required tests and records the exact tested SHA. Source integrity checks are guards, not substitutes for those tests. Consult completed CI artifacts for results; this document does not assert a pass in advance.

Before rollout: back up and restore a copy of the existing database into staging; test the existing migration history and keys; inspect each saved credential's live Cline metadata/catalog; verify weekly cooldowns across restarts and concurrent requests; correlate incoming 401s by digest/request ID; confirm no usage FK failures. Do not clear a known weekly quota simply to re-enable scheduling. Do not enable deliberately disabled accounts in bulk.

Native Cline migration remains explicit/offline via the existing migration CLI, not an automatic account rewrite on startup. Existing legacy accounts can remain legacy while the incident fix is validated. Upstream compiled-binary auto-upgrades still overwrite compiled customization: the upstream updater is intentionally not replaced here.

## Model catalog warning

Correction: rechecking the official ClinePass page on 2026-10-07 still lists `cline-pass/deepseek-v4-flash`. The earlier claim that V4 Flash was deprecated and replaced by V4.1 is withdrawn: the retrieved evidence does not establish that. Public documentation alone also cannot prove availability for a saved credential. Verify the live catalog and entitlement for that credential, distinguishing Pass model slugs from usage-billing/free slugs. Never silently remap `deepseek/deepseek-v4-flash` to another model or to usage-based billing.

Primary reference: https://docs.cline.bot/getting-started/clinepass
