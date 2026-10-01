#!/usr/bin/env python3
"""Verify protected Cline foundation anchors after an upstream upgrade.
This is an integrity guard, not an end-to-end readiness or migration test.
"""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = {
    'backend/internal/domain/cline.go': ['PlatformCline = "cline"'],
    'backend/internal/pkg/cline/contract.go': ['func ValidateUpstreamModel(', 'ErrFreeAPIUnsupported', 'func ParseUsage(', 'func ParseCatalog('],
    'backend/internal/pkg/cline/ratelimit.go': ['func Classify(', 'func RateLimitKeys('],
    'backend/internal/pkg/cline/body.go': ['func GuardSSEBody(', 'HasGenerationError(payload)', 'io.ErrUnexpectedEOF', 'ErrSSEFrameTooLarge'],
    'backend/internal/pkg/cline/json_body.go': ['func GuardJSONBody(', 'ErrExpectedSSE', 'ErrJSONBodyTooLarge'],
    'backend/internal/pkg/cline/error_status.go': ['func GenerationErrorStatus(', 'HasGenerationError(payload)'],
    'backend/internal/service/cline_platform.go': ['func NormalizeClineCredentials(', 'func (a *Account) ValidateClineOutboundBody('],
    'backend/internal/service/account.go': ['return a.IsClineModelSupported(requestedModel)', 'return a.GetClineBaseURL()'],
    'backend/internal/service/model_rate_limit.go': ['cline.RateLimitKeys(a.GetClineMode(), modelKey)', 'ClineRateLimitScope(a, scope)'],
    'backend/internal/service/ratelimit_service.go': ['s.handleClineScopedError(', 'isClineScopedError('],
    'backend/internal/service/openai_gateway_cc_pipeline.go': ['account.ValidateClineOutboundBody(body)', 's.prepareClineResponseGuard(ctx, account, body, stream)', 'guardResponse(resp)'],
    'backend/internal/service/cline_response_guard.go': ['prepareClineResponseGuard(', 'decoder.UseNumber()', 'context.WithTimeout(context.WithoutCancel(ctx)', 'cline.GuardSSEBody(', 'cline.GuardJSONBody(', 'handleClineScopedUpstreamError('],
    'backend/internal/service/cline_platform_ratelimit.go': ['handleClineScopedUpstreamError(', 'SetClineRateLimitIfLater('],
    'backend/internal/repository/cline_account_state.go': ['SetClineRateLimitIfLater(', 'SaveClineStateIfUnchanged(', 'credentials=$5::jsonb', 'reset_authoritative', 'enqueueSchedulerOutbox('],
    'backend/internal/repository/account_repo.go': ['service.NormalizeClineCredentials('],
    'backend/internal/pkg/cline/body_test.go': ['TestClineSSEFailurePreventsSuccessfulTerminal'],
    'backend/internal/pkg/cline/body_event_test.go': ['TestClineSSECompleteEventFailures', 'TestClineSSEEmptyDataHeartbeatAndMissingDone'],
    'backend/internal/pkg/cline/json_body_test.go': ['TestClineJSON200FailureAndBounds'],
    'backend/internal/pkg/cline/error_status_test.go': ['TestClineGenerationErrorStatusOnlyUsesErrorEnvelopes'],
    'backend/internal/service/cline_platform_test.go': ['TestClineAccountDoesNotBecomeDeepseek', 'TestClineCredentialsAndScopedRotation'],
    'backend/internal/service/cline_response_guard_test.go': ['TestClineResponseGuardFreeScopeUsesActualModel', 'TestClineResponseGuardFreezesCredentialsAndCancellation', 'TestClineResponseGuardNeverInventsQuotaFromOutput'],
}
errors=[]
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
manifest=json.loads((ROOT/'.wanchuan/patches/manifest.json').read_text())
modules={item['id']:item for item in manifest['modules']}
for module in ('cline-platform','cline-rate-limit-cas'):
    if module not in modules or modules[module].get('review_on_upstream_touch') is not True:
        errors.append(f'module absent or review disabled: {module}')
if errors:raise SystemExit('\n'.join(errors))
print('Cline foundation integrity: PASS (does not imply full platform rollout readiness)')
