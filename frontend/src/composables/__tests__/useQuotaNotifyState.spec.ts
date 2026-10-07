import { describe, it, expect, vi } from 'vitest'
vi.mock('@/api/admin',()=>({adminAPI:{accounts:{getManagementCapabilities:vi.fn()}}}))
import { useQuotaNotifyState } from '../useQuotaNotifyState'

describe('native quota notification configuration', () => {
  it.each(['create','update'] as const)('preserves explicit false independently of global inheritance on %s', mode => {
    const state = useQuotaNotifyState(); state.loadFromExtra({quota_notify_daily_enabled:false,quota_notify_daily_threshold:3})
    const extra:Record<string,unknown>={}; state.writeToExtra(extra,mode)
    expect(extra).toMatchObject({quota_notify_daily_enabled:false,quota_notify_daily_threshold:3})
    expect(extra).not.toHaveProperty('quota_notify_weekly_enabled')
  })
  it('removes an inherited override without resetting usage counters', () => {
    const state = useQuotaNotifyState(); const extra={quota_notify_total_enabled:true,quota_used:19}
    state.writeToExtra(extra,'update'); expect(extra).toEqual({quota_used:19})
  })
})
