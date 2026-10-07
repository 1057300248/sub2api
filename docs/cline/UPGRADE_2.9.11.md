# Sub2API v2.9.11 + Cline next release

## Pinned baseline and review boundaries

- Upstream: `ranxi2001/sub2api@e6addfe4aff745c859f9bb50817bc468e0e1f4f7` (`v2.9.11`).
- Previous verified Cline source/rollback: `0d923dc088c665cdbf268ad524160f0a55f0cb84` (PR #13).
- Review branch: `codex/sub2-v2911-cline-next-20261007`.
- Upstream dependencies now match v2.9.11 exactly, including its Vue 3.5.43 lock resolution and source-map-js 1.2.2. No dependency/audit maintenance exception remains.
- This version includes the full pinned upstream release, NOT arbitrary later production/main commits.
- The Cline-only manifest is based on v2.9.11. Comparing to the former v2.9.7 PR base includes historical upstream changes and overstates the independent Cline patch size.

## Merge resolutions

The local three-way rehearsal found four conflicts. Account creation preserves Cline's atomic create wrapper and upstream InitialQualityPlan transaction. CreateAccountInput contains both Schedulable and upstream InitialQualityPlan. package.json and pnpm-lock.yaml are exact v2.9.11 copies. Upstream OAuth-first scheduling remains opt-in and OpenAI-specific; it does not replace Cline quotas or enable paid fallback.

## Delivered compatibility and review fixes

1. Explicit final stream boolean for native and official-host legacy Cline requests; no global OpenAI-default change. Forced unsupported tools return an explicit client error before network I/O instead of becoming ordinary text generation.
2. Account-token wire authorization adds exactly one canonical `workos:` prefix; API keys are untouched. Raw saved representation/fingerprints are preserved so a format fix cannot erase historical cooldown evidence. Both authenticated metadata and inference use the same helper.
3. Account-token HTTP401 records credential-bound `reauth_required` and a scheduler event, without permanent status disablement or owning the official CLI refresh token. Replacement credentials / a newer validated profile can recover; stale credentials cannot poison a rotated account. API-key behavior remains separate.
4. Local total/daily/weekly first crossings create one scheduler event atomically with counters. Already-exhausted increments do not repeatedly enqueue. Includes rollover, concurrent increments, outbox-failure and outer-transaction regressions. Final database admission remains in place; in-flight overshoot remains possible (no reservation system).
5. Account/request header allowlists include official optional HTTP-Referer and X-Title. Additive migration270 updates the raw-write guard; old checksums/data are not rewritten. Authentication, tenant/route, cookie and transport headers stay protected.
6. Typed Chat structures retain reasoning_details. Cline Responses/Messages bridges preserve opaque provider details in a gateway-owned encrypted envelope, with complete output item/block lifecycles and conservative input matching. See NEXT_PROTOCOL_CONTRACT.md for interoperability boundaries.
7. Public model capability enrichment reads the exact-ID /ai/cline/models catalog; missing/invalid fields are unknown. Cloud buckets remain ignored and do not enable modes/permissions. UI distinguishes configured/catalog-listed/reported capability/last observed completion and unknown entitlement.
8. Provider evidence observes only explicit provider/routing metadata, preserves wire bytes, and separates unknown, matched, mismatch and conflicting reports. It never fabricates actual provider from the requested constraint or generated text; no retry/model/provider policy is changed.

## Inherited functions and limitations

Existing native account entry, common/advanced settings, scoped quota recovery, bounded request lifetime, actual usage accounting, header/default precedence and Cline migrations263–269 remain. Old non-Cline Ticket/cookie/updater/patchpack overlays are not restored. NewAPI is unchanged.

No Free API enablement, Cloud account mode, automatic OAuth refresh-token rotation, paid fallback, per-session budget system, monetary pre-reservation or background billable probes are introduced. Existing concurrency limits remain available. A failed request with real provider usage must still be accounted; this code cannot reverse provider-side subscription consumption.

## Verification and rollout

Cline-only CI requires new test roots by name and retains existing three-protocol, real PostgreSQL, migration, frontend and race gates. Shared account/protocol type changes also require full upstream Go unit/integration/lint and full frontend regression. This document is not a test-pass claim: use the final delivered commit's results.

Use a new reviewed deployment artifact and migrate in a disposable/staging database before rollout. No production account reset, provider call, SMTP delivery, release/image publication, main/stable merge or production deployment is part of this source change. A pure upstream binary upgrade still overwrites compiled Cline patches.
