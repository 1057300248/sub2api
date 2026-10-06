import { describe, expect, it } from 'vitest'
import type { Account } from '@/types'
import { clineAccountDraft, buildClineAccountPayload, CLINE_BASE_URL } from '../clineAccountForm'
import { clineAccountSettings, clineAccountSettingsPayload, validateClineHeaderRows, ClineSettingsError } from '../clineAccountSettings'
import { isHeaderOverrideCapable } from '../credentialsBuilder'

const account = {
  id: 7, platform: 'cline', type: 'apikey', name: 'Cline', notes: 'old note', concurrency: 3, priority: 2,
  status: 'active', schedulable: true, group_ids: [41, 99], proxy_id: 8, proxy_fallback_origin_id: 6,
  rate_multiplier: 2, group_rate_multiplier: 3, load_factor: 5,
  expires_at: Date.parse('2030-10-06T11:23:44Z') / 1000, auto_pause_on_expired: true,
  credentials: { api_key: 'server-only', account_mode: 'pass', cline_auth_type: 'api_key', base_url: CLINE_BASE_URL,
    model_mapping: { public: 'cline-pass/model' }, header_override_enabled: true, header_overrides: { 'User-Agent': 'default/1' } },
  account_groups: [{ group_id: 41, allowed_models: ['public'] }, { group_id: 99, allowed_models: ['old-public'] }],
  extra: { cost_multiplier: 0.6, cline_state: { protected: true }, model_rate_limits: { protected: true }, unrelated: 'preserve' }
} as unknown as Account

