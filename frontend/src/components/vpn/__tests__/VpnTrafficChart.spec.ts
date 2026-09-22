import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import VpnTrafficChart from '../VpnTrafficChart.vue'
import type { VpnDailyTraffic, VpnServer } from '@/api/vpn'

const { dailyTraffic } = vi.hoisted(() => ({ dailyTraffic: vi.fn() }))
vi.mock('@/api/vpn', () => ({ adminVpnAPI: { dailyTraffic } }))
vi.mock('vue-chartjs', () => ({ Line: { props: ['data', 'options'], template: '<div data-chart />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: ref('zh') }) }))
const response = (overrides: Partial<VpnDailyTraffic> = {}): VpnDailyTraffic => ({
  timezone: 'Asia/Shanghai', start_date: '2026-08-24', end_date: '2026-09-22',
  days: [{ date: '2026-09-20', used_bytes: null }, { date: '2026-09-21', used_bytes: 0 }, { date: '2026-09-22', used_bytes: 2048 }],
  total_bytes: 2048, available_from: '2026-09-20T16:00:00Z', synced_at: '2026-09-21T16:30:00Z', partial: false, unavailable_servers: 0, ...overrides
})
let wrapper: VueWrapper
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-09-21T16:30:00Z'))
  dailyTraffic.mockReset().mockResolvedValue(response())
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })
const render = () => { wrapper = mount(VpnTrafficChart, { props: { servers: [{ id: 3, name: 'Node' } as VpnServer] } }); return wrapper }

describe('VPN daily traffic chart', () => {
  it('defaults to thirty Shanghai calendar days and keeps missing values distinct from zero', async () => {
    render()
    await flushPromises()
    expect(dailyTraffic).toHaveBeenCalledWith({ start_date: '2026-08-24', end_date: '2026-09-22', server_id: undefined, user_id: undefined })
    const chart = wrapper.findComponent({ name: 'Line' })
    const actual = chart.exists() ? chart : wrapper.findComponent('[data-chart]')
    expect(actual.props('data').datasets[0].data).toEqual([null, 0, 2048])
    expect(actual.props('data').datasets[0].spanGaps).toBe(false)
    expect(wrapper.text()).toContain('vpn.trafficGaps')
    expect(wrapper.text()).toContain('0 B')
    expect(wrapper.text()).toContain('2.00 KiB')
  })
  it('requests seven and ninety day ranges, a node and a specific platform user', async () => {
    render()
    await flushPromises()
    await wrapper.findAll('select')[0].setValue(7)
    await flushPromises()
    expect(dailyTraffic).toHaveBeenLastCalledWith(expect.objectContaining({ start_date: '2026-09-16' }))
    await wrapper.findAll('select')[0].setValue(90)
    await wrapper.findAll('select')[1].setValue(3)
    await wrapper.find('input').setValue(17)
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(dailyTraffic).toHaveBeenLastCalledWith({ start_date: '2026-06-25', end_date: '2026-09-22', server_id: 3, user_id: 17 })
  })
  it('shows partial history, empty coverage and fetch failures without a misleading old chart', async () => {
    let finish!: (value: VpnDailyTraffic) => void
    dailyTraffic.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    render()
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('vpn.loading')
    finish(response({ partial: true, unavailable_servers: 1, days: [{ date: '2026-09-22', used_bytes: null }] }))
    await flushPromises()
    expect(wrapper.text()).toContain('vpn.trafficPartial')
    expect(wrapper.text()).toContain('vpn.noTrafficData')
    expect(wrapper.find('[data-chart]').exists()).toBe(false)
    dailyTraffic.mockRejectedValueOnce({ message: '所有节点不可用' })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toBe('所有节点不可用')
    expect(wrapper.find('[data-chart]').exists()).toBe(false)
  })
  it('rejects fractional and unsafe platform user ids', async () => {
    render()
    await flushPromises()
    for (const value of [1.5, Number.MAX_SAFE_INTEGER + 1]) {
      await wrapper.find('input').setValue(value)
      await wrapper.find('form').trigger('submit')
      await flushPromises()
      expect(wrapper.text()).toContain('vpn.invalidUserId')
    }
    expect(dailyTraffic).toHaveBeenCalledOnce()
  })
  it('describes incomplete history without claiming that zero servers are unavailable', async () => {
    dailyTraffic.mockResolvedValueOnce(response({ partial: true, unavailable_servers: 0, days: [{ date: '2026-09-22', used_bytes: 2048 }] }))
    render()
    await flushPromises()
    expect(wrapper.text()).toContain('vpn.trafficIncomplete')
    expect(wrapper.text()).not.toContain('vpn.trafficPartial')
    expect(wrapper.find('[data-chart]').exists()).toBe(true)
  })
  it('does not overwrite a newer selection with a slower previous request', async () => {
    let resolveOld!: (value: VpnDailyTraffic) => void
    dailyTraffic.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    render()
    await wrapper.findAll('select')[0].setValue(7)
    await flushPromises()
    resolveOld(response({ days: [], total_bytes: 0 }))
    await flushPromises()
    expect(wrapper.find('[data-chart]').exists()).toBe(true)
    expect(wrapper.text()).toContain('2.00 KiB')
  })
})
