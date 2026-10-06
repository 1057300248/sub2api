import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { Account, AdminGroup } from '@/types'
import { CLINE_BASE_URL } from '../clineAccountForm'

enableAutoUnmount(afterEach)
const api = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }))
const metadata = vi.hoisted(() => ({ getClineMetadata: vi.fn(), refreshClineMetadata: vi.fn() }))
vi.mock('@/api/admin/accounts', () => api)
// The metadata API has its own module, outside the accounts API mock. Keep
// that network boundary mocked rather than importing the application's HTTP
// client and createI18n through an intentionally minimal useI18n mock.
vi.mock('@/api/admin/clineMetadata', () => metadata)
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAvailableModels: vi.fn().mockResolvedValue([]) } } }))
import ClineAccountModal from '../ClineAccountModal.vue'
import HeaderOverrideEditor from '../HeaderOverrideEditor.vue'
import AccountGroupModelLimits from '../AccountGroupModelLimits.vue'
const Dialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' })
const account = {
  id: 17, name: 'Existing Cline', platform: 'cline', type: 'apikey', notes: '', concurrency: 1, priority: 1, schedulable: true, group_ids: [999],
  credentials: { api_key: 'secret-stays-server-side', account_mode: 'pass', cline_auth_type: 'api_key', base_url: CLINE_BASE_URL, model_mapping: { public: 'cline-pass/model' } }
} as unknown as Account
function render(a: Account | null = null) {
  return mount(ClineAccountModal, { props: { show: true, account: a, groups: [] }, global: { stubs: { BaseDialog: Dialog, ModelWhitelistSelector: true } } })
}
async function fillNew(w: ReturnType<typeof render>) {
  await w.get('[data-testid="cline-name"]').setValue('New Cline')
  await w.get('[data-testid="cline-key"]').setValue('fixture-key')
  await w.get('[data-testid="cline-public-model"]').setValue('public')
  await w.get('[data-testid="cline-upstream-model"]').setValue('cline-pass/model')
}
beforeEach(() => {
  vi.clearAllMocks()
  api.create.mockResolvedValue({ id: 1 })
  api.update.mockResolvedValue(account)
  const state = { mode: 'pass', auth_type: 'api_key', quota_status: 'unknown', credential_status: 'unknown', identity_verified: false, windows: [], catalog_status: 'unknown', persisted: false, incremental_cost_usd: null }
  metadata.getClineMetadata.mockResolvedValue(state)
  metadata.refreshClineMetadata.mockResolvedValue(state)
})

