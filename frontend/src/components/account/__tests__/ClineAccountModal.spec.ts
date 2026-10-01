import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ClineAccountModal from '../ClineAccountModal.vue'
import { getClineMetadata, refreshClineMetadata } from '@/api/admin/clineMetadata'

const create = vi.fn()
const update = vi.fn()
const listGroups = vi.fn()
const showError = vi.fn()

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin', () => ({ default: {
  accounts: { create: (...args: unknown[]) => create(...args), update: (...args: unknown[]) => update(...args) },
  groups: { getAll: (...args: unknown[]) => listGroups(...args) }
} }))
// The dedicated metadata module is a separate network boundary. Mocking only
// the admin barrel would load the real HTTP client and application i18n here.
vi.mock('@/api/admin/clineMetadata', () => ({ getClineMetadata: vi.fn(), refreshClineMetadata: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError }) }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: { template: '<div><slot /></div>' } }))

enableAutoUnmount(afterEach)
const mountModal = (props: Record<string, unknown> = {}) => mount(ClineAccountModal, { props: { show: true, ...props } })

beforeEach(() => {
  create.mockReset().mockResolvedValue({ id: 1 })
  update.mockReset().mockResolvedValue({ id: 2 })
  listGroups.mockReset().mockResolvedValue([])
  showError.mockReset()
  vi.mocked(getClineMetadata).mockReset()
  vi.mocked(refreshClineMetadata).mockReset()
})

describe('ClineAccountModal safety', () => {
  it('saves edits without probing and keeps cooldowns, limits and existing secret unspecified', async () => {
    const account = { id: 9, name: 'pass', platform: 'cline', type: 'apikey', group_ids: [11], credentials: { account_mode: 'pass', cline_auth_type: 'account_token', base_url: 'https://api.cline.bot/api/v1', api_protocol: 'chat_completions', model_mapping: { public: 'cline-pass/model' } }, extra: { model_rate_limits: { old: 'preserve' }, budget: 77 } }
    listGroups.mockResolvedValue([{ id: 11, name: 'existing', platform: 'cline' }])
    const wrapper = mountModal({ account })
    await flushPromises()
    await wrapper.get('[data-testid="cline-save-account"]').trigger('click')
    await flushPromises()
    expect(update).toHaveBeenCalledTimes(1)
    expect(create).not.toHaveBeenCalled()
    expect(getClineMetadata).not.toHaveBeenCalled()
    expect(refreshClineMetadata).not.toHaveBeenCalled()
    const [id, payload] = update.mock.calls[0]
    expect(id).toBe(9)
    expect(payload).not.toHaveProperty('extra')
    expect(payload).not.toHaveProperty('concurrency')
    expect(payload).not.toHaveProperty('group_ids')
    expect(payload.credentials).not.toHaveProperty('api_key')
    expect(payload.credentials.cline_auth_type).toBe('account_token')
    expect(wrapper.emitted('saved')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
  it('cannot accidentally submit an incompatible mode whitelist', async () => {
    const wrapper = mountModal({ account: { id: 9, name: 'pass', platform: 'cline', type: 'apikey', group_ids: [], credentials: { account_mode: 'pass', cline_auth_type: 'api_key', model_mapping: { public: 'cline-pass/model' } } } })
    await flushPromises()
    await wrapper.get('[data-testid="cline-mode"]').setValue('payg')
    await wrapper.get('[data-testid="cline-save-account"]').trigger('click')
    expect(update).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('clineAccount.error.modeModel')
  })
  it('does not log credential-bearing errors', async () => {
    const account = { id: 9, name: 'error', platform: 'cline', type: 'apikey', group_ids: [], credentials: { account_mode: 'free', cline_auth_type: 'api_key', api_key: 'secret-fixture', model_mapping: {} } }
    update.mockRejectedValue({ config: { headers: { Authorization: 'secret-fixture' } } })
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const wrapper = mountModal({ account })
    await flushPromises()
    await wrapper.get('[data-testid="cline-save-account"]').trigger('click')
    await flushPromises()
    expect(errorSpy).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('clineAccount.error.save')
    errorSpy.mockRestore()
  })
})
