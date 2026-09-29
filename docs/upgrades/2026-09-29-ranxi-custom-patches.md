# 2026-09-29: ranxi production + custom patch alignment

This is an AI-assisted, isolated source upgrade, not a production deployment.

## Pinned inputs

- Target: `ranxi2001/sub2api` `production` at `468061e082a0fcc1ef6a9c8f8b0ef1f773d26652`.
- Latest release observed: `v2.9.1`, commit `ca5dd3cf5299df8778cdfa752430253ad4e071e8`. The target contains nine additional commits; it is not the unmodified release tag.
- Previous customized branch: `codex/ranxi-v2.8.17-cline-cas-20260926`, `94ce53353ecc501c26308ddaef36d1f8bcec78e0`.
- Legacy main audited: `ee7cfaf3f4b927e0e557c649cecc275abb00458e`.
- Output: `1057300248/sub2api`, `codex/ranxi-latest-patches-20260929`.
- PR #4 merged the previous Cline branch into this new upgrade branch, not into main. Both the pinned upstream and Cline head are ancestors of the result.

## Patch disposition

| Source patch | Disposition at the new baseline |
| --- | --- |
| Cline `99cd2338`, through the validated `94ce5335` branch | Retained. Exact `api.cline.bot` hostname scope, composite `Try again in ...` cooldown parsing, bounded duration, monotonic database extension, no non-atomic fallback on CAS error, and provider fallback isolation. Existing upstream `SetRateLimitedIfLater` is reused, not overwritten. |
| Cookie/plan patch `b36a040d` | Semantically replaced by the newer ticket architecture: `HarvestCookies`, a separate `HarvestCookiesAt`, accepted-state feedback/rotation, session/identity/proxy-bound egress, personal/team shape validation, revocation/standby, and redacted cookie-count/expiry status. Keep these newer implementations instead of copying the old global cookie jar. |
| TTL consistency `ee7cfaf3` | Adapted, not reverted. Viper already defaults to 240/60 seconds. Correct the stale service fallback (3600/600) and expiry helper fallback to the current 240/60 policy. Explicit administrator values, including 900/300, remain unchanged; issued-time and route-credential lifetime caps still apply. |
| DeepSeek reasoning fallback `bcc73f8d` | Already implemented and extended upstream. Keep cache/plaintext preservation, missing-content placeholder, mapped outbound-model detection, and OpenCode Zen/Go compatibility. Run the actual inherited regression tests. |
| Gemini timing `763a325a` | Superseded by upstream `testing/synctest` and `synctest.Wait()`. Preserve deterministic virtual-time tests rather than restoring wall-clock sleeps. |
| Seedance/security `d099c069` | Already present or evolved upstream. Keep Seedance parse/close/usage fixes and upstream dependency graph, including gRPC 1.83.2, x/crypto 0.55.0 and x/net 0.58.0. Do not downgrade go.mod/go.sum. |

The legacy inventory was produced with:

```sh
git log --no-merges --right-only --cherry-pick --format='%H %s' \
  468061e082a0fcc1ef6a9c8f8b0ef1f773d26652...ee7cfaf3f4b927e0e557c649cecc275abb00458e
```

Patch-ID differences alone do not establish a missing feature: each of the five unique legacy commits was compared with the actual new implementation.

## Validation contract

The custom-patch workflow checks upstream/Cline ancestry, Cline unit tests, ten race-detector repetitions, PostgreSQL 18.6 monotonic CAS/scheduler tests, and backend compilation. The additional runner `tools/verify_custom_patch_alignment.py` requires 25 service tests and one config test to execute and pass, rejecting skipped or empty selections. Six newly added top-level tests cover default/explicit-config preservation, bounded expiry, stale-cookie isolation, cross-plan rejection and credential redaction. The inherited DeepSeek, Gemini, Cookie/plan, feedback and Seedance tests are required as well.

The standard CI retains full backend unit/integration tests, lint, shell/release-helper tests and frontend typecheck/critical Vitest; it also builds the production frontend bundle. Security jobs retain govulncheck and the repository's pnpm audit exception policy. Do not equate that policy check with an assertion that there are zero advisories.

Results must be read for the final output commit, not inferred from a previous green branch. The GitHub PR conversation records the final run IDs and actual outcomes. No real upstream credentials are used by the new tests; fixtures are synthetic.

## Deployment boundary and intentional differences

No main/default branch, tag, release, image, VPS configuration, database or production service is modified by this upgrade task. The temporary hash-guarded preparation workflow only created reviewed Git blobs and is absent from the final tree; persistent validation workflows have read-only repository permissions.

The legacy `codex_cookie_jar` account-extra representation and old raw captured-ticket fixtures are not imported or replayed. Old data is not deleted, but the new credential-scoped representation may need fresh harvesting. Saved harvest speed presets remain upstream-compatible, including their legacy JSON refresh setting; they are not silently rewritten to the default refresh value. Runtime overrides remain authoritative.

A later deployment should back up the database/configuration, review all upstream migrations since the running version, use a pinned custom build rather than the unmodified upstream image, and verify real Cline/credential/streaming behavior in staging. Production New API remains the primary gateway and is outside this source-upgrade scope. No live smoke test or production migration is claimed here.
