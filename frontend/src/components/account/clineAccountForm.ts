// Provider policy only. Creating/updating accounts, common form state, model
// widgets, proxy/group selectors and quotas belong to the native account forms.
import type { Account } from '@/types'
import type { HeaderOverrideRow } from './credentialsBuilder'
import type { GroupAllowedModels } from './groupAllowedModels'
import type { ModelMappingEntry } from '@/composables/useModelWhitelist'

export const CLINE_BASE_URL = 'https://api.cline.bot/api/v1'
export type ClineMode = 'pass' | 'free' | 'payg' | 'unknown'
export type ClineAuthType = 'api_key' | 'account_token'
export const clineModeOptions = [
  { value: 'pass', label: 'Cline Pass' },
  { value: 'payg', label: 'Cline PAYG' },
  { value: 'free', label: 'Cline Free' },
  { value: 'unknown', label: 'Cline · ?' }
]
export const clineAuthOptions = [
  { value: 'api_key', label: 'API Key' },
  { value: 'account_token', label: 'Account Token' }
]
export function clineMode(value: unknown): ClineMode {
  return value === 'pass' || value === 'free' || value === 'payg' ? value : 'unknown'
}
export function clineModeLabel(value: unknown): string {
  return clineModeOptions.find(option => option.value === clineMode(value))!.label
}
const bytes = (value: string) => new TextEncoder().encode(value).length
const validID = (value: string) => value.length > 0 && bytes(value) <= 256 && !value.includes('\0') && !/[\s*\\]/u.test(value)

// Validate BEFORE the shared mapping builder, which intentionally drops invalid
// general-provider rows. An invalid/stale Cline whitelist must remain visible.
export function clineModelError(mode: ClineMode, allowed: string[], mappings: ModelMappingEntry[]): string | null {
  if (!allowed.length && !mappings.length) return 'models'
  const seen = new Set<string>()
  for (const { from, to } of [...allowed.map(id => ({ from: id, to: id })), ...mappings]) {
    const id = from.trim(), model = to.trim()
    if (!validID(id) || !validID(model)) return 'models'
    if (seen.has(id)) return 'duplicateModel'
    seen.add(id)
    if (mode === 'pass' && (!model.startsWith('cline-pass/') || model === 'cline-pass/')) return 'passModel'
    if (mode === 'payg' && model.startsWith('cline-pass/')) return 'paygModel'
  }
  return null
}

// Reuse the native per-group widget/serializer, but do not let its generic empty
// list normalization broaden an explicitly selected Cline group whitelist.
export function clineGroupModelError(groupIDs: number[], limits: GroupAllowedModels): boolean {
  return groupIDs.some(id => id in limits && (!limits[id].length || limits[id].some(model => !validID(model.trim()))))
}

export function applyClineCredentialFields(credentials: Record<string, unknown>, mode: ClineMode, auth: ClineAuthType): string | null {
  if (auth !== 'api_key' && auth !== 'account_token') return 'authType'
  let base = typeof credentials.base_url === 'string' ? credentials.base_url.trim() : ''
  base ||= CLINE_BASE_URL
  try {
    const url = new URL(base)
    if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash || /\/(chat\/completions|responses|messages)\/?$/.test(url.pathname)) return 'baseURL'
    if (url.hostname.toLowerCase() === 'api.cline.bot') {
      if (url.port && url.port !== '443') return 'baseURL'
      if (!['', '/api', '/api/v1'].includes(url.pathname.replace(/\/+$/, ''))) return 'baseURL'
      base = CLINE_BASE_URL
    }
  } catch { return 'baseURL' }
  Object.assign(credentials, {
    base_url: base, account_mode: clineMode(mode), cline_auth_type: auth,
    api_protocol: 'chat_completions', pool_mode: false,
    cline_paid_fallback: false, cline_free_api_enabled: false, openai_passthrough: false
  })
  delete credentials.api_base_urls
  delete credentials.pool_mode_retry_count
  delete credentials.pool_mode_retry_status_codes
  return null
}

// The native HeaderOverrideEditor and serializer are reused; Cline's smaller
// upstream allowlist/bounds remain a provider validation hook, not another UI.
export function validateClineHeaderRows(rows: HeaderOverrideRow[]): boolean {
  const seen = new Set<string>()
  let total = 0
  for (const { name: rawName, value } of rows) {
    const name = rawName.trim(), lower = name.toLowerCase()
    if (!name && !value) continue
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) || bytes(name) > 128 ||
      !(['user-agent', 'accept-language', 'x-request-id', 'x-client-name', 'x-client-version', 'http-referer', 'x-title'].includes(lower) || lower.startsWith('x-metadata-')) ||
      seen.has(lower) || bytes(value) > 2048 || [...value].some(c => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127)) return false
    seen.add(lower)
    total += bytes(name) + bytes(value)
  }
  const encoded = Object.fromEntries(rows.filter(row => row.name.trim()).map(row => [row.name.trim().toLowerCase(), row.value.trim()]))
  return seen.size <= 16 && total <= 8192 && bytes(JSON.stringify(encoded)) <= 16384
}

export const CLINE_QUOTA_CONFIG_KEYS = [
  'quota_limit', 'quota_daily_limit', 'quota_weekly_limit',
  'quota_daily_reset_mode', 'quota_daily_reset_hour', 'quota_weekly_reset_mode',
  'quota_weekly_reset_day', 'quota_weekly_reset_hour', 'quota_reset_timezone',
  ...['total', 'daily', 'weekly'].flatMap(dim => ['enabled', 'threshold', 'threshold_type'].map(suffix => `quota_notify_${dim}_${suffix}`))
]
// Preserve the already-deployed Cline delta API contract while using the native
// quota/notification serializer. Never send a stale runtime/probe snapshot.
export function clineExtraDelta(next: Record<string, unknown>, before: Record<string, unknown>): Record<string, unknown> {
  const delta: Record<string, unknown> = {}
  for (const key of CLINE_QUOTA_CONFIG_KEYS) {
    if (JSON.stringify(next[key] ?? null) !== JSON.stringify(before[key] ?? null)) delta[key] = next[key] ?? null
  }
  // Native cost serializer intentionally omits an unchanged probed cost.
  for (const key of ['cost_multiplier', 'cost_multiplier_auto_sync']) {
    if (key in next && next[key] !== before[key]) delta[key] = next[key]
  }
  if ((next.upstream_request_id_header ?? '') !== (before.upstream_request_id_header ?? '')) {
    delta.upstream_request_id_header = next.upstream_request_id_header ?? ''
  }
  return delta
}

export function clineTestModelAllowed(account: Account | null | undefined, id: string): boolean {
  if (account?.platform !== 'cline' || account.type !== 'apikey' || !validID(id)) return false
  const mapping = account.credentials?.model_mapping
  if (!mapping || typeof mapping !== 'object' || Array.isArray(mapping) || !Object.prototype.hasOwnProperty.call(mapping, id)) return false
  const upstream = (mapping as Record<string, unknown>)[id]
  if (typeof upstream !== 'string' || !validID(upstream)) return false
  const mode = clineMode(account.credentials?.account_mode)
  return mode === 'pass' ? upstream.startsWith('cline-pass/') && upstream !== 'cline-pass/' : mode === 'payg' && !upstream.startsWith('cline-pass/')
}
