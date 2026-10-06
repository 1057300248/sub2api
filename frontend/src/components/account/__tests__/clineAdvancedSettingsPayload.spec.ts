import { describe, it, expect } from 'vitest'
import type { Account } from '@/types'
import { clineAdvancedDraft, clineAdvancedPayload, ClineAdvancedError } from '../clineAdvancedSettings'
const saved = { platform: 'cline', type: 'apikey', credentials: { api_key: 'never-submit', account_mode: 'pass', custom_error_codes_enabled: true, custom_error_codes: [503], temp_unschedulable_enabled: true, temp_unschedulable_rules: [{ error_code: 503, duration_minutes: 5, keywords: ['busy'], description: 'old' }] }, extra: { quota_limit: 100, quota_used: 80, quota_daily_limit: 10, quota_daily_used: 7, quota_notify_total_enabled: true, quota_notify_total_threshold: 20, quota_notify_total_threshold_type: 'percentage', cline_state: { secret: 'protected' }, model_rate_limits: { protected: true } } } as unknown as Account

describe('Cline advanced settings deltas', () => {
  it('defaults to no new limits or rules and never replays saved runtime or credentials', () => {
    expect(clineAdvancedPayload(clineAdvancedDraft())).toEqual({ credentials: {}, extra: {} })
    expect(clineAdvancedPayload(clineAdvancedDraft(saved), saved)).toEqual({ credentials: {}, extra: {} })
  })
  it('preserves zero, explicit off and nullable removals separately', () => {
    const d = clineAdvancedDraft(saved)
    d.totalLimit = 0; d.dailyLimit = null; d.notify.total.enabled = false; d.notify.total.threshold = 0
    d.customErrorEnabled = false; d.tempEnabled = false
    const payload = clineAdvancedPayload(d, saved)
    expect(payload.extra).toEqual({ quota_limit: 0, quota_daily_limit: null, quota_notify_total_enabled: false, quota_notify_total_threshold: 0 })
    expect(payload.credentials).toMatchObject({ custom_error_codes_enabled: false, custom_error_codes: [503], temp_unschedulable_enabled: false, temp_unschedulable_rules: saved.credentials.temp_unschedulable_rules })
    expect(JSON.stringify(payload)).not.toContain('never-submit')
    expect(saved.extra?.quota_used).toBe(80)
  })
  it('accepts Sunday, midnight, rolling and nonpreset IANA timezone without inventing timestamps', () => {
    const d = clineAdvancedDraft(); Object.assign(d, { weeklyLimit: 3, dailyResetMode: 'rolling', weeklyResetMode: 'fixed', weeklyResetDay: 0, weeklyResetHour: 0, resetTimezone: 'America/Indiana/Indianapolis' })
    expect(clineAdvancedPayload(d).extra).toEqual({ quota_weekly_limit: 3, quota_daily_reset_mode: 'rolling', quota_weekly_reset_mode: 'fixed', quota_weekly_reset_day: 0, quota_weekly_reset_hour: 0, quota_reset_timezone: 'America/Indiana/Indianapolis' })
  })
  it.each([-1, Infinity, NaN, 1e13])('rejects invalid amount %s', value => { const d = clineAdvancedDraft(); d.totalLimit = value; expect(() => clineAdvancedPayload(d)).toThrow(ClineAdvancedError) })
  it('rejects invalid timezone, hour and percentage', () => {
    for (const change of [{ resetTimezone: 'No/Such' }, { weeklyResetHour: 24 }, { weeklyResetDay: 0.5 }]) { const d = clineAdvancedDraft(); Object.assign(d, change); expect(() => clineAdvancedPayload(d)).toThrow(ClineAdvancedError) }
    const d = clineAdvancedDraft(); d.notify.weekly = { enabled: true, threshold: 101, thresholdType: 'percentage' }; expect(() => clineAdvancedPayload(d)).toThrow(ClineAdvancedError)
  })
  it('validates code/rule bounds even when a changed policy is disabled', () => {
    const d = clineAdvancedDraft(); d.customErrorCodes = '503, 503'; expect(() => clineAdvancedPayload(d)).toThrow(ClineAdvancedError)
    d.customErrorCodes = ''; d.customErrorEnabled = true; expect(() => clineAdvancedPayload(d)).toThrow(ClineAdvancedError)
    d.customErrorEnabled = false; d.rules = [{ error_code: 503, duration_minutes: 0, keywords: 'busy', description: '' }]; expect(() => clineAdvancedPayload(d)).toThrow(ClineAdvancedError)
    d.rules[0].duration_minutes = 1; d.rules[0].keywords = 'bad\rkeyword'; expect(() => clineAdvancedPayload(d)).toThrow(ClineAdvancedError)
    d.rules[0].keywords = 'Busy\nbusy'; expect(() => clineAdvancedPayload(d)).toThrow(ClineAdvancedError)
  })
  it('keeps rule order and positive settings but no provider override in the payload', () => {
    const d = clineAdvancedDraft(); d.tempEnabled = true; d.rules = [{ error_code: 503, keywords: 'overload\ntry later', duration_minutes: 5, description: 'first' }, { error_code: 500, keywords: 'temporary', duration_minutes: 1, description: 'second' }]
    expect(clineAdvancedPayload(d).credentials.temp_unschedulable_rules).toEqual([{ error_code: 503, keywords: ['overload', 'try later'], duration_minutes: 5, description: 'first' }, { error_code: 500, keywords: ['temporary'], duration_minutes: 1, description: 'second' }])
  })
})
