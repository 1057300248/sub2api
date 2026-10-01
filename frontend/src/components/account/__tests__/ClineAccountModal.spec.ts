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
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
import ClineAccountModal from '../ClineAccountModal.vue'
const Dialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' })
const account = {
  id: 17, name: 'Existing Cline', platform: 'cline', type: 'apikey', notes: '', concurrency: 1, priority: 1, schedulable: true, group_ids: [999],
  credentials: { api_key: 'secret-stays-server-side', account_mode: 'pass', cline_auth_type: 'api_key', base_url: CLINE_BASE_URL, model_mapping: { public: 'cline-pass/model' } }
} as unknown as Account
function render(a: Account | null = null) {
  return mount(ClineAccountModal, { props: { show: true, account: a, groups: [] }, global: { stubs: { BaseDialog: Dialog } } })
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
    expect(metadata.getClineMetadata).toHaveBeenCalledExactlyOnceWith(17, expect.any(AbortSignal))
    expect(metadata.refreshClineMetadata).not.toHaveBeenCalled()
    await w.get('[data-testid="cline-refresh-metadata"]').trigger('click'); await flushPromises()
    expect(metadata.refreshClineMetadata).toHaveBeenCalledExactlyOnceWith(17, expect.any(AbortSignal))
    expect(api.create).not.toHaveBeenCalled(); expect(api.update).not.toHaveBeenCalled()
  })
})
