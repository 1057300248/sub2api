# Cline platform development status

Updated: 2026-10-01. Development branch: `codex/cline-platform-modular-20261001`.
Base: `wanchuan/stable@71906b39eb2fe14bca512564aa9414d504cbdb20`.

**Status: foundational backend integration, not a complete usable platform release. Do not merge, promote, deploy, migrate live accounts, or advertise end-to-end Cline support yet.**

## Committed foundation

- Independent `cline` domain/service identity; Cline is not a CN provider or DeepSeek account.
- Explicit `pass`, `free`, `payg`, `unknown` modes and API-key/account-token credential kinds.
- Cline-specific canonical base URL and Chat Completions-only configuration.
- Exact account model mappings, with no wildcard or implicit paid fallback.
- Repository create/update validation and a final outbound model/mode guard in the shared CC send path.
- Strict dynamic-catalog and optional quota-response parsers; missing windows are unknown, not zero usage.
- Separate Pass five-hour/weekly/monthly limits, Free model scopes and generic throttling.
- Scoped PostgreSQL CAS implementation, credential-rotation predicates, snapshot refresh and scheduler outbox notification.
- Cline SSE structural-error reader: explicit generation failures become read errors and cannot reach successful finalization. Generated text containing the word "error" is unaffected.
- Additive database CHECK migration, with no account/group/history data conversion.
- `cline-platform` source-overlay manifest entry and integrity verifier. Existing `cline-rate-limit-cas` module is retained.

The Free public-API guard remains closed. A catalog entry alone does not establish API entitlement. No user credential or paid inference request was used during this development work.

## CI repair scope (2026-10-01)

The full unit run on `a0a4d0be` failed because two static settings API response fixtures did not include the newly registered `cline` platform quota. Both fixtures now explicitly include `daily`, `weekly`, and `monthly` null values, retaining strict whole-response JSON equality and all existing platform values.

The reported lint findings are addressed by the already committed repository formatting plus checked response-body cleanup and lowercase error prefixes. New tests cover source Close error propagation and sticky terminal SSE errors with a single callback.

Cline validation is now read-only: it checks out the event SHA, rejects formatting drift instead of fixing it, never executes the historical source generator, never commits or pushes, and asserts the source is unchanged at completion. Five Python regression tests enforce these invariants. Focused Go jobs use the unit build tag and inspect JSON events to reject missing/skipped required tests; compile-only packages are not presented as executed tests. The temporary exact-blob repair preparation workflow is removed from the final tree.

Read the Actions results for the exact current SHA before claiming that the full CI has passed. CI success alone does not complete the rollout requirements below. Earlier runs `36814869424` and `36815636742` covered focused package/service tests, not the pending Cline PostgreSQL concurrency/migration or end-to-end platform tests.

## Required before rollout

1. **Platform registration:** account/group validation, composite routes, gateway predicates, scheduler platform buckets, snapshots/credential retention, import/export, quota and monitor registries. Do not simply add Cline to `IsCNProvider` or `IsMultiProtocolAPIKeyProvider`.
2. **Management UI:** independent creation/editing controls, Pass/Free/PAYG labels, credential kind, full model IDs, filter/icon support, error states and explicit model whitelist selection. No Cline UI has been committed yet.
3. **Metadata service:** bounded HTTP catalog retrieval, cached last-valid catalog, optional Pass usage endpoint, read-only state and explicit refresh APIs. Only parsers/state types/CAS storage currently exist; fetch endpoints and UI are not yet implemented. Quota queries must never convert 403/network/schema errors into an invented subscription state.
4. **All request paths:** reject accidental native Responses dispatch, verify Chat/Responses/Messages bridge eligibility, cover HTTP-200 non-stream errors, and propagate structured in-stream quota failures into scoped scheduling state. The current SSE reader protects success/failure semantics but has no quota callback yet.
5. **Write-path audit:** bulk updates, direct Extra mutations, state ownership, passthrough/header overrides, operational account testing and generic balance probes need a complete Cline-specific audit. The existing `PreserveClineStateExtra` helper has tests but is not yet wired to every mutation path.
6. **Migration tool:** exact-host legacy DeepSeek/Cline preview, affected groups/API-key permissions, operator-confirmed mode and credential kind, in-flight request draining, reversible transaction and cache invalidation. No migration-preview endpoint or migration executor is implemented or executed.
7. **Quota identity and recovery:** correlate multiple keys belonging to one upstream account; validate authoritative new-window transitions, unknown retry-vs-reset behavior, Redis/cache outages and process restart. Current scopes are per local account/credential fingerprint, not proven upstream-subject-wide limits.
8. **Billing and capabilities:** preserve existing pricing, distinguish subscription market-cost metadata from actual incremental cost, verify reasoning/tool usage and partial-failure settlement, and do not infer unsupported native capabilities.
9. **Tests:** actual Cline PostgreSQL CAS concurrency and migration tests, mocked HTTP forwarding for all three protocols, new UI tests, and manually authorized account validation. Passing the existing general CI does not substitute for these feature-specific tests.

## Development tooling

`tools/cline_platform_apply.py` is retained only as a historical reviewed-baseline integration helper. It is not run by CI and is not a production upgrader. Source changes must be committed before normal read-only validation; CI must not rewrite the checked source or move refs.

Preserve `wanchuan/stable`, the current release channel and existing DeepSeek accounts. Resume by reading this file and the current branch, not by assuming that an earlier local draft or conversational progress report was committed.
