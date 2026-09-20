import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import UserVpnView from '../user/VpnView.vue'
import AdminVpnView from '../admin/VpnView.vue'
import VpnSubscriptionDetails from '@/components/vpn/VpnSubscriptionDetails.vue'
import type { VpnSubscription } from '@/api/vpn'
import { GIB, gibToBytes, vpnIsAvailable } from '@/utils/vpn'

const mocks = vi.hoisted(() => ({
  get: vi.fn(), create: vi.fn(), refresh: vi.fn(), servers: vi.fn(), summary: vi.fn(), subscriptions: vi.fn(),
  saveServer: vi.fn(), probe: vi.fn(), adminCreate: vi.fn(), update: vi.fn(), action: vi.fn(),
  listUsers: vi.fn(), copy: vi.fn(), success: vi.fn()
}))
vi.mock('@/api/vpn', () => ({
  vpnAPI: { get: mocks.get, create: mocks.create, refresh: mocks.refresh },
  adminVpnAPI: { servers: mocks.servers, summary: mocks.summary, subscriptions: mocks.subscriptions, saveServer: mocks.saveServer, probe: mocks.probe, create: mocks.adminCreate, update: mocks.update, action: mocks.action }
}))
vi.mock('@/api/admin/users', () => ({ list: mocks.listUsers, default: { list: mocks.listUsers } }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.success }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: (key: string) => key.startsWith('vpn.states.') || key === 'vpn.reasons.balance_required', locale: ref('zh') })
}))

const subscription = (overrides: Partial<VpnSubscription> = {}): VpnSubscription => ({
  id: 8, user_id: 17, user_email: 'person@example.com', server_id: 3, server_name: 'Node', remote_username: 'pc_sample',
  status: 'active', apply_status: 'applied', access_state: 'allowed', quota_bytes: 30 * GIB, upload_bytes: GIB,
  download_bytes: 2 * GIB, used_bytes: 3 * GIB, remaining_bytes: 27 * GIB, period_start: '2026-08-21T16:00:00Z',
  period_end: '2026-09-21T16:00:00Z', next_reset_at: '2026-09-21T16:00:00Z', sampled_at: '2026-09-20T01:00:00Z',
  synced_at: '2026-09-20T01:00:01Z', accounting_status: 'ok', last_error: '', operation_status: 'succeeded',
  created_at: '2026-09-01T00:00:00Z', subscription_urls: { clash: 'https://vpn.example/sub/secret/clash-meta', base64: 'https://vpn.example/sub/secret/v2ray' }, ...overrides
})
const server = { id: 3, name: 'Node', base_url: 'https://vpn.example', admin_username: 'integration', enabled: true, healthy: true, health_error: '', personal_user_count: 2, assigned_count: 1, pending_count: 0, last_checked_at: null, created_at: '' }
const wrappers: VueWrapper[] = []
function view(component: typeof UserVpnView | typeof AdminVpnView) {
  const wrapper = mount(component, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" role="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>' }
  } } })
  wrappers.push(wrapper)
  return wrapper
}
function button(wrapper: VueWrapper, label: string) {
  const match = wrapper.findAll('button').find(item => item.text() === label)
  if (!match) throw new Error(`Missing button: ${label}`)
  return match
}
beforeEach(() => {
  vi.clearAllMocks()
  mocks.get.mockResolvedValue({ subscription: null, can_create: true, ineligible_reason: '', default_quota_bytes: 30 * GIB })
  mocks.servers.mockResolvedValue([server])
  mocks.summary.mockResolvedValue({ total: 1, active: 1, disabled: 0, limited: 0, pending: 0, failed: 0, used_bytes: 3 * GIB, quota_bytes: 30 * GIB, healthy_servers: 1, total_servers: 1 })
  mocks.subscriptions.mockResolvedValue({ items: [subscription()], total: 1, page: 1, page_size: 20 })
  mocks.listUsers.mockResolvedValue({ items: [{ id: 17, email: 'person@example.com' }] })
  mocks.update.mockResolvedValue(subscription())
  mocks.action.mockResolvedValue(subscription())
  mocks.adminCreate.mockResolvedValue(subscription())
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.useRealTimers() })

