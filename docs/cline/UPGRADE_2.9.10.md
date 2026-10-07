# Sub2API 2.9.10 + Cline only

- Upstream: `ranxi2001/sub2api@ab7cb27ceed6390441277784cc5560694d95fd73` (`v2.9.10`).
- Cline source: `1057300248/sub2api@116ab640aca8331ea74e12ce8773c2fc85463d54`; original base `bc83ff9c367883e5b7d0140e6bb42e2e7cc5239c`.
- Scope: only Cline platform/CAS and their necessary routing, schema, frontend and tests.
- A pristine upstream tree is used; shared-file patches are applied with Git three-way merging.
- Both upstream and previous Cline history are recorded as ancestors, while the resulting tree explicitly retires non-Cline overlays.

The branch also carries three explicitly bounded maintenance files required to keep the
candidate releasable: the platform-options test includes the retained `cline` value, and
the frontend dependency manifest/lock file pin Vue `3.5.43` and `source-map-js` `1.2.2`.
These are security/test maintenance only; the Cline boundary checker allows these exact
paths and continues to reject every other unrelated path.

## Retained Cline contracts

Independent Cline accounts and legacy official-host OpenAI/DeepSeek compatibility;
Pass/payg/Free/unknown classification (Free and unknown public inference remain disabled);
explicit model mapping and no silent paid fallback; scoped quota/CAS and wrapped 429 handling;
request-level custom parameters through Chat, Responses and Messages (including compaction);
SSE/JSON error guards, bounded cancellation and earliest caller deadline;
observed-usage settlement and credential/metadata ownership; Cline-only administration and UI.
Historical Cline migrations 263-267 and the exact TypeSafe/Cline CHECK-union compatibility
remain necessary for upgrades. They are not replaced, renumbered or removed.

## Removed non-Cline customization

Ticket TTL/default overrides and legacy `codex_cookie_jar` privacy/write overlays;
Wanchuan release-source, revision, manifest and updater customizations;
Wanchuan sync/promote/scheduler/patchpack machinery; generic model/operations regression
wrappers, unrelated test changes and dependency overrides. Those implementations now
match the pinned upstream release, not the older fork.

## Operational consequences

The built-in updater now behaves exactly like upstream and downloads upstream releases.
It DOES NOT reapply Cline. Do not use it to update a deployment that must retain Cline;
deploy an explicitly built and tested Cline branch artifact instead. No replacement
custom updater or forced Ticket configuration is introduced by this branch.

Legacy `codex_cookie_jar` rows are not deleted by this code change. The custom protection
was removed as requested. Before production adoption, inspect and sanitize historical
rows and import/backup workflows using a separately reviewed data operation. Do not
expose an export that still contains obsolete credential material.

This branch operation does not migrate production, rebind accounts, publish a release,
change main/stable, call real providers, or incur inference charges.

## Verification

Run `python3 tools/verify_cline_only.py`, `python3 tools/verify_cline_platform.py`,
Cline unit/race suites, real PostgreSQL migration/ownership/HTTP ledger tests, and frontend
tests/build. CI results must be read for the exact final commit; this document does not
claim unexecuted checks passed. The Git history must include the pinned upstream object.

The pinned integrity manifest and this runbook are explicitly tracked despite upstream
ignore rules. They are documentation/test metadata, not a replacement fork updater.
