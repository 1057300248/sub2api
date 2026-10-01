# Cline platform development status

Updated: 2026-10-01. Development branch: `codex/cline-platform-modular-20261001`.
Base: `wanchuan/stable@71906b39eb2fe14bca512564aa9414d504cbdb20`.

**Status: foundational backend integration, not a complete usable platform release. Do not merge, promote, deploy, migrate live accounts, or advertise end-to-end Cline support yet.**

## Committed and exercised

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

The Free public-API guard is intentionally closed. A catalog entry alone does not establish API entitlement. No user credential or paid inference request was used during this development work.

## Verified so far

Run `36814869424` on GitHub Actions successfully executed:

```sh
cd backend
go test -race -count=3 ./internal/pkg/cline
go test -count=1 -run '^TestCline' ./internal/service ./internal/repository ./internal/handler/admin
```

The service tests ran; the repository/admin packages were compiled with that test filter. This is **not** a PostgreSQL integration test or a full backend regression run. The generated host integration was committed as `b5ce2e5d3c97faa04b61ef677e977af86144546c` only after those checks passed.

## Required before rollout

1. **Platform registration:** account/group validation, composite routes, gateway predicates, scheduler platform buckets, snapshots/credential retention, import/export, quota and monitor registries. Do not simply add Cline to `IsCNProvider` or `IsMultiProtocolAPIKeyProvider`.
2. **Management UI:** independent creation/editing controls, Pass/Free/PAYG labels, credential kind, full model IDs, filter/icon support, error states and explicit model whitelist selection. No Cline UI has been committed yet.
3. **Metadata service:** bounded HTTP catalog retrieval, cached last-valid catalog, optional Pass usage endpoint, read-only state and explicit refresh APIs. Only parsers/state types/CAS storage currently exist; fetch endpoints and UI are not yet implemented. Quota queries must never convert 403/network/schema errors into an invented subscription state.
4. **All request paths:** reject accidental native Responses dispatch, verify Chat/Responses/Messages bridge eligibility, cover HTTP-200 non-stream errors, and propagate structured in-stream quota failures into scoped scheduling state. The current SSE reader protects success/failure semantics but has no quota callback yet.
5. **Write-path audit:** bulk updates, direct Extra mutations, state ownership, passthrough/header overrides, operational account testing and generic balance probes need a complete Cline-specific audit. The existing `PreserveClineStateExtra` helper has tests but is not yet wired to every mutation path.
6. **Migration tool:** exact-host legacy DeepSeek/Cline preview, affected groups/API-key permissions, operator-confirmed mode and credential kind, in-flight request draining, reversible transaction and cache invalidation. No migration-preview endpoint or migration executor is implemented or executed.
7. **Quota identity and recovery:** correlate multiple keys belonging to one upstream account; validate authoritative new-window transitions, unknown retry-vs-reset behavior, Redis/cache outages and process restart. Current scopes are per local account/credential fingerprint, not proven upstream-subject-wide limits.
8. **Billing and capabilities:** preserve existing pricing, distinguish subscription market-cost metadata from actual incremental cost, verify reasoning/tool usage and partial-failure settlement, and do not infer unsupported native capabilities.
9. **Tests:** actual PostgreSQL CAS concurrency and migration tests, full backend regressions, frontend typecheck/build/unit tests, mocked HTTP forwarding for all three protocols and manually authorized account validation. The new SQL migration has not been exercised against a production or disposable database yet.

## Development tooling

`tools/cline_platform_apply.py` is a one-time, reviewed-baseline integration helper used because local execution became unavailable. It refuses unexpected upstream edits and is not an automatic production upgrader. Its branch-only GitHub workflow can format and commit verified generated changes **only** to the named development branch; it cannot merge or release. Remove this temporary generation step after the host integration is finalized, retaining normal read-only CI and the module integrity tests.

Preserve `wanchuan/stable`, the current release channel and existing DeepSeek accounts. Resume by reading this file and the current branch, not by assuming that an earlier local draft or conversational progress report was committed.
