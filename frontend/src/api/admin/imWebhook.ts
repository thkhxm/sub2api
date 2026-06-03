/**
 * Admin IM Webhook 通知配置 API
 *
 * 用于配置即时通讯（飞书 / 企业微信 / Telegram）的 webhook 通知渠道。
 * 后端契约（统一信封 {code,message,data}）：
 * - GET  /api/v1/admin/im-webhook/config
 * - PUT  /api/v1/admin/im-webhook/config   body 同 config
 * - POST /api/v1/admin/im-webhook/test     body: { title?, text? }
 */

import { apiClient } from '../client'

/** 渠道类型 */
export type ImWebhookChannelType = 'feishu' | 'wecom' | 'telegram'

/** 单条 webhook 渠道配置 */
export interface ImWebhookChannel {
  type: ImWebhookChannelType
  /** webhook 地址（飞书/企业微信机器人地址；Telegram 可留空，走 bot token） */
  url: string
  /** 飞书签名校验密钥（可选） */
  secret?: string
  /** Telegram chat id（type=telegram 时使用） */
  telegram_chat_id?: string
  /** Telegram bot token（type=telegram 时使用） */
  telegram_bot_token?: string
  /** 单条渠道是否启用 */
  enabled: boolean
}

/** IM webhook 总配置 */
export interface ImWebhookConfig {
  /** 总开关 */
  enabled: boolean
  channels: ImWebhookChannel[]
}

/** 发送测试请求体 */
export interface ImWebhookTestRequest {
  title?: string
  text?: string
}

/** 发送测试返回（后端可能返回每条渠道的结果，宽松解析） */
export interface ImWebhookTestResult {
  success?: boolean
  message?: string
  results?: Array<{
    type: ImWebhookChannelType
    success: boolean
    error?: string
  }>
}

/** GET /api/v1/admin/im-webhook/config */
export async function getConfig(): Promise<ImWebhookConfig> {
  const { data } = await apiClient.get<ImWebhookConfig>('/admin/im-webhook/config')
  return data
}

/** PUT /api/v1/admin/im-webhook/config */
export async function updateConfig(config: ImWebhookConfig): Promise<ImWebhookConfig> {
  const { data } = await apiClient.put<ImWebhookConfig>('/admin/im-webhook/config', config)
  return data
}

/** POST /api/v1/admin/im-webhook/test */
export async function test(request: ImWebhookTestRequest = {}): Promise<ImWebhookTestResult> {
  const { data } = await apiClient.post<ImWebhookTestResult>('/admin/im-webhook/test', request)
  return data
}

export const imWebhookAPI = {
  getConfig,
  updateConfig,
  test
}

export default imWebhookAPI
