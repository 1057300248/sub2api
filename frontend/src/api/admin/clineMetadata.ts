import { apiClient } from '../client'

export interface ClineCatalogModel { id: string; name?: string; description?: string }
export interface ClineMetadata {
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
