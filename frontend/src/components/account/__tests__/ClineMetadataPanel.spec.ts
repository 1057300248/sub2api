import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ClineMetadataPanel from '../ClineMetadataPanel.vue'
import en from '@/i18n/locales/en/clineMetadata'
import zh from '@/i18n/locales/zh/clineMetadata'
import { getClineMetadata, refreshClineMetadata, type ClineMetadata } from '@/api/admin/clineMetadata'

vi.mock('@/api/admin/clineMetadata', () => ({ getClineMetadata: vi.fn(), refreshClineMetadata: vi.fn() }))
const fixture = (): ClineMetadata => ({ mode: 'pass', last_success_at: new Date().toISOString(), auth_type: 'api_key', quota_status: 'partial', credential_status: 'unknown', identity_verified: false, windows: [{ type: 'five_hour', percent_used: null }, { type: 'weekly', percent_used: 0 }, { type: 'monthly', percent_used: null }], catalog: { clinePass: [{ id: 'cline-pass/model' }], recommended: [], free: [{ id: 'vendor/free' }] }, catalog_status: 'stale', persisted: true, incremental_cost_usd: null })
const setup = (accountId = 42) => mount(ClineMetadataPanel, { props: { accountId, mode: 'pass' }, global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { clineMetadata: en } } })] } })

beforeEach(() => { vi.resetAllMocks(); vi.mocked(getClineMetadata).mockResolvedValue(fixture()); vi.mocked(refreshClineMetadata).mockResolvedValue(fixture()) })

describe('Cline metadata safety', () => {
  it('reads cached state only and distinguishes zero from unknown', async () => {
    const wrapper = setup()
    await flushPromises()
    expect(getClineMetadata).toHaveBeenCalledTimes(1)
    expect(refreshClineMetadata).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="cline-window-five_hour"]').text()).toBe('—')
    expect(wrapper.get('[data-testid="cline-window-weekly"]').text()).toBe('0%')
    expect(wrapper.emitted('select')).toBeUndefined()
    wrapper.unmount()
  })
  it('refreshes only on a click and preserves the last observation on failure', async () => {
    const wrapper = setup()
    await flushPromises()
    vi.mocked(refreshClineMetadata).mockRejectedValue({ config: { headers: { Authorization: 'secret-fixture' } } })
    await wrapper.get('[data-testid="cline-refresh-metadata"]').trigger('click')
    await flushPromises()
    expect(refreshClineMetadata).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('secret-fixture')
    expect(wrapper.get('[data-testid="cline-window-five_hour"]').text()).toBe('—')
    wrapper.unmount()
  })
  it('requires an explicit model selection and cannot select Free', async () => {
    const wrapper = setup()
    await flushPromises()
    const button = wrapper.findAll('button').find(b => b.text() === en.add)!
    await button.trigger('click')
    expect(wrapper.emitted('select')).toEqual([['cline-pass/model']])
    await wrapper.setProps({ mode: 'free' })
    expect(wrapper.findAll('button').find(b => b.text() === en.add)!.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('discards an old account response after switching accounts', async () => {
    let resolveOld!: (value: ClineMetadata) => void
    vi.mocked(getClineMetadata).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = setup()
    const next = fixture()
    next.windows[1].percent_used = 75
    vi.mocked(getClineMetadata).mockResolvedValueOnce(next)
    await wrapper.setProps({ accountId: 43 })
    await flushPromises()
    resolveOld(fixture())
    await flushPromises()
    expect(wrapper.get('[data-testid="cline-window-weekly"]').text()).toBe('75%')
    wrapper.unmount()
  })
  it('keeps translation keys aligned', () => { expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort()) })
})
