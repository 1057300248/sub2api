# Cline platform status

Development branch: `codex/cline-platform-modular-20261001`.

## Closeout candidate

Independent routing, explicit account management, three text-protocol bridges, credential-scoped limits and the prior protocol/SQL regressions remain in place. This closeout adds opt-in read-only metadata/catalog/quota controls; bounded official GETs, last-valid cache and unknown windows; verified active-account shared cooldowns and durable fail-closed admission; atomic limit/state/outbox persistence; direct/bulk credential write guards; portable-state import/export isolation; cached-only Cline quota monitoring; and an offline preview/apply/rollback migration CLI.

Migration is deliberately not an automatic online conversion. It requires disabled/drained accounts, stopped workers, exact preview approval, a direct administrative PostgreSQL connection and unchanged configuration/permissions. It preserves account IDs, pricing/history and binding restrictions; it does not grant access or rebind customer keys. Both directions keep the account unschedulable and retain conservative cooldowns. Details: `.wanchuan/CLINE_CLOSEOUT_RUNBOOK.md`.

All new code must pass the exact current SHA's normal read-only CI. The development source/blob preparation workflow is not validation and is removed from the final tree. No local execution was possible during this closeout; no local test pass is claimed. Tests cover metadata error boundaries, authenticated GET redaction, protocol partial-failure billing, SQL leases/shared limits/outbox rollback/credential guards and isolated migration transactions. The fixture-backed monetary test is not a real-provider billing reconciliation.

## Operational acceptance still requires authorization

No real account/key was probed, no paid inference was requested, and no live account was migrated. No main/stable ref, release channel or production deployment was changed. Free public API forwarding and paid fallback remain disabled. Exact-SHA synthetic/disposable-database CI cannot establish real upstream entitlement or production deployment readiness. Use the staged, reversible acceptance runbook after explicit approval.
