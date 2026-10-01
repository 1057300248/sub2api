"""Temporary development-only wiring, removed before exact-SHA validation."""
import importlib.util
import json
import os
import urllib.error
import yaml

spec=importlib.util.spec_from_file_location('cline_prepare','.wanchuan/cline-closeout-prepare.py')
m=importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
m.CANDIDATE='01aeb74c175523776af09f5637d7a005a886b3ba'
original_edit=m.edit

def reviewed_edit(path,old,new,count=1):
    if path=='backend/internal/service/channel_monitor_validate.go' and old=='case domain.PlatformOpenCodeGo:':
        old=old.replace('domain.','')
        new=new.replace('domain.','')
    return original_edit(path,old,new,count)
m.edit=reviewed_edit
original_prepare=m.prepare

def complete_prepare():
    result=original_prepare()
    m.edit('backend/internal/pkg/cline/body.go','return nil, ErrStreamFailure','return failureUsageEvent(data), ErrStreamFailure')
    m.edit('backend/internal/service/cline_billing_test.go','Header:http.Header{"Content-Type":[]string{"text/event-stream"}}','Header:http.Header{"Content-Type":[]string{"text/event-stream"},"X-Request-Id":[]string{"cline-billing-stable"}}')
    # Exercise usage located in the terminal failure itself as well as in a
    # preceding partial chunk, using the same unchanged monetary pipeline.
    test=m.read('backend/internal/service/cline_billing_test.go')
    terminal=test.replace('func TestClinePartialFailurePreservesObservedUsageAndBillingIdentity','func TestClineTerminalFailureUsageIsNotLostOrDoubleCounted',1)
    terminal=terminal.replace('"generation failed\\\"}}','"generation failed\\\"}}') if False else terminal
    original='payload:="data: "+partial+"\\n\\ndata: {\\\"error\\\":{\\\"message\\\":\\\"generation failed\\\"}}\\n\\ndata: [DONE]\\n\\n"'
    replacement='payload:="data: "+partial+"\\n\\ndata: {\\\"error\\\":{\\\"message\\\":\\\"generation failed\\\"},\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":4,\\\"total_tokens\\\":12}}\\n\\ndata: [DONE]\\n\\n"'
    if terminal.count(original)!=1:raise ValueError('terminal billing fixture anchor mismatch')
    terminal=terminal.replace(original,replacement)
    terminal=terminal.replace(',"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`','}`',1)
    function=terminal[terminal.index('func TestClineTerminalFailureUsageIsNotLostOrDoubleCounted'):]
    result['backend/internal/service/cline_billing_test.go']=test+'\n'+function
    # Current profile identity must not overwrite another active account's
    # observed restrictions on a same-key account switch.
    result['backend/internal/repository/cline_closeout_integration_test.go']+='''
func TestClinePostgresIdentitySwitchDoesNotReattributeOldLimits(t *testing.T) {
 ctx:=context.Background();repo,a:=clinePostgresAccount(t)
 first,err:=cline.ParseSubjectHash([]byte(fmt.Sprintf(`{"id":"user","active_account_id":"first-%d"}`,a.ID)));require.NoError(t,err)
 second,err:=cline.ParseSubjectHash([]byte(fmt.Sprintf(`{"id":"user","active_account_id":"second-%d"}`,a.ID)));require.NoError(t,err)
 t.Cleanup(func(){_,err:=integrationDB.ExecContext(ctx,"DELETE FROM cline_shared_limits WHERE subject_hash=$1 OR subject_hash=$2",first,second);require.NoError(t,err)})
 now:=time.Now().UTC();state:=clineVerifiedState(a,first,now)
 saved,err:=repo.SaveClineStateIfUnchanged(ctx,a,state);require.NoError(t,err);require.True(t,saved)
 a.Extra[service.ClineStateExtraKey]=state
 require.NoError(t,repo.SetClineRateLimitIfLater(ctx,a,cline.ScopePass+"weekly",time.Now().Add(time.Hour),"pass_limit",true))
 saved,err=repo.SaveClineStateIfUnchanged(ctx,a,clineVerifiedState(a,second,now.Add(time.Second)));require.NoError(t,err);require.True(t,saved)
 // a still carries the pre-send first-subject snapshot. Its late response
 // may extend its conservative local cooldown but must not poison subject 2.
 require.NoError(t,repo.SetClineRateLimitIfLater(ctx,a,cline.ScopePass+"monthly",time.Now().Add(2*time.Hour),"pass_limit",true))
 var count int
 require.NoError(t,integrationDB.QueryRowContext(ctx,"SELECT COUNT(*) FROM cline_shared_limits WHERE subject_hash=$1",second).Scan(&count))
 require.Zero(t,count)
}
'''
    workflow='.github/workflows/cline-platform-development.yml'
    wf=yaml.safe_load(result[workflow])
    if True in wf:wf['on']=wf.pop(True)
    for step in wf['jobs']['integration']['steps']:
        name=step.get('name')
        if name=='Policy and SSE race tests':step['run']=step['run'].replace('required = {',"required = {'TestClineTerminalFailurePreservesOnlyValidatedUsage', ",1)
        if name=='Cline service and legacy cooldown regression tests':step['run']=step['run'].replace('required = {',"required = {'TestClineTerminalFailureUsageIsNotLostOrDoubleCounted', ",1)
    for step in wf['jobs']['postgres']['steps']:
        if step.get('name')=='Actual Cline PostgreSQL CAS and migration regressions':step['run']=step['run'].replace('required = {',"required = {'TestClinePostgresIdentitySwitchDoesNotReattributeOldLimits', ",1)
    result[workflow]=yaml.safe_dump(wf,sort_keys=False,width=120)
    return result
m.prepare=complete_prepare
if __name__=='__main__':
    try:m.main()
    except urllib.error.HTTPError as error:raise SystemExit('blob transfer HTTP '+str(error.code)) from None
