import { apiClient } from './client'

export interface VpnSubscription {
  id: number
  user_id: number
  user_email: string
  server_id: number
  server_name: string
  group_id?: number
  group_name?: string
  group_quota_bytes?: number
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
  delete_requested_at?: string | null
  deleted_at?: string | null
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
  traffic_quota_bytes?: number
  traffic_used_offset_bytes?: number
}

export interface VpnEgress {
  username: string
  status: string
  apply_status: string
  access_state: string
  unlimited: boolean
  subscription_urls: { clash: string; base64: string; singbox: string } | null
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
  traffic_quota_bytes?: number
  traffic_used_bytes?: number | null
  traffic_remaining_bytes?: number | null
  traffic_period_start?: string | null
  traffic_period_end?: string | null
  traffic_sampled_at?: string | null
  traffic_available_from?: string | null
  traffic_accounting_status?: string
  traffic_used_offset_bytes?: number
  traffic_offset_period_start?: string | null
  allocation_quota_bytes?: number | null
  allocation_available_bytes?: number | null
  allocation_ratio?: number | null
}

export interface VpnDailyTraffic {
  timezone: string
  start_date: string
  end_date: string
  days: Array<{ date: string; used_bytes: number | null }>
  total_bytes: number
  available_from: string | null
  synced_at: string | null
  partial: boolean
  unavailable_servers: number
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

export interface VpnGroup {
  id: number
  name: string
  quota_bytes: number
  is_default: boolean
  member_count: number
  subscription_count: number
  pending_count: number
  failed_count: number
}

export interface VpnUserGroup {
  user_id: number
  group_id: number
  group_name: string
  quota_bytes: number
}

export const vpnAPI = {
  get: async () => (await apiClient.get<VpnSubscriptionResponse>('/vpn/subscription')).data,
  create: async () => (await apiClient.post<VpnSubscription>('/vpn/subscription')).data,
  refresh: async () => (await apiClient.post<VpnSubscriptionResponse>('/vpn/subscription/refresh')).data
}

export const adminVpnAPI = {
  groups: async () => (await apiClient.get<VpnGroup[]>('/admin/vpn/groups')).data,
  createGroup: async (input: { name: string; quota_bytes: number }) =>
    (await apiClient.post<VpnGroup>('/admin/vpn/groups', input)).data,
  updateGroup: async (id: number, input: { name?: string; quota_bytes?: number }) =>
    (await apiClient.put<VpnGroup>(`/admin/vpn/groups/${id}`, input)).data,
  setUserGroup: async (userId: number, groupId: number) =>
    (await apiClient.put<VpnUserGroup>(`/admin/vpn/users/${userId}/group`, { group_id: groupId })).data,
  delete: async (id: number) => (await apiClient.delete<VpnSubscription>(`/admin/vpn/subscriptions/${id}`)).data,
  dailyTraffic: async (params: { start_date: string; end_date: string; server_id?: number; user_id?: number }) =>
    (await apiClient.get<VpnDailyTraffic>('/admin/vpn/traffic/daily', { params })).data,
  servers: async () => (await apiClient.get<VpnServer[]>('/admin/vpn/servers')).data,
  saveServer: async (input: VpnServerInput, id?: number) => id
    ? (await apiClient.put<VpnServer>(`/admin/vpn/servers/${id}`, input)).data
    : (await apiClient.post<VpnServer>('/admin/vpn/servers', input)).data,
  probe: async (id: number) => (await apiClient.post<VpnServer>(`/admin/vpn/servers/${id}/probe`)).data,
  egress: async (id: number) => (await apiClient.get<VpnEgress>(`/admin/vpn/servers/${id}/egress`)).data,
  ensureEgress: async (id: number) => (await apiClient.post<VpnEgress>(`/admin/vpn/servers/${id}/egress`)).data,
  summary: async () => (await apiClient.get<VpnSummary>('/admin/vpn/summary')).data,
  subscriptions: async (params: { page: number; page_size: number; q?: string; server_id?: number; status?: string; sort_by?: 'created_at' | 'used_bytes'; sort_order?: 'asc' | 'desc' }) =>
    (await apiClient.get<{ items: VpnSubscription[]; total: number; page: number; page_size: number }>('/admin/vpn/subscriptions', { params })).data,
  create: async (userId: number) => (await apiClient.post<VpnSubscription>('/admin/vpn/subscriptions', { user_id: userId })).data,
  update: async (id: number, input: { quota_bytes?: number; enabled?: boolean }) =>
    (await apiClient.put<VpnSubscription>(`/admin/vpn/subscriptions/${id}`, input)).data,
  rotate: async (id: number, targetServerId: number) =>
    (await apiClient.post<VpnSubscription>(`/admin/vpn/subscriptions/${id}/revoke`, { target_server_id: targetServerId })).data,
  action: async (id: number, action: 'refresh' | 'retry') =>
    (await apiClient.post<VpnSubscription>(`/admin/vpn/subscriptions/${id}/${action}`)).data
}
