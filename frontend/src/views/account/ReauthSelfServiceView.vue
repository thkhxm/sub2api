<template>
  <div class="min-h-screen bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white">
    <!-- 顶部品牌栏 -->
    <header class="border-b border-gray-200 bg-white/95 dark:border-dark-800 dark:bg-dark-900/95">
      <div class="mx-auto flex max-w-3xl items-center justify-between gap-4 px-4 py-4 sm:px-6">
        <div class="flex min-w-0 items-center gap-3">
          <span
            class="flex h-10 w-10 flex-shrink-0 items-center justify-center overflow-hidden rounded-xl bg-white shadow-sm ring-1 ring-gray-200 dark:bg-dark-800 dark:ring-dark-700"
          >
            <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-base font-semibold text-gray-950 dark:text-white">
            {{ siteName }}
          </span>
        </div>
        <span class="text-sm font-medium text-gray-500 dark:text-gray-400">
          {{ t('accountReauth.pageTitle') }}
        </span>
      </div>
    </header>

    <main class="mx-auto max-w-3xl px-4 py-8 sm:px-6 lg:py-12">
      <!-- 加载中 -->
      <div v-if="stage === 'loading'" class="flex min-h-[320px] flex-col items-center justify-center gap-4">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('accountReauth.loading') }}</p>
      </div>

      <!-- 终态错误（token 无效/过期/已用过等） -->
      <section
        v-else-if="stage === 'error'"
        class="rounded-xl border border-red-200 bg-red-50 p-6 dark:border-red-500/30 dark:bg-red-500/10"
      >
        <div class="flex items-start gap-3">
          <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-red-500">
            <Icon name="exclamationTriangle" size="md" class="text-white" :stroke-width="2" />
          </span>
          <div class="min-w-0">
            <h1 class="text-lg font-semibold text-red-800 dark:text-red-200">
              {{ t('accountReauth.errorTitle') }}
            </h1>
            <p class="mt-2 whitespace-pre-line text-sm leading-6 text-red-700 dark:text-red-300">
              {{ errorMessage }}
            </p>
            <p class="mt-3 text-xs text-red-600/80 dark:text-red-300/70">
              {{ t('accountReauth.errorHint') }}
            </p>
          </div>
        </div>
      </section>

      <!-- 成功页 -->
      <section
        v-else-if="stage === 'success'"
        class="rounded-xl border border-green-200 bg-green-50 p-6 dark:border-green-500/30 dark:bg-green-500/10"
      >
        <div class="flex flex-col items-center gap-4 py-6 text-center">
          <span class="flex h-14 w-14 items-center justify-center rounded-full bg-green-500">
            <Icon name="checkCircle" size="lg" class="text-white" :stroke-width="2" />
          </span>
          <h1 class="text-xl font-bold text-green-800 dark:text-green-200">
            {{ t('accountReauth.successTitle') }}
          </h1>
          <p class="max-w-md text-sm leading-6 text-green-700 dark:text-green-300">
            {{
              t('accountReauth.successDesc', {
                name: successResult?.account_name || info?.account_name || ''
              })
            }}
          </p>
          <p class="text-xs text-green-600/80 dark:text-green-300/70">
            {{ t('accountReauth.successHint') }}
          </p>
        </div>
      </section>

      <!-- 信息确认 + 授权流程 -->
      <div v-else class="space-y-6">
        <!-- 账号信息卡片 -->
        <section
          class="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-900"
        >
          <h1 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('accountReauth.infoTitle') }}
          </h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('accountReauth.infoDesc') }}
          </p>

          <dl class="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <dt class="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {{ t('accountReauth.fields.accountName') }}
              </dt>
              <dd class="mt-1 break-all text-sm font-medium text-gray-900 dark:text-white">
                {{ info?.account_name || '-' }}
              </dd>
            </div>
            <div>
              <dt class="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {{ t('accountReauth.fields.platform') }}
              </dt>
              <dd class="mt-1 text-sm font-medium text-gray-900 dark:text-white">
                {{ platformLabel }}
              </dd>
            </div>
            <div>
              <dt class="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {{ t('accountReauth.fields.owner') }}
              </dt>
              <dd class="mt-1 break-all text-sm font-medium text-gray-900 dark:text-white">
                {{ info?.owner_name || '-' }}
                <span v-if="info?.owner_email" class="text-gray-500 dark:text-gray-400">
                  ({{ info.owner_email }})
                </span>
              </dd>
            </div>
            <div v-if="expiresAtLabel">
              <dt class="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {{ t('accountReauth.fields.expiresAt') }}
              </dt>
              <dd class="mt-1 text-sm font-medium text-gray-900 dark:text-white">
                {{ expiresAtLabel }}
              </dd>
            </div>
          </dl>

          <!-- 开始按钮（仅 info 阶段） -->
          <div v-if="stage === 'info'" class="mt-6">
            <button
              type="button"
              class="btn btn-primary"
              :disabled="generating"
              @click="handleStart"
            >
              <svg
                v-if="generating"
                class="-ml-1 mr-2 h-4 w-4 animate-spin"
                fill="none"
                viewBox="0 0 24 24"
              >
                <circle
                  class="opacity-25"
                  cx="12"
                  cy="12"
                  r="10"
                  stroke="currentColor"
                  stroke-width="4"
                ></circle>
                <path
                  class="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                ></path>
              </svg>
              <Icon v-else name="link" size="sm" class="mr-2" />
              {{ generating ? t('accountReauth.starting') : t('accountReauth.startButton') }}
            </button>
            <p v-if="flowError" class="mt-3 whitespace-pre-line text-sm text-red-600 dark:text-red-400">
              {{ flowError }}
            </p>
          </div>
        </section>

        <!-- 授权流程（authorizing 阶段，复用 OAuthAuthorizationFlow） -->
        <section v-if="stage === 'authorizing'" class="space-y-5">
          <OAuthAuthorizationFlow
            ref="oauthFlowRef"
            add-method="oauth"
            :auth-url="authUrl"
            :session-id="sessionId"
            :loading="generating || exchanging"
            :error="flowError"
            :show-help="false"
            :show-proxy-warning="false"
            :show-cookie-option="false"
            :allow-multiple="false"
            :platform="flowPlatform"
          />

          <div class="flex justify-end">
            <button
              type="button"
              class="btn btn-primary"
              :disabled="!canExchange"
              @click="handleExchange"
            >
              <svg
                v-if="exchanging"
                class="-ml-1 mr-2 h-4 w-4 animate-spin"
                fill="none"
                viewBox="0 0 24 24"
              >
                <circle
                  class="opacity-25"
                  cx="12"
                  cy="12"
                  r="10"
                  stroke="currentColor"
                  stroke-width="4"
                ></circle>
                <path
                  class="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                ></path>
              </svg>
              {{ exchanging ? t('accountReauth.verifying') : t('accountReauth.completeButton') }}
            </button>
          </div>
        </section>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import Icon from '@/components/icons/Icon.vue'
