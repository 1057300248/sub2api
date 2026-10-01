#!/usr/bin/env python3
"""Reviewed, baseline-guarded development-branch source integration.
This tool edits only tracked source files and never accesses running services.
"""
from pathlib import Path
import re
import subprocess

BASE = '71906b39eb2fe14bca512564aa9414d504cbdb20'
ROOT = Path(__file__).resolve().parents[1]

def replace(text,old,new,count=1):
    actual=text.count(old)
    if actual!=count:raise RuntimeError(f'Expected {count}, got {actual}: {old[:100]!r}')
    return text.replace(old,new)

def function(text,name,change):
    hits=list(re.finditer(r'(?ms)^func[^\n]*\b'+re.escape(name)+r'\(.*?^}',text))
    if len(hits)!=1:raise RuntimeError(f'Function {name}: {len(hits)} matches')
    m=hits[0];return text[:m.start()]+change(m.group())+text[m.end():]

def prepend(text,name,code):
    def change(body):
        at=body.index('{\n')+2
        return body[:at]+code+body[at:]
    return function(text,name,change)

def imported(text,path):
    if '"'+path+'"' in text:return text
    return replace(text,'import (\n','import (\n\t"'+path+'"\n')

def edit(name,change):
    p=ROOT/name
    baseline=subprocess.check_output(['git','show',BASE+':'+name],cwd=ROOT,text=True)
    desired=change(baseline)
    if p.suffix=='.go':desired=subprocess.check_output(['gofmt'],input=desired,text=True)
    current=p.read_text()
    if current not in (baseline,desired):raise RuntimeError(name+': upstream source changed; manual review required')
    if current!=desired:p.write_text(desired);print('UPDATED',name)

P='github.com/Wei-Shaw/sub2api/internal/pkg/cline'

def account(text):
    text=function(text,'IsOpenAICompatible',lambda s:replace(s,'a.IsOpenCodeGo())','a.IsOpenCodeGo() || a.IsCline())'))
    text=prepend(text,'IsModelSupported','\tif a.IsCline() { return a.IsClineModelSupported(requestedModel) }\n')
    for name in ['GetBaseURL','GetOpenAIBaseURL']:text=prepend(text,name,'\tif a.IsCline() { return a.GetClineBaseURL() }\n')
    text=prepend(text,'GetOpenAIProtocolAPIKey','\tif a.IsCline() && a.Type == AccountTypeAPIKey { return a.GetCredential("api_key") }\n')
    return text
edit('backend/internal/service/account.go',account)

def domain(text):
    start=text.index('var AllowedQuotaPlatforms');end=text.index('\n}',start)+2
    part=replace(text[start:end],'\tPlatformOpenCodeGo,','\tPlatformOpenCodeGo,\n\tPlatformCline,')
    return text[:start]+part+text[end:]
edit('backend/internal/service/domain_constants.go',domain)

def model_limits(text):
    text=imported(text,P)
    return replace(text,'switch a.Platform {','switch a.Platform {\n\tcase PlatformCline:\n\t\tfor _, scope := range cline.RateLimitKeys(a.GetClineMode(), modelKey) { keys = append(keys, ClineRateLimitScope(a, scope)) }')
edit('backend/internal/service/model_rate_limit.go',model_limits)

def limits(text):
    text=prepend(text,'CheckErrorPolicy','\tif isClineScopedError(account, statusCode, responseBody) { return ErrorPolicyNone }\n')
    return prepend(text,'handleUpstreamErrorAfterStreakReset','\tif s.handleClineScopedError(ctx, account, statusCode, headers, responseBody, firstRequestedModel(requestedModel)) { return false }\n')
edit('backend/internal/service/ratelimit_service.go',limits)

def pipeline(text):
    text=imported(text,P)
    text=prepend(text,'sendCCUpstreamRequest','\tif err := account.ValidateClineOutboundBody(body); err != nil { return nil, err }\n')
    def outgoing(body):
        return replace(body,'\treturn resp, nil\n}', '\tif account.IsCline() && resp.StatusCode < 400 && stream {\n\t\tlineLimit := defaultMaxLineSize\n\t\tif s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 { lineLimit = s.cfg.Gateway.MaxLineSize }\n\t\tresp.Body = cline.GuardSSEBody(resp.Body, lineLimit, nil)\n\t}\n\treturn resp, nil\n}')
    return function(text,'sendCCUpstreamRequest',outgoing)
edit('backend/internal/service/openai_gateway_cc_pipeline.go',pipeline)

def repo(text):
    for name in ['Create','Update']:
        def change(part):
            at=part.index('{\n')+2
            match=re.search(r'\b(\w+) \*service\.Account',part[:at])
            if not match:raise RuntimeError('Account parameter missing from repository '+name)
            arg=match.group(1)
            return part[:at]+f'\tif err := service.NormalizeClineCredentials({arg}.Platform, {arg}.Type, {arg}.Credentials); err != nil {{ return err }}\n'+part[at:]
        text=function(text,name,change)
    return text
edit('backend/internal/repository/account_repo.go',repo)
