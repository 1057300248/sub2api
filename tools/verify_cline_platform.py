#!/usr/bin/env python3
"""Verify protected Cline foundation anchors after an upstream upgrade.
This is an integrity guard, not an end-to-end readiness or migration test.
"""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = {
    'frontend/src/components/account/CreateAccountModal.vue': ['id="create-account-form"', 'createAccountAndFinish(form.platform, form.type, credentials, extra)', 'adminAPI.accounts.create', 'selectClinePlatform'],
    'frontend/src/components/account/clineAccountForm.ts': ['cline_auth_type', 'cline_free_api_enabled: false', 'clineModelError', 'applyClineCredentialFields', 'clineExtraDelta'],
    'frontend/src/views/admin/AccountsView.vue': ['<CreateAccountModal', '<EditAccountModal', 'clineModeLabel'],
    'frontend/src/constants/platforms.ts': ["value: 'cline'"],
    'Makefile': ['CreateAccountModal.spec.ts', 'EditAccountModal.spec.ts', 'clineAccountForm.spec.ts', 'ModelWhitelistSelector.spec.ts'],
    'backend/internal/domain/cline.go': ['PlatformCline = "cline"'],
    'backend/internal/pkg/cline/contract.go': ['func ValidateUpstreamModel(', 'ErrFreeAPIUnsupported', 'func ParseUsage(', 'func ParseCatalog('],
    'backend/internal/pkg/cline/ratelimit.go': ['func Classify(', 'func RateLimitKeys('],
    'backend/internal/pkg/cline/body.go': ['func GuardSSEBody(', 'HasGenerationError(payload)', 'io.ErrUnexpectedEOF', 'ErrSSEFrameTooLarge'],
    'backend/internal/pkg/cline/json_body.go': ['func GuardJSONBody(', 'ErrExpectedSSE', 'ErrJSONBodyTooLarge'],
    'backend/internal/pkg/cline/error_status.go': ['func GenerationErrorStatus(', 'HasGenerationError(payload)'],
    'backend/internal/service/cline_platform.go': ['func NormalizeClineCredentials(', 'func (a *Account) ValidateClineOutboundBody('],
    'backend/internal/service/cline_custom_parameters.go': ['mergeClineCustomRequestParameters(', 'jsonStructFieldNames(apicompat.ChatCompletionsRequest{})', 'validClineCustomParameterName('],
    'backend/internal/service/account.go': ['return a.IsClineModelSupported(requestedModel)', 'return a.GetClineBaseURL()'],
    'backend/internal/service/model_rate_limit.go': ['cline.RateLimitKeys(a.GetClineMode(), modelKey)', 'ClineRateLimitScope(a, scope)'],
    'backend/internal/service/ratelimit_service.go': ['s.handleClineScopedError(', 'clineProtectedError('],
    'backend/internal/service/openai_gateway_cc_pipeline.go': ['account.ValidateClineOutboundBody(body)', 's.prepareClineResponseGuard(ctx, account, body, stream)', 'guardResponse(resp)'],
    'backend/internal/service/cline_response_guard.go': ['prepareClineResponseGuard(', 'decoder.UseNumber()', 'context.WithTimeout(context.WithoutCancel(ctx)', 'cline.GuardSSEBody(', 'cline.GuardJSONBody(', 'handleClineScopedUpstreamError('],
    'backend/internal/service/cline_platform_ratelimit.go': ['handleClineScopedUpstreamError(', 'SetClineRateLimitIfLater('],
    'backend/internal/repository/cline_account_state.go': ['SetClineRateLimitIfLater(', 'SaveClineStateIfUnchanged(', 'credentials=$5::jsonb', 'reset_authoritative', 'INSERT INTO scheduler_outbox'],
    'backend/internal/repository/account_repo.go': ['service.NormalizeClineCredentials(', 'service.PreserveClineStateExtra(', 'clineExtraUpdateSQL('],
    'backend/internal/pkg/cline/body_test.go': ['TestClineSSEFailurePreventsSuccessfulTerminal'],
    'backend/internal/pkg/cline/body_event_test.go': ['TestClineSSECompleteEventFailures', 'TestClineSSEEmptyDataHeartbeatAndMissingDone'],
    'backend/internal/pkg/cline/json_body_test.go': ['TestClineJSON200FailureAndBounds'],
    'backend/internal/pkg/cline/error_status_test.go': ['TestClineGenerationErrorStatusOnlyUsesErrorEnvelopes'],
    'backend/internal/service/cline_platform_test.go': ['TestClineAccountDoesNotBecomeDeepseek', 'TestClineCredentialsAndScopedRotation'],
    'backend/internal/service/cline_response_guard_test.go': ['TestClineResponseGuardFreeScopeUsesActualModel', 'TestClineResponseGuardFreezesCredentialsAndCancellation', 'TestClineResponseGuardNeverInventsQuotaFromOutput'],
    'backend/internal/service/cline_routing.go': ['SupportsClineEndpointCapability(', 'ClineModelIDs('],
    'backend/internal/service/cline_routing_test.go': ['TestClineRoutingRegistrationAndCapabilities', 'TestClineThreeProtocolHTTPForwarding', 'TestClineThreeProtocolFailuresNeverFinalizeSuccessfully', 'TestClineThreeProtocolRejectsUnentitledModelsBeforeNetwork', 'TestClineTokenCountNeverCallsNativeUpstream', 'TestClineCustomRequestParametersReachFinalChatBody', 'TestClineCustomRequestParametersCannotOverrideChatCoreFields'],
    'backend/internal/service/cline_account_probe.go': ['testClineAccountConnection(', 'sendCCUpstreamRequest(', 'ValidateClineOutboundBody('],
    'backend/internal/service/account_test_service.go': ['s.testClineAccountConnection('],
    'backend/internal/service/openai_gateway_scheduling.go': ['PlatformCline'],
    'backend/internal/server/routes/gateway.go': ['service.PlatformCline'],
    'backend/internal/repository/cline_scheduler_credentials.go': ['filterSchedulerAccountCredentials(', 'cline_auth_type'],
    'backend/internal/repository/scheduler_cache.go': ['filterSchedulerAccountCredentials(&account)'],
    'backend/internal/repository/cline_managed_extra.go': ['clineManagedExtraDeltaSQL(', "'cline_state' - 'cline_route' - 'model_rate_limits'"],
    'backend/internal/repository/cline_postgres_integration_test.go': ['TestClinePostgresConcurrentScopedCAS', 'TestClinePostgresCredentialRotationRejectsStaleObservations', 'TestClinePostgresManagedStateSurvivesAllExtraEdits', 'TestClinePostgresCreationCannotImportManagedState', 'TestClinePostgresMigrationAndQuotaSchema'],
    'backend/internal/handler/cline_dispatch_test.go': ['TestClineStandaloneAndCompositeDispatch'],
    'backend/internal/handler/admin/account_cline_registration_test.go': ['TestClineGroupAndCompositeRequestBindings'],
}