describe('VPN quota and availability', () => {
  it('converts GiB without allowing zero, negative, nonfinite or unsafe quotas', () => {
    expect(gibToBytes(30)).toBe(32212254720)
    expect(gibToBytes(1.5)).toBe(1610612736)
    for (const value of [0, -1, NaN, Infinity, Number.MAX_SAFE_INTEGER]) expect(gibToBytes(value)).toBeNull()
  })
  it('requires an applied, active and permitted subscription before declaring it usable', () => {
    expect(vpnIsAvailable(subscription())).toBe(true)
    expect(vpnIsAvailable(subscription({ apply_status: 'pending' }))).toBe(false)
    expect(vpnIsAvailable(subscription({ access_state: 'unknown' }))).toBe(false)
    expect(vpnIsAvailable(subscription({ status: 'limited' }))).toBe(false)
  })
  it('hides URLs during pending application and exposes real directional usage and sample warnings', () => {
    const wrapper = mount(VpnSubscriptionDetails, { props: { subscription: subscription({ apply_status: 'pending', accounting_status: 'gap_detected' }) } })
    wrappers.push(wrapper)
    expect(wrapper.findAll('input')).toHaveLength(0)
    expect(wrapper.text()).toContain('vpn.staleNotice')
    expect(wrapper.text()).toContain('1.00 GiB')
    expect(wrapper.text()).toContain('2.00 GiB')
    expect(wrapper.text()).not.toContain('vpn.ready')
  })
  it('copies the selected format without putting the credential into a navigation link', async () => {
    const s = subscription()
    const wrapper = mount(VpnSubscriptionDetails, { props: { subscription: s } })
    wrappers.push(wrapper)
    await wrapper.findAll('button')[1].trigger('click')
    expect(mocks.copy).toHaveBeenCalledWith(s.subscription_urls!.base64)
    expect(wrapper.findAll('a')).toHaveLength(0)
    expect(wrapper.find('input').attributes('type')).toBe('password')
  })
})

