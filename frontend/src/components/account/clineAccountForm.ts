import type { Account, CreateAccountRequest, UpdateAccountRequest } from '@/types'

export const CLINE_BASE_URL = 'https://api.cline.bot/api/v1'
export type ClineMode = 'pass' | 'free' | 'payg' | 'unknown'
export type ClineAuthType = 'api_key' | 'account_token'
export interface ClineModelRow { publicID: string; upstreamID: string }
export interface ClineAccountDraft {
  name: string
  notes: string
  mode: ClineMode
  authType: ClineAuthType | ''
  apiKey: string
  baseURL: string
  models: ClineModelRow[]
  concurrency: number
  priority: number
  groupIDs: number[]
  schedulable: boolean
}
export type ClineFormIssue = 'name' | 'key' | 'authType' | 'baseURL' | 'models' | 'duplicateModel' | 'passModel' | 'paygModel' | 'concurrency' | 'priority' | 'groups' | 'platform'
export class ClineFormError extends Error {
  constructor(public readonly issue: ClineFormIssue) { super(issue) }
}
export function clineMode(value: unknown): ClineMode {
  return value === 'pass' || value === 'free' || value === 'payg' ? value : 'unknown'
}
export function clineModeLabel(value: unknown): string {
  return { pass: 'Cline Pass', free: 'Cline Free', payg: 'Cline PAYG', unknown: 'Cline · ?' }[clineMode(value)]
}
export function clineAccountDraft(account?: Account | null): ClineAccountDraft {
  if (account && account.platform !== 'cline') throw new ClineFormError('platform')
  const credentials = account?.credentials ?? {}
  const mapping = credentials.model_mapping
  // Keep invalid legacy rows visible and unsavable until the operator fixes
  // them. Do not drop a stale whitelist entry or substitute paid model IDs.
  const models = mapping && typeof mapping === 'object' && !Array.isArray(mapping)
    ? Object.entries(mapping).map(([publicID, upstreamID]) => ({ publicID, upstreamID: typeof upstreamID === 'string' ? upstreamID : '' }))
    : []
  const authType = credentials.cline_auth_type
  return {
    name: account?.name ?? '', notes: account?.notes ?? '',
    mode: account ? clineMode(credentials.account_mode) : 'pass',
    authType: !account ? 'api_key' : authType === 'api_key' || authType === 'account_token' ? authType : '',
    // Never put an existing secret/redacted placeholder into the form DOM.
    apiKey: '', baseURL: typeof credentials.base_url === 'string' ? credentials.base_url : CLINE_BASE_URL,
    models: models.length ? models : [{ publicID: '', upstreamID: '' }],
    concurrency: account?.concurrency ?? 1, priority: account?.priority ?? 1,
    groupIDs: [...(account?.group_ids ?? [])], schedulable: account?.schedulable ?? true
  }
}
function validID(value: string): boolean {
  return value.length > 0 && new TextEncoder().encode(value).length <= 256 && !value.includes("\0") && !/[\s*\\]/u.test(value)
}
function checkedCredentials(draft: ClineAccountDraft, editing: boolean): Record<string, unknown> {
  if (!editing && !draft.apiKey.trim()) throw new ClineFormError('key')
  if (draft.authType !== 'api_key' && draft.authType !== 'account_token') throw new ClineFormError('authType')
  let baseURL = draft.baseURL.trim() || CLINE_BASE_URL
  try {
    const u = new URL(baseURL)
    if (u.protocol !== 'https:' || !u.hostname || u.username || u.password || u.search || u.hash || /\/(chat\/completions|responses|messages)\/?$/.test(u.pathname)) throw new Error('base')
    if (u.hostname.toLowerCase() === 'api.cline.bot') {
      if (u.port && u.port !== '443') throw new Error('port')
      if (!['', '/api', '/api/v1'].includes(u.pathname.replace(/\/+$/, ''))) throw new Error('path')
      baseURL = CLINE_BASE_URL
    }
  } catch { throw new ClineFormError('baseURL') }
  if (!draft.models.length) throw new ClineFormError('models')
  const seen = new Set<string>()
  const entries = draft.models.map(row => {
    const id = row.publicID.trim(), model = row.upstreamID.trim()
    if (!validID(id) || !validID(model)) throw new ClineFormError('models')
    if (seen.has(id)) throw new ClineFormError('duplicateModel')
    seen.add(id)
    if (draft.mode === 'pass' && (!model.startsWith('cline-pass/') || model === 'cline-pass/')) throw new ClineFormError('passModel')
    if (draft.mode === 'payg' && model.startsWith('cline-pass/')) throw new ClineFormError('paygModel')
    return [id, model]
  })
  const result: Record<string, unknown> = {
    account_mode: clineMode(draft.mode), cline_auth_type: draft.authType,
    base_url: baseURL, api_protocol: 'chat_completions', model_mapping: Object.fromEntries(entries),
    pool_mode: false, cline_paid_fallback: false, cline_free_api_enabled: false, openai_passthrough: false
  }
  if (draft.apiKey.trim()) result.api_key = draft.apiKey.trim()
  return result
}
export function buildClineAccountPayload(draft: ClineAccountDraft, account?: Account | null): CreateAccountRequest | UpdateAccountRequest {
  if (account && account.platform !== 'cline') throw new ClineFormError('platform')
  if (!draft.name.trim()) throw new ClineFormError('name')
  if (!Number.isInteger(draft.concurrency) || draft.concurrency < 1 || draft.concurrency > 1000) throw new ClineFormError('concurrency')
  if (!Number.isInteger(draft.priority) || draft.priority < 0 || draft.priority > 10000) throw new ClineFormError('priority')
  if (draft.groupIDs.some(id => !Number.isSafeInteger(id) || id <= 0)) throw new ClineFormError('groups')
  const common = {
    name: draft.name.trim(), notes: draft.notes || null, credentials: checkedCredentials(draft, !!account),
    concurrency: draft.concurrency, priority: draft.priority
  }
  const groupIDs = [...new Set(draft.groupIDs)]
  if (!account) return { ...common, platform: 'cline', type: 'apikey', group_ids: groupIDs }
  const update: UpdateAccountRequest = {
    ...common, schedulable: (draft.mode === 'pass' || draft.mode === 'payg') && draft.schedulable
  }
  const before = [...(account.group_ids ?? [])].sort((a, b) => a - b)
  if (JSON.stringify(before) !== JSON.stringify([...groupIDs].sort((a, b) => a - b))) update.group_ids = groupIDs
  // Do not send Extra, billing settings, probe state, a platform conversion,
  // or unchanged group bindings/limits as collateral of a credential edit.
  return update
}
