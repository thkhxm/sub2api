import { apiClient } from './client'

export interface VpnSubscription {
  id: number
  user_id: number
  user_email: string
  server_id: number
  server_name: string
  remote_username: string
  status: string
  apply_status: string
  access_state: string
  quota_bytes: number
  upload_bytes: number
  download_bytes: number
  used_bytes: number
  remaining_bytes: number
  period_start: string | null
  period_end: string | null
  next_reset_at: string | null
  sampled_at: string | null
  synced_at: string | null
  accounting_status: string
  last_error: string
  operation_status: string
  created_at: string
  subscription_urls: { clash: string; base64: string } | null
}

export interface VpnSubscriptionResponse {
  subscription: VpnSubscription | null
  can_create: boolean
  ineligible_reason: string
  default_quota_bytes: number
}

export interface VpnServerInput {
  name: string
  base_url: string
  admin_username: string
  admin_password: string
  ca_pem?: string
  enabled: boolean
}

export interface VpnServer {
  id: number
  name: string
  base_url: string
  admin_username: string
  enabled: boolean
  healthy: boolean
  health_error: string
  personal_user_count: number
  assigned_count: number
  pending_count: number
  last_checked_at: string | null
  created_at: string
}

export interface VpnSummary {
  total: number
  active: number
  disabled: number
  limited: number
  pending: number
  failed: number
  used_bytes: number
  quota_bytes: number
  healthy_servers: number
  total_servers: number
}

export const vpnAPI = {
  get: async () => (await apiClient.get<VpnSubscriptionResponse>('/vpn/subscription')).data,
  create: async () => (await apiClient.post<VpnSubscription>('/vpn/subscription')).data,
  refresh: async () => (await apiClient.post<VpnSubscriptionResponse>('/vpn/subscription/refresh')).data
}

export const adminVpnAPI = {
  servers: async () => (await apiClient.get<VpnServer[]>('/admin/vpn/servers')).data,
  saveServer: async (input: VpnServerInput, id?: number) => id
    ? (await apiClient.put<VpnServer>(`/admin/vpn/servers/${id}`, input)).data
    : (await apiClient.post<VpnServer>('/admin/vpn/servers', input)).data,
  probe: async (id: number) => (await apiClient.post<VpnServer>(`/admin/vpn/servers/${id}/probe`)).data,
  summary: async () => (await apiClient.get<VpnSummary>('/admin/vpn/summary')).data,
  subscriptions: async (params: { page: number; page_size: number; q?: string; server_id?: number; status?: string }) =>
    (await apiClient.get<{ items: VpnSubscription[]; total: number; page: number; page_size: number }>('/admin/vpn/subscriptions', { params })).data,
  create: async (userId: number) => (await apiClient.post<VpnSubscription>('/admin/vpn/subscriptions', { user_id: userId })).data,
  update: async (id: number, input: { quota_bytes?: number; enabled?: boolean }) =>
    (await apiClient.put<VpnSubscription>(`/admin/vpn/subscriptions/${id}`, input)).data,
  action: async (id: number, action: 'refresh' | 'retry' | 'revoke') =>
    (await apiClient.post<VpnSubscription>(`/admin/vpn/subscriptions/${id}/${action}`)).data
}
