"""Development transport only: immutable source + exact edits -> formatted blobs.
No candidate execution, checkout mutation, commits, ref movement or test claim.
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess
import urllib.error
import urllib.request
import yaml

REPO = '1057300248/sub2api'
BRANCH = 'refs/heads/codex/cline-platform-modular-20261001'
BASE = 'b9938376887715684177193a5448fdaeab86283b'
CANDIDATE = 'a9374a16b7b45b0624b346b1018326449ea05cee'
files = {}

def git(*args):
    return subprocess.check_output(['git', *args])

def read(path):
    if path not in files:
        files[path] = git('show', CANDIDATE + ':' + path).decode('utf-8')
    return files[path]

def edit(path, old, new, count=1):
    text = read(path)
    if text.count(old) != count:
        raise ValueError(f'{path}: expected {count} exact anchors, found {text.count(old)}: {old[:120]!r}')
    files[path] = text.replace(old, new)


def prepare():
    # Public source only. FETCH_HEAD is local development metadata, not a remote ref.
    subprocess.run(['git', 'fetch', '--no-tags', 'origin', CANDIDATE], check=True)
    for name in git('diff', '--name-only', BASE, CANDIDATE).decode().splitlines():
        if name != '.github/workflows/cline-closeout-inspect.yml':
            read(name)
    edit('backend/internal/pkg/cline/metadata.go', 'if len(body) > MaxBodyBytes {', 'if len(body) > MaxBodyBytes || HasGenerationError(body) {')
    edit('backend/internal/service/cline_metadata.go', '\tdecoder = json.NewDecoder(bytes.NewReader(encoded))', '\tcredentials = nil\n\tdecoder = json.NewDecoder(bytes.NewReader(encoded))')
    edit('backend/internal/service/openai_gateway_cc_pipeline.go', '\t// DeepSeek thinking mode', '\tif err := s.checkClineAdmission(ctx, account, body); err != nil {\n\t\treturn nil, err\n\t}\n\t// DeepSeek thinking mode')
    edit('backend/internal/service/cline_response_guard.go', '\tsnapshot := &Account{ID: account.ID, Platform: account.Platform, Type: account.Type, Credentials: snapshotCredentials}', '\tsnapshot := &Account{ID: account.ID, Platform: account.Platform, Type: account.Type, Credentials: snapshotCredentials}\n\tif state := account.GetClineState(); state != nil {\n\t\tsnapshot.Extra = map[string]any{ClineStateExtraKey: state}\n\t}')
    edit('backend/internal/service/cline_admission.go', 'var ErrClineObservedCooldown =', 'var ErrClineMetadataRequired = infraerrors.New(http.StatusConflict, "CLINE_METADATA_REQUIRED", "Refresh the saved Cline credential metadata before forwarding")\n\nvar ErrClineObservedCooldown =')
    edit('backend/internal/service/cline_admission.go', 'errors.Is(err, ErrClineMetadataChanged) {', 'errors.Is(err, ErrClineMetadataChanged) || errors.Is(err, ErrClineMetadataRequired) {')
    edit('backend/internal/repository/cline_metadata_repository.go', '\tif state == nil || !cline.ValidSubjectHash(state.Identity) {\n\t\treturn nil\n\t}', '\tif state == nil || !cline.ValidSubjectHash(state.Identity) {\n\t\tif cline.IsOfficialBase(current.GetClineBaseURL()) { return service.ErrClineMetadataRequired }\n\t\treturn nil\n\t}')
    # Fence in-flight errors to the identity captured before transport. Do not
    # attribute an old response to a newly discovered upstream active account.
    edit('backend/internal/repository/cline_account_state.go', "AND extra#>>'{cline_state,credential_fingerprint}'=$11\n", "AND extra#>>'{cline_state,credential_fingerprint}'=$11\n AND extra#>>'{cline_state,identity}'=$13 AND $13::text<>''\n")
    edit('backend/internal/repository/cline_account_state.go', 'service.ClineCredentialFingerprint(account), service.SchedulerOutboxEventAccountChanged)', 'service.ClineCredentialFingerprint(account), service.SchedulerOutboxEventAccountChanged, clineCapturedSubject(account))')
    files['backend/internal/repository/cline_account_state.go'] += '\nfunc clineCapturedSubject(account *service.Account) string {\n if state := account.GetClineState(); state != nil && cline.ValidSubjectHash(state.Identity) { return state.Identity }; return ""\n}\n'
    edit('backend/internal/repository/cline_account_state.go', 'query := `WITH changed AS (\n UPDATE accounts SET extra=jsonb_set(COALESCE(extra,\'{}\'::jsonb),\'{cline_state}\',$1::jsonb,true),updated_at=NOW()', 'query := `WITH previous AS (SELECT id,extra#>>\'{cline_state,identity}\' AS previous_identity FROM accounts WHERE id=$2 FOR UPDATE), changed AS (\n UPDATE accounts SET extra=jsonb_set(COALESCE(extra,\'{}\'::jsonb),\'{cline_state}\',$1::jsonb,true),updated_at=NOW()')
    edit('backend/internal/repository/cline_account_state.go', " WHERE id=$2 AND platform='cline' AND credentials=$3::jsonb AND deleted_at IS NULL", " FROM previous WHERE accounts.id=$2 AND accounts.id=previous.id AND platform='cline' AND credentials=$3::jsonb AND deleted_at IS NULL")
    edit('backend/internal/repository/cline_account_state.go', ' RETURNING id,extra\n ), observed AS (', ' RETURNING accounts.id,extra,previous.previous_identity\n ), observed AS (')
    edit('backend/internal/repository/cline_account_state.go', " WHERE $5::text ~ '^[0-9a-f]{64}$' AND left(e.key,length($7::text))=$7", " WHERE $5::text ~ '^[0-9a-f]{64}$' AND (previous_identity IS NULL OR previous_identity='' OR previous_identity=$5) AND left(e.key,length($7::text))=$7")
    edit('backend/internal/repository/cline_account_state.go', ' AND length(scope)<=300\n ON CONFLICT', ' AND length(scope)<=300 ORDER BY subject_hash,account_mode,scope\n ON CONFLICT')
    edit('backend/internal/repository/cline_closeout_integration_test.go', 'saved,err:=repo.SaveClineStateIfUnchanged(ctx,account,clineVerifiedState(account,subject,time.Now().UTC()))', 'state:=clineVerifiedState(account,subject,time.Now().UTC())\n\t\tsaved,err:=repo.SaveClineStateIfUnchanged(ctx,account,state)\n\t\taccount.Extra[service.ClineStateExtraKey]=state')
    # Persist failures are never logged with driver diagnostics/query parameters.
    edit('backend/internal/service/cline_platform_ratelimit.go', '"scope", limit.Scope, "error", err)', '"scope", limit.Scope)\n\t\tif s.runtimeBlocker != nil { s.runtimeBlocker.BlockAccountScheduling(account, time.Now().Add(5*time.Minute), "cline_state_persist_failed") }')
    # Existing protocol tests still execute real forwarding; only the newly
    # required durable-admission interface is replaced by an explicit test fake.
    for path in ['backend/internal/service/cline_routing_test.go','backend/internal/service/cline_account_probe_test.go']:
        text=read(path)
        target='httpUpstream: upstream'
        if target not in text: raise ValueError('missing test transport anchor: '+path)
        files[path]=text.replace(target,target+', accountRepo: &clineAdmissionTestRepository{}')
    edit('backend/internal/server/routes/admin.go', 'accounts := admin.Group("/accounts")', 'accounts := admin.Group("/accounts")\n\tregisterClineAccountRoutes(accounts, h)')
    edit('backend/internal/handler/admin/account_data.go', 'Extra:               service.RedactOpenAICodexTicketExtra(acc.Extra),', 'Extra:               portableClineExtra(acc.Platform, service.RedactOpenAICodexTicketExtra(acc.Extra)),')
    edit('backend/internal/handler/admin/account_data.go', 'func validateDataAccount(item DataAccount) error {', 'func validateDataAccount(item DataAccount) error {\n\tif err := validateClineDataAccount(item); err != nil { return err }')
    edit('backend/internal/service/channel_monitor_quota_fetcher.go', 'case domain.PlatformOpenCodeGo:', 'case domain.PlatformCline:\n\t\treturn ClineMonitorQuotaSnapshot(account, time.Now().UTC())\n\tcase domain.PlatformOpenCodeGo:')
    edit('backend/internal/service/channel_monitor_validate.go', 'case domain.PlatformOpenCodeGo:', 'case domain.PlatformCline:\n\t\treturn clineMonitorCapability(account)\n\tcase domain.PlatformOpenCodeGo:')
    # Lossless JSON round-trips in credential-bearing offline snapshots.
    edit('backend/internal/clinemigration/migration.go', 'import (\n', 'import (\n\t"bytes"\n', 1)
    edit('backend/internal/clinemigration/migration.go', 'if json.Unmarshal(credentials,&s.Credentials)!=nil || json.Unmarshal(extra,&s.Extra)!=nil', 'if decodeExact(credentials,&s.Credentials)!=nil || decodeExact(extra,&s.Extra)!=nil')
    edit('backend/internal/clinemigration/migration.go', 'if json.Unmarshal(beforeJSON,&before)!=nil', 'if decodeExact(beforeJSON,&before)!=nil')
    files['backend/internal/clinemigration/migration.go'] += '\nfunc decodeExact(body []byte, target any) error { decoder:=json.NewDecoder(bytes.NewReader(body));decoder.UseNumber();return decoder.Decode(target) }\n'
    edit('backend/internal/clinemigration/migration.go', '\t\tif value,exists:=limit["rate_limit_reset_at"];exists{', '\t\tif value,exists:=limit["reset_unix"];exists{\n\t\t\tnumber,ok:=value.(json.Number);if !ok{return nil,ErrSpecification};seconds,err:=number.Int64();if err!=nil{return nil,ErrSpecification};at:=time.Unix(seconds,0).UTC();if latest==nil||at.After(*latest){latest=&at}\n\t\t}\n\t\tif value,exists:=limit["rate_limit_reset_at"];exists{')
    # Opt-in saved-account panel; unsaved secrets never enter metadata requests.
    modal='frontend/src/components/account/ClineAccountModal.vue'
    edit(modal, "import BaseDialog from '@/components/common/BaseDialog.vue'", "import BaseDialog from '@/components/common/BaseDialog.vue'\nimport ClineMetadataPanel from './ClineMetadataPanel.vue'")
    edit(modal, "const saving = ref(false)", "const saving = ref(false)\nconst showMetadata = ref(false)\nfunction addCatalogModel(id: string) {\n  if (saving.value || draft.models.some(row => row.publicID === id)) return\n  draft.models.push({ publicID: id, upstreamID: id })\n}")
    edit(modal, "  if (!show) { draft.apiKey = ''; return }", "  showMetadata.value = false\n  if (!show) { draft.apiKey = ''; return }")
    edit(modal, '      <p class="text-xs text-gray-500">{{ t(\'clineAccount.noProbe\') }}</p>', '      <template v-if="account && show">\n        <button v-if="!showMetadata" type="button" class="btn btn-secondary" :disabled="saving" @click="showMetadata = true">{{ t(\'clineMetadata.open\') }}</button>\n        <ClineMetadataPanel v-if="showMetadata" :account-id="account.id" :mode="draft.mode" @select="addCatalogModel" />\n      </template>\n      <p class="text-xs text-gray-500">{{ t(\'clineAccount.noProbe\') }}</p>')
    for language in ['en','zh']:
        name=f'frontend/src/i18n/locales/{language}/index.ts'
        edit(name,"import clineAccount from './clineAccount'","import clineAccount from './clineAccount'\nimport clineMetadata from './clineMetadata'")
        edit(name,'  clineAccount,','  clineAccount,\n  clineMetadata,')
    edit('Makefile', 'src/components/account/__tests__/ClineAccountModal.spec.ts', 'src/components/account/__tests__/ClineMetadataPanel.spec.ts \\\n\t\tsrc/components/account/__tests__/ClineAccountModal.spec.ts')
    panel='frontend/src/components/account/ClineMetadataPanel.vue'
    edit(panel,'const windows =', 'const clock = ref(Date.now())\nconst timer = setInterval(() => { clock.value = Date.now() }, 1000)\nconst windows =')
    edit(panel,'onBeforeUnmount(() => { sequence++; controller?.abort() })','onBeforeUnmount(() => { sequence++; controller?.abort(); clearInterval(timer) })')
    edit(panel,'function percent(kind: string): string {', "function percent(kind: string): string {\n  const at = Date.parse(metadata.value?.last_success_at || '')\n  if (error.value || !Number.isFinite(at) || clock.value - at >= 300000 || at > clock.value + 120000) return '—'")
    edit('frontend/src/components/account/__tests__/ClineMetadataPanel.spec.ts', "mode: 'pass', auth_type:", "mode: 'pass', last_success_at: new Date().toISOString(), auth_type:")
    # Upgrade integrity covers the wired call sites, not only helper existence.
    verify='tools/verify_cline_platform.py'
    edit(verify,"'enqueueSchedulerOutbox('","'INSERT INTO scheduler_outbox'")
    extra={
        'backend/internal/service/cline_metadata.go':['RefreshClineMetadata(', 'WithHTTPUpstreamRedirectsDisabled', 'ClineMetadataForAccount('],
        'backend/internal/service/openai_gateway_cc_pipeline.go':['s.checkClineAdmission(ctx, account, body)'],
        'backend/internal/repository/cline_metadata_repository.go':['CheckClineAdmission(', 'cline_shared_limits', 'ClaimClineMetadataRefresh('],
        'backend/internal/server/routes/admin.go':['registerClineAccountRoutes(accounts, h)'],
        'backend/internal/server/routes/cline_admin.go':['GetClineMetadata', 'RefreshClineMetadata'],
        'backend/internal/handler/admin/account_data.go':['validateClineDataAccount(item)', 'portableClineExtra(acc.Platform'],
        'backend/internal/clinemigration/migration.go':['func Preview(', 'func Apply(', 'func Rollback(', 'pg_try_advisory_xact_lock', 'ALL_WORKERS_STOPPED'],
        'backend/migrations/265_cline_credential_write_guard.sql':['wanchuan_cline_guard_credentials'],
        'frontend/src/components/account/ClineAccountModal.vue':['ClineMetadataPanel'],
    }
    marker='errors=[]'
    addition='\nfor _name, _anchors in '+repr(extra)+'.items():\n    REQUIRED.setdefault(_name, []).extend(_anchors)\n\n'
    edit(verify,marker,addition+marker)
    manifest='.wanchuan/patches/manifest.json'
    data=json.loads(read(manifest));module=next(m for m in data['modules'] if m['id']=='cline-platform')
    for glob in ['backend/internal/clinemigration/**','backend/cmd/cline-migrate/**','backend/migrations/26[4-6]_cline*.sql','backend/internal/server/routes/cline_admin.go','backend/internal/server/routes/admin.go','backend/internal/handler/admin/account_data.go','backend/internal/service/channel_monitor_quota_fetcher.go','backend/internal/service/channel_monitor_validate.go','frontend/src/api/admin/clineMetadata.ts','frontend/src/i18n/locales/*/clineMetadata.ts']:
        if glob not in module['protected_globs']:module['protected_globs'].append(glob)
    files[manifest]=json.dumps(data,indent=2)+'\n'
    # Preserve all existing checks; expand exact test execution assertions.
    workflow='.github/workflows/cline-platform-development.yml'
    wf=yaml.safe_load(read(workflow))
    if True in wf:wf['on']=wf.pop(True)
    steps=wf['jobs']['integration']['steps']
    for step in steps:
        run=step.get('run','')
        if step.get('name')=='Check exact source and formatting without mutation':
            run=run.replace('gofmt -l ', 'gofmt -l backend/internal/clinemigration/*.go backend/cmd/cline-migrate/*.go backend/internal/handler/admin/account_cline*.go backend/internal/server/routes/cline_admin.go ',1)
        if step.get('name')=='Cline service and legacy cooldown regression tests':
            run=run.replace('required = {',"required = {'TestClineMetadataRefreshUsesBoundedCredentialSafeGETs', 'TestClineMetadataErrorsPreserveUnknownAndLastValidCatalog', 'TestClineMetadataBoundariesAndPersistenceFailure', 'TestClineMetadataExpiryAndIdentityRotation', 'TestClineAdmissionFailsClosedWithoutDurableState', 'TestClineMonitorCannotTurnUnknownIntoHealthyZero', 'TestClinePartialFailurePreservesObservedUsageAndBillingIdentity', ",1)
        if step.get('name')=='Cline registration and write ownership regressions':
            run=run.replace('required = {',"required = {'TestClineMetadataHandlerIsLocalAndRedacted', 'TestClinePortableDataRetainsModesButNotManagedGrants', ",1)
        if step.get('name')=='Settings API contract regression and server build':
            run+='\ngo test -v -tags=unit ./internal/clinemigration\ngo build -o "$RUNNER_TEMP/cline-migrate" ./cmd/cline-migrate\n'
        if run:step['run']=run
    for step in wf['jobs']['postgres']['steps']:
        if step.get('name')=='Actual Cline PostgreSQL CAS and migration regressions':
            step['run']=step['run'].replace('required = {',"required = {'TestClinePostgresSharedSubjectLimitsAndRestart', 'TestClinePostgresRefreshLeaseAndObservationOrdering', 'TestClinePostgresOutboxFailureRollsBackState', 'TestClinePostgresDirectAndBulkCredentialGuards', 'TestClinePostgresAdmissionRejectsStaleCredentials', ",1)
    wf['jobs']['offline-migration']={
      'if': "github.repository == '1057300248/sub2api' && github.ref == 'refs/heads/codex/cline-platform-modular-20261001'",
      'runs-on':'ubuntu-latest','timeout-minutes':20,'env':{'CLINE_MIGRATION_TEST_DSN':'postgres://cline_fixture:fixture_only@127.0.0.1:55432/cline_fixture?sslmode=disable'},
      'steps':[
        {'uses':'actions/checkout@v6','with':{'ref':'${{ github.sha }}','persist-credentials':False}},
        {'uses':'actions/setup-go@v6','with':{'go-version-file':'backend/go.mod','cache-dependency-path':'backend/go.sum'}},
        {'name':'Start isolated disposable migration database','run':'docker run --detach --rm --name cline-migration-fixture -e POSTGRES_USER=cline_fixture -e POSTGRES_PASSWORD=fixture_only -e POSTGRES_DB=cline_fixture -p 127.0.0.1:55432:5432 postgres:16'},
        {'name':'Execute real migration transaction regressions','working-directory':'backend','shell':'bash','run':'''set -euo pipefail
go test -json -tags=integration -count=3 ./internal/clinemigration | tee "$RUNNER_TEMP/cline-migration-tests.jsonl"
python3 - <<'PYTEST'
import collections,json,os
from pathlib import Path
events=[json.loads(line) for line in (Path(os.environ['RUNNER_TEMP'])/'cline-migration-tests.jsonl').read_text().splitlines() if line.strip()]
passed=collections.Counter(e.get('Test') for e in events if e.get('Action')=='pass')
required={'TestClineMigrationPostgresApplyRollbackAndIdempotency','TestClineMigrationPostgresRejectsDriftAndUnsafeRollback','TestClineMigrationPostgresFailureIsAtomic','TestClineMigrationPostgresRequiresQuiescenceAndKeepsCooldown'}
missing=sorted(t for t in required if passed[t]!=3)
if missing or any(e.get('Action')=='skip' for e in events):raise SystemExit(f'migration tests missing/skipped: {missing}')
print('Four real PostgreSQL migration tests each passed three times; no live database used.')
PYTEST
'''},
        {'name':'Remove disposable migration database','if':'always()','run':'docker rm --force cline-migration-fixture || true'},
        {'name':'Verify checked source remained unchanged','env':{'EXPECTED_SHA':'${{ github.sha }}'},'run':'test "$(git rev-parse HEAD)" = "$EXPECTED_SHA"\ngit diff --exit-code\n'}]}
    files[workflow]=yaml.safe_dump(wf,sort_keys=False,width=120)
    status='.wanchuan/CLINE_PLATFORM_STATUS.md'
    files[status]='''# Cline platform status

Development branch: `codex/cline-platform-modular-20261001`.

## Closeout candidate

Independent routing, explicit account management, three text-protocol bridges, credential-scoped limits and the prior protocol/SQL regressions remain in place. This closeout adds opt-in read-only metadata/catalog/quota controls; bounded official GETs, last-valid cache and unknown windows; verified active-account shared cooldowns and durable fail-closed admission; atomic limit/state/outbox persistence; direct/bulk credential write guards; portable-state import/export isolation; cached-only Cline quota monitoring; and an offline preview/apply/rollback migration CLI.

Migration is deliberately not an automatic online conversion. It requires disabled/drained accounts, stopped workers, exact preview approval, a direct administrative PostgreSQL connection and unchanged configuration/permissions. It preserves account IDs, pricing/history and binding restrictions; it does not grant access or rebind customer keys. Both directions keep the account unschedulable and retain conservative cooldowns. Details: `.wanchuan/CLINE_CLOSEOUT_RUNBOOK.md`.

All new code must pass the exact current SHA's normal read-only CI. The development source/blob preparation workflow is not validation and is removed from the final tree. No local execution was possible during this closeout; no local test pass is claimed. Tests cover metadata error boundaries, authenticated GET redaction, protocol partial-failure billing, SQL leases/shared limits/outbox rollback/credential guards and isolated migration transactions. The fixture-backed monetary test is not a real-provider billing reconciliation.

## Operational acceptance still requires authorization

No real account/key was probed, no paid inference was requested, and no live account was migrated. No main/stable ref, release channel or production deployment was changed. Free public API forwarding and paid fallback remain disabled. Exact-SHA synthetic/disposable-database CI cannot establish real upstream entitlement or production deployment readiness. Use the staged, reversible acceptance runbook after explicit approval.
'''
    return files

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs): raise ValueError('unexpected redirect')

def main():
    if os.environ.get('GITHUB_REPOSITORY')!=REPO or os.environ.get('GITHUB_REF')!=BRANCH:raise ValueError('unexpected repository/ref')
    if git('rev-parse','HEAD').decode().strip()!=os.environ['GITHUB_SHA']:raise ValueError('incorrect checkout')
    prepared=prepare()
    receipts=[]
    opener=urllib.request.build_opener(NoRedirect())
    for name,text in sorted(prepared.items()):
        if name.startswith('.github/workflows/cline-closeout-'):continue
        body=text.encode()
        if name.endswith('.go'):body=subprocess.check_output(['gofmt'],input=body)
        expected=hashlib.sha1(b'blob '+str(len(body)).encode()+b'\0'+body).hexdigest()
        request=urllib.request.Request('https://api.github.com/repos/'+REPO+'/git/blobs',data=json.dumps({'encoding':'base64','content':base64.b64encode(body).decode()}).encode(),headers={'Authorization':'Bearer '+os.environ['GH_TOKEN'],'Accept':'application/vnd.github+json','Content-Type':'application/json'},method='POST')
        with opener.open(request,timeout=30) as response:saved=json.load(response)
        if saved.get('sha')!=expected:raise ValueError('blob mismatch: '+name)
        receipts.append({'path':name,'sha':expected})
        print(name+' '+expected,flush=True)
    subprocess.run(['git','diff','--exit-code'],check=True)
    out=Path(os.environ['RUNNER_TEMP'])/'cline-closeout-blobs.json'
    out.write_text(json.dumps({'event_sha':os.environ['GITHUB_SHA'],'candidate':CANDIDATE,'files':receipts},indent=2)+'\n')
    print('Immutable reviewed blobs prepared only; no candidate execution, ref update or validation claim.')

if __name__=='__main__':
    try:main()
    except urllib.error.HTTPError as error:raise SystemExit('blob transfer HTTP '+str(error.code)) from None
