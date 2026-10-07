import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ClineAccountUsageCell from '../ClineAccountUsageCell.vue'
import type { Account } from '@/types'
import type { ClineMetadata } from '@/api/admin/clineMetadata'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const NOW = Date.parse('2026-10-06T12:00:00Z')
function account(patch: Partial<ClineMetadata> = {}): Account {
  return { id: 1, platform: 'cline', type: 'apikey', credentials: { account_mode: 'pass' }, cline_usage: {
    mode: 'pass', auth_type: 'api_key', persisted: true, quota_status: 'ok', credential_status: 'valid', identity_verified: true,
    catalog_status: 'unknown', incremental_cost_usd: null, last_success_at: new Date(NOW).toISOString(),
    windows: [{ type: 'five_hour', percent_used: 0 }, { type: 'weekly', percent_used: 30 }, { type: 'monthly', percent_used: 100 }],
    ...patch
  } } as Account
}
describe('ClineAccountUsageCell saved projection', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(NOW); vi.stubGlobal('fetch', vi.fn()) })
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })
  it('distinguishes observed zero from missing and ages locally without upstream requests', async () => {
    const wrapper = mount(ClineAccountUsageCell, { props: { account: account() } })
    expect(wrapper.get('[data-testid="cline-usage-five_hour"]').text()).toContain('0.0%')
    expect(wrapper.get('[data-testid="cline-usage-weekly"]').text()).toContain('30.0%')
    expect(wrapper.get('[data-testid="cline-usage-monthly"]').text()).toContain('100.0%')
    await vi.advanceTimersByTimeAsync(300000)
    await nextTick()
    expect(wrapper.text()).not.toContain('%')
    expect(wrapper.text()).toContain('clineAccount.usageUnknown')
    expect(fetch).not.toHaveBeenCalled()
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
  it.each([
    { quota_status: 'unknown' }, { persisted: false }, { windows: [] },
    { last_success_at: new Date(NOW + 1000).toISOString() }, { last_success_at: new Date(NOW - 300000).toISOString() },
    { last_success_at: 'invalid' }
  ])('never manufactures a zero quota from unavailable observations %j', patch => {
    const wrapper = mount(ClineAccountUsageCell, { props: { account: account(patch) } })
    expect(wrapper.text()).not.toContain('%')
    expect(wrapper.text()).toContain('clineAccount.usageUnknown')
    wrapper.unmount()
  })
  it('keeps omitted partial windows unknown and retains pending recovery past reset', async () => {
    const wrapper = mount(ClineAccountUsageCell, { props: { account: account({ quota_status: 'partial',
      windows: [{ type: 'five_hour', percent_used: 100, resets_at: new Date(NOW + 30000).toISOString() }],
      cooldowns: [{ type: 'five_hour', source: 'metadata', status: 'cooling', reset_at: new Date(NOW + 30000).toISOString() }]
    }) } })
    expect(wrapper.get('[data-testid="cline-usage-weekly"]').text()).toContain('clineAccount.usageUnknown')
    await vi.advanceTimersByTimeAsync(60000)
    await nextTick()
    expect(wrapper.get('[data-testid="cline-usage-five_hour"]').text()).toContain('clineAccount.usageUnknown')
    expect(wrapper.find('[data-testid="cline-usage-cooldown"]').exists()).toBe(true)
    expect(fetch).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it.each([null, -1, 101, NaN, Infinity])('does not display an invalid percent %s', percent_used => {
    const wrapper = mount(ClineAccountUsageCell, { props: { account: account({ windows: [{ type: 'five_hour', percent_used }] }) } })
    expect(wrapper.text()).not.toContain('%')
    wrapper.unmount()
  })
  it('does not claim Pass quota for PAYG', () => {
    const a = account(); a.credentials = { account_mode: 'payg' }
    const wrapper = mount(ClineAccountUsageCell, { props: { account: a } })
    expect(wrapper.text()).toContain('clineMetadata.passOnly')
    expect(wrapper.find('[data-testid="cline-usage-five_hour"]').exists()).toBe(false)
    expect(fetch).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
