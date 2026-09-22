<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-6">
      <div class="flex items-center justify-between gap-4">
        <h1 class="text-2xl font-bold">{{ t('vpn.title') }}</h1>
        <button class="btn btn-secondary" :disabled="busy || loading" @click="load(true)">{{ t('vpn.refresh') }}</button>
      </div>
      <div class="card space-y-2 p-5 text-sm text-gray-600 dark:text-gray-300">
        <p>{{ t('vpn.policy', { quota: data ? formatVpnBytes(data.default_quota_bytes) : '—' }) }}</p><p>{{ t('vpn.createPolicy') }}</p>
      </div>
      <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
      <p v-if="loading && !data" role="status">{{ t('vpn.loading') }}</p>
      <div v-else-if="data?.subscription" class="card p-6">
        <VpnSubscriptionDetails :subscription="data.subscription" />
      </div>
      <div v-else-if="data" class="card space-y-4 p-8 text-center">
        <h2 class="text-lg font-semibold">{{ t('vpn.noSubscription') }}</h2>
        <p>{{ t('vpn.quota') }}: {{ formatVpnBytes(data.default_quota_bytes) }}</p>
        <p v-if="!data.can_create" class="text-amber-600">{{ ineligibleReason }}</p>
        <button class="btn btn-primary" :disabled="!data.can_create || busy || loading" @click="create">{{ t('vpn.create') }}</button>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import VpnSubscriptionDetails from '@/components/vpn/VpnSubscriptionDetails.vue'
import { vpnAPI, type VpnSubscriptionResponse } from '@/api/vpn'
import { formatVpnBytes, vpnError, vpnIsPending } from '@/utils/vpn'
import { useAppStore } from '@/stores/app'

const { t, te } = useI18n()
const app = useAppStore()
const data = ref<VpnSubscriptionResponse | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const ineligibleReason = computed(() => {
  const reason = data.value?.ineligible_reason
  if (!reason) return t('vpn.unavailable')
  return te(`vpn.reasons.${reason}`) ? t(`vpn.reasons.${reason}`) : reason
})
let poll: ReturnType<typeof setTimeout> | undefined
let disposed = false

function schedule() {
  clearTimeout(poll)
  if (!disposed && data.value?.subscription && vpnIsPending(data.value.subscription)) {
    poll = setTimeout(() => { void load() }, 5000)
  }
}
async function load(refresh = false) {
  if (loading.value || busy.value) return
  loading.value = true
  error.value = ''
  try { data.value = await (refresh ? vpnAPI.refresh() : vpnAPI.get()) }
  catch (e) { error.value = vpnError(e, t('vpn.loadFailed')) }
  finally { loading.value = false; schedule() }
}
async function create() {
  if (busy.value || !data.value?.can_create) return
  busy.value = true
  error.value = ''
  try {
    const subscription = await vpnAPI.create()
    data.value = { ...data.value, subscription, can_create: false }
    app.showSuccess(t('vpn.accepted'))
  } catch (e) { error.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false; schedule() }
}
onMounted(() => { void load() })
onUnmounted(() => { disposed = true; clearTimeout(poll) })
</script>
