# Cline in the native account workflow

## Corrected architecture

Baseline: upstream `v2.10.0` (`5ca3cca21eeaf4ca8a694a7f2f8f0ecd9575c549`), with the deployed Cline fixes through `19d83b8bf17d1b9ce75e4c5b220176ba11b8480b`. Refactor branch: `codex/cline-native-account-flow-20261007`.

A provider-specific platform does not require a separate account management application. The previous toolbar shortcut and standalone create/edit/advanced forms duplicated native controls, serializers and lifecycle handling. They are removed, not merely hidden behind a renamed button. Cline is a platform tab in the original **Add account** form. Existing Cline rows open the original **Edit account** dialog. Batch changes use the original **Bulk edit** dialog.

Removed production modules: `ClineAccountModal.vue`, `ClineAdvancedSettings.vue`, `clineAccountSettings.ts`, `clineAdvancedSettings.ts`. Their old form tests are replaced by actual native create/edit/list/bulk tests. `clineAccountForm.ts` is now a provider-policy hook only; it has no account API client, common form state, or duplicate account serializer. Obsolete standalone controls and localization are not an alternate supported workflow.

## Reuse map / reviewed paths

| Concern | Existing implementation used | Cline-specific remainder |
|---|---|---|
| Entry / lifecycle | AccountsView, CreateAccountModal, EditAccountModal, BulkEditAccountModal; normal detail hydration, saved events and refresh pausing | Mode and credential-type fields inside the native API-key section |
| Create / update / batch | Original `adminAPI.accounts.create/update/bulkUpdate`, standard admin account handlers, AdminService and account repository | Deployed credential/extra delta semantics and provider validation; no new CRUD endpoint |
| Models | ModelWhitelistSelector, native mapping rows, useModelWhitelist; AccountGroupModelLimits for saved groups | Explicit IDs, mode boundaries, no wildcard or empty allow-all; invalid saved rows remain visible until repaired |
| Model discovery | Original `/accounts/models/sync-upstream-preview` and `/:id/models/sync-upstream`; AccountTestService | Mode-scoped catalog adapter through existing Cline metadata transport; no new sync API/service |
| Proxy / groups / common attributes | ProxySelector, GroupSelector, native rate/cost/load/concurrency/priority/expiry fields | No separate selector or common-settings draft |
| Local limits / alerts | QuotaLimitCard, QuotaDimensionRow, useQuotaNotifyState and the normal final account serializer | Only administrator configuration is converted to the deployed delta contract; no counter snapshot replay |
| Headers / errors / pause | HeaderOverrideEditor, credentialsBuilder, original custom error / temporary pause controls | Cline allowlist/size guards and protected auth/payment/quota error handling |
| Account operations | Original account action menu and TempUnschedStatusModal | Existing narrow local reset/pause methods preserve upstream quota blocks |
| Scheduling / billing | Shared account selection, OpenAIGatewayService, account state/outbox, common usage billing/ledger | Provider credential/subject quota admission, exact cooldown parsing, failure/stream termination guards |
| Provider information | Existing Cline metadata readout embedded in standard edit form | Read-only model catalog; no duplicate model editing or account saving |

The review follows all Cline integration paths above (frontend entry, credential and extra serializers, admin CRUD, model discovery, provider transport, scheduler/admission, local/upstream limits and usage finalization) against the native flow. It is not a claim that every unrelated subsystem of the repository has been manually proven correct.

## Discovery is not entitlement

The standard model sync buttons now work for Cline, instead of reporting that the provider is unsupported. The adapter validates connection settings, uses the configured shared transport/proxy, enforces the existing bounded body/timeout/redirect rules, and reads only the selected Pass/PAYG/Free public catalog bucket. Public catalog requests carry no saved API key or account token. A public listing does not establish credential entitlement or remaining quota. Failed discovery, wrong-mode IDs and malformed catalogs are errors, not guessed default models. Custom API origins require explicit model configuration; they are not silently treated as the official Cline origin.

The native selector keeps existing saved entries. Switching modes does not rewrite mapping targets. Responses arriving after platform, mode, key, base URL or selected account changes cannot populate the new draft. During editing, saved-account discovery is disabled while connection settings are changed but unsaved. The provider metadata section always describes the saved account; its catalog is read-only.

## Existing data / safety contracts

No database migration or data rewrite is introduced by this refactor. Existing Cline platform identifiers, account IDs, group IDs, API keys, usage logs, and native/legacy routes remain. No automatic conversion from old OpenAI/DeepSeek-mounted accounts occurs. A blank replacement credential keeps the server-side key. Create/edit/bulk use normal access control and audit paths.

Editing unrelated settings must not replay stale managed `extra`, quota counters, metadata or probed costs. Explicit false is distinct from omitted/inherited notification state; explicit clears use the deployed null sentinels for local quota configuration. Unknown saved proxy IDs and non-preset saved IANA timezones remain visible. A runtime proxy fallback is not persisted as the configured origin by an unrelated edit. Empty per-group Cline restrictions are rejected instead of silently becoming unrestricted.

The generic API-key create path previously bypassed the native `createAccountAndFinish` quota/notification finalizer. It now calls that same finalizer, with regression coverage, rather than copying quota serialization for Cline. All other provider flows remain covered by the full frontend suite.

Provider-specific safety is not redundant account management: Pass weekly/monthly parsing and monotonic cooldown persistence, credential rotation/subject isolation, failure-aware SSE/tool/reasoning handling, bounded cancellation and common usage settlement remain intact. Pool-mode same-account retries, paid fallback and unverified Free API inference remain disabled. Removing them merely to reduce the patch size would reintroduce known failures. Account Token remains a manually maintained credential, not a new OAuth-refresh implementation.

## Verification and rollout

Required tests now exercise the standard Add/Edit/Bulk dialogs and list entry, provider-only credential hook, native model-sync endpoints, late-response suppression, model whitelist preservation, generic quota serializer/notifications, and shared selector preservation. The existing Cline policy/service/race/PostgreSQL/42 HTTP lifecycle/offline migration suites remain required. Old standalone-form suites are removed because those forms no longer exist, not skipped to pass CI. Exact changed paths are checked against the pinned upstream manifest, and structural guards reject reintroduction of the four retired modules.

Use the completed CI artifacts for the exact delivered commit; this document does not assert an unrun test passed. Local/browser tests use synthetic fixtures and mocked responses, not production accounts. No live model call, key rotation, account enabling, production database access or deployment is part of this source refactor. Deploy only after testing the existing database backup in staging. The original upstream binary updater still cannot preserve compiled customizations automatically.