for _name, _anchors in {'backend/internal/service/cline_metadata.go': ['RefreshClineMetadata(', 'WithHTTPUpstreamRedirectsDisabled', 'ClineMetadataForAccount('], 'backend/internal/service/openai_gateway_cc_pipeline.go': ['s.checkClineAdmission(ctx, account, body)'], 'backend/internal/repository/cline_metadata_repository.go': ['CheckClineAdmission(', 'cline_shared_limits', 'ClaimClineMetadataRefresh('], 'backend/internal/server/routes/admin.go': ['registerClineAccountRoutes(accounts, h)'], 'backend/internal/server/routes/cline_admin.go': ['GetClineMetadata', 'RefreshClineMetadata'], 'backend/internal/handler/admin/account_data.go': ['validateClineDataAccount(item)', 'portableClineExtra(acc.Platform'], 'backend/internal/clinemigration/migration.go': ['func Preview(', 'func Apply(', 'func Rollback(', 'pg_try_advisory_xact_lock', 'ALL_WORKERS_STOPPED'], 'backend/migrations/265_cline_credential_write_guard.sql': ['wanchuan_cline_guard_credentials'], 'frontend/src/components/account/EditAccountModal.vue': ['ClineMetadataPanel']}.items():
    REQUIRED.setdefault(_name, []).extend(_anchors)

