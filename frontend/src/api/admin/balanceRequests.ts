/**
 * Admin Balance Request API endpoints
 *
 * PunkcodeAI 桌面端用户在客户端发起余额申请后，管理员通过此接口审批。
 * 一键同意（可改金额）/ 拒绝（必填原因）。
 */

import { apiClient } from '../client'
import type { BasePaginationResponse } from '@/types'

/** balance_requests 表的领域模型 */
export interface BalanceRequest {
  id: number
  user_id: number
  amount_usd: number
  approved_amount_usd?: number | null
  note: string
  status: 'pending' | 'approved' | 'rejected'
  reviewer_id?: number | null
  reviewed_at?: string | null
  reject_reason: string
  created_at: string
  updated_at: string
}

export interface ListBalanceRequestsParams {
  page?: number
  page_size?: number
  status?: '' | 'pending' | 'approved' | 'rejected'
}

export interface ApproveBalanceRequestRequest {
  /** 不传则按原申请金额；传则覆盖 */
  approved_amount_usd?: number
}

export interface RejectBalanceRequestRequest {
  reason: string
}

/** GET /api/v1/admin/balance-requests */
export async function list(
  params?: ListBalanceRequestsParams,
  options?: { signal?: AbortSignal }
): Promise<BasePaginationResponse<BalanceRequest>> {
  const { data } = await apiClient.get<BasePaginationResponse<BalanceRequest>>(
    '/admin/balance-requests',
    {
      params: {
        page: params?.page ?? 1,
        page_size: params?.page_size ?? 20,
        ...(params?.status ? { status: params.status } : {})
      },
      signal: options?.signal
    }
  )
  return data
}

/** POST /api/v1/admin/balance-requests/:id/approve */
export async function approve(
  id: number,
  request: ApproveBalanceRequestRequest = {}
): Promise<BalanceRequest> {
  const { data } = await apiClient.post<BalanceRequest>(
    `/admin/balance-requests/${id}/approve`,
    request
  )
  return data
}

/** POST /api/v1/admin/balance-requests/:id/reject */
export async function reject(
  id: number,
  request: RejectBalanceRequestRequest
): Promise<BalanceRequest> {
  const { data } = await apiClient.post<BalanceRequest>(
    `/admin/balance-requests/${id}/reject`,
    request
  )
  return data
}

export default {
  list,
  approve,
  reject
}
