import { describe, it, expect } from 'vitest'
import { CLINE_BASE_URL, clineModeLabel, clineModelError, applyClineCredentialFields, clineExtraDelta, validateClineHeaderRows } from '../clineAccountForm'
import { splitModelMappingObject } from '@/composables/useModelWhitelist'

describe('Cline policy hooks inside native account forms', () => {
  it('adds provider-only connection fields without serializing common account state', () => {
    const credentials = { api_key: 'fixture', base_url: 'https://API.CLINE.BOT/api/', model_mapping: { public: 'cline-pass/model' } }
    expect(applyClineCredentialFields(credentials, 'pass', 'api_key')).toBeNull()
    expect(credentials).toMatchObject({ base_url: CLINE_BASE_URL, account_mode: 'pass', cline_auth_type: 'api_key', api_protocol: 'chat_completions', pool_mode: false, cline_paid_fallback: false, cline_free_api_enabled: false })
    for (const name of ['proxy_id', 'concurrency', 'extra', 'rate_multiplier']) expect(credentials).not.toHaveProperty(name)
  })
  it.each(['http://api.cline.bot', 'https://key@api.cline.bot', CLINE_BASE_URL+'?key=x', CLINE_BASE_URL+'#x', CLINE_BASE_URL+'/responses', 'https://api.cline.bot:444', 'https://api.cline.bot/wrong'])('rejects unsafe base %s without changing the payload', (base_url) => {
    const credentials = { base_url }; expect(applyClineCredentialFields(credentials, 'pass', 'api_key')).toBe('baseURL'); expect(credentials).toEqual({ base_url })
  })
  it.each([[], [{from:'',to:'cline-pass/a'}], [{from:'*',to:'cline-pass/a'}], [{from:'a b',to:'cline-pass/a'}], [{from:'a',to:''}], [{from:'a',to:'cline-pass/a'},{from:'a',to:'cline-pass/b'}]])('rejects malformed or duplicate raw mappings %j', (...mappings) => {
    expect(clineModelError('pass', [], mappings)).not.toBeNull()
  })
  it('retains invalid saved rows only when the provider requires strict whitelisting', () => {
    const mapping = { old: null, ' ': 'cline-pass/a', paid: 'vendor/paid' }
    const result = splitModelMappingObject(mapping, true)
    expect(result.modelMappings).toEqual([{from:'old',to:''},{from:' ',to:'cline-pass/a'},{from:'paid',to:'vendor/paid'}])
    expect(clineModelError('pass', result.allowedModels, result.modelMappings)).not.toBeNull()
    expect(splitModelMappingObject(mapping).modelMappings).toEqual([{from:'paid',to:'vendor/paid'}])
  })
  it('does not auto-rewrite model mappings on a mode change', () => {
    const rows = [{from:'alias',to:'cline-pass/model'}]
    expect(clineModelError('pass', [], rows)).toBeNull(); expect(clineModelError('payg', [], rows)).toBe('paygModel')
    expect(rows).toEqual([{from:'alias',to:'cline-pass/model'}])
    expect(clineModelError('pass', ['cline-pass/'], [])).toBe('passModel')
    expect(clineModelError('pass', ['cline-pass/model'], rows)).toBeNull()
    expect(clineModelError('pass', ['cline-pass/model'], [{from:'cline-pass/model',to:'cline-pass/other'}])).toBe('duplicateModel')
  })
  it('sends only changed configuration and explicit deletion, never server-managed counters', () => {
    const before = { quota_limit: 12, quota_used: 3, quota_notify_daily_enabled: false, cline_state: { active: true }, model_rate_limits: { blocked: true }, cost_multiplier: 0.8, cost_multiplier_auto_sync: true, upstream_request_id_header: 'x-id' }
    expect(clineExtraDelta({...before}, before)).toEqual({})
    const next = { quota_notify_daily_enabled: true, cost_multiplier: 0.7, cost_multiplier_auto_sync: false, quota_used: 0 }
    expect(clineExtraDelta(next, before)).toEqual({quota_limit:null,quota_notify_daily_enabled:true,cost_multiplier:0.7,cost_multiplier_auto_sync:false,upstream_request_id_header:''})
    expect(before.quota_used).toBe(3)
  })
  it.each(['Authorization','X-Api-Key','X-Tenant-ID','Host','Cookie','Content-Length'])('rejects protected header %s', (name) => {
    expect(validateClineHeaderRows([{name,value:'blocked'}])).toBe(false)
  })
  it('preserves safe application headers but rejects duplicates/control bytes and bounded overflow', () => {
    expect(validateClineHeaderRows([{name:'X-Title',value:'App'},{name:'HTTP-Referer',value:'https://example.test'},{name:'X-Metadata-App',value:'1'}])).toBe(true)
    expect(validateClineHeaderRows([{name:'X-Title',value:'App'},{name:'x-title',value:'duplicate'}])).toBe(false)
    expect(validateClineHeaderRows([{name:'User-Agent',value:'a\r\nb'}])).toBe(false)
    expect(validateClineHeaderRows([{name:'User-Agent',value:'a'.repeat(2049)}])).toBe(false)
    expect(validateClineHeaderRows(Array.from({length:17},(_,i)=>({name:'X-Metadata-'+i,value:'v'})))).toBe(false)
  })
  it('keeps Free/unknown modes explicit without enabling paid fallback or Free API', () => {
    for (const mode of ['free','unknown'] as const) {
      const credentials: Record<string, unknown> = {api_key:'fixture'}
      expect(applyClineCredentialFields(credentials,mode,'api_key')).toBeNull()
      expect(credentials).toMatchObject({account_mode:mode,cline_free_api_enabled:false,cline_paid_fallback:false})
    }
    expect(['pass','free','payg','unknown'].map(clineModeLabel)).toEqual(['Cline Pass','Cline Free','Cline PAYG','Cline · ?'])
  })
})