for name, anchors in {
    'backend/internal/service/cline_request_lifetime.go': ['context.WithDeadlineCause(', 'p.Deadline()', 'ErrClineCallerDeadline', 'clineBodyDisconnectResult('],
    'backend/internal/service/openai_gateway_responses_chat_fallback.go': ['beginClineBodyDrain(resp.Body)', 'ClientDisconnect:', 'clineBodyClientDisconnected(resp.Body, clientDisconnected)'],
    'backend/internal/service/openai_gateway_messages_chat_fallback.go': ['beginClineBodyDrain(resp.Body)', 'clineBodyClientDisconnected(resp.Body, clientDisconnected)'],
    'backend/internal/handler/cline_forward_termination.go': ['func finishClineForward(', 'ErrClineCallerDeadline', 'submit(result)'],
    'backend/internal/handler/openai_gateway_handler.go': ['finishClineForward(c, account, result, err, submitResponsesUsage', 'finishClineForward(c, account, result, err, submitMessagesUsage'],
    'backend/internal/handler/openai_chat_completions.go': ['finishClineForward(c, account, result, err, submitChatUsage'],
    'backend/internal/handler/cline_http_lifecycle_integration_test.go': ['TestClineHTTPHandlersDurableBillingAndTermination', 'repository.ApplyMigrations', 'repository.NewUsageBillingRepository', 'router.ServeHTTP', 'usage_billing_dedup'],
}.items():
    REQUIRED.setdefault(name, []).extend(anchors)

# Visible entry and common-setting anchors must survive upstream transplants.
# Actual rendered component/API assertions live in the required CI suites.
for name, anchors in {
    'frontend/src/components/account/CreateAccountModal.vue': ['data-testid="create-platform-cline"', '<ModelWhitelistSelector', '<QuotaLimitCard', '<HeaderOverrideEditor'],
    'frontend/src/components/account/EditAccountModal.vue': ['HeaderOverrideEditor', 'AccountGroupModelLimits', '<ProxySelector', '<GroupSelector', 'submitUpdateAccount', 'clineGroupModelError'],
    'frontend/src/components/account/clineAccountForm.ts': ['clineExtraDelta', 'cost_multiplier', 'validateClineHeaderRows'],
    'frontend/src/components/account/AccountUsageCell.vue': ['ClineAccountUsageCell'],
    'backend/internal/service/cline_account_settings.go': ['mergeClineAccountCredentials', 'applyClineSchedulingSettings', 'validateClineAccountHeaderSettings'],
    'backend/migrations/268_cline_header_settings_guard.sql': ['cline_guard_header_settings', 'cardinality(seen)', 'encoded_bytes>16384'],
    'backend/internal/handler/dto/account_cline_view.go': ['ClineMetadataForAccount', 'view.Catalog = nil'],
}.items():
    REQUIRED.setdefault(name, []).extend(anchors)

for name, anchors in {
    'frontend/src/components/account/BulkEditAccountModal.vue': ['adminAPI.accounts.bulkUpdate', '<HeaderOverrideEditor', 'validateClineHeaderRows'],
    'backend/internal/service/cline_advanced_settings.go': ['ValidateClineLocalQuotaSettings', 'clineProtectedError', 'PreserveClineLocalQuotaRuntime', 'isClineScopedError('],
    'backend/internal/repository/cline_advanced_operations.go': ['ResetClineLocalQuota', 'ClearClineTemporaryPause', 'INSERT INTO scheduler_outbox'],
    'backend/migrations/269_cline_advanced_settings_guard.sql': ['cline_guard_advanced_settings', 'reset_changed', 'custom_error_codes'],
}.items():
    REQUIRED.setdefault(name, []).extend(anchors)

# A future upgrade must not silently reintroduce the parallel account UI.
for filename in ('ClineAccountModal.vue', 'ClineAdvancedSettings.vue', 'clineAccountSettings.ts', 'clineAdvancedSettings.ts'):
    assert not (ROOT/'frontend/src/components/account'/filename).exists(), f'Retired duplicate: {filename}'
