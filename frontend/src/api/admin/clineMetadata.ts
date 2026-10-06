import { apiClient } from '../client'

export interface ClineModelCapabilities {
  context_window?: number
  max_output_tokens?: number
  supports_images?: boolean
  supports_tools?: boolean
  supports_reasoning?: boolean
  supports_prompt_cache?: boolean
  reasoning_efforts?: string[]
  source: string
}
export interface ClineCatalogModel { id: string; name?: string; description?: string; capabilities?: ClineModelCapabilities }
export interface ClineModelState { id: string; listed: boolean; configured: boolean; capabilities?: ClineModelCapabilities; capability_status: string; entitlement: string; last_successful_at?: string }
export interface ClineRoutingObservation { requested?: string[]; constraint_status: string; actual?: string; status: string; source?: string; upstream_model: string; observed_at: string; complete: boolean; http_status: number }
export interface ClineMetadata {
  routing?: ClineRoutingObservation
  models?: ClineModelState[]
  auto_refresh?: boolean
  next_refresh_at?: string
  recovery_status?: string
  cooldowns?: { type: string; source: string; reset_at?: string; retry_at?: string; status: string }[] | null
  mode: string
  auth_type: string
  quota_status: string
  credential_status: string
  identity_verified: boolean
  windows: { type: string; percent_used: number | null; resets_at?: string }[]
  catalog?: { recommended: ClineCatalogModel[] | null; clinePass: ClineCatalogModel[] | null; free: ClineCatalogModel[] | null }
  catalog_status: string
  catalog_fetched_at?: string
  fetched_at?: string
  last_success_at?: string
  persisted: boolean
  error?: string
  incremental_cost_usd: number | null
}

export async function getClineMetadata(id: number, signal?: AbortSignal): Promise<ClineMetadata> {
  const { data } = await apiClient.get<ClineMetadata>(`/admin/accounts/${id}/cline/state`, { signal })
  return data
}

export async function refreshClineMetadata(id: number, signal?: AbortSignal): Promise<ClineMetadata> {
  const { data } = await apiClient.post<ClineMetadata>(`/admin/accounts/${id}/cline/refresh`, {}, { signal, timeout: 25000 })
  return data
}
