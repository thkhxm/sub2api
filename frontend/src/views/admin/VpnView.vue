<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h1 class="text-2xl font-bold">{{ t('vpn.adminTitle') }}</h1>
        <button class="btn btn-secondary" :disabled="loading || busy" @click="load">{{ t('vpn.refresh') }}</button>
      </div>
      <p v-if="error" role="alert" class="break-words text-red-600">{{ error }}</p>
      <div v-if="summary" class="grid grid-cols-2 gap-3 md:grid-cols-4">
        <div v-for="[label, value] in summaryFields" :key="label" class="card p-4">
          <p class="text-sm text-gray-500">{{ t(`vpn.${label}`) }}</p><p class="mt-1 text-xl font-semibold">{{ value }}</p>
        </div>
      </div>
      <section class="card space-y-4 p-5">
        <div class="flex items-center justify-between gap-3">
          <h2 class="text-lg font-semibold">{{ t('vpn.servers') }}</h2>
          <button class="btn btn-primary" :disabled="busy" @click="openServer()">{{ t('vpn.addServer') }}</button>
        </div>
        <p v-if="!servers.length && !loading" class="text-gray-500">{{ t('vpn.noServers') }}</p>
        <div class="grid gap-4 xl:grid-cols-2">
          <article v-for="server in servers" :key="server.id" :aria-label="server.name" class="flex min-w-0 flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800/50">
            <div class="flex flex-col items-start gap-3 px-5 pt-5 sm:flex-row sm:justify-between">
              <div class="w-full min-w-0 flex-1 sm:w-auto">
                <h3 class="break-words font-semibold text-gray-900 dark:text-gray-100">{{ server.name }}</h3>
                <p class="mt-1.5 break-all text-xs leading-5 text-gray-500 dark:text-gray-400">{{ server.base_url }}</p>
              </div>
              <span class="inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset" :class="!server.enabled ? 'bg-gray-100 text-gray-600 ring-gray-200 dark:bg-dark-700 dark:text-gray-400 dark:ring-dark-600' : server.healthy ? 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-900/20 dark:text-emerald-400 dark:ring-emerald-800' : 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-400 dark:ring-amber-800'">
                <span aria-hidden="true" class="h-1.5 w-1.5 rounded-full bg-current"></span>
                {{ t(server.enabled ? 'vpn.enabled' : 'vpn.disabled') }} · {{ t(server.healthy ? 'vpn.healthy' : 'vpn.unhealthy') }}
              </span>
            </div>
            <div class="space-y-4 p-5">
              <dl class="grid grid-cols-2 gap-3 sm:grid-cols-3">
                <div class="min-w-0 rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900/50">
                  <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('vpn.quota') }}</dt>
                  <dd class="mt-2 break-words text-base font-semibold tracking-tight text-gray-900 tabular-nums dark:text-gray-100 sm:text-lg">{{ server.traffic_quota_bytes ? formatVpnBytes(server.traffic_quota_bytes) : t('vpn.notConfigured') }}</dd>
                </div>
                <div class="min-w-0 rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900/50">
                  <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('vpn.used') }}</dt>
                  <dd class="mt-2 break-words text-base font-semibold tracking-tight text-gray-900 tabular-nums dark:text-gray-100 sm:text-lg">{{ trafficBytes(server.traffic_used_bytes) }}</dd>
                </div>
                <div class="col-span-2 min-w-0 rounded-xl border border-primary-200 bg-primary-50 p-3 dark:border-primary-800 dark:bg-primary-900/20 sm:col-span-1">
                  <dt class="flex flex-wrap items-center gap-1.5 text-xs text-primary-700 dark:text-primary-300">
                    {{ t('vpn.remaining') }}
                    <span v-if="trafficCurrent(server) && server.traffic_quota_bytes && server.traffic_remaining_bytes != null && server.traffic_accounting_status === 'partial_history'" class="rounded bg-white/80 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-dark-800 dark:text-amber-400">{{ t('vpn.estimated') }}</span>
                  </dt>
                  <dd class="mt-2 break-words text-base font-semibold tracking-tight text-primary-700 tabular-nums dark:text-primary-300 sm:text-lg">{{ trafficCurrent(server) && server.traffic_quota_bytes ? trafficBytes(server.traffic_remaining_bytes) : '—' }}</dd>
                </div>
              </dl>
              <dl class="grid grid-cols-3 divide-x divide-gray-200 rounded-xl border border-gray-200 bg-gray-50/80 py-3 text-center dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-900/30">
                <div class="flex min-w-0 flex-col px-2"><dt class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('vpn.personalCount') }}</dt><dd class="mt-auto pt-1 text-base font-semibold text-gray-900 tabular-nums dark:text-gray-100">{{ server.personal_user_count }}</dd></div>
                <div class="flex min-w-0 flex-col px-2"><dt class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('vpn.assignedCount') }}</dt><dd class="mt-auto pt-1 text-base font-semibold text-gray-900 tabular-nums dark:text-gray-100">{{ server.assigned_count }}</dd></div>
                <div class="flex min-w-0 flex-col px-2"><dt class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('vpn.pendingCount') }}</dt><dd class="mt-auto pt-1 text-base font-semibold text-gray-900 tabular-nums dark:text-gray-100">{{ server.pending_count }}</dd></div>
              </dl>
              <dl class="grid grid-cols-1 gap-x-6 gap-y-3 text-xs sm:grid-cols-2">
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('vpn.lastChecked') }}</dt><dd class="mt-1 break-words font-medium text-gray-700 tabular-nums dark:text-gray-300">{{ time(server.last_checked_at) }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('vpn.sampled') }}</dt><dd class="mt-1 break-words font-medium text-gray-700 tabular-nums dark:text-gray-300">{{ time(server.traffic_sampled_at || null) }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('vpn.reset') }}</dt><dd class="mt-1 break-words font-medium text-gray-700 tabular-nums dark:text-gray-300">{{ time(server.traffic_period_end || null) }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('vpn.availableFrom') }}</dt><dd class="mt-1 break-words font-medium text-gray-700 tabular-nums dark:text-gray-300">{{ time(server.traffic_available_from || null) }}</dd></div>
                <div class="flex flex-wrap items-center gap-2 sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">{{ t('vpn.accounting') }}</dt><dd class="rounded-md bg-gray-100 px-2 py-1 font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ state(server.traffic_accounting_status || 'unknown') }}</dd></div>
              </dl>
              <p v-if="!trafficCurrent(server)" class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400">{{ t('vpn.staleNotice') }}</p>
              <p v-if="server.health_error" class="break-words rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-600 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">{{ server.health_error }}</p>
            </div>
            <div class="mt-auto flex flex-wrap justify-end gap-2 border-t border-gray-200 bg-gray-50/80 px-5 py-3 dark:border-dark-700 dark:bg-dark-900/30">
              <button class="btn btn-secondary" :disabled="busy" @click="openServer(server)">{{ t('vpn.editServer') }}</button>
              <button class="btn btn-secondary" :disabled="busy" @click="probe(server.id)">{{ t('vpn.probe') }}</button>
            </div>
          </article>
        </div>
      </section>
      <section class="card space-y-4 p-5" data-testid="vpn-groups">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-lg font-semibold">{{ t('vpn.groups') }}</h2>
          <button class="btn btn-primary" :disabled="busy || loading" @click="openGroup()">{{ t('vpn.addGroup') }}</button>
        </div>
        <p class="text-sm text-gray-500">{{ t('vpn.groupsHint') }}</p>
        <p v-if="!groups.length && !loading" class="text-gray-500">{{ t('vpn.noGroups') }}</p>
        <div class="grid gap-4 xl:grid-cols-2">
          <div v-for="group in groups" :key="group.id" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700" :data-group-id="group.id">
            <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="font-semibold">{{ group.name }} <span v-if="group.is_default" class="text-sm text-primary-500">{{ t('vpn.defaultGroup') }}</span></h3><span>{{ formatVpnBytes(group.quota_bytes) }} / {{ t('vpn.month') }}</span></div>
            <dl class="flex flex-wrap gap-x-5 gap-y-2 text-sm">
              <div><dt class="text-gray-500">{{ t('vpn.groupMembers') }}</dt><dd>{{ group.member_count }}</dd></div>
              <div><dt class="text-gray-500">{{ t('vpn.subscriptions') }}</dt><dd>{{ group.subscription_count }}</dd></div>
              <div><dt class="text-gray-500">{{ t('vpn.groupPending') }}</dt><dd>{{ group.pending_count }}</dd></div>
              <div><dt class="text-gray-500">{{ t('vpn.states.failed') }}</dt><dd :class="group.failed_count ? 'text-red-600' : ''">{{ group.failed_count }}</dd></div>
            </dl>
            <p v-if="group.failed_count" class="text-sm text-amber-600">{{ t('vpn.groupRetryHint') }}</p>
            <button class="btn btn-secondary" :disabled="busy" @click="openGroup(group)">{{ t('vpn.editGroup') }}</button>
          </div>
        </div>
      </section>
      <VpnTrafficChart :servers="servers" />
      <section class="card space-y-4 p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-lg font-semibold">{{ t('vpn.subscriptions') }}</h2>
          <button class="btn btn-primary" :disabled="busy" @click="openCreate">{{ t('vpn.createForUser') }}</button>
        </div>
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="filter">
          <label class="min-w-48 flex-1 text-sm">{{ t('vpn.search') }}<input v-model="query" class="input mt-1" /></label>
          <label class="text-sm">{{ t('vpn.server') }}<select v-model="serverFilter" class="input mt-1"><option value="">{{ t('vpn.allServers') }}</option><option v-for="server in servers" :key="server.id" :value="server.id">{{ server.name }}</option></select></label>
          <label class="text-sm">{{ t('vpn.status') }}<select v-model="statusFilter" class="input mt-1"><option value="">{{ t('vpn.allStatuses') }}</option><option v-for="status in statuses" :key="status" :value="status">{{ state(status) }}</option></select></label>
          <button class="btn btn-secondary" :disabled="loading || busy">{{ t('vpn.filter') }}</button>
        </form>
        <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('vpn.loading') }}</p>
        <p v-if="!items.length && !loading" class="py-5 text-center text-gray-500">{{ t('vpn.noResults') }}</p>
        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead><tr class="border-b border-gray-200 text-gray-500 dark:border-dark-700"><th class="p-3">{{ t('vpn.selectUser') }}</th><th class="p-3">{{ t('vpn.server') }}</th><th class="p-3">{{ t('vpn.status') }}</th><th class="p-3">{{ t('vpn.used') }} / {{ t('vpn.quota') }}</th><th class="p-3">{{ t('vpn.sampled') }}</th><th class="p-3">{{ t('vpn.details') }}</th></tr></thead>
            <tbody>
              <tr v-for="item in items" :key="item.id" class="border-b border-gray-100 dark:border-dark-700">
                <td class="p-3"><div>{{ item.user_email || `#${item.user_id}` }}</div><div class="text-xs text-gray-500">#{{ item.user_id }}<span v-if="item.group_name"> · {{ item.group_name }}</span></div></td>
                <td class="p-3">{{ item.server_name }}</td>
                <td class="p-3"><div>{{ state(item.status) }} · {{ state(item.apply_status) }}</div><div class="text-xs text-gray-500">{{ state(item.accounting_status) }}</div><p v-if="item.last_error" class="max-w-xs break-words text-xs text-red-600">{{ item.last_error }}</p></td>
                <td class="whitespace-nowrap p-3">{{ formatVpnBytes(item.used_bytes) }} / {{ formatVpnBytes(item.quota_bytes) }}</td>
                <td class="p-3">{{ time(item.sampled_at) }}</td>
                <td class="p-3"><button class="btn btn-secondary whitespace-nowrap" @click="selected = item">{{ t('vpn.details') }}</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <p class="text-sm text-gray-500">{{ t('vpn.page', { page, total }) }}</p>
          <div class="flex gap-2"><button class="btn btn-secondary" :disabled="page <= 1 || loading || busy" @click="changePage(-1)">{{ t('vpn.previous') }}</button><button class="btn btn-secondary" :disabled="page * pageSize >= total || loading || busy" @click="changePage(1)">{{ t('vpn.next') }}</button></div>
        </div>
      </section>
    </div>

    <BaseDialog :show="showServer" :title="t(serverId ? 'vpn.editServer' : 'vpn.addServer')" :show-close-button="!busy" :close-on-escape="!busy" @close="closeServer">
      <form class="space-y-4" @submit.prevent="saveServer">
        <p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p>
        <label class="block text-sm">{{ t('vpn.name') }}<input v-model="serverForm.name" class="input mt-1" required maxlength="200" /></label>
        <label class="block text-sm">{{ t('vpn.baseUrl') }}<input v-model="serverForm.base_url" class="input mt-1" type="url" required :disabled="serverHasBindings" /></label>
        <p class="text-xs text-gray-500">{{ t('vpn.bindingHint') }}</p>
        <label class="block text-sm">{{ t('vpn.adminUsername') }}<input v-model="serverForm.admin_username" class="input mt-1" autocomplete="off" required /></label>
        <label class="block text-sm">{{ t('vpn.adminPassword') }}<input v-model="serverForm.admin_password" class="input mt-1" type="password" autocomplete="new-password" :required="!serverId" /></label>
        <p v-if="serverId" class="text-xs text-gray-500">{{ t('vpn.passwordHint') }}</p>
        <label class="block text-sm">{{ t('vpn.caPem') }}<textarea v-model="serverForm.ca_pem" class="input mt-1 font-mono" rows="4" /></label>
        <p class="text-xs text-gray-500">{{ t('vpn.caHint') }}</p>
        <label class="block text-sm">{{ t('vpn.nodeQuotaGiB') }}<input v-model.number="serverQuotaGiB" class="input mt-1" type="number" min="0" step="any" required /></label>
        <label class="block text-sm">{{ t('vpn.nodeOffsetGiB') }}<input v-model.number="serverOffsetGiB" class="input mt-1" type="number" min="0" step="any" required /></label>
        <p class="text-xs text-gray-500">{{ t('vpn.nodeOffsetHint') }}</p>
        <label class="flex items-center gap-2 text-sm"><input v-model="serverForm.enabled" type="checkbox" />{{ t('vpn.enabled') }}</label>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="closeServer">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy">{{ t('vpn.save') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog :show="showCreate" :title="t('vpn.createForUser')" :show-close-button="!busy" :close-on-escape="!busy" @close="showCreate = false">
      <div class="space-y-4">
        <p class="text-sm text-gray-500">{{ t('vpn.createHint') }}</p>
        <p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p>
        <form class="flex items-end gap-2" @submit.prevent="searchUsers">
          <label class="min-w-0 flex-1 text-sm">{{ t('vpn.search') }}<input v-model="userQuery" class="input mt-1" /></label>
          <button class="btn btn-secondary" :disabled="searching || busy">{{ t('vpn.searchUsers') }}</button>
        </form>
        <p v-if="searching" role="status">{{ t('vpn.loading') }}</p>
        <label class="block text-sm">{{ t('vpn.selectUser') }}<select v-model="createUserId" class="input mt-1"><option :value="null" disabled>{{ t('vpn.selectUser') }}</option><option v-for="user in users" :key="user.id" :value="user.id">{{ user.email }} (#{{ user.id }})</option></select></label>
        <div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="showCreate = false">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !createUserId" @click="createSubscription">{{ t('vpn.createForUser') }}</button></div>
      </div>
    </BaseDialog>

    <BaseDialog :show="!!selected" :title="selected?.user_email || t('vpn.details')" width="wide" :show-close-button="!busy" :close-on-escape="!busy" @close="selected = null">
      <template v-if="selected">
        <p v-if="dialogError" role="alert" class="mb-4 text-sm text-red-600">{{ dialogError }}</p>
        <VpnSubscriptionDetails :subscription="selected" />
        <div class="mt-5 space-y-2 border-t border-gray-200 pt-4 dark:border-dark-700">
          <label class="block text-sm">{{ t('vpn.group') }}<select v-model.number="targetGroupId" class="input mt-1" :disabled="busy || vpnIsPending(selected) || deletionRequested(selected)"><option :value="null" disabled>{{ t('vpn.selectGroup') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} · {{ formatVpnBytes(group.quota_bytes) }}</option></select></label>
          <p class="text-xs text-gray-500">{{ t('vpn.switchGroupHint') }}</p>
          <button class="btn btn-secondary" :disabled="busy || !targetGroup || targetGroupId === selected.group_id || vpnIsPending(selected) || deletionRequested(selected)" @click="showSwitchGroup = true">{{ t('vpn.switchGroup') }}</button>
        </div>
        <div class="mt-5 flex flex-wrap gap-2 border-t border-gray-200 pt-4 dark:border-dark-700">
          <button class="btn btn-secondary" :disabled="busy" @click="runAction('refresh')">{{ t('vpn.refresh') }}</button>
          <button class="btn btn-secondary" :disabled="busy || vpnIsPending(selected) || deletionRequested(selected)" @click="openEdit">{{ t('vpn.edit') }}</button>
          <button class="btn btn-secondary" :disabled="busy || !canRetry" @click="runAction('retry')">{{ t('vpn.retry') }}</button>
          <button class="btn btn-danger" :disabled="busy || vpnIsPending(selected) || deletionRequested(selected)" @click="showRevoke = true">{{ t('vpn.revoke') }}</button>
          <button class="btn btn-danger" :disabled="busy || selected.status === 'deleted' || (deletionRequested(selected) && vpnIsPending(selected))" @click="showDelete = true">{{ t('vpn.deleteSubscription') }}</button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog :show="showEdit" :title="t('vpn.edit')" :z-index="60" :show-close-button="!busy" :close-on-escape="!busy" @close="showEdit = false">
      <form class="space-y-4" @submit.prevent="saveSubscription">
        <p v-if="editError" role="alert" class="text-sm text-red-600">{{ editError }}</p>
        <label class="block text-sm">{{ t('vpn.quotaGiB') }}<input v-model.number="quotaGiB" class="input mt-1" type="number" step="any" min="0.000000001" required /></label>
        <label class="flex items-center gap-2 text-sm"><input v-model="subscriptionEnabled" type="checkbox" />{{ t('vpn.enabled') }}</label>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="showEdit = false">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy">{{ t('vpn.save') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog :show="showRevoke" :title="t('vpn.revoke')" :z-index="60" :show-close-button="!busy" :close-on-escape="!busy" @close="showRevoke = false">
      <p>{{ t('vpn.revokeConfirm') }}</p>
      <p v-if="revokeError" role="alert" class="mt-3 text-sm text-red-600">{{ revokeError }}</p>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="showRevoke = false">{{ t('vpn.cancel') }}</button><button class="btn btn-danger" :disabled="busy" @click="runAction('revoke')">{{ t('vpn.confirm') }}</button></div></template>
    </BaseDialog>
    <BaseDialog :show="showDelete" :title="t('vpn.deleteSubscription')" :z-index="60" :show-close-button="!busy" :close-on-escape="!busy" @close="showDelete = false">
      <p>{{ t('vpn.deleteConfirm') }}</p>
      <p v-if="deleteError" role="alert" class="mt-3 text-sm text-red-600">{{ deleteError }}</p>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="showDelete = false">{{ t('vpn.cancel') }}</button><button class="btn btn-danger" :disabled="busy" @click="deleteSubscription">{{ t('vpn.confirmDelete') }}</button></div></template>
    </BaseDialog>
    <BaseDialog :show="showGroup" :title="t(editingGroup ? 'vpn.editGroup' : 'vpn.addGroup')" :show-close-button="!busy" :close-on-escape="!busy" @close="closeGroup">
      <form class="space-y-4" @submit.prevent="prepareGroupSave">
        <p v-if="groupError" role="alert" class="text-sm text-red-600">{{ groupError }}</p>
        <label class="block text-sm">{{ t('vpn.groupName') }}<input v-model="groupName" class="input mt-1" required maxlength="100" /></label>
        <label class="block text-sm">{{ t('vpn.quotaGiB') }}<input v-model.number="groupQuotaGiB" class="input mt-1" type="number" min="0.000000001" step="any" required /></label>
        <p class="text-sm text-gray-500">{{ t('vpn.groupQuotaHint') }}</p>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="closeGroup">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy">{{ t('vpn.save') }}</button></div>
      </form>
    </BaseDialog>
    <BaseDialog :show="showGroupConfirm" :title="t('vpn.confirmGroupQuota')" :z-index="60" :show-close-button="!busy" :close-on-escape="!busy" @close="showGroupConfirm = false">
      <p>{{ t('vpn.groupQuotaConfirm', { name: editingGroup?.name, count: editingGroup?.subscription_count || 0, quota: formatVpnBytes(pendingGroupInput?.quota_bytes || 0) }) }}</p>
      <p v-if="groupError" role="alert" class="mt-3 text-sm text-red-600">{{ groupError }}</p>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="showGroupConfirm = false">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy" @click="saveGroup">{{ t('vpn.confirmGroupQuota') }}</button></div></template>
    </BaseDialog>
    <BaseDialog :show="showSwitchGroup" :title="t('vpn.switchGroup')" :z-index="60" :show-close-button="!busy" :close-on-escape="!busy" @close="showSwitchGroup = false">
      <p>{{ t('vpn.switchGroupConfirm', { name: targetGroup?.name, quota: formatVpnBytes(targetGroup?.quota_bytes || 0) }) }}</p>
      <p v-if="switchGroupError" role="alert" class="mt-3 text-sm text-red-600">{{ switchGroupError }}</p>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="showSwitchGroup = false">{{ t('vpn.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !targetGroup || !selected || deletionRequested(selected) || vpnIsPending(selected)" @click="switchGroup">{{ t('vpn.confirmSwitchGroup') }}</button></div></template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import VpnSubscriptionDetails from '@/components/vpn/VpnSubscriptionDetails.vue'
import VpnTrafficChart from '@/components/vpn/VpnTrafficChart.vue'
import { adminVpnAPI, type VpnGroup, type VpnServer, type VpnServerInput, type VpnSubscription, type VpnSummary } from '@/api/vpn'
import { list as listUsers } from '@/api/admin/users'
import { formatVpnBytes, formatVpnTime, GIB, gibToBytes, vpnError, vpnIsPending } from '@/utils/vpn'
import { useAppStore } from '@/stores/app'

const { t, te, locale } = useI18n()
const app = useAppStore()
const servers = ref<VpnServer[]>([])
const groups = ref<VpnGroup[]>([])
const showGroup = ref(false)
const showGroupConfirm = ref(false)
const editingGroup = ref<VpnGroup | null>(null)
const groupName = ref('')
const groupQuotaGiB = ref(0)
const groupError = ref('')
const pendingGroupInput = ref<{ name: string; quota_bytes?: number } | null>(null)
const targetGroupId = ref<number | null>(null)
const targetGroup = computed(() => groups.value.find(group => group.id === targetGroupId.value))
const showSwitchGroup = ref(false)
const switchGroupError = ref('')
const summary = ref<VpnSummary | null>(null)
const items = ref<VpnSubscription[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const query = ref('')
const serverFilter = ref<number | ''>('')
const statusFilter = ref('')
const statuses = ['active', 'disabled', 'limited', 'expired', 'provisioning', 'failed', 'deleting', 'deleted']
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const dialogError = ref('')
const editError = ref('')
const revokeError = ref('')
const deleteError = ref('')
const selected = ref<VpnSubscription | null>(null)
const showServer = ref(false)
const showCreate = ref(false)
const showEdit = ref(false)
const showRevoke = ref(false)
const showDelete = ref(false)
const serverQuotaGiB = ref(0)
const serverOffsetGiB = ref(0)
let initialServerQuota = 0
let initialServerOffset = 0
const serverId = ref<number>()
const serverHasBindings = ref(false)
const blankServer = (): VpnServerInput => ({ name: '', base_url: '', admin_username: '', admin_password: '', ca_pem: '', enabled: true })
const serverForm = reactive(blankServer())
const userQuery = ref('')
const users = ref<Array<{ id: number; email: string }>>([])
const createUserId = ref<number | null>(null)
const searching = ref(false)
const quotaGiB = ref(0)
const subscriptionEnabled = ref(true)
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
const state = (value: string) => te(`vpn.states.${value}`) ? t(`vpn.states.${value}`) : value || '—'
const time = (value: string | null) => formatVpnTime(value, locale.value)
const trafficBytes = (value?: number | null) => value == null ? '—' : formatVpnBytes(value)
const trafficCurrent = (server: VpnServer) => ['ok', 'partial_history'].includes(server.traffic_accounting_status || '') && server.traffic_used_bytes != null && !!server.traffic_sampled_at && !!server.traffic_period_end && new Date(server.traffic_period_end).getTime() > Date.now()
const deletionRequested = (subscription: VpnSubscription) => !!subscription.delete_requested_at || ['deleting', 'deleted'].includes(subscription.status)
const canRetry = computed(() => !!selected.value && (['failed', 'pending'].includes(selected.value.operation_status) || selected.value.apply_status === 'failed'))
const summaryFields = computed(() => {
  const s = summary.value
  return s ? [['total', s.total], ['states.active', s.active], ['states.disabled', s.disabled], ['states.limited', s.limited], ['states.pending', s.pending], ['states.failed', s.failed], ['used', formatVpnBytes(s.used_bytes)], ['quota', formatVpnBytes(s.quota_bytes)], ['healthyServers', `${s.healthy_servers} / ${s.total_servers}`]] : []
})

function schedule() {
  clearTimeout(timer)
  if (!disposed && (groups.value.some(group => group.pending_count > 0) || items.value.some(vpnIsPending) || (selected.value && vpnIsPending(selected.value)))) {
    timer = setTimeout(() => { if (!busy.value) void load(); else schedule() }, 5000)
  }
}
async function load() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const [nodes, totals, subscriptions, vpnGroups] = await Promise.all([
      adminVpnAPI.servers(), adminVpnAPI.summary(),
      adminVpnAPI.subscriptions({ page: page.value, page_size: pageSize, q: query.value || undefined, server_id: serverFilter.value || undefined, status: statusFilter.value || undefined }),
      adminVpnAPI.groups()
    ])
    servers.value = nodes
    groups.value = vpnGroups
    summary.value = totals
    items.value = subscriptions.items
    total.value = subscriptions.total
    if (selected.value) {
      const current = selected.value
      const id = current.id
      const visible = items.value.find(item => item.id === id)
      if (visible) selected.value = visible
      else {
        // 生效后的状态可能不再匹配筛选；详情仍按固定远端用户名跟进原订阅。
        const detail = await adminVpnAPI.subscriptions({ page: 1, page_size: 20, q: current.remote_username })
        let updated = detail.items.find(item => item.id === id)
        if (!updated && deletionRequested(current)) {
          const archived = await adminVpnAPI.subscriptions({ page: 1, page_size: 20, q: current.remote_username, status: 'deleted' })
          updated = archived.items.find(item => item.id === id)
        }
        if (selected.value?.id === id) {
          if (updated?.status === 'deleted' && statusFilter.value !== 'deleted') {
            selected.value = null
            showDelete.value = false
            showEdit.value = false
            showRevoke.value = false
            showSwitchGroup.value = false
            app.showSuccess(t('vpn.deletedSuccess'))
          } else selected.value = updated || selected.value
        }
      }
    }
  } catch (e) { error.value = vpnError(e, t('vpn.loadFailed')) }
  finally { loading.value = false; schedule() }
}
function filter() { page.value = 1; void load() }
function changePage(delta: number) { page.value += delta; void load() }
function openGroup(group?: VpnGroup) {
  editingGroup.value = group ? { ...group } : null
  groupName.value = group?.name || ''
  groupQuotaGiB.value = (group?.quota_bytes || groups.value.find(item => item.is_default)?.quota_bytes || 0) / GIB
  groupError.value = ''
  pendingGroupInput.value = null
  showGroupConfirm.value = false
  showGroup.value = true
}
function closeGroup() {
  if (busy.value) return
  showGroup.value = false
  showGroupConfirm.value = false
  pendingGroupInput.value = null
}
function prepareGroupSave() {
  if (busy.value) return
  const bytes = gibToBytes(groupQuotaGiB.value)
  groupError.value = ''
  if (!groupName.value.trim()) { groupError.value = t('vpn.invalidGroupName'); return }
  if (bytes === null) { groupError.value = t('vpn.invalidQuota'); return }
  const quotaChanged = !editingGroup.value || bytes !== editingGroup.value.quota_bytes
  pendingGroupInput.value = { name: groupName.value.trim(), ...(quotaChanged ? { quota_bytes: bytes } : {}) }
  if (editingGroup.value && quotaChanged) showGroupConfirm.value = true
  else void saveGroup()
}
async function saveGroup() {
  if (busy.value || !pendingGroupInput.value) return
  busy.value = true
  groupError.value = ''
  try {
    const input = pendingGroupInput.value
    if (editingGroup.value) await adminVpnAPI.updateGroup(editingGroup.value.id, input)
    else await adminVpnAPI.createGroup({ name: input.name, quota_bytes: input.quota_bytes! })
    showGroupConfirm.value = false
    showGroup.value = false
    app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) { groupError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
async function switchGroup() {
  if (busy.value || !selected.value || !targetGroup.value || deletionRequested(selected.value) || vpnIsPending(selected.value)) return
  busy.value = true
  switchGroupError.value = ''
  try {
    const result = await adminVpnAPI.setUserGroup(selected.value.user_id, targetGroup.value.id)
    updateSelected({ ...selected.value, group_id: result.group_id, group_name: result.group_name, group_quota_bytes: result.quota_bytes })
    showSwitchGroup.value = false
    app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) { switchGroupError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
function openServer(server?: VpnServer) {
  serverQuotaGiB.value = (server?.traffic_quota_bytes || 0) / GIB
  serverOffsetGiB.value = server?.traffic_offset_period_start && server.traffic_offset_period_start === server.traffic_period_start ? (server.traffic_used_offset_bytes || 0) / GIB : 0
  initialServerQuota = serverQuotaGiB.value
  initialServerOffset = serverOffsetGiB.value
  serverId.value = server?.id
  serverHasBindings.value = !!server && (server.assigned_count > 0 || server.pending_count > 0)
  Object.assign(serverForm, blankServer(), server ? { name: server.name, base_url: server.base_url, admin_username: server.admin_username, enabled: server.enabled } : {})
  dialogError.value = ''
  showServer.value = true
}
function closeServer() { if (!busy.value) { showServer.value = false; Object.assign(serverForm, blankServer()) } }
async function saveServer() {
  if (busy.value) return
  try {
    const url = new URL(serverForm.base_url)
    if (url.protocol !== 'https:' || url.username || url.password) throw new Error()
  } catch { dialogError.value = t('vpn.invalidHttps'); return }
  const quota = serverQuotaGiB.value === 0 ? 0 : gibToBytes(serverQuotaGiB.value)
  const offset = serverOffsetGiB.value === 0 ? 0 : gibToBytes(serverOffsetGiB.value)
  if (quota === null || offset === null) { dialogError.value = t('vpn.invalidNodeTraffic'); return }
  busy.value = true
  dialogError.value = ''
  try {
    const input = { ...serverForm }
    if (!serverId.value || serverQuotaGiB.value !== initialServerQuota) input.traffic_quota_bytes = quota
    if (!serverId.value || serverOffsetGiB.value !== initialServerOffset) input.traffic_used_offset_bytes = offset
    if (!input.ca_pem?.trim()) delete input.ca_pem
    await adminVpnAPI.saveServer(input, serverId.value)
    showServer.value = false
    Object.assign(serverForm, blankServer())
    await load()
  } catch (e) { dialogError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
async function probe(id: number) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try { await adminVpnAPI.probe(id); await load() }
  catch (e) { error.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
function openCreate() {
  dialogError.value = ''
  createUserId.value = null
  userQuery.value = ''
  users.value = []
  showCreate.value = true
  void searchUsers()
}
async function searchUsers() {
  if (searching.value) return
  searching.value = true
  dialogError.value = ''
  try {
    const result = await listUsers(1, 50, { search: userQuery.value, status: 'active' })
    users.value = result.items
    createUserId.value = null
  } catch (e) { dialogError.value = vpnError(e, t('vpn.loadFailed')) }
  finally { searching.value = false }
}
function updateSelected(subscription: VpnSubscription) {
  selected.value = subscription
  items.value = items.value.map(item => item.id === subscription.id ? subscription : item)
  schedule()
}
async function createSubscription() {
  if (busy.value || !createUserId.value) return
  busy.value = true
  dialogError.value = ''
  try {
    const subscription = await adminVpnAPI.create(createUserId.value)
    showCreate.value = false
    page.value = 1
    query.value = ''
    serverFilter.value = ''
    statusFilter.value = ''
    updateSelected(subscription)
    app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) { dialogError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
function openEdit() {
  if (!selected.value || deletionRequested(selected.value)) return
  quotaGiB.value = selected.value.quota_bytes / GIB
  subscriptionEnabled.value = selected.value.status !== 'disabled'
  editError.value = ''
  showEdit.value = true
}
async function saveSubscription() {
  if (busy.value || !selected.value || deletionRequested(selected.value)) return
  const bytes = gibToBytes(quotaGiB.value)
  if (bytes === null) { editError.value = t('vpn.invalidQuota'); return }
  busy.value = true
  editError.value = ''
  try {
    updateSelected(await adminVpnAPI.update(selected.value.id, { quota_bytes: bytes, enabled: subscriptionEnabled.value }))
    showEdit.value = false
    app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) { editError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
async function runAction(action: 'refresh' | 'retry' | 'revoke') {
  if (busy.value || !selected.value) return
  if (action === 'revoke' && deletionRequested(selected.value)) return
  busy.value = true
  dialogError.value = ''
  revokeError.value = ''
  try {
    updateSelected(await adminVpnAPI.action(selected.value.id, action))
    showRevoke.value = false
    if (action !== 'refresh') app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) {
    const message = vpnError(e, t('vpn.actionFailed'))
    if (action === 'revoke') revokeError.value = message
    else dialogError.value = message
  } finally { busy.value = false }
}
async function deleteSubscription() {
  if (busy.value || !selected.value) return
  busy.value = true
  deleteError.value = ''
  try {
    updateSelected(await adminVpnAPI.delete(selected.value.id))
    showDelete.value = false
    app.showSuccess(t('vpn.accepted'))
    await load()
  } catch (e) { deleteError.value = vpnError(e, t('vpn.actionFailed')) }
  finally { busy.value = false }
}
watch(() => selected.value?.id, () => { dialogError.value = '' })
watch([() => selected.value?.id, () => selected.value?.group_id], () => { targetGroupId.value = selected.value?.group_id || null })
watch(showSwitchGroup, () => { switchGroupError.value = '' })
watch(showRevoke, () => { revokeError.value = '' })
watch(showDelete, () => { deleteError.value = '' })
onMounted(() => { void load() })
onUnmounted(() => { disposed = true; clearTimeout(timer) })
</script>
