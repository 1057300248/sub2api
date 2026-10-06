import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ClineMetadataPanel from '../ClineMetadataPanel.vue'
import en from '@/i18n/locales/en/clineMetadata'
import zh from '@/i18n/locales/zh/clineMetadata'
import { getClineMetadata, refreshClineMetadata, type ClineMetadata } from '@/api/admin/clineMetadata'

vi.mock('@/api/admin/clineMetadata', () => ({ getClineMetadata: vi.fn(), refreshClineMetadata: vi.fn() }))
const fixture = (): ClineMetadata => ({ mode: 'pass', last_success_at: new Date().toISOString(), auth_type: 'api_key', quota_status: 'partial', credential_status: 'unknown', identity_verified: false, windows: [{ type: 'five_hour', percent_used: null }, { type: 'weekly', percent_used: 0 }, { type: 'monthly', percent_used: null }], catalog: { clinePass: [{ id: 'cline-pass/model' }], recommended: [], free: [{ id: 'vendor/free' }] }, catalog_status: 'stale', persisted: true, incremental_cost_usd: null })
// The application uses runtime-only vue-i18n. Static message functions work
// without enabling a message compiler in either the application or the tests.
const messages = Object.fromEntries(Object.entries(en).map(([key, value]) => [key, () => value]))
const setup = (accountId = 42) => mount(ClineMetadataPanel, { props: { accountId, mode: 'pass' }, global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { clineMetadata: messages } } })] } })

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
  it.each([
    { name: 'explicit Pass model', mode: 'pass', id: 'cline-pass/model', enabled: true },
    { name: 'explicit PAYG model', mode: 'payg', id: 'vendor/model', enabled: true },
    { name: 'maximum length', mode: 'pass', id: 'cline-pass/' + 'a'.repeat(245), enabled: true },
    { name: 'empty ID', mode: 'pass', id: '', enabled: false },
    { name: 'empty Pass suffix', mode: 'pass', id: 'cline-pass/', enabled: false },
    { name: 'wildcard', mode: 'pass', id: 'cline-pass/*', enabled: false },
    { name: 'backslash', mode: 'pass', id: 'cline-pass/model\\suffix', enabled: false },
    { name: 'NUL', mode: 'pass', id: 'cline-pass/model' + String.fromCharCode(0), enabled: false },
    { name: 'ASCII whitespace', mode: 'pass', id: 'cline-pass/model name', enabled: false },
    { name: 'Unicode whitespace', mode: 'pass', id: 'cline-pass/model\u00a0name', enabled: false },
    { name: 'overlong ID', mode: 'pass', id: 'cline-pass/' + 'a'.repeat(246), enabled: false },
    { name: 'PAYG target in Pass', mode: 'pass', id: 'vendor/model', enabled: false },
    { name: 'Pass target in PAYG', mode: 'payg', id: 'cline-pass/model', enabled: false },
    { name: 'Free is observation only', mode: 'free', id: 'vendor/free', enabled: false }
  ])('validates catalog selection: $name', async ({ mode, id, enabled }) => {
    const data = fixture()
    data.catalog = { clinePass: [{ id }], recommended: [{ id }], free: [{ id }] }
    vi.mocked(getClineMetadata).mockResolvedValueOnce(data)
    const wrapper = setup()
    try {
      await wrapper.setProps({ mode })
      await flushPromises()
      const buttons = wrapper.findAll('button').filter(button => button.text() === en.add)
      expect(buttons).toHaveLength(1)
      expect(buttons[0].element.disabled).toBe(!enabled)
      await buttons[0].trigger('click')
      expect(wrapper.emitted('select')).toEqual(enabled ? [[id]] : undefined)
      expect(refreshClineMetadata).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
    }
  })
  it('does not offer any catalog selection for unknown mode', async () => {
    const wrapper = setup()
    try {
      await wrapper.setProps({ mode: 'unknown' })
      await flushPromises()
      expect(wrapper.findAll('button').filter(button => button.text() === en.add)).toHaveLength(0)
      expect(wrapper.emitted('select')).toBeUndefined()
    } finally {
      wrapper.unmount()
    }
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