import OAuthAuthorizationFlow from '@/components/account/OAuthAuthorizationFlow.vue'
import { useReauthSelfService } from '@/composables/useReauthSelfService'
import type { AccountPlatform } from '@/types'

// 暴露给父组件读取的 OAuth 子组件接口（defineExpose 会自动解包 ref）
interface OAuthFlowExposed {
  authCode: string
  oauthState: string
  reset: () => void
}

const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => appStore.siteLogo || '')

const token = String(route.query.token || '')

const {
  stage,
  info,
  authUrl,
  sessionId,
  generating,
  exchanging,
  errorMessage,
  flowError,
  successResult,
  loadInfo,
  generateUrl,
  exchangeCode
} = useReauthSelfService(token)

const oauthFlowRef = ref<OAuthFlowExposed | null>(null)

// 平台 → OAuthAuthorizationFlow 的 platform prop（codex 账号即 openai）
const flowPlatform = computed<AccountPlatform>(() => {
  const p = (info.value?.platform || 'openai').toLowerCase()
  if (p === 'openai' || p === 'anthropic' || p === 'gemini' || p === 'antigravity') {
    return p as AccountPlatform
  }
  // codex / chatgpt 等同 openai
  if (p === 'codex' || p === 'chatgpt') {
    return 'openai'
  }
  return 'openai'
})

// 平台展示名
const platformLabel = computed(() => {
  const p = (info.value?.platform || '').toLowerCase()
  const map: Record<string, string> = {
    openai: 'OpenAI (Codex)',
    codex: 'OpenAI (Codex)',
    chatgpt: 'OpenAI (Codex)',
    anthropic: 'Anthropic',
    gemini: 'Gemini',
    antigravity: 'Antigravity'
  }
  return map[p] || info.value?.platform || '-'
})

// 过期时间展示
const expiresAtLabel = computed(() => {
  const raw = info.value?.expires_at
  if (raw == null || raw === '') return ''
  let date: Date
  if (typeof raw === 'number') {
    // 秒级时间戳兼容
    date = new Date(raw < 1e12 ? raw * 1000 : raw)
  } else {
    date = new Date(raw)
  }
  if (Number.isNaN(date.getTime())) {
    return String(raw)
  }
  return date.toLocaleString()
})

// 能否提交兑换：已有 code + session + 不在加载中
const canExchange = computed(() => {
  const code = oauthFlowRef.value?.authCode || ''
  return !!code.trim() && !!sessionId.value && !exchanging.value
})

const handleStart = async () => {
  await generateUrl()
}

const handleExchange = async () => {
  const code = oauthFlowRef.value?.authCode || ''
  const state = oauthFlowRef.value?.oauthState || ''
  await exchangeCode(code, state)
}

onMounted(() => {
  // 公开页直达时，确保站点品牌信息（名称/Logo）已加载
  appStore.fetchPublicSettings().catch(() => {
    // 品牌信息加载失败不阻断重授权流程
  })
  loadInfo()
})
</script>
