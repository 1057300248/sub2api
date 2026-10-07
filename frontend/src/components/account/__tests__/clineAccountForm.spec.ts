import { describe, expect, it } from 'vitest'
import type { Account, CreateAccountRequest, UpdateAccountRequest } from '@/types'
import { CLINE_BASE_URL, buildClineAccountPayload, clineAccountDraft, clineModeLabel, ClineFormError } from '../clineAccountForm'

function draft() {
  return { ...clineAccountDraft(), name: 'Cline test', apiKey: 'not-a-real-key', models: [{ publicID: 'public-model', upstreamID: 'cline-pass/model' }] }
}
const existing = {
  id: 41, name: 'Cline old', platform: 'cline', type: 'apikey', notes: '', concurrency: 2, priority: 3, group_ids: [7], schedulable: true,
  credentials: { api_key: 'secret-never-in-dom', cline_auth_type: 'account_token', account_mode: 'pass', base_url: CLINE_BASE_URL, model_mapping: { old: 'cline-pass/old' } },
  extra: { cline_state: { quota_status: 'unknown' }, model_rate_limits: { limit: true } }
} as unknown as Account

describe('independent Cline account form contract', () => {
  it('creates a canonical explicit Cline account without paid fallback or extra state', () => {
    const p = buildClineAccountPayload(draft()) as CreateAccountRequest
    expect(p.platform).toBe('cline'); expect(p.type).toBe('apikey')
    expect(p.credentials).toMatchObject({ api_key: 'not-a-real-key', account_mode: 'pass', cline_auth_type: 'api_key', api_protocol: 'chat_completions', base_url: CLINE_BASE_URL, model_mapping: { 'public-model': 'cline-pass/model' }, cline_paid_fallback: false, cline_free_api_enabled: false, pool_mode: false })
    expect(p).not.toHaveProperty('extra'); expect(p).not.toHaveProperty('rate_multiplier')
  })
  it('keeps existing credentials, group model restrictions and pricing off the edit payload', () => {
    const d = clineAccountDraft(existing)
    expect(d.apiKey).toBe('')
    const p = buildClineAccountPayload(d, existing) as UpdateAccountRequest
    expect(p.credentials).not.toHaveProperty('api_key')
    for (const name of ['platform', 'extra', 'rate_multiplier', 'group_ids', 'group_allowed_models', 'proxy_id']) expect(p).not.toHaveProperty(name)
    d.apiKey = 'replacement'; d.groupIDs = [8]
    const changed = buildClineAccountPayload(d, existing)
    expect(changed.credentials?.api_key).toBe('replacement'); expect(changed.group_ids).toEqual([8])
    expect(existing.credentials.api_key).toBe('secret-never-in-dom')
  })
  it('never rewrites an incompatible model when the mode changes', () => {
    const d = draft(); d.mode = 'payg'
    expect(() => buildClineAccountPayload(d)).toThrowError(new ClineFormError('paygModel'))
    expect(d.models[0].upstreamID).toBe('cline-pass/model')
    d.models[0].upstreamID = 'vendor/model'
    expect(buildClineAccountPayload(d).credentials?.model_mapping).toEqual({ 'public-model': 'vendor/model' })
    d.mode = 'pass'
    expect(() => buildClineAccountPayload(d)).toThrowError(new ClineFormError('passModel'))
  })
  it('keeps Free and unknown explicitly unschedulable when editing', () => {
    for (const mode of ['free', 'unknown'] as const) {
      const d = clineAccountDraft(existing); d.mode = mode
      const p = buildClineAccountPayload(d, existing) as UpdateAccountRequest
      expect(p.schedulable).toBe(false); expect(p.credentials?.account_mode).toBe(mode)
      expect(p.credentials?.cline_free_api_enabled).toBe(false)
    }
  })
  it('rejects blank, duplicate and wildcard whitelist entries rather than dropping them', () => {
    for (const models of [[], [{ publicID: '', upstreamID: 'cline-pass/model' }], [{ publicID: '*', upstreamID: 'cline-pass/model' }], [{ publicID: 'a b', upstreamID: 'cline-pass/model' }], [{ publicID: 'a', upstreamID: '' }], [{ publicID: 'a', upstreamID: 'cline-pass/a' }, { publicID: 'a', upstreamID: 'cline-pass/b' }]]) {
      const d = draft(); d.models = models
      expect(() => buildClineAccountPayload(d)).toThrow(ClineFormError)
    }
  })
  it('retains invalid legacy model rows for explicit correction', () => {
    const a = { ...existing, credentials: { ...existing.credentials, model_mapping: { old: null, other: 'vendor/paid' } } } as unknown as Account
    const d = clineAccountDraft(a)
    expect(d.models).toEqual([{ publicID: 'old', upstreamID: '' }, { publicID: 'other', upstreamID: 'vendor/paid' }])
    expect(() => buildClineAccountPayload(d, a)).toThrow(ClineFormError)
  })
  it('rejects non-HTTPS, credentials, request endpoints and official host path mistakes', () => {
    for (const url of ['http://api.cline.bot', 'https://key@api.cline.bot', CLINE_BASE_URL + '?key=x', CLINE_BASE_URL + '#x', CLINE_BASE_URL + '/responses', 'https://api.cline.bot:444', 'https://api.cline.bot/wrong']) {
      const d = draft(); d.baseURL = url
      expect(() => buildClineAccountPayload(d)).toThrowError(new ClineFormError('baseURL'))
    }
    const d = draft(); d.baseURL = 'https://API.CLINE.BOT/api/'
    expect(buildClineAccountPayload(d).credentials?.base_url).toBe(CLINE_BASE_URL)
  })
  it('requires a new credential, known credential kind and valid scheduling fields', () => {
    for (const changes of [{ apiKey: '' }, { authType: '' as const }, { concurrency: 0 }, { concurrency: 1.5 }, { priority: -1 }, { groupIDs: [-1] }, { name: '' }]) expect(() => buildClineAccountPayload({ ...draft(), ...changes })).toThrow(ClineFormError)
  })
  it('refuses platform conversion and preserves distinct labels', () => {
    expect(() => clineAccountDraft({ ...existing, platform: 'deepseek' })).toThrow(ClineFormError)
    expect(() => buildClineAccountPayload(draft(), { ...existing, platform: 'deepseek' })).toThrow(ClineFormError)
    expect(['pass', 'free', 'payg', 'unknown'].map(clineModeLabel)).toEqual(['Cline Pass', 'Cline Free', 'Cline PAYG', 'Cline · ?'])
  })
})
