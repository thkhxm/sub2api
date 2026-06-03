/**
 * 账号自助重授权 - 公开端点 API
 *
 * 这些端点**不需要登录**，仅靠 URL 中的一次性签名 token 鉴权。
 * 因此这里使用裸 axios 实例（不挂 Authorization 头、不走 token 刷新逻辑），
 * 并自行解析统一信封 { code, message, data }。
 *
 * 后端契约：
 * - GET  /api/v1/account-reauth/info?token=<签名token>
 * - POST /api/v1/account-reauth/generate-url   body: { token }
 * - POST /api/v1/account-reauth/exchange-code  body: { token, session, code, state }
 *
 * 错误码（reason）：
 *   REAUTH_INVALID_TOKEN / REAUTH_INVALID_SIGNATURE / REAUTH_INVALID_PAYLOAD
 *   REAUTH_TOKEN_EXPIRED / REAUTH_TOKEN_CONSUMED / REAUTH_PLATFORM_UNSUPPORTED
 */

import axios, { AxiosError } from 'axios'

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1'

/** 公开端点专用裸 axios 实例：不带凭据头，不走拦截器 */
const publicClient = axios.create({
  baseURL: API_BASE_URL,
  // 公开端点无需携带 cookie，避免误带登录态
  withCredentials: false,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json'
  }
})

/** 统一信封 */
interface ApiEnvelope<T> {
  code: number
  message?: string
  data?: T
  reason?: string
  metadata?: Record<string, unknown>
}

/** 解析后的结构化错误，供页面按 reason 做文案映射 */
export interface ReauthApiError {
  status: number
  code?: number | string
  reason?: string
  message: string
  metadata?: Record<string, unknown>
}

/**
 * 解析统一信封：成功返回 data，失败抛出结构化 ReauthApiError。
 * 同时兜底处理后端直接返回 HTTP 错误（非 200）的情况。
 */
function unwrap<T>(payload: unknown, httpStatus: number): T {
  const envelope = (payload && typeof payload === 'object' ? payload : {}) as ApiEnvelope<T>
  if ('code' in envelope) {
    if (envelope.code === 0) {
      return envelope.data as T
    }
    throw {
      status: httpStatus,
      code: envelope.code,
      reason: envelope.reason,
      message: envelope.message || 'Unknown error',
      metadata: envelope.metadata
    } as ReauthApiError
  }
  // 非标准信封，原样返回
  return payload as T
}

/** 把 axios 错误转换为结构化 ReauthApiError */
function toReauthError(err: unknown): ReauthApiError {
  const axiosErr = err as AxiosError<ApiEnvelope<unknown>>
  if (axiosErr && axiosErr.isAxiosError) {
    const resp = axiosErr.response
    if (resp) {
      const data = (resp.data && typeof resp.data === 'object' ? resp.data : {}) as ApiEnvelope<unknown>
      return {
        status: resp.status,
        code: data.code,
        reason: data.reason,
        message: data.message || axiosErr.message || 'Request failed',
        metadata: data.metadata
      }
    }
    return {
      status: 0,
      message: axiosErr.message || 'Network error'
    }
  }
  // 已经是 ReauthApiError（来自 unwrap 抛出的对象）
  if (err && typeof err === 'object' && 'message' in (err as Record<string, unknown>)) {
    return err as ReauthApiError
  }
  return { status: 0, message: 'Unknown error' }
}

export interface ReauthAccountInfo {
  account_id: number
  account_name: string
  platform: string
  owner_name: string
  /** 脱敏后的所有者邮箱 */
  owner_email: string
  /** 链接过期时间（后端透传，通常为 ISO 字符串或时间戳） */
  expires_at: string | number
}

export interface ReauthGenerateUrlResult {
  auth_url: string
  session_id: string
}

export interface ReauthExchangeResult {
  account_id: number
  account_name: string
  platform: string
}

/**
 * 获取重授权账号信息（脱敏）
 */
export async function getReauthInfo(token: string): Promise<ReauthAccountInfo> {
  try {
    const resp = await publicClient.get('/account-reauth/info', {
      params: { token }
    })
    return unwrap<ReauthAccountInfo>(resp.data, resp.status)
  } catch (err) {
    throw toReauthError(err)
  }
}

/**
 * 生成 OAuth 授权 URL
 */
export async function generateReauthUrl(token: string): Promise<ReauthGenerateUrlResult> {
  try {
    const resp = await publicClient.post('/account-reauth/generate-url', { token })
    return unwrap<ReauthGenerateUrlResult>(resp.data, resp.status)
  } catch (err) {
    throw toReauthError(err)
  }
}

/**
 * 用授权码兑换，完成重授权
 */
export async function exchangeReauthCode(payload: {
  token: string
  session: string
  code: string
  state: string
}): Promise<ReauthExchangeResult> {
  try {
    const resp = await publicClient.post('/account-reauth/exchange-code', {
      token: payload.token,
      session: payload.session,
      code: payload.code,
      state: payload.state
    })
    return unwrap<ReauthExchangeResult>(resp.data, resp.status)
  } catch (err) {
    throw toReauthError(err)
  }
}

export const accountReauthAPI = {
  getInfo: getReauthInfo,
  generateUrl: generateReauthUrl,
  exchangeCode: exchangeReauthCode
}

export default accountReauthAPI
