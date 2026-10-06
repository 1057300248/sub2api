import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { Account } from '@/types'
import ClineAdvancedSettings from '../ClineAdvancedSettings.vue'
import QuotaDimensionRow from '../QuotaDimensionRow.vue'
import { clineAdvancedDraft, clineAdvancedPayload } from '../clineAdvancedSettings'
const calls = vi.hoisted(() => ({ resetAccountQuota: vi.fn(), resetTempUnschedulable: vi.fn(), refreshClineMetadata: vi.fn(), getManagementCapabilities: vi.fn() }))
vi.mock('@/api/admin/accounts', () => calls)
vi.mock('@/api/admin/clineMetadata', () => calls)
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: calls } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
const account = { id: 23, platform: 'cline', type: 'apikey', credentials: {}, extra: { quota_limit: 100, quota_notify_total_enabled: true, quota_notify_total_threshold: 20, quota_notify_total_threshold_type: 'percentage' } } as Account
beforeEach(() => { vi.clearAllMocks(); calls.getManagementCapabilities.mockResolvedValue({ account_quota_notify_enabled: true }); calls.resetAccountQuota.mockResolvedValue(account); calls.resetTempUnschedulable.mockResolvedValue({}); calls.refreshClineMetadata.mockResolvedValue({}) })
function render() {
  const host = defineComponent({ components: { ClineAdvancedSettings }, setup() { const draft = ref(clineAdvancedDraft(account)); return { draft, account } }, template: '<ClineAdvancedSettings v-model="draft" :account="account" :can-operate="true" />' })
  return mount(host)
}
describe('Cline advanced real controls', () => {
  it('edits amounts and preserves zero notification threshold using real inputs', async () => {
    const w = render(); await flushPromises()
    const daily = w.findAllComponents(QuotaDimensionRow).find(c => c.props('dim') === 'daily')!
    await daily.get('input[type=number]').setValue('12.5')
    await w.get('[data-testid="cline-notify-total"] input[type=number]').setValue('0')
    const payload = clineAdvancedPayload(w.getComponent(ClineAdvancedSettings).props('modelValue'), account)
    expect(payload.extra).toEqual({ quota_daily_limit: 12.5, quota_notify_total_threshold: 0 })
    expect(calls.resetAccountQuota).not.toHaveBeenCalled(); expect(calls.refreshClineMetadata).not.toHaveBeenCalled()
  })
  it('adds ordered rules and disables them without erasing their configuration', async () => {
    const w = render(); await w.get('[data-testid=cline-add-rule]').trigger('click')
    await w.get('[data-testid=cline-temp-rule] textarea').setValue('temporary\ntry later')
    await w.get('[data-testid=cline-temp-enabled]').setValue(true)
    await w.get('[data-testid=cline-temp-enabled]').setValue(false)
    const payload = clineAdvancedPayload(w.getComponent(ClineAdvancedSettings).props('modelValue'), account)
    expect(payload.credentials.temp_unschedulable_enabled).toBe(false)
    expect(payload.credentials.temp_unschedulable_rules).toEqual([{ error_code: 503, duration_minutes: 5, keywords: ['temporary', 'try later'], description: '' }])
  })
  it.each(['quota', 'pause', 'recheck'] as const)('confirms only the %s operation without invoking the other actions', async action => {
    const w = render(); const c = w.getComponent(ClineAdvancedSettings)
    await w.get(`[data-testid=cline-action-${action}]`).trigger('click')
    expect(calls.resetAccountQuota).not.toHaveBeenCalled(); expect(calls.resetTempUnschedulable).not.toHaveBeenCalled(); expect(calls.refreshClineMetadata).not.toHaveBeenCalled()
    await w.get('[data-testid=cline-confirm-operation]').trigger('click'); await flushPromises()
    for (const [key, fn] of Object.entries({ quota: calls.resetAccountQuota, pause: calls.resetTempUnschedulable, recheck: calls.refreshClineMetadata })) { expect(fn).toHaveBeenCalledTimes(key === action ? 1 : 0) }
    expect(c.emitted('busy')).toEqual([[true], [false]]); expect(c.emitted('operated')).toHaveLength(1)
  })
  it('blocks state operations while the parent has unsaved changes', async () => {
    const w = mount(ClineAdvancedSettings, { props: { account, canOperate: false, modelValue: clineAdvancedDraft(account) } })
    expect(w.get<HTMLButtonElement>('[data-testid=cline-action-quota]').element.disabled).toBe(true)
    await w.get('[data-testid=cline-action-quota]').trigger('click')
    expect(w.find('[data-testid=cline-confirm-operation]').exists()).toBe(false); expect(calls.resetAccountQuota).not.toHaveBeenCalled()
  })
})
