<template>
  <div class="space-y-5">
    <p role="status" :class="available ? 'text-emerald-600' : 'text-amber-600'">{{ t(available ? 'vpn.ready' : 'vpn.notAvailable') }}</p>
    <p v-if="vpnIsPending(subscription)" class="text-sm text-amber-600">{{ t('vpn.pendingNotice') }}</p>
    <dl class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <div v-for="[label, value] in fields" :key="label" class="min-w-0">
        <dt class="text-sm text-gray-500 dark:text-gray-400">{{ t(`vpn.${label}`) }}</dt>
        <dd class="mt-1 break-words font-medium">{{ value }}</dd>
      </div>
    </dl>
    <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700" role="progressbar" :aria-label="t('vpn.used')" :aria-valuenow="usagePercent" :aria-valuemin="0" :aria-valuemax="100">
      <div class="h-full bg-primary-500" :style="{ width: `${usagePercent}%` }" />
    </div>
    <p class="text-xs text-gray-500">{{ t('vpn.timezone') }}</p>
    <p v-if="subscription.accounting_status !== 'ok'" role="alert" class="text-sm text-amber-600">{{ t('vpn.staleNotice') }}</p>
    <p v-if="subscription.last_error" role="alert" class="break-words text-sm text-red-600">{{ t('vpn.lastError') }}: {{ subscription.last_error }}</p>
    <section v-if="subscription.apply_status === 'applied' && subscription.subscription_urls" class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
      <h3 class="font-semibold">{{ t('vpn.links') }}</h3>
      <p class="text-sm text-gray-500">{{ t('vpn.linksSecret') }}</p>
      <div v-for="kind in (['clash', 'base64'] as const)" :key="kind" class="space-y-1">
        <label :for="`vpn-url-${subscription.id}-${kind}`" class="text-sm">{{ t(`vpn.${kind}`) }}</label>
        <div class="flex gap-2">
          <input :id="`vpn-url-${subscription.id}-${kind}`" class="input min-w-0 flex-1" type="password" readonly :value="subscription.subscription_urls[kind]" @focus="($event.target as HTMLInputElement).select()" />
          <button class="btn btn-secondary shrink-0" type="button" @click="copyToClipboard(subscription.subscription_urls[kind])">{{ t('vpn.copy') }}</button>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { VpnSubscription } from '@/api/vpn'
import { useClipboard } from '@/composables/useClipboard'
import { formatVpnBytes, formatVpnTime, vpnIsAvailable, vpnIsPending } from '@/utils/vpn'

const props = defineProps<{ subscription: VpnSubscription }>()
const { t, te, locale } = useI18n()
const { copyToClipboard } = useClipboard()
const available = computed(() => vpnIsAvailable(props.subscription))
const state = (value: string) => te(`vpn.states.${value}`) ? t(`vpn.states.${value}`) : value || '—'
const usagePercent = computed(() => Math.min(100, Math.max(0, props.subscription.quota_bytes ? props.subscription.used_bytes / props.subscription.quota_bytes * 100 : 0)))
const fields = computed(() => {
  const s = props.subscription
  const time = (value: string | null) => formatVpnTime(value, locale.value)
  return [
    ['server', s.server_name], ['status', state(s.status)], ['applyStatus', state(s.apply_status)],
    ['accessState', state(s.access_state)], ['operationStatus', state(s.operation_status)], ['accounting', state(s.accounting_status)],
    ['quota', formatVpnBytes(s.quota_bytes)], ['used', formatVpnBytes(s.used_bytes)], ['remaining', formatVpnBytes(s.remaining_bytes)],
    ['upload', formatVpnBytes(s.upload_bytes)], ['download', formatVpnBytes(s.download_bytes)],
    ['period', `${time(s.period_start)} – ${time(s.period_end)}`], ['reset', time(s.next_reset_at)],
    ['sampled', time(s.sampled_at)], ['synced', time(s.synced_at)]
  ]
})
</script>
