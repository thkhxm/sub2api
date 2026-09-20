import type { VpnSubscription } from '@/api/vpn'

export const GIB = 1024 ** 3

export function gibToBytes(value: number): number | null {
  const bytes = Math.round(value * GIB)
  return Number.isFinite(value) && value > 0 && Number.isSafeInteger(bytes) && bytes > 0 ? bytes : null
}

export function formatVpnBytes(bytes: number): string {
  if (!Number.isFinite(bytes)) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(2)} KiB`
  if (bytes < GIB) return `${(bytes / 1024 ** 2).toFixed(2)} MiB`
  return `${(bytes / GIB).toFixed(2)} GiB`
}

export function vpnIsPending(subscription: VpnSubscription): boolean {
  return ['pending', 'running'].includes(subscription.operation_status) || subscription.apply_status === 'pending'
}

export function vpnIsAvailable(subscription: VpnSubscription): boolean {
  return subscription.status === 'active' && subscription.apply_status === 'applied' && subscription.access_state === 'allowed'
}

export function formatVpnTime(value: string | null, locale: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '—'
  return date.toLocaleString(locale === 'zh' ? 'zh-CN' : locale, { timeZone: 'Asia/Shanghai', hour12: false })
}

export function vpnError(error: unknown, fallback: string): string {
  return error && typeof error === 'object' && 'message' in error && typeof error.message === 'string'
    ? error.message : fallback
}
