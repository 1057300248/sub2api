> **UI superseded by [Native account flow](NATIVE_ACCOUNT_FLOW.md).** Cline now uses the original CreateAccountModal, EditAccountModal and BulkEditAccountModal. The standalone editor and advanced bulk section described in this historical document were removed. Local quota/reset/notification validation and database ownership safeguards below remain the deployed API contract. Local reset is available through the original account action menu; temporary pause through the original status modal; metadata through the inline provider readout. There is no second account CRUD entry or form.

# Native Cline advanced settings

Completes the two remaining v2.9.10 native-editor gaps: local monetary quotas and
notifications, and custom error/temporary-pause rules. It does not introduce a
new ledger or modify NewAPI, provider entitlements, authentication, model mapping,
paid fallback, or Free inference eligibility. All advanced policies start absent
or disabled on a new account. Existing administrator settings are preserved.

## Local budgets and notification semantics

Create/edit supports total, daily and weekly local amount limits, rolling/fixed
reset modes, hour, weekday (Sunday = 0), and timezone. Amount zero or absent means
unlimited. An explicit JSON null removes only the named administrator-owned quota
setting; missing keys preserve saved settings. Per-dimension notification toggle,
remaining threshold and amount/percentage use the existing upstream mail service:
for example a 20% remaining threshold on a 100-unit limit is crossed at 80 used.
Blank/zero thresholds do not send alerts. Delivery still requires the global
notification switch and configured administrator recipients; the editor never
changes those global settings. Local prices/multipliers are not actual Pass bills.

Only edited settings leave the browser. Used counters and period timestamps are
not round-tripped. The repository re-reads current counters under its account
lock; generic Extra/bulk writes cannot replace them. The final Cline send boundary
also checks the database's local budget, preventing a stale scheduler snapshot
from bypassing a newly exhausted budget. Existing in-flight requests still settle;
this is not a pre-reservation system or a strict no-overshoot guarantee.

## Error policies

HTTP 400–599 codes are editable (maximum 64 distinct codes). Rules contain a status,
one or more literal case-insensitive keywords, duration 1–10080 minutes and optional
description; maximum 32 rules, 20 keywords per rule. Order is preserved. Disabled
policies keep their saved rules. An enabled policy must contain at least one rule
or code. No arbitrary regex or code execution is introduced.

Authentication/payment/permission failures, quota 429 and recognized Cline scoped
errors always retain built-in handling, even when omitted from a custom allowlist.
Custom temporary rules cannot shorten them. For remaining selected errors, a
matching Cline rule creates a local account-wide temporary pause (not a provider
quota scope), before the generic selected-error fallback. Non-Cline behavior stays
upstream-owned. Model-not-found protection remains ahead of custom handling.

## Three distinct operations

The editor exposes separately confirmed actions only while it is unchanged:
1. Reset local used quota: total/daily/weekly counters and local period starts,
   not provider state, rate limits, temporary pause, credentials or status.
2. Clear local temporary pause: that pause only, not monetary usage or Pass scopes.
3. Recheck Pass metadata: the existing metadata refresh, not a billable inference
   probe and not a promise of recovery.

State-changing account save is disabled while an action is in flight. The generic
Cline temporary-status modal uses the same narrow pause action rather than broad
recover-state. Generic model-limit clearing also preserves Cline's provider scopes.
Local reset and pause clearing atomically enqueue a scheduler event, participate
in an outer transaction, and roll back if the outbox insert fails.

## Bulk settings and database contract

A Cline-only API-key selection offers opt-in advanced bulk fields. Blank fields
leave existing values untouched. Separate explicit actions remove all local
limits or disable notifications/error policies; counters and existing cooldowns
are never reset by bulk settings. Mixed selections do not expose Cline-only UI,
and server/database validation still protects mixed API requests.

Additive migration 269 validates administrator fields for native Cline raw/bulk
writes, removes only known null configuration sentinels, and recomputes changed
fixed-window reset schedules. Old migration checksums and historical account data
are not rewritten. Unchanged runtime-only writes have a trigger fast path.

## Verification and rollout

Tests cover native and bulk rendered forms, off/zero/null behavior, bounds, safe
operations, SQL mixed-statement atomicity, stale-counter preservation, final
admission, outer-transaction/outbox rollback, and Pass cooldown independence.
The Cline workflow requires the new tests by name and runs the entire frontend
suite with no skips. Read the exact delivered commit's logs before rollout.
These tests use synthetic credentials/transports and disposable PostgreSQL. No
production deployment, provider request, email delivery or account reset is run
as part of implementing this change.