for filename in ('CreateAccountModal.vue', 'EditAccountModal.vue', 'BulkEditAccountModal.vue'):
    text = (ROOT/'frontend/src/components/account'/filename).read_text()
    assert 'ClineAccountModal' not in text and 'ClineAdvancedSettings' not in text, filename
assert 'create-cline-account' not in (ROOT/'frontend/src/views/admin/AccountsView.vue').read_text()
assert 'accountsAPI' not in (ROOT/'frontend/src/components/account/clineAccountForm.ts').read_text()
for name, anchors in {
    'backend/internal/service/upstream_models.go': ['s.syncClineUpstreamModelCatalog(ctx, account)'],
    'backend/internal/service/cline_upstream_models.go': ['fetchClineMetadata(ctx, account, cline.CatalogURL, false)', 'cline.ParseCatalog', 'catalog.Models(mode)'],
    'frontend/src/components/account/ModelWhitelistSelector.vue': ['syncEpoch', 'syncDisabled', 'accountsAPI.syncUpstreamModelsPreview'],
}.items():
    REQUIRED.setdefault(name, []).extend(anchors)

errors=[]
for name, anchors in {
    'backend/internal/service/openai_gateway_cc_pipeline.go': ['normalizeClineFinalRequest(', 'configureClineReasoning(', 'observeClineProviderResponse('],
    'backend/internal/service/cline_request_contract.go': ['validateClineLoweredToolChoice(', 'request["stream"] = json.RawMessage("false")'],
    'backend/internal/service/cline_reasoning_replay.go': ['cline.NewReasoningCodec(', 'state.codec.Open(', 'key.UserID', 'attachClineResponsesReasoningEvents('],
    'backend/internal/pkg/cline/auth.go': ['func WireCredential(', 'AuthAccountToken', 'return "workos:" + key'],
    'backend/internal/service/cline_platform.go': ['GetClineWireCredential(', 'cline.WireCredential('],
    'backend/internal/repository/cline_reauthentication.go': ['credentials=$2::jsonb', 'reauth_required', 'INSERT INTO scheduler_outbox'],
    'backend/internal/repository/cline_provider_observation.go': ['credentials=$2::jsonb', 'observed_unix_ns'],
    'backend/migrations/270_cline_official_application_headers.sql': ['http-referer', 'x-title', 'encoded_bytes>16384'],
}.items():
    REQUIRED.setdefault(name, []).extend(anchors)

for name, anchors in REQUIRED.items():
    path=ROOT/name
    if not path.is_file():errors.append(f'missing: {name}');continue
    text=path.read_text()
    for anchor in anchors:
        if anchor not in text:errors.append(f'{name}: missing protected anchor {anchor!r}')
pipeline_path=ROOT/'backend/internal/service/openai_gateway_cc_pipeline.go'
if pipeline_path.is_file():
    pipeline=pipeline_path.read_text()
    prepare=pipeline.find('s.prepareClineResponseGuard(ctx, account, body, stream)')
    send=pipeline.find('s.doOpenAIUpstream(upstreamReq, proxyURL, account)')
    guard=pipeline.find('guardResponse(resp)')
    if not (0 <= prepare < send < guard):
        errors.append('Cline request identity must be captured before transport and guarded after it')
manifest=json.loads((ROOT/'docs/cline/patches.json').read_text())
# Account development is also performed on Windows. Case-only file pairs
# would pass Linux tests but overwrite each other in a case-insensitive checkout.
case_paths = {}
for name in manifest['allowed_changed_files']:
    folded = name.casefold()
    if folded in case_paths and case_paths[folded] != name:
        errors.append(f'case-insensitive path collision: {case_paths[folded]} / {name}')
    case_paths[folded] = name
modules={item['id']:item for item in manifest['modules']}
for module in ('cline-platform','cline-rate-limit-cas'):
    if module not in modules or modules[module].get('review_on_upstream_touch') is not True:
        errors.append(f'module absent or review disabled: {module}')
if errors:raise SystemExit('\n'.join(errors))
print('Cline foundation integrity: PASS (does not imply full platform rollout readiness)')