describe('VPN user flow', () => {
  it('translates the balance eligibility reason returned by the backend', async () => {
    mocks.get.mockResolvedValue({ subscription: null, can_create: false, ineligible_reason: 'balance_required', default_quota_bytes: 30 * GIB })
    const wrapper = view(UserVpnView)
    await flushPromises()
    expect(wrapper.text()).toContain('vpn.reasons.balance_required')
  })
  it('blocks creation when the server reports ineligibility', async () => {
    mocks.get.mockResolvedValue({ subscription: null, can_create: false, ineligible_reason: '余额不足', default_quota_bytes: 30 * GIB })
    const wrapper = view(UserVpnView)
    await flushPromises()
    expect(wrapper.text()).toContain('余额不足')
    expect(button(wrapper, 'vpn.create').attributes('disabled')).toBeDefined()
    expect(mocks.create).not.toHaveBeenCalled()
  })
  it('creates once and polls GET until applied without consuming manual refresh calls', async () => {
    vi.useFakeTimers()
    mocks.create.mockResolvedValue(subscription({ status: 'provisioning', apply_status: 'pending', operation_status: 'pending', access_state: 'unknown', subscription_urls: null }))
    const wrapper = view(UserVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.create').trigger('click')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('vpn.pendingNotice')
    expect(wrapper.findAll('button').some(item => item.text() === 'vpn.create')).toBe(false)
    mocks.get.mockResolvedValue({ subscription: subscription(), can_create: false, ineligible_reason: '', default_quota_bytes: 30 * GIB })
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.text()).toContain('vpn.ready')
    expect(mocks.refresh).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(10000)
    expect(mocks.get).toHaveBeenCalledTimes(2)
  })
  it('keeps the existing subscription visible when refresh fails', async () => {
    mocks.get.mockResolvedValue({ subscription: subscription(), can_create: false, ineligible_reason: '', default_quota_bytes: 30 * GIB })
    mocks.refresh.mockRejectedValue({ message: '稍后再试', status: 429 })
    const wrapper = view(UserVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.refresh').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('稍后再试')
    expect(wrapper.text()).toContain('30.00 GiB')
  })
})

describe('VPN administrator flow', () => {
  it('preserves credentials and locks the address for a bound node', async () => {
    const wrapper = view(AdminVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.editServer').trigger('click')
    const dialog = wrapper.find('[role="dialog"]')
    expect(dialog.find('input[type="url"]').attributes('disabled')).toBeDefined()
    expect((dialog.find('input[type="password"]').element as HTMLInputElement).value).toBe('')
    await dialog.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.saveServer).toHaveBeenCalledWith({ name: 'Node', base_url: 'https://vpn.example', admin_username: 'integration', admin_password: '', enabled: true }, 3)
  })
  it('rejects a non-HTTPS management URL without sending credentials', async () => {
    const wrapper = view(AdminVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.addServer').trigger('click')
    const dialog = wrapper.find('[role="dialog"]')
    await dialog.find('input[type="url"]').setValue('http://vpn.example')
    await dialog.find('form').trigger('submit')
    expect(mocks.saveServer).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('vpn.invalidHttps')
  })
  it('searches existing users and activates for the selected user', async () => {
    const wrapper = view(AdminVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.createForUser').trigger('click')
    await flushPromises()
    const dialog = wrapper.find('[role="dialog"]')
    await dialog.find('select').setValue(17)
    await dialog.findAll('button').find(item => item.text() === 'vpn.createForUser')!.trigger('click')
    await flushPromises()
    expect(mocks.adminCreate).toHaveBeenCalledWith(17)
    expect(mocks.listUsers).toHaveBeenCalledWith(1, 50, { search: '', status: 'active' })
  })
  it('updates quota in bytes and supports disabling the same subscription', async () => {
    const wrapper = view(AdminVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.details').trigger('click')
    await button(wrapper, 'vpn.edit').trigger('click')
    const dialog = wrapper.findAll('[role="dialog"]').at(-1)!
    await dialog.find('input[type="number"]').setValue(45.5)
    await dialog.find('input[type="checkbox"]').setValue(false)
    await dialog.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(8, { quota_bytes: 45.5 * GIB, enabled: false })
  })
  it('requires explicit confirmation to rotate credentials and preserves errors', async () => {
    mocks.action.mockRejectedValue({ status: 409, message: '操作处理中' })
    const wrapper = view(AdminVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.details').trigger('click')
    await button(wrapper, 'vpn.revoke').trigger('click')
    expect(mocks.action).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('vpn.revokeConfirm')
    await button(wrapper, 'vpn.confirm').trigger('click')
    await flushPromises()
    expect(mocks.action).toHaveBeenCalledWith(8, 'revoke')
    expect(wrapper.text()).toContain('操作处理中')
  })
  it('retries an existing failed operation while preventing creation of a second account', async () => {
    mocks.subscriptions.mockResolvedValue({ items: [subscription({ apply_status: 'failed', operation_status: 'failed', status: 'provisioning' })], total: 1 })
    const wrapper = view(AdminVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.details').trigger('click')
    await button(wrapper, 'vpn.retry').trigger('click')
    await flushPromises()
    expect(mocks.action).toHaveBeenCalledWith(8, 'retry')
    expect(mocks.adminCreate).not.toHaveBeenCalled()
  })
  it('disables quota editing and rotation while another operation is pending', async () => {
    mocks.subscriptions.mockResolvedValue({ items: [subscription({ apply_status: 'pending', operation_status: 'pending' })], total: 1 })
    const wrapper = view(AdminVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.details').trigger('click')
    expect(button(wrapper, 'vpn.edit').attributes('disabled')).toBeDefined()
    expect(button(wrapper, 'vpn.revoke').attributes('disabled')).toBeDefined()
  })
  it('continues refreshing open details after an applied subscription leaves the filtered list', async () => {
    vi.useFakeTimers()
    mocks.subscriptions.mockResolvedValue({ items: [subscription({ apply_status: 'pending', operation_status: 'running' })], total: 1 })
    const wrapper = view(AdminVpnView)
    await flushPromises()
    await button(wrapper, 'vpn.details').trigger('click')
    mocks.subscriptions.mockResolvedValueOnce({ items: [], total: 0 }).mockResolvedValueOnce({ items: [subscription()], total: 1 })
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.subscriptions).toHaveBeenLastCalledWith({ page: 1, page_size: 20, q: 'pc_sample' })
    expect(wrapper.find('[role="dialog"]').text()).toContain('vpn.ready')
  })
})
