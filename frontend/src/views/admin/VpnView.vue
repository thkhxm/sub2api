<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h1 class="text-2xl font-bold">{{ t('vpn.adminTitle') }}</h1>
        <button class="btn btn-secondary" :disabled="loading || busy" @click="load">{{ t('vpn.refresh') }}</button>
      </div>
      <p v-if="error" role="alert" class="break-words text-red-600">{{ error }}</p>
      <div v-if="summary" class="grid grid-cols-2 gap-3 md:grid-cols-4">
        <div v-for="[label, value] in summaryFields" :key="label" class="card p-4">
          <p class="text-sm text-gray-500">{{ t(`vpn.${label}`) }}</p><p class="mt-1 text-xl font-semibold">{{ value }}</p>
        </div>
      </div>
      <section class="card space-y-4 p-5">
        <div class="flex items-center justify-between gap-3">
          <h2 class="text-lg font-semibold">{{ t('vpn.servers') }}</h2>
          <button class="btn btn-primary" :disabled="busy" @click="openServer()">{{ t('vpn.addServer') }}</button>
        </div>
        <p v-if="!servers.length && !loading" class="text-gray-500">{{ t('vpn.noServers') }}</p>
        <div class="grid gap-4 xl:grid-cols-2">
          <article v-for="server in servers" :key="server.id" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h3 class="font-semibold">{{ server.name }}</h3>
              <span :class="server.enabled && server.healthy ? 'text-emerald-600' : 'text-amber-600'">{{ t(server.enabled ? 'vpn.enabled' : 'vpn.disabled') }} · {{ t(server.healthy ? 'vpn.healthy' : 'vpn.unhealthy') }}</span>
            </div>
            <p class="break-all text-sm text-gray-500">{{ server.base_url }}</p>
            <dl class="flex flex-wrap gap-x-5 gap-y-2 text-sm">
              <div><dt class="text-gray-500">{{ t('vpn.personalCount') }}</dt><dd>{{ server.personal_user_count }}</dd></div>
              <div><dt class="text-gray-500">{{ t('vpn.assignedCount') }}</dt><dd>{{ server.assigned_count }}</dd></div>
              <div><dt class="text-gray-500">{{ t('vpn.pendingCount') }}</dt><dd>{{ server.pending_count }}</dd></div>
              <div><dt class="text-gray-500">{{ t('vpn.lastChecked') }}</dt><dd>{{ time(server.last_checked_at) }}</dd></div>
            </dl>
            <p v-if="server.health_error" class="break-words text-sm text-red-600">{{ server.health_error }}</p>
            <div class="flex flex-wrap gap-2">
              <button class="btn btn-secondary" :disabled="busy" @click="openServer(server)">{{ t('vpn.editServer') }}</button>
              <button class="btn btn-secondary" :disabled="busy" @click="probe(server.id)">{{ t('vpn.probe') }}</button>
            </div>
          </article>
        </div>
      </section>
      <section class="card space-y-4 p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-lg font-semibold">{{ t('vpn.subscriptions') }}</h2>
          <button class="btn btn-primary" :disabled="busy" @click="openCreate">{{ t('vpn.createForUser') }}</button>
        </div>
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="filter">
          <label class="min-w-48 flex-1 text-sm">{{ t('vpn.search') }}<input v-model="query" class="input mt-1" /></label>
          <label class="text-sm">{{ t('vpn.server') }}<select v-model="serverFilter" class="input mt-1"><option value="">{{ t('vpn.allServers') }}</option><option v-for="server in servers" :key="server.id" :value="server.id">{{ server.name }}</option></select></label>
          <label class="text-sm">{{ t('vpn.status') }}<select v-model="statusFilter" class="input mt-1"><option value="">{{ t('vpn.allStatuses') }}</option><option v-for="status in statuses" :key="status" :value="status">{{ state(status) }}</option></select></label>
          <button class="btn btn-secondary" :disabled="loading || busy">{{ t('vpn.filter') }}</button>
        </form>
        <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('vpn.loading') }}</p>
        <p v-if="!items.length && !loading" class="py-5 text-center text-gray-500">{{ t('vpn.noResults') }}</p>
        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead><tr class="border-b border-gray-200 text-gray-500 dark:border-dark-700"><th class="p-3">{{ t('vpn.selectUser') }}</th><th class="p-3">{{ t('vpn.server') }}</th><th class="p-3">{{ t('vpn.status') }}</th><th class="p-3">{{ t('vpn.used') }} / {{ t('vpn.quota') }}</th><th class="p-3">{{ t('vpn.sampled') }}</th><th class="p-3">{{ t('vpn.details') }}</th></tr></thead>
            <tbody>
              <tr v-for="item in items" :key="item.id" class="border-b border-gray-100 dark:border-dark-700">
                <td class="p-3"><div>{{ item.user_email || `#${item.user_id}` }}</div><div class="text-xs text-gray-500">#{{ item.user_id }}</div></td>
                <td class="p-3">{{ item.server_name }}</td>
                <td class="p-3"><div>{{ state(item.status) }} · {{ state(item.apply_status) }}</div><div class="text-xs text-gray-500">{{ state(item.accounting_status) }}</div><p v-if="item.last_error" class="max-w-xs break-words text-xs text-red-600">{{ item.last_error }}</p></td>
                <td class="whitespace-nowrap p-3">{{ formatVpnBytes(item.used_bytes) }} / {{ formatVpnBytes(item.quota_bytes) }}</td>
                <td class="p-3">{{ time(item.sampled_at) }}</td>
                <td class="p-3"><button class="btn btn-secondary whitespace-nowrap" @click="selected = item">{{ t('vpn.details') }}</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <p class="text-sm text-gray-500">{{ t('vpn.page', { page, total }) }}</p>
          <div class="flex gap-2"><button class="btn btn-secondary" :disabled="page <= 1 || loading || busy" @click="changePage(-1)">{{ t('vpn.previous') }}</button><button class="btn btn-secondary" :disabled="page * pageSize >= total || loading || busy" @click="changePage(1)">{{ t('vpn.next') }}</button></div>
        </div>
      </section>
    </div>

    <BaseDialog :show="showServer" :title="t(serverId ? 'vpn.editServer' : 'vpn.addServer')" :show-close-button="!busy" :close-on-escape="!busy" @close="closeServer">
      <form class="space-y-4" @submit.prevent="saveServer">
        <p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p>
        <label class="block text-sm">{{ t('vpn.name') }}<input v-model="serverForm.name" class="input mt-1" required maxlength="200" /></label>
        <label class="block text-sm">{{ t('vpn.baseUrl') }}<input v-model="serverForm.base_url" class="input mt-1" type="url" required :disabled="serverHasBindings" /></label>
        <p class="text-xs text-gray-500">{{ t('vpn.bindingHint') }}</p>
        <label class="block text-sm">{{ t('vpn.adminUsername') }}<input v-model="serverForm.admin_username" class="input mt-1" autocomplete="off" required /></label>
        <label class="block text-sm">{{ t('vpn.adminPassword') }}<input v-model="serverForm.admin_password" class="input mt-1" type="password" autocomplete="new-password" :required="!serverId" /></label>
        <p v-if="serverId" class="text-xs text-gray-500">{{ t('vpn.passwordHint') }}</p>
        <label class="block text-sm">{{ t('vpn.caPem') }}<textarea v-model="serverForm.ca_pem" class="input mt-1 font-mono" rows="4" /></label>
        <p class="text-xs text-gray-500">{{ t('vpn.caHint') }}</p>
        <label class="flex items-center gap-2 text-sm"><input v-model="serverForm.enabled" type="checkbox" />{{ t('vpn.enabled') }}</label>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="closeServer">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy">{{ t('vpn.save') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog :show="showCreate" :title="t('vpn.createForUser')" :show-close-button="!busy" :close-on-escape="!busy" @close="showCreate = false">
      <div class="space-y-4">
        <p class="text-sm text-gray-500">{{ t('vpn.createHint') }}</p>
        <p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p>
        <form class="flex items-end gap-2" @submit.prevent="searchUsers">
          <label class="min-w-0 flex-1 text-sm">{{ t('vpn.search') }}<input v-model="userQuery" class="input mt-1" /></label>
          <button class="btn btn-secondary" :disabled="searching || busy">{{ t('vpn.searchUsers') }}</button>
        </form>
        <p v-if="searching" role="status">{{ t('vpn.loading') }}</p>
        <label class="block text-sm">{{ t('vpn.selectUser') }}<select v-model="createUserId" class="input mt-1"><option :value="null" disabled>{{ t('vpn.selectUser') }}</option><option v-for="user in users" :key="user.id" :value="user.id">{{ user.email }} (#{{ user.id }})</option></select></label>
        <div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="showCreate = false">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !createUserId" @click="createSubscription">{{ t('vpn.createForUser') }}</button></div>
      </div>
    </BaseDialog>

    <BaseDialog :show="!!selected" :title="selected?.user_email || t('vpn.details')" width="wide" :show-close-button="!busy" :close-on-escape="!busy" @close="selected = null">
      <template v-if="selected">
        <p v-if="dialogError" role="alert" class="mb-4 text-sm text-red-600">{{ dialogError }}</p>
        <VpnSubscriptionDetails :subscription="selected" />
        <div class="mt-5 flex flex-wrap gap-2 border-t border-gray-200 pt-4 dark:border-dark-700">
          <button class="btn btn-secondary" :disabled="busy" @click="runAction('refresh')">{{ t('vpn.refresh') }}</button>
          <button class="btn btn-secondary" :disabled="busy || vpnIsPending(selected)" @click="openEdit">{{ t('vpn.edit') }}</button>
          <button class="btn btn-secondary" :disabled="busy || !canRetry" @click="runAction('retry')">{{ t('vpn.retry') }}</button>
          <button class="btn btn-danger" :disabled="busy || vpnIsPending(selected)" @click="showRevoke = true">{{ t('vpn.revoke') }}</button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog :show="showEdit" :title="t('vpn.edit')" :z-index="60" :show-close-button="!busy" :close-on-escape="!busy" @close="showEdit = false">
      <form class="space-y-4" @submit.prevent="saveSubscription">
        <p v-if="editError" role="alert" class="text-sm text-red-600">{{ editError }}</p>
        <label class="block text-sm">{{ t('vpn.quotaGiB') }}<input v-model.number="quotaGiB" class="input mt-1" type="number" step="any" min="0.000000001" required /></label>
        <label class="flex items-center gap-2 text-sm"><input v-model="subscriptionEnabled" type="checkbox" />{{ t('vpn.enabled') }}</label>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="showEdit = false">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy">{{ t('vpn.save') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog :show="showRevoke" :title="t('vpn.revoke')" :z-index="60" :show-close-button="!busy" :close-on-escape="!busy" @close="showRevoke = false">
      <p>{{ t('vpn.revokeConfirm') }}</p>
      <p v-if="revokeError" role="alert" class="mt-3 text-sm text-red-600">{{ revokeError }}</p>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="showRevoke = false">{{ t('vpn.cancel') }}</button><button class="btn btn-danger" :disabled="busy" @click="runAction('revoke')">{{ t('vpn.confirm') }}</button></div></template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import VpnSubscriptionDetails from '@/components/vpn/VpnSubscriptionDetails.vue'
import { adminVpnAPI, type VpnServer, type VpnServerInput, type VpnSubscription, type VpnSummary } from '@/api/vpn'
import { list as listUsers } from '@/api/admin/users'
import { formatVpnBytes, formatVpnTime, GIB, gibToBytes, vpnError, vpnIsPending } from '@/utils/vpn'
import { useAppStore } from '@/stores/app'

const { t, te, locale } = useI18n()
const app = useAppStore()
const servers = ref<VpnServer[]>([])
const summary = ref<VpnSummary | null>(null)
const items = ref<VpnSubscription[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const query = ref('')
const serverFilter = ref<number | ''>('')
const statusFilter = ref('')
const statuses = ['active', 'disabled', 'limited', 'expired', 'provisioning', 'failed']
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const dialogError = ref('')
const editError = ref('')
const revokeError = ref('')
const selected = ref<VpnSubscription | null>(null)
const showServer = ref(false)
const showCreate = ref(false)
const showEdit = ref(false)
const showRevoke = ref(false)
const serverId = ref<number>()
const serverHasBindings = ref(false)
const blankServer = (): VpnServerInput => ({ name: '', base_url: '', admin_username: '', admin_password: '', ca_pem: '', enabled: true })
const serverForm = reactive(blankServer())
const userQuery = ref('')
const users = ref<Array<{ id: number; email: string }>>([])
const createUserId = ref<number | null>(null)
const searching = ref(false)
const quotaGiB = ref(30)
const subscriptionEnabled = ref(true)
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
const state = (value: string) => te(`vpn.states.${value}`) ? t(`vpn.states.${value}`) : value || '—'
const time = (value: string | null) => formatVpnTime(value, locale.value)
const canRetry = computed(() => !!selected.value && (['failed', 'pending'].includes(selected.value.operation_status) || selected.value.apply_status === 'failed'))
const summaryFields = computed(() => {
  const s = summary.value
  return s ? [['total', s.total], ['states.active', s.active], ['states.disabled', s.disabled], ['states.limited', s.limited], ['states.pending', s.pending], ['states.failed', s.failed], ['used', formatVpnBytes(s.used_bytes)], ['quota', formatVpnBytes(s.quota_bytes)], ['healthyServers', `${s.healthy_servers} / ${s.total_servers}`]] : []
})

function schedule() {
  clearTimeout(timer)
  if (!disposed && (items.value.some(vpnIsPending) || (selected.value && vpnIsPending(selected.value)))) {
    timer = setTimeout(() => { if (!busy.value) void load(); else schedule() }, 5000)
  }
}
async function load() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const [nodes, totals, subscriptions] = await Promise.all([
      adminVpnAPI.servers(), adminVpnAPI.summary(),
      adminVpnAPI.subscriptions({ page: page.value, page_size: pageSize, q: query.value || undefined, server_id: serverFilter.value || undefined, status: statusFilter.value || undefined })
    ])
    servers.value = nodes
    summary.value = totals
    items.value = subscriptions.items
    total.value = subscriptions.total
    if (selected.value) {
      const id = selected.value.id
      const visible = items.value.find(item => item.id === id)
      if (visible) selected.value = visible
      else {
        // 生效后的状态可能不再匹配筛选；详情仍按固定远端用户名跟进原订阅。
        const detail = await adminVpnAPI.subscriptions({ page: 1, page_size: 20, q: selected.value.remote_username })
        if (selected.value?.id === id) selected.value = detail.items.find(item => item.id === id) || selected.value
      }
    }
  } catch (e) { error.value = vpnError(e, t('vpn.loadFailed')) }
  finally { loading.value = false; schedule() }
}
function filter() { page.value = 1; void load() }
function changePage(delta: number) { page.value += delta; void load() }
function openServer(server?: VpnServer) {
  serverId.value = server?.id
  serverHasBindings.value = !!server && (server.assigned_count > 0 || server.pending_count > 0)
  Object.assign(serverForm, blankServer(), server ? { name: server.name, base_url: server.base_url, admin_username: server.admin_username, enabled: server.enabled } : {})
  dialogError.value = ''
  showServer.value = true
}
function closeServer() { if (!busy.value) { showServer.value = false; Object.assign(serverForm, blankServer()) } }
async function saveServer() {
  if (busy.value) return
  try {
    const url = new URL(serverForm.base_url)
    if (url.protocol !== 'https:' || url.username || url.password) throw new Error()
  } catch { dialogError.value = t('vpn.invalidHttps'); return }
  busy.value = true
  dialogError.value = ''
  try {
    const input = { ...serverForm }
    if (!input.ca_pem?.trim()) delete input.ca_pem
    await adminVpnAPI.saveServer(input, serverId.value)
    showServer.value = false
    Object.assign(serverForm, blankServer())
    await load()
  } catch (e) { dialogError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
async function probe(id: number) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try { await adminVpnAPI.probe(id); await load() }
  catch (e) { error.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
function openCreate() {
  dialogError.value = ''
  createUserId.value = null
  userQuery.value = ''
  users.value = []
  showCreate.value = true
  void searchUsers()
}
async function searchUsers() {
  if (searching.value) return
  searching.value = true
  dialogError.value = ''
  try {
    const result = await listUsers(1, 50, { search: userQuery.value, status: 'active' })
    users.value = result.items
    createUserId.value = null
  } catch (e) { dialogError.value = vpnError(e, t('vpn.loadFailed')) }
  finally { searching.value = false }
}
function updateSelected(subscription: VpnSubscription) {
  selected.value = subscription
  items.value = items.value.map(item => item.id === subscription.id ? subscription : item)
  schedule()
}
async function createSubscription() {
  if (busy.value || !createUserId.value) return
  busy.value = true
  dialogError.value = ''
  try {
    const subscription = await adminVpnAPI.create(createUserId.value)
    showCreate.value = false
    page.value = 1
    query.value = ''
    serverFilter.value = ''
    statusFilter.value = ''
    updateSelected(subscription)
    app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) { dialogError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
function openEdit() {
  if (!selected.value) return
  quotaGiB.value = selected.value.quota_bytes / GIB
  subscriptionEnabled.value = selected.value.status !== 'disabled'
  editError.value = ''
  showEdit.value = true
}
async function saveSubscription() {
  if (busy.value || !selected.value) return
  const bytes = gibToBytes(quotaGiB.value)
  if (bytes === null) { editError.value = t('vpn.invalidQuota'); return }
  busy.value = true
  editError.value = ''
  try {
    updateSelected(await adminVpnAPI.update(selected.value.id, { quota_bytes: bytes, enabled: subscriptionEnabled.value }))
    showEdit.value = false
    app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) { editError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
async function runAction(action: 'refresh' | 'retry' | 'revoke') {
  if (busy.value || !selected.value) return
  busy.value = true
  dialogError.value = ''
  revokeError.value = ''
  try {
    updateSelected(await adminVpnAPI.action(selected.value.id, action))
    showRevoke.value = false
    if (action !== 'refresh') app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) {
    const message = vpnError(e, t('vpn.actionFailed'))
    if (action === 'revoke') revokeError.value = message
    else dialogError.value = message
  } finally { busy.value = false }
}
watch(() => selected.value?.id, () => { dialogError.value = '' })
watch(showRevoke, () => { revokeError.value = '' })
onMounted(() => { void load() })
onUnmounted(() => { disposed = true; clearTimeout(timer) })
</script>
