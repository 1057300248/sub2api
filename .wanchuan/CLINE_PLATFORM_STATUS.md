# Cline platform development status

Updated: 2026-10-01. Development branch: `codex/cline-platform-modular-20261001`.
Base: `wanchuan/stable@71906b39eb2fe14bca512564aa9414d504cbdb20`.

**Status: registered development platform with an independent management form and guarded protocol bridges; not a complete platform release. Do not merge, promote, deploy, migrate live accounts, or advertise end-to-end Cline support yet.**

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

Cline validation is read-only: it checks out the event SHA, rejects formatting drift instead of fixing it, never executes the historical source generator, never commits or pushes, and asserts the source is unchanged at completion. Five Python regression tests enforce these invariants. Focused Go jobs use the unit build tag and inspect JSON events to reject missing/skipped required tests; compile-only packages are not presented as executed tests. The temporary exact-blob repair preparation workflow is removed from the final tree.

Read the Actions results for the exact current SHA before claiming that the full CI has passed. CI success alone does not complete the rollout requirements below. Earlier runs `36814869424` and `36815636742` covered focused package/service tests, not the pending Cline PostgreSQL concurrency/migration or end-to-end platform tests.

## Failure-boundary continuation (2026-10-01)

Continued from `cd321ebd2c94da2731a578f6d73379bfb0bfdde5`, without changing the stable branch or production.

- SSE validation now waits for a complete bounded event, supports multiline JSON, named error events before/after data, CR/LF/CRLF and BOM, preserves empty heartbeats, and rejects truncation or a missing `[DONE]`. Error events never release their payload as successful generated content.
- Independent structural-field decoding prevents a malformed sibling from hiding an explicit error. Generated prose, reasoning and tool arguments are not searched for failure strings.
- A bounded JSON body guard catches HTTP-200 error envelopes before forwarding bytes. JSON returned to a streaming request cannot become an empty successful stream. The Cline JSON guard has a 16 MiB body ceiling; SSE uses the configured line ceiling as a total-event ceiling too.
- The shared CC send path captures a deep credential snapshot and the actual outgoing model before transport. Confirmed stream/JSON limit errors call the existing scoped CAS with that original identity, using a synchronous five-second bounded context independent of client cancellation. The actual model is not mapped a second time.
- Unknown generation errors remain failures without invented quota state. No retries, paid fallbacks, account migration or new Free entitlement were introduced.
- Added policy and service-level regression tests, and expanded required-test execution checks plus protected upgrade anchors. Service tests use an observing repository fake; they are not PostgreSQL concurrency or full HTTP-route integration tests.

Earlier selected-file smoke results are not used as rollout evidence. The later exact-SHA predecessor `ddcf1eb56952a4cdbb8e351311ae7b3aaa5be2ce` passed general CI `36830707439` (unit, integration, lint, frontend, release helpers and shell). New source changes require new exact-SHA validation. No live-account or paid inference test was performed.

## Registration, management UI and write-ownership continuation (2026-10-01)

- Register Cline independently in concrete/composite text routing, group binding, scheduler normalization and canonical buckets, available-model lists, token estimation and the quota schema validator. Do not add Cline to CN-provider or multi-native-protocol predicates.
- Preserve Cline mode/auth-kind/base URL in scheduler projections so admission and scoped cooldown fingerprints agree with hydrated accounts. Keep native Responses, compaction, embeddings, image generation, realtime and header overrides disabled. Requests to the three supported text interfaces bridge through Chat Completions, including when stale native-probe flags exist.
- Move Cline creation/edit validation to shared repository entry points. Preserve `cline_state` and `model_rate_limits` from the latest locked database row during full edits and protect those keys in direct/bulk Extra deltas. Non-Cline and unrelated Extra SQL shapes remain unchanged.
- Route explicitly requested administrative connection tests through the same guarded Cline pipeline with an explicit permitted model and a 64-token output cap. Opening or saving the configuration form never probes inference.
- Add an independent Cline creation/edit dialog, platform/group filters, icon and mode labels. Expose Pass/Free/PAYG/unknown and API-key/account-token selections. Preserve invalid whitelist rows for operator correction, never rewrite model IDs on mode changes, omit a blank edit key to retain the secret, and preserve unchanged group bindings. Free/unknown cannot be forwarded; the catalog does not grant entitlement.
- Add 24 mocked transport success combinations (three text protocols, two permitted modes, two credential kinds, streaming/non-streaming) plus protocol failure, blocked-mode, dispatch, scheduler projection and repository ownership regressions. These exercise gateway forwarding with synthetic transports, not an authenticated live deployment or complete monetary settlement.
- Add five real PostgreSQL feature tests (24 simultaneous scoped writers, credential rotation, managed state edits, safe creation, migration/schema compatibility). A separate exact-SHA read-only CI job runs all five three times and rejects skips/missing events. They are not claimed as executed until that job passes.
- Extend upgrade-protected paths and required test execution checks; include both new UI suites in the existing frontend-critical target without removing existing suites.

Local validation used the full repository and its Go 1.27.0 toolchain. Cline service/repository/handler/admin targeted tests passed. The first broad unit run exposed SQL-shape and composite expected-list regressions; those were fixed without disabling assertions and their targeted legacy regressions passed. The broad run also encountered DNS-dependent channel-monitor validation tests in the offline environment, so it is not reported as a full local pass. UI validation passed 17 form/component tests, three locale completeness tests, ESLint on the new component/helper, and the full frontend typecheck. Disposable PostgreSQL and complete CI validation remain separate evidence. No live account or paid inference was used.

Temporary source/dependency transfer and reviewed-blob preparation workflows are development transport only, not validation. They are removed from the final feature tree. All candidate source is committed before the normal read-only exact-SHA CI runs.

## Required before rollout

1. **Metadata and quota UI:** bounded catalog retrieval, cached last-valid catalog, optional Pass usage endpoint, read-only state and explicit refresh APIs. Parsers/state types/CAS storage exist, but HTTP metadata fetching and quota controls are not implemented. Network/schema/403 responses must not invent subscription state or zero usage.
2. **Remaining administrative paths:** complete import/export, monitoring and bulk/direct credential-write audits. Individual shared repository create/update and managed Extra ownership are covered; that is not a claim that all generic credential mutation paths are Cline-aware.
3. **Migration tool:** exact-host legacy DeepSeek/Cline preview, affected groups/API-key permissions, operator-confirmed mode/auth kind, request draining, reversible transaction and cache invalidation. No migration preview/executor is implemented or executed.
4. **Quota identity and recovery:** correlate multiple keys of one upstream subject; distinguish authoritative window rollover from unknown retry timing, Redis/cache outages and restart recovery. Current identity is per local account/credential fingerprint. Persistence failure and outbox atomicity require further review.
5. **Billing and capabilities:** preserve existing prices, distinguish subscription market-cost metadata from incremental cost, and validate reasoning/tool usage and partial-failure monetary settlement. Mock protocol usage assertions are not complete billing verification.
6. **Acceptance:** exact-current-SHA general CI, PostgreSQL feature CI and frontend-critical checks, followed by manually authorized real-account testing and a reversible rollout plan. Do not merge, promote or deploy just because an earlier commit was green.

## Development tooling

`tools/cline_platform_apply.py` remains a historical reviewed-baseline helper, not a production upgrader or a CI source generator. Changes must be committed before read-only validation; validation must never rewrite checked source or move refs.

Preserve `wanchuan/stable`, the current release channel and existing DeepSeek accounts. Resume by reading this file and the actual branch rather than assuming an earlier local draft was pushed.