describe('independent Cline account modal', () => {
  it('does not query or test credentials when opened', async () => {
    render(account); await flushPromises()
    expect(api.create).not.toHaveBeenCalled(); expect(api.update).not.toHaveBeenCalled()
    expect(metadata.getClineMetadata).not.toHaveBeenCalled(); expect(metadata.refreshClineMetadata).not.toHaveBeenCalled()
  })
  it('submits a complete explicit Cline create payload once', async () => {
    const w = render(); await fillNew(w)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(api.create).toHaveBeenCalledTimes(1)
    expect(api.create.mock.calls[0][0]).toMatchObject({ platform: 'cline', type: 'apikey', credentials: { cline_auth_type: 'api_key', api_protocol: 'chat_completions', account_mode: 'pass', model_mapping: { public: 'cline-pass/model' } } })
    expect(w.emitted('saved')).toHaveLength(1); expect(w.emitted('close')).toHaveLength(1)
    expect(metadata.getClineMetadata).not.toHaveBeenCalled(); expect(metadata.refreshClineMetadata).not.toHaveBeenCalled()
  })
  it('never loads a stored secret into the edit DOM or overwrites unseen group limits', async () => {
    const w = render(account)
    expect(w.html()).not.toContain('secret-stays-server-side')
    expect(w.get<HTMLInputElement>('[data-testid="cline-key"]').element.value).toBe('')
    expect(w.text()).toContain('#999')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(api.update).toHaveBeenCalledTimes(1)
    expect(api.update.mock.calls[0][1].credentials).not.toHaveProperty('api_key')
    expect(api.update.mock.calls[0][1]).not.toHaveProperty('group_ids')
    expect(api.update.mock.calls[0][1]).not.toHaveProperty('extra')
  })
  it('retains model IDs and blocks saving incompatible mode changes', async () => {
    const w = render(account)
    await w.get('[data-testid="cline-mode"]').setValue('payg')
    expect(w.get<HTMLInputElement>('[data-testid="cline-upstream-model"]').element.value).toBe('cline-pass/model')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(api.update).not.toHaveBeenCalled(); expect(w.get('[role="alert"]').text()).toBe('clineAccount.errors.paygModel')
  })
  it('never grants public Free API forwarding', async () => {
    const w = render(account); await w.get('[data-testid="cline-mode"]').setValue('free')
    expect(w.get('[role="status"]').text()).toBe('clineAccount.freeNotice')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(api.update.mock.calls[0][1]).toMatchObject({ schedulable: false, credentials: { account_mode: 'free', cline_free_api_enabled: false, cline_paid_fallback: false } })
  })
  it('retains input after errors without exposing upstream or Axios credential details', async () => {
    api.create.mockRejectedValue({ message: 'secret-response', config: { data: 'fixture-key' } })
    const w = render(); await fillNew(w); await w.get('form').trigger('submit'); await flushPromises()
    expect(w.get('[role="alert"]').text()).toBe('clineAccount.saveFailed')
    expect(w.text()).not.toContain('secret-response'); expect(w.emitted('saved')).toBeUndefined()
    expect(w.get<HTMLInputElement>('[data-testid="cline-name"]').element.value).toBe('New Cline')
  })
  it('disallows duplicate submissions while saving and clears secrets on close', async () => {
    let resolve: (v: unknown) => void = () => {}
    api.create.mockReturnValue(new Promise(r => { resolve = r }))
    const w = render(); await fillNew(w); await w.get('form').trigger('submit'); await w.get('form').trigger('submit')
    expect(api.create).toHaveBeenCalledTimes(1)
    resolve({}); await flushPromises(); await w.setProps({ show: false }); await w.setProps({ show: true })
    expect(w.get<HTMLInputElement>('[data-testid="cline-key"]').element.value).toBe('')
  })
  it('filters new groups without removing unavailable existing bindings', async () => {
    const w = render(account)
    await w.setProps({ allowComposite: false, groups: [{ id: 1, name: 'Cline group', platform: 'cline' }, { id: 2, name: 'Other platform', platform: 'deepseek' }, { id: 3, name: 'Composite hidden', platform: 'composite' }] as AdminGroup[] })
    expect(w.text()).toContain('Cline group'); expect(w.text()).toContain('#999')
    expect(w.text()).not.toContain('Other platform'); expect(w.text()).not.toContain('Composite hidden')
  })
  it('reads saved-account metadata only after explicit panel and refresh actions', async () => {
    const w = render(account)
    await w.get('[data-testid="cline-key"]').setValue('unsaved-replacement-key')
    await flushPromises()
    expect(metadata.getClineMetadata).not.toHaveBeenCalled()
    const open = w.findAll('button').find(button => button.text() === 'clineMetadata.open')!
    await open.trigger('click'); await flushPromises()
    expect(metadata.getClineMetadata).toHaveBeenCalledTimes(1)
    expect(metadata.getClineMetadata).toHaveBeenCalledWith(17, expect.any(AbortSignal))
    expect(metadata.refreshClineMetadata).not.toHaveBeenCalled()
    await w.get('[data-testid="cline-refresh-metadata"]').trigger('click'); await flushPromises()
    expect(metadata.refreshClineMetadata).toHaveBeenCalledTimes(1)
    expect(metadata.refreshClineMetadata).toHaveBeenCalledWith(17, expect.any(AbortSignal))
    expect(api.create).not.toHaveBeenCalled(); expect(api.update).not.toHaveBeenCalled()
  })
})

it('explains manual account tokens without probing or changing models', async () => {
  const w=render(account)
  await w.get('[data-testid="cline-auth"]').setValue('account_token')
  expect(w.get('[data-testid="cline-manual-token-notice"]').text()).toBe('clineAccount.manualTokenNotice')
  expect(w.get<HTMLInputElement>('[data-testid="cline-upstream-model"]').element.value).toBe('cline-pass/model')
  expect(metadata.getClineMetadata).not.toHaveBeenCalled()
  expect(metadata.refreshClineMetadata).not.toHaveBeenCalled()
  expect(api.create).not.toHaveBeenCalled()
  expect(api.update).not.toHaveBeenCalled()
})


