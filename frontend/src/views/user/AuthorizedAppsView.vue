<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex justify-end gap-3">
          <button
            @click="loadApps"
            :disabled="loading"
            class="btn btn-secondary"
            :title="t('common.refresh')"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="apps" :loading="loading">
          <template #cell-app="{ row }">
            <div class="flex items-center gap-2">
              <span
                class="font-medium"
                :class="row.client_disabled
                  ? 'text-gray-400 dark:text-gray-500 line-through'
                  : 'text-gray-900 dark:text-white'"
              >
                {{ row.client_name || row.client_id }}
              </span>
              <span
                v-if="row.client_disabled"
                class="px-2 py-0.5 rounded text-xs bg-gray-100 dark:bg-dark-700 text-gray-500"
                :title="t('authorizedApps.disabledTooltip')"
              >
                {{ t('authorizedApps.disabledBadge') }}
              </span>
            </div>
            <div class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
              {{ row.client_id }}
            </div>
          </template>

          <template #cell-scopes="{ row }">
            <div class="flex flex-wrap gap-1">
              <span
                v-for="scope in row.scopes"
                :key="scope"
                class="inline-flex items-center px-2 py-0.5 rounded text-xs bg-primary-50 dark:bg-primary-900/30 text-primary-700 dark:text-primary-300"
              >
                {{ scope }}
              </span>
              <span v-if="row.scopes.length === 0" class="text-xs text-gray-400">
                —
              </span>
            </div>
          </template>

          <template #cell-first_authorized_at="{ row }">
            <span class="text-sm text-gray-600 dark:text-gray-300">
              {{ formatRelativeWithDateTime(row.first_authorized_at) }}
            </span>
          </template>

          <template #cell-last_used_at="{ row }">
            <span class="text-sm text-gray-600 dark:text-gray-300">
              <template v-if="row.last_used_at">
                {{ formatRelativeWithDateTime(row.last_used_at) }}
              </template>
              <template v-else>—</template>
            </span>
          </template>

          <template #cell-active_token_count="{ row }">
            <span
              class="inline-flex items-center justify-center min-w-[1.5rem] h-6 px-2 rounded-full text-xs font-medium bg-gray-100 dark:bg-dark-700 text-gray-700 dark:text-gray-200"
            >
              {{ row.active_token_count }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <button
              @click="confirmRevoke(row)"
              class="text-sm text-red-600 dark:text-red-400 hover:text-red-700 dark:hover:text-red-300"
            >
              {{ t('authorizedApps.revoke') }}
            </button>
          </template>

          <template #empty>
            <EmptyState
              :title="t('authorizedApps.emptyTitle')"
              :description="t('authorizedApps.empty')"
            />
          </template>
        </DataTable>
      </template>
    </TablePageLayout>

    <ConfirmDialog
      :show="showRevokeDialog"
      :title="t('authorizedApps.revokeConfirmTitle')"
      :message="t('authorizedApps.revokeConfirmMessage', { name: selectedApp?.client_name || selectedApp?.client_id })"
      :confirm-text="t('authorizedApps.revoke')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="handleRevoke"
      @cancel="showRevokeDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import oauthGrantsAPI from '@/api/oauthGrants'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatRelativeWithDateTime } from '@/utils/format'
import type { AuthorizedApp } from '@/types'
import type { Column } from '@/components/common/types'

const { t } = useI18n()
const appStore = useAppStore()

const apps = ref<AuthorizedApp[]>([])
const loading = ref(false)
const showRevokeDialog = ref(false)
const selectedApp = ref<AuthorizedApp | null>(null)

const columns = computed<Column[]>(() => [
  { key: 'app', label: t('authorizedApps.columns.app'), sortable: false },
  { key: 'scopes', label: t('authorizedApps.columns.scopes'), sortable: false },
  { key: 'first_authorized_at', label: t('authorizedApps.columns.firstAuthorized'), sortable: false },
  { key: 'last_used_at', label: t('authorizedApps.columns.lastUsed'), sortable: false },
  { key: 'active_token_count', label: t('authorizedApps.columns.sessions'), sortable: false },
  { key: 'actions', label: t('common.actions'), sortable: false }
])

const loadApps = async () => {
  loading.value = true
  try {
    apps.value = await oauthGrantsAPI.list()
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : t('authorizedApps.loadFailed')
    appStore.showError(message)
  } finally {
    loading.value = false
  }
}

const confirmRevoke = (app: AuthorizedApp) => {
  selectedApp.value = app
  showRevokeDialog.value = true
}

const handleRevoke = async () => {
  if (!selectedApp.value) return
  try {
    const { revoked } = await oauthGrantsAPI.revoke(selectedApp.value.client_id)
    appStore.showSuccess(t('authorizedApps.revoked', { count: revoked }))
    showRevokeDialog.value = false
    selectedApp.value = null
    await loadApps()
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : t('authorizedApps.revokeFailed')
    appStore.showError(message)
  }
}

onMounted(() => {
  loadApps()
})
</script>
