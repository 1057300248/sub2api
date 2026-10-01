# Cline closeout and reversible acceptance

Development branch: `codex/cline-platform-modular-20261001`. This document is not permission to merge, release, deploy, use live credentials or migrate production.

## Metadata and scheduling

Open the opt-in Cline catalog/quota panel to read local state. Explicit Refresh uses bounded GETs to the official recommended-model catalog, `/users/me`, and optional Pass usage endpoint, never inference. Catalog GET is unauthenticated; private endpoints receive only the saved credential. Redirects/private destinations are blocked. A PostgreSQL lease prevents concurrent/excessive refreshes across workers.

Missing/expired windows and network/schema/403 errors remain unknown, not zero. Last-valid catalogs are labeled stale. Model selection is explicit; refresh never rewrites the whitelist. Pass reference prices are not extra subscription charges. Downstream prices are unchanged; incremental upstream cost is not inferred.

Shared cooldown identity hashes the verified active upstream account, not a human user ID/email. Explicitly refresh a new or rotated official credential before forwarding. A credential-bound verified identity survives optional usage-endpoint failure; rotation requires verification again. Custom origins receive no official metadata calls. Free/unknown API forwarding stays disabled.

Final admission reads PostgreSQL, not a possibly stale Redis snapshot. Database outages fail closed. Observed limits and scheduler outbox commit atomically. Requests admitted before a new limit cannot be recalled; this is coordination of observed restrictions, not a quota reservation or a guarantee against 429s. Authoritative future cooldowns are not shortened by a smaller retry estimate. Monitors read fresh cached observations only; refresh metadata explicitly before relying on them.

## Administrative writes and exports

Shared create/edit/import validation and a PostgreSQL credential-write guard enforce Cline mode/protocol/model restrictions, including bulk/direct writes. Portable exports exclude server-owned Cline identity/cooldown observations. Explicit administrator backups retain the existing secret-bearing credential export contract and must be protected.

## Offline migration

Build from `backend`: `go build -o cline-migrate ./cmd/cline-migrate`. Apply the additive reviewed schema migrations before using the tool. Secure the database backup and rollback journal, which necessarily contain existing credentials. No migration was executed on a live account during development.

Prepare target Cline/composite groups. Disable the account, drain in-flight requests and stop every gateway/worker using the database. Use a direct administrative PostgreSQL connection with visibility of other sessions; not a transaction pool hiding clients. Connection/lock checks are additional guards, not proof that a disconnected worker is stopped.

Example specification (placeholder IDs/models, not a production plan):

```json
{"account_id":42,"mode":"pass","auth_type":"api_key","model_mapping":{"existing-public-alias":"cline-pass/confirmed-model"},"group_moves":[{"from_group_id":10,"to_group_id":20}]}
```

Only an explicit exact official Cline origin stored on a legacy DeepSeek API-key row is eligible. Each old group binding needs an explicit target; public aliases must remain identical. Model IDs, mode and credential kind are operator-confirmed. No wildcard or paid fallback is introduced.

Supply `CLINE_MIGRATION_DSN` in a protected environment, never shell arguments, logs or committed configuration:

```sh
./cline-migrate --action preview --input spec.json --output plan.json
./cline-migrate --action apply --input plan.json --approve '<exact plan approval>' --maintenance-confirm ALL_WORKERS_STOPPED
./cline-migrate --action rollback --input plan.json --approve '<same approval>' --maintenance-confirm ALL_WORKERS_STOPPED
```

Preview is read-only, creates a new mode-0600 plan file and never overwrites an existing path. The digest binds account, credentials, groups/bindings and relevant customer-key/user permissions. Drift requires another preview. Apply is a single transaction with bindings, private journal and outbox; repeating it is idempotent. Account IDs, prices, historical bills and binding priority/model restrictions stay intact. Existing cooldowns are carried conservatively, not reset. Accounts remain unschedulable after both apply and rollback.

The tool does NOT move customer API keys, grant access to target groups, create groups or rewrite bills. Review and explicitly authorize any customer routing/access changes separately; successful account migration alone does not change which group a customer key selects.

Rollback refuses any subsequent key rotation, metadata refresh, group/permission or other configuration drift. Rehearse rollback BEFORE refreshing metadata or accepting new work; later recovery requires a separate reviewed plan, not force-overwriting changed accounts. Review schema compatibility before binary rollback; do not blindly drop additive tables.

## Acceptance

Verify the exact candidate SHA's general CI, Cline PostgreSQL/protocol jobs and isolated migration tests. Development source/blob/formatting transport is not validation. Rehearse on a disposable database without real credentials, including outbox/database failure and stale snapshots. Then obtain explicit authorization for real-account metadata and any paid inference separately, including reasoning/tool streams, cancellation and partial-failure usage reconciliation. No synthetic test proves real-provider usage accuracy or production readiness.

Partial-failure regressions use actual forwarding/RecordUsage services with synthetic transports/repositories, asserting observed token preservation, unchanged prices and duplicate-command behavior. A staged deployment, account enablement and customer permission changes require separate approval. Stable/main and release channels remain untouched.
