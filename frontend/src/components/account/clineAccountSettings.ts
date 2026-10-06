import type { Account, CreateAccountRequest, UpdateAccountRequest } from '@/types'
import { groupAllowedModelsFromAccount, type GroupAllowedModels } from './groupAllowedModels'
import type { HeaderOverrideRow } from './credentialsBuilder'
import { readAccountCostMultiplier, isValidAccountCostMultiplier } from '@/utils/accountCost'

export interface ClineAccountSettings {
  proxyID: number | null
  rateMultiplier: number
  groupRateMultiplier: number
  costMultiplier: number
  loadFactor: number | null
  expiresAt: string
  autoPauseOnExpired: boolean
  status: 'active' | 'inactive' | 'error'
  groupAllowedModels: GroupAllowedModels
  headerOverrideEnabled: boolean
  headerOverrideRows: HeaderOverrideRow[]
}
export type ClineSettingsIssue = 'proxy' | 'rateMultiplier' | 'costMultiplier' | 'loadFactor' | 'expiresAt' | 'status' | 'groupModels' | 'headers'
export class ClineSettingsError extends Error {
  constructor(public readonly issue: ClineSettingsIssue) { super(issue) }
}
function localDateTime(value: string | number | null | undefined): string {
  if (!value) return ''
  const date = new Date(typeof value === 'number' ? value * 1000 : value)
  if (!Number.isFinite(date.getTime())) return String(value)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}
export function clineAccountSettings(account?: Account | null): ClineAccountSettings {
  const rawHeaders = account?.credentials?.header_overrides
  return {
    // A temporary failover proxy is runtime state, not the configured origin.
    proxyID: account?.proxy_fallback_origin_id ?? account?.proxy_id ?? null,
    rateMultiplier: account?.rate_multiplier ?? 1,
    groupRateMultiplier: account?.group_rate_multiplier ?? 1,
    costMultiplier: readAccountCostMultiplier(account?.extra),
    loadFactor: account?.load_factor ?? null,
    expiresAt: localDateTime(account?.expires_at),
    autoPauseOnExpired: account?.auto_pause_on_expired ?? true,
    status: account?.status ?? 'active',
    groupAllowedModels: groupAllowedModelsFromAccount(account),
    headerOverrideEnabled: account?.credentials?.header_override_enabled === true,
    // Keep malformed old rows visible rather than silently dropping them.
    headerOverrideRows: rawHeaders && typeof rawHeaders === 'object' && !Array.isArray(rawHeaders)
      ? Object.entries(rawHeaders).map(([name, value]) => ({ name, value: typeof value === 'string' ? value : '' })) : []
  }
}
export function validateClineHeaderRows(rows: HeaderOverrideRow[]): boolean {
  const seen = new Set<string>()
  let total = 0
  const bytes = (s: string) => new TextEncoder().encode(s).length
  for (const row of rows) {
    const name = row.name.trim(), value = row.value
    if (!name && !value) continue
    const lower = name.toLowerCase()
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) || bytes(name) > 128 ||
        !(['user-agent', 'accept-language', 'x-request-id', 'x-client-name', 'x-client-version', 'http-referer', 'x-title'].includes(lower) || lower.startsWith('x-metadata-')) ||
        seen.has(lower) || bytes(value) > 2048 || [...value].some(char => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)) return false
    seen.add(lower)
    total += bytes(name) + bytes(value)
  }
  return seen.size <= 16 && total <= 8192
}
export function clineAccountHeaderCredentials(settings: ClineAccountSettings): Record<string, unknown> {
  if (!settings.headerOverrideEnabled) return { header_override_enabled: false, header_overrides: {} }
  if (!validateClineHeaderRows(settings.headerOverrideRows)) throw new ClineSettingsError('headers')
  const headers = Object.fromEntries(settings.headerOverrideRows.filter(r => r.name.trim()).map(r => [r.name.trim().toLowerCase(), r.value.trim()]))
  if (new TextEncoder().encode(JSON.stringify(headers)).length > 16384) throw new ClineSettingsError('headers')
  return { header_override_enabled: true, header_overrides: headers }
}
function checkedLimits(groupIDs: number[], limits: GroupAllowedModels): GroupAllowedModels {
  const out: GroupAllowedModels = {}
  for (const id of [...groupIDs].sort((a, b) => a - b)) {
    if (!(id in limits)) continue
    const models = limits[id]
    if (!models.length || models.some(m => !m.trim() || (/[\s*\\]/.test(m.trim()) || m.includes('\0')) || new TextEncoder().encode(m.trim()).length > 256)) throw new ClineSettingsError('groupModels')
    out[id] = [...new Set(models.map(m => m.trim()))].sort()
  }
  return out
}
export function clineAccountSettingsPayload(settings: ClineAccountSettings, groupIDs: number[], account?: Account | null): Partial<CreateAccountRequest & UpdateAccountRequest> {
  const before = clineAccountSettings(account)
  const out: Partial<CreateAccountRequest & UpdateAccountRequest> = {}
  if (settings.proxyID !== null && (!Number.isSafeInteger(settings.proxyID) || settings.proxyID <= 0)) throw new ClineSettingsError('proxy')
  if (![settings.rateMultiplier, settings.groupRateMultiplier].every(n => typeof n === 'number' && Number.isFinite(n) && n >= 0)) throw new ClineSettingsError('rateMultiplier')
  if (!isValidAccountCostMultiplier(settings.costMultiplier)) throw new ClineSettingsError('costMultiplier')
  if (settings.costMultiplier !== before.costMultiplier) out.extra = { cost_multiplier: settings.costMultiplier }
  // v-model.number produces an empty string for cleared number inputs.
  const load = settings.loadFactor === null || String(settings.loadFactor) === '' ? null : settings.loadFactor
  if (load !== null && (!Number.isInteger(load) || load < 1 || load > 10000)) throw new ClineSettingsError('loadFactor')
  if (!['active', 'inactive', 'error'].includes(settings.status)) throw new ClineSettingsError('status')
  if (settings.proxyID !== before.proxyID) out.proxy_id = settings.proxyID ?? 0
  if (settings.rateMultiplier !== before.rateMultiplier) out.rate_multiplier = settings.rateMultiplier
  if (settings.groupRateMultiplier !== before.groupRateMultiplier) out.group_rate_multiplier = settings.groupRateMultiplier
  if (load !== before.loadFactor) out.load_factor = load ?? 0
  if (settings.autoPauseOnExpired !== before.autoPauseOnExpired) out.auto_pause_on_expired = settings.autoPauseOnExpired
  if (account && settings.status !== before.status) out.status = settings.status
  if (settings.expiresAt !== before.expiresAt) {
    if (!settings.expiresAt) out.expires_at = 0
    else {
      // Native datetime-local controls may emit zero fractional seconds.
      // Accept that representation while keeping the API at whole seconds.
      const input = settings.expiresAt.replace(/\.0{1,3}$/, '')
      if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(input)) throw new ClineSettingsError('expiresAt')
      const date = new Date(input)
      if (!Number.isFinite(date.getTime())) throw new ClineSettingsError('expiresAt')
      const normalized = localDateTime(date.toISOString())
      if (normalized.slice(0, input.length) !== input) throw new ClineSettingsError('expiresAt')
      out.expires_at = Math.floor(date.getTime() / 1000)
    }
  }
  if (account) {
    const limits = checkedLimits(groupIDs, settings.groupAllowedModels)
    const previous = checkedLimits(account.group_ids ?? [], before.groupAllowedModels)
    if (JSON.stringify(limits) !== JSON.stringify(previous)) out.group_allowed_models = limits
  }
  return out
}