describe('Cline common settings DOM and save', () => {
  it('submits proxy, distinct rates, load, expiry and scheduling through the same create request', async () => {
    const w = render(); await fillNew(w)
    await w.setProps({ proxies: [{ id: 4, name: 'working proxy', status: 'active' }] as any })
    await w.get('[data-testid="cline-proxy"]').setValue('4')
    await w.get('[data-testid="cline-rate"]').setValue('0')
    await w.get('[data-testid="cline-group-rate"]').setValue('2.5')
    await w.get('[data-testid="cline-cost"]').setValue('0.2')
    await w.get('[data-testid="cline-load-factor"]').setValue('7')
    await w.get('[data-testid="cline-expiry"]').setValue('2030-10-06T11:23:44')
    await w.get('[data-testid="cline-auto-pause"]').setValue(false)
    await w.get('[data-testid="cline-schedulable"]').setValue(false)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(w.find('[role="alert"]').exists(), w.text()).toBe(false)
    expect(api.create).toHaveBeenCalledTimes(1)
    expect(api.create.mock.calls[0][0]).toMatchObject({ proxy_id: 4, rate_multiplier: 0, group_rate_multiplier: 2.5, extra: { cost_multiplier: 0.2 }, load_factor: 7, expires_at: new Date('2030-10-06T11:23:44').getTime()/1000, auto_pause_on_expired: false, schedulable: false })
    expect(api.update).not.toHaveBeenCalled()
    expect(metadata.refreshClineMetadata).not.toHaveBeenCalled()
  })
  it('renders unavailable configured proxies, preserves them unchanged, then explicitly clears nullable settings', async () => {
    const a = { ...account, proxy_id: 9, proxy_fallback_origin_id: 8, load_factor: 4, expires_at: Date.parse('2030-10-06T11:23:44Z') / 1000, auto_pause_on_expired: true, status: 'active' } as Account
    const w = render(a)
    expect(w.get<HTMLSelectElement>('[data-testid="cline-proxy"]').element.value).toBe('8')
    expect(w.get('[data-testid="cline-proxy"]').text()).toContain('#8')
    await w.get('[data-testid="cline-proxy"]').setValue('')
    await w.get('[data-testid="cline-load-factor"]').setValue('')
    await w.get('[data-testid="cline-expiry"]').setValue('')
    await w.get('[data-testid="cline-status"]').setValue('inactive')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(w.find('[role="alert"]').exists(), w.text()).toBe(false)
    expect(api.update).toHaveBeenCalledTimes(1)
    expect(api.update.mock.calls[0][1]).toMatchObject({ proxy_id: 0, load_factor: 0, expires_at: 0, status: 'inactive' })
  })
  it('saves edited account default headers and rejects protected names before any write', async () => {
    const w = render(account)
    await w.get('[data-testid="cline-headers-enabled"]').setValue(true)
    w.getComponent(HeaderOverrideEditor).vm.$emit('update:rows', [{ name: 'Authorization', value: 'secret-must-not-send' }])
    await flushPromises(); await w.get('form').trigger('submit'); await flushPromises()
    expect(api.update).not.toHaveBeenCalled()
    expect(w.get('[role="alert"]').text()).toBe('clineAccount.errors.headers')
    w.getComponent(HeaderOverrideEditor).vm.$emit('update:rows', [{ name: 'User-Agent', value: 'client/2' }])
    await flushPromises(); await w.get('form').trigger('submit'); await flushPromises()
    expect(api.update.mock.calls[0][1].credentials).toMatchObject({ header_override_enabled: true, header_overrides: { 'user-agent': 'client/2' } })
    expect(api.update.mock.calls[0][1].credentials).not.toHaveProperty('api_key')
  })
  it('offers per-group model settings without silently clearing unseen restrictions', async () => {
    const a = { ...account, group_ids: [999], account_groups: [{ group_id: 999, allowed_models: ['public'] }] } as Account
    const w = render(a)
    expect(w.getComponent(AccountGroupModelLimits).props('modelValue')).toEqual({ 999: ['public'] })
    await w.get('[data-testid="group-model-limit-999"] [data-testid="group-model-limit-all"]').trigger('click')
    await w.get('[data-testid="group-model-limit-999"] [data-testid="group-model-limit-selected"]').trigger('click')
    expect(w.get('[data-testid="group-model-limit-empty"]').text()).toBe('clineAccount.errors.groupModels')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(api.update).not.toHaveBeenCalled()
    expect(w.get('[role="alert"]').text()).toBe('clineAccount.errors.groupModels')
  })
})

it('saves Cline advanced deltas without replaying local used counters or Pass state', async () => {
  const a = { ...account, extra: { quota_limit: 10, quota_used: 9, cline_state: { marker: 'runtime-only' }, cost_multiplier: 0.2 } } as Account
  const w = render(a)
  const child = w.findComponent({ name: 'ClineAdvancedSettings' })
  child.vm.$emit('update:modelValue', { ...child.props('modelValue'), totalLimit: 20 })
  await flushPromises(); await w.get('form').trigger('submit'); await flushPromises()
  expect(api.update).toHaveBeenCalledTimes(1)
  expect(api.update.mock.calls[0][1].extra).toEqual({ quota_limit: 20 })
  expect(JSON.stringify(api.update.mock.calls[0][1])).not.toContain('runtime-only')
})