describe('Cline account settings parity', () => {
  it('keeps original proxy, all invisible bindings, distinct multipliers and unchanged expiry without collateral writes', () => {
    const d = clineAccountDraft(account)
    expect(d).toMatchObject({ proxyID: 6, rateMultiplier: 2, groupRateMultiplier: 3, costMultiplier: 0.6, loadFactor: 5, groupAllowedModels: { 41: ['public'], 99: ['old-public'] } })
    expect(new Date(d.expiresAt).getTime()).toBe(account.expires_at! * 1000)
    expect(clineAccountSettingsPayload(d, d.groupIDs, account)).toEqual({})
    const payload = buildClineAccountPayload(d, account)
    expect(payload).not.toHaveProperty('extra')
    expect(payload).not.toHaveProperty('proxy_id')
    expect(payload).not.toHaveProperty('group_allowed_models')
    expect(payload.credentials).not.toHaveProperty('api_key')
    d.groupAllowedModels[41].push('another')
    expect(account.account_groups![0].allowed_models).toEqual(['public'])
  })
  it('writes zero-valued multipliers independently and uses explicit reset sentinels for nullable fields', () => {
    const d = { ...clineAccountDraft(account), proxyID: null, loadFactor: null, expiresAt: '', rateMultiplier: 0, groupRateMultiplier: 0, costMultiplier: 0, autoPauseOnExpired: false, status: 'inactive' as const, schedulable: false, notes: '' }
    expect(buildClineAccountPayload(d, account)).toMatchObject({ proxy_id: 0, load_factor: 0, expires_at: 0, rate_multiplier: 0, group_rate_multiplier: 0, extra: { cost_multiplier: 0 }, auto_pause_on_expired: false, status: 'inactive', schedulable: false, notes: '' })
    expect(buildClineAccountPayload(d, account).extra).toEqual({ cost_multiplier: 0 })
  })
  it('round-trips local dates at second resolution and never normalizes invalid dates into another day', () => {
    const d = clineAccountDraft(account)
    d.expiresAt = '2031-05-04T02:03:04'
    expect(clineAccountSettingsPayload(d, d.groupIDs, account).expires_at).toBe(new Date(d.expiresAt).getTime()/1000)
    for (const invalid of ['2031-02-30T02:03', '2031-13-01T02:03', 'not-date', '2031-05-04T25:03']) {
      expect(() => clineAccountSettingsPayload({ ...d, expiresAt: invalid }, d.groupIDs, account)).toThrow(ClineSettingsError)
    }
  })
  it('rejects malformed common settings instead of accepting browser number coercion', () => {
    for (const delta of [{ proxyID: 0 }, { proxyID: -1 }, { proxyID: 1.5 }, { rateMultiplier: NaN }, { rateMultiplier: Infinity }, { groupRateMultiplier: -1 }, { rateMultiplier: '2' }, { costMultiplier: 1000001 }, { costMultiplier: '' }, { loadFactor: 1.5 }, { loadFactor: -1 }, { loadFactor: 10001 }, { status: 'typo' }]) {
      const d = { ...clineAccountDraft(account), ...delta } as ReturnType<typeof clineAccountDraft>
      expect(() => buildClineAccountPayload(d, account), JSON.stringify(delta)).toThrow(ClineSettingsError)
    }
    expect(clineAccountSettingsPayload({ ...clineAccountSettings(account), loadFactor: '' as unknown as number }, [41, 99], account)).toEqual({ load_factor: 0 })
  })
  it('preserves invisible group restrictions; empty restricted selections never broaden model access', () => {
    const d = clineAccountDraft(account)
    d.groupAllowedModels[41] = ['new-public']
    expect(buildClineAccountPayload(d, account)).toHaveProperty('group_allowed_models', { 41: ['new-public'], 99: ['old-public'] })
    d.groupAllowedModels[41] = []
    expect(() => buildClineAccountPayload(d, account)).toThrow(ClineSettingsError)
    delete d.groupAllowedModels[41]
    expect(buildClineAccountPayload(d, account)).toHaveProperty('group_allowed_models', { 99: ['old-public'] })
    d.groupIDs = [41]
    expect(buildClineAccountPayload(d, account)).toMatchObject({ group_ids: [41], group_allowed_models: {} })
  })
  it('supports safe account default headers and explicit clearing without round-tripping secrets', () => {
    expect(isHeaderOverrideCapable('cline', 'apikey')).toBe(true)
    expect(isHeaderOverrideCapable('cline', 'oauth')).toBe(false)
    const d = clineAccountDraft(account)
    expect(buildClineAccountPayload(d, account).credentials).toMatchObject({ header_override_enabled: true, header_overrides: { 'user-agent': 'default/1' } })
    d.headerOverrideEnabled = false
    expect(buildClineAccountPayload(d, account).credentials).toMatchObject({ header_override_enabled: false, header_overrides: {} })
    expect(account.credentials?.header_overrides).toEqual({ 'User-Agent': 'default/1' })
  })
  it('uses the same positive header list and UTF-8 bounds as request-level overrides', () => {
    for (const name of ['User-Agent', 'Accept-Language', 'X-Request-ID', 'X-Client-Name', 'X-Client-Version', 'X-Metadata-Trace']) expect(validateClineHeaderRows([{ name, value: 'fixture' }])).toBe(true)
    for (const name of ['Host', 'Authorization', 'Cookie', 'Content-Type', 'Accept', 'X-Forwarded-For', 'X-Cline-Account-ID', 'X-Tenant-ID', 'Content-Length', 'Connection']) expect(validateClineHeaderRows([{ name, value: 'fixture' }])).toBe(false)
    expect(validateClineHeaderRows([{ name: 'User-Agent', value: 'a' }, { name: 'uSeR-aGeNt', value: 'b' }])).toBe(false)
    for (const value of ['a\r\nb', '\0', 'a\t', '\x7f', '中'.repeat(683)]) expect(validateClineHeaderRows([{ name: 'X-Metadata-Test', value }])).toBe(false)
    expect(validateClineHeaderRows(Array.from({ length: 17 }, (_, i) => ({ name: `X-Metadata-${i}`, value: 'x' })))).toBe(false)
    expect(validateClineHeaderRows(Array.from({ length: 5 }, (_, i) => ({ name: `X-Metadata-${i}`, value: 'x'.repeat(2048) })))).toBe(false)
  })
  it('does not transmit headers, Extra or test/probe options for untouched new defaults', () => {
    const d = { ...clineAccountDraft(), name: 'New', apiKey: 'fixture', models: [{ publicID: 'public', upstreamID: 'cline-pass/model' }] }
    expect(buildClineAccountPayload(d)).not.toHaveProperty('extra')
    expect(buildClineAccountPayload(d).credentials).not.toHaveProperty('header_overrides')
    d.schedulable = false
    expect(buildClineAccountPayload(d)).toHaveProperty('schedulable', false)
    d.schedulable = true; d.mode = 'free'
    expect(buildClineAccountPayload(d)).toHaveProperty('schedulable', false)
  })
})
