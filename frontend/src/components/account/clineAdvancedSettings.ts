import type { Account } from '@/types'
import type { QuotaResetMode, QuotaThresholdType } from '@/constants/account'

export const CLINE_QUOTA_DIMS = ['total', 'daily', 'weekly'] as const
export type ClineQuotaDimension = typeof CLINE_QUOTA_DIMS[number]
export interface ClinePauseRuleDraft {
  error_code: number
  keywords: string
  duration_minutes: number
  description: string
}
export interface ClineAdvancedDraft {
  totalLimit: number | null
  dailyLimit: number | null
  weeklyLimit: number | null
  dailyResetMode: QuotaResetMode | null
  dailyResetHour: number | null
  weeklyResetMode: QuotaResetMode | null
  weeklyResetDay: number | null
  weeklyResetHour: number | null
  resetTimezone: string | null
  notify: Record<ClineQuotaDimension, { enabled: boolean | null; threshold: number | null; thresholdType: QuotaThresholdType | null }>
  customErrorEnabled: boolean
  customErrorCodes: string
  tempEnabled: boolean
  rules: ClinePauseRuleDraft[]
}
export class ClineAdvancedError extends Error {
  constructor(public readonly issue: 'quota' | 'reset' | 'notify' | 'errorCodes' | 'rules') { super(issue) }
}
const quotaFields = {
  totalLimit: 'quota_limit', dailyLimit: 'quota_daily_limit', weeklyLimit: 'quota_weekly_limit',
  dailyResetMode: 'quota_daily_reset_mode', dailyResetHour: 'quota_daily_reset_hour',
  weeklyResetMode: 'quota_weekly_reset_mode', weeklyResetDay: 'quota_weekly_reset_day',
  weeklyResetHour: 'quota_weekly_reset_hour', resetTimezone: 'quota_reset_timezone'
} as const
export function clineAdvancedDraft(account?: Account | null): ClineAdvancedDraft {
  const extra = account?.extra ?? {}, credentials = account?.credentials ?? {}
  const fields = Object.fromEntries(Object.entries(quotaFields).map(([field, key]) => [field, extra[key] ?? null]))
  const rules = credentials.temp_unschedulable_rules
  return {
    ...fields,
    notify: Object.fromEntries(CLINE_QUOTA_DIMS.map(dim => [dim, {
      enabled: extra[`quota_notify_${dim}_enabled`] ?? null,
      threshold: extra[`quota_notify_${dim}_threshold`] ?? null,
      thresholdType: extra[`quota_notify_${dim}_threshold_type`] ?? null
    }])),
    customErrorEnabled: credentials.custom_error_codes_enabled === true,
    customErrorCodes: Array.isArray(credentials.custom_error_codes) ? credentials.custom_error_codes.join(', ') : '',
    tempEnabled: credentials.temp_unschedulable_enabled === true,
    rules: Array.isArray(rules) ? rules.map(r => ({ error_code: r.error_code, duration_minutes: r.duration_minutes,
      keywords: Array.isArray(r.keywords) ? r.keywords.join('\n') : '', description: r.description ?? '' })) : []
  } as ClineAdvancedDraft
}
const bytes = (s: string) => new TextEncoder().encode(s).length
const control = (s: string) => [...s].some(c => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127)
const numeric = (v: unknown, max: number) => typeof v === 'number' && Number.isFinite(v) && v >= 0 && v <= max
const empty = (v: unknown) => v == null || v === ''
const normalizeEmpty = (v: unknown) => empty(v) ? null : v
const split = (s: string) => s.split(/[,，\n]/).map(x => x.trim()).filter(Boolean)

// Only edited configuration keys leave the browser. Never replay counters,
// timestamps or provider quota evidence from an account-list snapshot.
export function clineAdvancedPayload(draft: ClineAdvancedDraft, account?: Account | null): {
  credentials: Record<string, unknown>; extra: Record<string, unknown>
} {
  const before = clineAdvancedDraft(account), extra: Record<string, unknown> = {}, credentials: Record<string, unknown> = {}
  for (const [field, key] of Object.entries(quotaFields) as [keyof typeof quotaFields, string][]) {
    const value = normalizeEmpty(draft[field])
    if (value === normalizeEmpty(before[field])) continue
    if (value !== null) {
      if (field.endsWith('Limit') && !numeric(value, 1e12)) throw new ClineAdvancedError('quota')
      if (field.endsWith('Mode') && value !== 'rolling' && value !== 'fixed') throw new ClineAdvancedError('reset')
      if ((field.endsWith('Hour') || field.endsWith('Day')) && (!Number.isInteger(value) || !numeric(value, field.endsWith('Day') ? 6 : 23))) throw new ClineAdvancedError('reset')
      if (field === 'resetTimezone') {
        if (typeof value !== 'string' || value === 'Local' || bytes(value) > 100) throw new ClineAdvancedError('reset')
        try { new Intl.DateTimeFormat('en', { timeZone: value }).format() } catch { throw new ClineAdvancedError('reset') }
      }
    }
    extra[key] = value
  }
  for (const dim of CLINE_QUOTA_DIMS) {
    const next = draft.notify[dim], old = before.notify[dim]
    if (JSON.stringify(next) === JSON.stringify(old)) continue
    if (next.enabled !== null && typeof next.enabled !== 'boolean') throw new ClineAdvancedError('notify')
    const threshold = normalizeEmpty(next.threshold), type = next.thresholdType ?? 'fixed'
    if (!['fixed', 'percentage'].includes(type) || (threshold !== null && !numeric(threshold, type === 'percentage' ? 100 : 1e12))) throw new ClineAdvancedError('notify')
    for (const [field, suffix] of [['enabled', 'enabled'], ['threshold', 'threshold'], ['thresholdType', 'threshold_type']] as const) {
      if (normalizeEmpty(next[field]) !== normalizeEmpty(old[field])) extra[`quota_notify_${dim}_${suffix}`] = normalizeEmpty(next[field])
    }
  }
  if (draft.customErrorEnabled !== before.customErrorEnabled || draft.customErrorCodes !== before.customErrorCodes) {
    const codes = split(draft.customErrorCodes)
    if (codes.length > 64 || new Set(codes).size !== codes.length || codes.some(v => !/^\d{3}$/.test(v) || +v < 400 || +v > 599) || (draft.customErrorEnabled && !codes.length)) throw new ClineAdvancedError('errorCodes')
    credentials.custom_error_codes_enabled = draft.customErrorEnabled
    credentials.custom_error_codes = codes.map(Number)
  }
  if (draft.tempEnabled !== before.tempEnabled || JSON.stringify(draft.rules) !== JSON.stringify(before.rules)) {
    if (draft.rules.length > 32 || (draft.tempEnabled && !draft.rules.length)) throw new ClineAdvancedError('rules')
    const rules = draft.rules.map(r => {
      const keywords = split(r.keywords)
      if (!Number.isInteger(r.error_code) || r.error_code < 400 || r.error_code > 599 || !Number.isInteger(r.duration_minutes) || r.duration_minutes < 1 || r.duration_minutes > 10080 ||
          !keywords.length || keywords.length > 20 || new Set(keywords.map(k => k.toLowerCase())).size !== keywords.length || keywords.some(k => bytes(k) > 256 || control(k)) || bytes(r.description) > 512 || control(r.description)) throw new ClineAdvancedError('rules')
      return { error_code: r.error_code, keywords, duration_minutes: r.duration_minutes, description: r.description }
    })
    credentials.temp_unschedulable_enabled = draft.tempEnabled
    credentials.temp_unschedulable_rules = rules
  }
  return { credentials, extra }
}
