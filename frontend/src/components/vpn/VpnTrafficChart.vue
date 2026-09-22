<template>
  <section class="card space-y-4 p-5" aria-labelledby="vpn-traffic-title">
    <h2 id="vpn-traffic-title" class="text-lg font-semibold">{{ t('vpn.dailyTraffic') }}</h2>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="load">
      <label class="text-sm">{{ t('vpn.dateRange') }}<select v-model.number="range" class="input mt-1"><option v-for="days in [7, 30, 90]" :key="days" :value="days">{{ t('vpn.lastDays', { days }) }}</option></select></label>
      <label class="text-sm">{{ t('vpn.server') }}<select v-model="serverId" class="input mt-1"><option value="">{{ t('vpn.allServers') }}</option><option v-for="server in servers" :key="server.id" :value="server.id">{{ server.name }}</option></select></label>
      <label class="text-sm">{{ t('vpn.trafficUserId') }}<input v-model="userId" class="input mt-1" type="number" min="1" step="1" :placeholder="t('vpn.allUsers')" /></label>
      <button class="btn btn-secondary" :disabled="loading">{{ t('vpn.filter') }}</button>
    </form>
    <p class="text-xs text-gray-500">{{ t('vpn.timezone') }} · {{ t('vpn.trafficHistoryHint') }}</p>
    <p v-if="loading" role="status">{{ t('vpn.loading') }}</p>
    <p v-else-if="error" role="alert" class="text-red-600">{{ error }}</p>
    <template v-else-if="result">
      <p v-if="result.unavailable_servers > 0" role="status" class="text-sm text-amber-600">{{ t('vpn.trafficPartial', { count: result.unavailable_servers }) }}</p>
      <p v-else-if="result.partial" role="status" class="text-sm text-amber-600">{{ t('vpn.trafficIncomplete') }}</p>
      <p v-if="hasGaps" class="text-sm text-amber-600">{{ t('vpn.trafficGaps') }}</p>
      <div class="flex flex-wrap gap-x-5 gap-y-2 text-sm text-gray-500">
        <span>{{ t('vpn.trafficTotal') }}: {{ formatVpnBytes(result.total_bytes) }}</span>
        <span>{{ t('vpn.availableFrom') }}: {{ time(result.available_from) }}</span>
        <span>{{ t('vpn.synced') }}: {{ time(result.synced_at) }}</span>
      </div>
      <div v-if="hasData" class="h-64"><Line :data="chartData" :options="options" :aria-label="t('vpn.dailyTraffic')" role="img" /></div>
      <p v-else class="py-8 text-center text-gray-500">{{ t('vpn.noTrafficData') }}</p>
      <details v-if="hasData" class="text-sm">
        <summary class="cursor-pointer">{{ t('vpn.trafficDetails') }}</summary>
        <div class="mt-2 max-h-64 overflow-auto"><table class="w-full text-left"><thead><tr><th>{{ t('vpn.date') }}</th><th>{{ t('vpn.used') }}</th></tr></thead><tbody><tr v-for="day in result.days" :key="day.date"><td>{{ day.date }}</td><td>{{ day.used_bytes === null ? t('vpn.noTrafficData') : formatVpnBytes(day.used_bytes) }}</td></tr></tbody></table></div>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Tooltip, type ChartOptions } from 'chart.js'
import { Line } from 'vue-chartjs'
import { adminVpnAPI, type VpnDailyTraffic, type VpnServer } from '@/api/vpn'
import { formatVpnBytes, formatVpnTime, vpnError } from '@/utils/vpn'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip)
defineProps<{ servers: VpnServer[] }>()
const { t, locale } = useI18n()
const range = ref(30)
const serverId = ref<number | ''>('')
const userId = ref('')
const loading = ref(false)
const error = ref('')
const result = ref<VpnDailyTraffic | null>(null)
let requestId = 0
const time = (value: string | null) => formatVpnTime(value, locale.value)
const hasData = computed(() => result.value?.days.some(day => day.used_bytes !== null))
const hasGaps = computed(() => result.value?.days.some(day => day.used_bytes === null))
const chartData = computed(() => ({
  labels: result.value?.days.map(day => day.date) || [],
  datasets: [{ label: t('vpn.used'), data: result.value?.days.map(day => day.used_bytes) || [], borderColor: '#3b82f6', backgroundColor: '#3b82f6', spanGaps: false, tension: 0, pointRadius: 3 }]
}))
const options = computed<ChartOptions<'line'>>(() => ({
  responsive: true, maintainAspectRatio: false,
  interaction: { intersect: false, mode: 'index' },
  plugins: { tooltip: { callbacks: { label: context => `${t('vpn.used')}: ${formatVpnBytes(Number(context.raw))}` } } },
  scales: { y: { beginAtZero: true, ticks: { callback: value => formatVpnBytes(Number(value)) } } }
}))
async function load() {
  const current = ++requestId
  const uid = Number(userId.value)
  if (userId.value !== '' && (!Number.isSafeInteger(uid) || uid <= 0)) {
    error.value = t('vpn.invalidUserId'); result.value = null; loading.value = false; return
  }
  loading.value = true
  error.value = ''
  result.value = null
  // 用上海的日历日期计算区间，避免浏览器本地时区改变日界。
  const end = new Date(Date.now() + 8 * 60 * 60 * 1000)
  const start = new Date(end)
  start.setUTCDate(start.getUTCDate() - range.value + 1)
  try {
    const data = await adminVpnAPI.dailyTraffic({ start_date: start.toISOString().slice(0, 10), end_date: end.toISOString().slice(0, 10), server_id: serverId.value || undefined, user_id: uid || undefined })
    if (current === requestId) result.value = data
  } catch (e) { if (current === requestId) error.value = vpnError(e, t('vpn.loadFailed')) }
  finally { if (current === requestId) loading.value = false }
}
watch([range, serverId], () => { void load() })
onMounted(() => { void load() })
onUnmounted(() => { requestId++ })
</script>
