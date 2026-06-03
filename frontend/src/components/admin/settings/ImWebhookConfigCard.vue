<template>
  <div class="space-y-6">
    <!-- 主卡片 -->
    <div class="card">
      <div
        class="flex items-center justify-between border-b border-gray-100 px-6 py-4 dark:border-dark-700"
      >
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('admin.settings.imWebhook.title') }}
          </h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.imWebhook.description') }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="testing || loading || saving"
          @click="handleTest"
        >
          <svg
            v-if="testing"
            class="h-4 w-4 animate-spin"
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
          {{ testing ? t('admin.settings.imWebhook.testing') : t('admin.settings.imWebhook.sendTest') }}
        </button>
      </div>

      <!-- 加载中 -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else class="space-y-6 p-6">
        <!-- 总开关 -->
        <div class="flex items-center justify-between">
          <div>
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.settings.imWebhook.enableLabel') }}
            </label>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.imWebhook.enableHint') }}
            </p>
          </div>
          <Toggle v-model="config.enabled" />
        </div>

        <!-- 渠道列表 -->
        <div class="border-t border-gray-100 pt-6 dark:border-dark-700">
          <div class="mb-3 flex items-center justify-between">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.settings.imWebhook.channels') }}
            </label>
          </div>

          <p
            v-if="config.channels.length === 0"
            class="rounded-lg border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-400 dark:border-dark-600 dark:text-gray-500"
          >
            {{ t('admin.settings.imWebhook.noChannels') }}
          </p>

          <div v-else class="space-y-4">
            <div
              v-for="(channel, index) in config.channels"
              :key="index"
              class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
            >
              <div class="mb-3 flex items-center justify-between gap-3">
                <div class="flex flex-1 items-center gap-3">
                  <!-- 渠道类型 -->
                  <div class="w-40">
                    <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                      {{ t('admin.settings.imWebhook.channelType') }}
                    </label>
                    <select v-model="channel.type" class="input">
                      <option value="feishu">{{ t('admin.settings.imWebhook.types.feishu') }}</option>
                      <option value="wecom">{{ t('admin.settings.imWebhook.types.wecom') }}</option>
                      <option value="telegram">{{ t('admin.settings.imWebhook.types.telegram') }}</option>
                    </select>
                  </div>
                  <!-- 单条启用 -->
                  <div class="flex flex-col items-start">
                    <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                      {{ t('admin.settings.imWebhook.channelEnabled') }}
                    </label>
                    <Toggle v-model="channel.enabled" />
                  </div>
                </div>
                <!-- 删除 -->
                <button
                  type="button"
                  class="mt-5 rounded-lg p-2 text-red-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                  :title="t('common.delete')"
                  @click="removeChannel(index)"
                >
                  <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      stroke-width="2"
                      d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
                    />
                  </svg>
                </button>
              </div>

              <!-- Webhook URL（飞书 / 企业微信必填；Telegram 可选） -->
              <div v-if="channel.type !== 'telegram'" class="mb-3">
                <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.imWebhook.url') }}
                </label>
                <input
                  v-model="channel.url"
                  type="text"
                  class="input font-mono text-sm"
                  :placeholder="urlPlaceholder(channel.type)"
                />
              </div>

              <!-- 飞书签名密钥 -->
              <div v-if="channel.type === 'feishu'" class="mb-3">
                <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.imWebhook.secret') }}
                </label>
                <input
                  v-model="channel.secret"
                  type="password"
                  class="input font-mono text-sm"
                  :placeholder="t('admin.settings.imWebhook.secretPlaceholder')"
                />
                <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                  {{ t('admin.settings.imWebhook.secretHint') }}
                </p>
              </div>

              <!-- Telegram 专属字段 -->
              <template v-if="channel.type === 'telegram'">
                <div class="mb-3">
                  <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.imWebhook.telegramBotToken') }}
                  </label>
                  <input
                    v-model="channel.telegram_bot_token"
                    type="password"
                    class="input font-mono text-sm"
                    placeholder="123456789:ABCdef..."
                  />
                </div>
                <div class="mb-3">
                  <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.imWebhook.telegramChatId') }}
                  </label>
                  <input
                    v-model="channel.telegram_chat_id"
                    type="text"
                    class="input font-mono text-sm"
                    placeholder="-1001234567890"
                  />
                </div>
              </template>
            </div>
          </div>

          <!-- 新增渠道 -->
          <button
            type="button"
            class="mt-4 w-full rounded-lg border-2 border-dashed border-gray-300 px-4 py-2 text-sm text-gray-600 transition-colors hover:border-gray-400 hover:text-gray-700 dark:border-dark-600 dark:text-gray-400 dark:hover:border-dark-500 dark:hover:text-gray-300"
            @click="addChannel"
          >
            <svg class="mr-1 inline h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
            </svg>
            {{ t('admin.settings.imWebhook.addChannel') }}
          </button>
        </div>

        <!-- 保存按钮 -->
        <div class="flex justify-end border-t border-gray-100 pt-6 dark:border-dark-700">
          <button
            type="button"
            class="btn btn-primary"
            :disabled="saving || loading"
            @click="handleSave"
          >
            <svg
              v-if="saving"
              class="h-4 w-4 animate-spin"
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
            {{ saving ? t('admin.settings.saving') : t('admin.settings.saveSettings') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import Toggle from '@/components/common/Toggle.vue'
import type {
  ImWebhookConfig,
  ImWebhookChannel,
  ImWebhookChannelType
} from '@/api/admin'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const testing = ref(false)

const config = reactive<ImWebhookConfig>({
  enabled: false,
  channels: []
})

const urlPlaceholder = (type: ImWebhookChannelType): string => {
  if (type === 'feishu') return 'https://open.feishu.cn/open-apis/bot/v2/hook/...'
  if (type === 'wecom') return 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...'
  return ''
}

const applyConfig = (data: ImWebhookConfig | null | undefined) => {
  config.enabled = data?.enabled ?? false
  config.channels = Array.isArray(data?.channels)
    ? data!.channels.map((c) => ({
        type: c.type,
        url: c.url || '',
        secret: c.secret || '',
        telegram_chat_id: c.telegram_chat_id || '',
        telegram_bot_token: c.telegram_bot_token || '',
        enabled: c.enabled ?? true
      }))
    : []
}

const loadConfig = async () => {
  loading.value = true
  try {
    const data = await adminAPI.imWebhook.getConfig()
    applyConfig(data)
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.settings.imWebhook.loadFailed')))
  } finally {
    loading.value = false
  }
}

const addChannel = () => {
  const channel: ImWebhookChannel = {
    type: 'feishu',
    url: '',
    secret: '',
    telegram_chat_id: '',
    telegram_bot_token: '',
    enabled: true
  }
  config.channels.push(channel)
}

const removeChannel = (index: number) => {
  config.channels.splice(index, 1)
}

/** 提交前清洗：按渠道类型只保留相关字段，去掉空值噪声 */
const buildPayload = (): ImWebhookConfig => {
  return {
    enabled: config.enabled,
    channels: config.channels.map((c) => {
      const base: ImWebhookChannel = {
        type: c.type,
        url: (c.url || '').trim(),
        enabled: c.enabled
      }
      if (c.type === 'feishu') {
        const secret = (c.secret || '').trim()
        if (secret) base.secret = secret
      }
      if (c.type === 'telegram') {
        const botToken = (c.telegram_bot_token || '').trim()
        const chatId = (c.telegram_chat_id || '').trim()
        if (botToken) base.telegram_bot_token = botToken
        if (chatId) base.telegram_chat_id = chatId
      }
      return base
    })
  }
}

const handleSave = async () => {
  saving.value = true
  try {
    const data = await adminAPI.imWebhook.updateConfig(buildPayload())
    applyConfig(data)
    appStore.showSuccess(t('admin.settings.imWebhook.saveSuccess'))
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.settings.imWebhook.saveFailed')))
  } finally {
    saving.value = false
  }
}

const handleTest = async () => {
  testing.value = true
  try {
    const result = await adminAPI.imWebhook.test({
      title: t('admin.settings.imWebhook.testTitle'),
      text: t('admin.settings.imWebhook.testText')
    })
    // 后端可能返回逐渠道结果；有失败项则提示，否则成功
    const failed = (result.results || []).filter((r) => !r.success)
    if (failed.length > 0) {
      const detail = failed
        .map((r) => `${r.type}: ${r.error || t('admin.settings.imWebhook.testFailed')}`)
        .join('\n')
      appStore.showError(detail)
    } else if (result.success === false) {
      appStore.showError(result.message || t('admin.settings.imWebhook.testFailed'))
    } else {
      appStore.showSuccess(result.message || t('admin.settings.imWebhook.testSuccess'))
    }
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.settings.imWebhook.testFailed')))
  } finally {
    testing.value = false
  }
}

onMounted(() => {
  loadConfig()
})
</script>
