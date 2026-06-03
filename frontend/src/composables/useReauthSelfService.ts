/**
 * 成员自助重授权流程编排 composable
 *
 * 封装公开端点的三步流程：
 *   1. loadInfo()      —— 用 token 拉取脱敏账号信息
 *   2. generateUrl()   —— 拿 OAuth 授权 URL + session
 *   3. exchangeCode()  —— 用回填的 code/state 兑换，完成重授权
 *
 * 不依赖任何登录态 / admin token，全部走 accountReauthAPI 的裸客户端。
 */

import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  accountReauthAPI,
  type ReauthAccountInfo,
  type ReauthApiError
} from '@/api/accountReauth'

/** 自助页阶段 */
export type ReauthStage = 'loading' | 'info' | 'authorizing' | 'success' | 'error'

/** 已知错误码 → i18n key 后缀 */
const REASON_I18N_SUFFIX: Record<string, string> = {
  REAUTH_INVALID_TOKEN: 'invalidToken',
  REAUTH_INVALID_SIGNATURE: 'invalidSignature',
  REAUTH_INVALID_PAYLOAD: 'invalidPayload',
  REAUTH_TOKEN_EXPIRED: 'tokenExpired',
  REAUTH_TOKEN_CONSUMED: 'tokenConsumed',
  REAUTH_PLATFORM_UNSUPPORTED: 'platformUnsupported'
}

export function useReauthSelfService(token: string) {
  const { t } = useI18n()

  const stage = ref<ReauthStage>('loading')
  const info = ref<ReauthAccountInfo | null>(null)

  // OAuth 流程状态
  const authUrl = ref('')
  const sessionId = ref('')
  const generating = ref(false)
  const exchanging = ref(false)

  // 错误信息
  const errorReason = ref<string>('')
  const errorMessage = ref<string>('')
  // 表单内（OAuth 子组件）展示的错误（generate / exchange 阶段的可恢复错误）
  const flowError = ref<string>('')

  const successResult = ref<{ account_id: number; account_name: string; platform: string } | null>(null)

  const hasToken = computed(() => !!token && token.trim().length > 0)

  /** 把结构化错误翻译为友好文案；优先按 reason 映射，否则回退后端 message */
  const resolveErrorMessage = (err: ReauthApiError): string => {
    const reason = err.reason
    if (reason && REASON_I18N_SUFFIX[reason]) {
      const key = `accountReauth.errors.${REASON_I18N_SUFFIX[reason]}`
      const translated = t(key)
      if (translated !== key) {
        return translated
      }
    }
    return err.message || t('accountReauth.errors.generic')
  }

  /** 进入终态错误页 */
  const setFatalError = (err: ReauthApiError) => {
    errorReason.value = err.reason || ''
    errorMessage.value = resolveErrorMessage(err)
    stage.value = 'error'
  }

  /** 步骤一：加载账号信息 */
  const loadInfo = async () => {
    if (!hasToken.value) {
      setFatalError({ status: 400, reason: 'REAUTH_INVALID_TOKEN', message: t('accountReauth.errors.invalidToken') })
      return
    }
    stage.value = 'loading'
    try {
      const data = await accountReauthAPI.getInfo(token)
      info.value = data
      stage.value = 'info'
    } catch (err) {
      setFatalError(err as ReauthApiError)
    }
  }

  /** 步骤二：生成授权 URL（进入 authorizing 阶段） */
  const generateUrl = async () => {
    if (!hasToken.value) return
    generating.value = true
    flowError.value = ''
    try {
      const data = await accountReauthAPI.generateUrl(token)
      authUrl.value = data.auth_url
      sessionId.value = data.session_id
      stage.value = 'authorizing'
    } catch (err) {
      const e = err as ReauthApiError
      // token 类终态错误直接跳错误页；其余在流程内提示
      if (e.reason && REASON_I18N_SUFFIX[e.reason]) {
        setFatalError(e)
      } else {
        flowError.value = resolveErrorMessage(e)
      }
    } finally {
      generating.value = false
    }
  }

  /** 步骤三：兑换授权码，完成重授权 */
  const exchangeCode = async (code: string, state: string) => {
    if (!hasToken.value) return
    if (!code.trim()) {
      flowError.value = t('accountReauth.errors.missingCode')
      return
    }
    if (!sessionId.value) {
      flowError.value = t('accountReauth.errors.missingSession')
      return
    }
    exchanging.value = true
    flowError.value = ''
    try {
      const data = await accountReauthAPI.exchangeCode({
        token,
        session: sessionId.value,
        code: code.trim(),
        state: state.trim()
      })
      successResult.value = data
      stage.value = 'success'
    } catch (err) {
      const e = err as ReauthApiError
      if (e.reason && REASON_I18N_SUFFIX[e.reason]) {
        setFatalError(e)
      } else {
        flowError.value = resolveErrorMessage(e)
      }
    } finally {
      exchanging.value = false
    }
  }

  return {
    // 状态
    stage,
    info,
    authUrl,
    sessionId,
    generating,
    exchanging,
    errorReason,
    errorMessage,
    flowError,
    successResult,
    hasToken,
    // 方法
    loadInfo,
    generateUrl,
    exchangeCode
  }
}
