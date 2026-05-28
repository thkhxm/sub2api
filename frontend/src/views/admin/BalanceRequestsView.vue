<template>
  <AppLayout>
    <TablePageLayout>
      <!-- 顶部过滤 + 刷新 -->
      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <Select
            v-model="filters.status"
            :options="statusFilterOptions"
            class="w-36"
            @change="reload"
          />
          <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
            <button
              @click="reload"
              :disabled="loading"
              class="btn btn-secondary"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="items" :loading="loading">
          <template #cell-amount_usd="{ value }">
            <span class="text-sm font-medium text-gray-900 dark:text-white">
              ${{ formatAmount(value) }}
            </span>
          </template>

          <template #cell-approved_amount_usd="{ value }">
            <span class="text-sm text-gray-600 dark:text-gray-300">
              {{ value == null ? '—' : '$' + formatAmount(value) }}
            </span>
          </template>

          <template #cell-status="{ value }">
            <span :class="['badge', statusBadgeClass(value)]">
              {{ statusLabel(value) }}
            </span>
          </template>

          <template #cell-note="{ value }">
            <span
              class="block max-w-xs truncate text-sm text-gray-700 dark:text-gray-300"
              :title="value || ''"
            >
              {{ value || '—' }}
            </span>
          </template>

          <template #cell-reject_reason="{ row }">
            <span
              v-if="row.status === 'rejected' && row.reject_reason"
              class="block max-w-xs truncate text-sm text-red-600 dark:text-red-400"
              :title="row.reject_reason"
            >
              {{ row.reject_reason }}
            </span>
            <span v-else class="text-sm text-gray-400">—</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ formatDateTime(value) }}
            </span>
          </template>

          <template #cell-reviewed_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ value ? formatDateTime(value) : '—' }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div v-if="row.status === 'pending'" class="flex items-center space-x-1">
              <button
                @click="openApproveDialog(row)"
                class="btn btn-sm btn-primary"
                :title="t('admin.balanceRequests.approve')"
              >
                {{ t('admin.balanceRequests.approve') }}
              </button>
              <button
                @click="openRejectDialog(row)"
                class="btn btn-sm btn-secondary"
                :title="t('admin.balanceRequests.reject')"
              >
                {{ t('admin.balanceRequests.reject') }}
              </button>
            </div>
            <span v-else class="text-sm text-gray-400">—</span>
          </template>
        </DataTable>

        <Pagination
          v-if="total > 0"
          :page="page"
          :page-size="pageSize"
          :total="total"
          @update:page="onPageChange"
          @update:page-size="onPageSizeChange"
        />
      </template>
    </TablePageLayout>

    <!-- 同意弹窗 -->
    <BaseDialog
      :show="showApproveDialog"
      :title="t('admin.balanceRequests.approveTitle')"
      width="normal"
      @close="closeApproveDialog"
    >
      <form id="approve-balance-request-form" @submit.prevent="confirmApprove" class="space-y-4">
        <div v-if="currentRow" class="rounded-md bg-gray-50 p-3 text-sm dark:bg-dark-800">
          <p>
            <span class="text-gray-500">{{ t('admin.balanceRequests.userId') }}：</span>
            <span class="font-medium">{{ currentRow.user_id }}</span>
          </p>
          <p class="mt-1">
            <span class="text-gray-500">{{ t('admin.balanceRequests.originalAmount') }}：</span>
            <span class="font-medium">${{ formatAmount(currentRow.amount_usd) }}</span>
          </p>
          <p v-if="currentRow.note" class="mt-1">
            <span class="text-gray-500">{{ t('admin.balanceRequests.note') }}：</span>
            <span class="font-medium">{{ currentRow.note }}</span>
          </p>
        </div>
        <div>
          <label class="input-label">
            {{ t('admin.balanceRequests.approvedAmount') }}
            <span class="ml-1 text-xs font-normal text-gray-400">
              ({{ t('admin.balanceRequests.approvedAmountHint') }})
            </span>
          </label>
          <input
            v-model.number="approveForm.approved_amount_usd"
            type="number"
            step="0.01"
            min="0.01"
            max="10000"
            required
            class="input"
          />
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" @click="closeApproveDialog" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="approve-balance-request-form"
            :disabled="submitting"
            class="btn btn-primary"
          >
            {{ submitting ? t('common.saving') : t('admin.balanceRequests.confirmApprove') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- 拒绝弹窗 -->
    <BaseDialog
      :show="showRejectDialog"
      :title="t('admin.balanceRequests.rejectTitle')"
      width="normal"
      @close="closeRejectDialog"
    >
      <form id="reject-balance-request-form" @submit.prevent="confirmReject" class="space-y-4">
        <div v-if="currentRow" class="rounded-md bg-gray-50 p-3 text-sm dark:bg-dark-800">
          <p>
            <span class="text-gray-500">{{ t('admin.balanceRequests.userId') }}：</span>
            <span class="font-medium">{{ currentRow.user_id }}</span>
          </p>
          <p class="mt-1">
            <span class="text-gray-500">{{ t('admin.balanceRequests.originalAmount') }}：</span>
            <span class="font-medium">${{ formatAmount(currentRow.amount_usd) }}</span>
          </p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.balanceRequests.rejectReason') }}</label>
          <textarea
            v-model="rejectForm.reason"
            rows="3"
            required
            maxlength="500"
            class="input"
            :placeholder="t('admin.balanceRequests.rejectReasonPlaceholder')"
          ></textarea>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" @click="closeRejectDialog" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="reject-balance-request-form"
            :disabled="submitting"
            class="btn btn-primary"
          >
            {{ submitting ? t('common.saving') : t('admin.balanceRequests.confirmReject') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { BalanceRequest } from '@/api/admin/balanceRequests'
import { formatDateTime } from '@/utils/format'
import type { Column } from '@/components/common/types'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()

// ============ 列表状态 ============
const items = ref<BalanceRequest[]>([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)

const filters = reactive<{ status: '' | 'pending' | 'approved' | 'rejected' }>({
  status: 'pending'
})

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.balanceRequests.statusAll') },
  { value: 'pending', label: t('admin.balanceRequests.statusPending') },
  { value: 'approved', label: t('admin.balanceRequests.statusApproved') },
  { value: 'rejected', label: t('admin.balanceRequests.statusRejected') }
])

const columns: Column[] = [
  { key: 'id', label: 'ID' },
  { key: 'user_id', label: t('admin.balanceRequests.userId') },
  { key: 'amount_usd', label: t('admin.balanceRequests.amount') },
  { key: 'approved_amount_usd', label: t('admin.balanceRequests.approvedAmountShort') },
  { key: 'status', label: t('admin.balanceRequests.status') },
  { key: 'note', label: t('admin.balanceRequests.note') },
  { key: 'reject_reason', label: t('admin.balanceRequests.rejectReason') },
  { key: 'created_at', label: t('admin.balanceRequests.createdAt') },
  { key: 'reviewed_at', label: t('admin.balanceRequests.reviewedAt') },
  { key: 'actions', label: t('common.actions') }
]

async function reload() {
  loading.value = true
  try {
    const resp = await adminAPI.balanceRequests.list({
      page: page.value,
      page_size: pageSize.value,
      status: filters.status
    })
    items.value = resp.items
    total.value = resp.total
  } finally {
    loading.value = false
  }
}

function onPageChange(p: number) {
  page.value = p
  reload()
}

function onPageSizeChange(ps: number) {
  pageSize.value = ps
  page.value = 1
  reload()
}

onMounted(reload)

// ============ Approve / Reject 弹窗 ============
const showApproveDialog = ref(false)
const showRejectDialog = ref(false)
const currentRow = ref<BalanceRequest | null>(null)
const submitting = ref(false)

const approveForm = reactive<{ approved_amount_usd: number }>({
  approved_amount_usd: 0
})

const rejectForm = reactive<{ reason: string }>({
  reason: ''
})

function openApproveDialog(row: BalanceRequest) {
  currentRow.value = row
  approveForm.approved_amount_usd = row.amount_usd
  showApproveDialog.value = true
}

function closeApproveDialog() {
  showApproveDialog.value = false
  currentRow.value = null
}

function openRejectDialog(row: BalanceRequest) {
  currentRow.value = row
  rejectForm.reason = ''
  showRejectDialog.value = true
}

function closeRejectDialog() {
  showRejectDialog.value = false
  currentRow.value = null
}

async function confirmApprove() {
  if (!currentRow.value) return
  submitting.value = true
  try {
    // 如果管理员没改金额，传 undefined 让后端按原金额
    const payload =
      approveForm.approved_amount_usd === currentRow.value.amount_usd
        ? {}
        : { approved_amount_usd: approveForm.approved_amount_usd }
    await adminAPI.balanceRequests.approve(currentRow.value.id, payload)
    closeApproveDialog()
    await reload()
  } finally {
    submitting.value = false
  }
}

async function confirmReject() {
  if (!currentRow.value) return
  submitting.value = true
  try {
    await adminAPI.balanceRequests.reject(currentRow.value.id, {
      reason: rejectForm.reason.trim()
    })
    closeRejectDialog()
    await reload()
  } finally {
    submitting.value = false
  }
}

// ============ 辅助函数 ============
function formatAmount(v: number | null | undefined): string {
  if (v == null) return '—'
  return Number(v).toFixed(2)
}

function statusLabel(value: string): string {
  return t(`admin.balanceRequests.status${value.charAt(0).toUpperCase() + value.slice(1)}`)
}

function statusBadgeClass(value: string): string {
  switch (value) {
    case 'pending':
      return 'badge-warning'
    case 'approved':
      return 'badge-success'
    case 'rejected':
      return 'badge-danger'
    default:
      return 'badge-secondary'
  }
}
</script>
