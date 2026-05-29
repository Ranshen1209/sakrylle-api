<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex justify-end gap-3">
          <button
            type="button"
            @click="loadApps"
            :disabled="loading"
            class="btn btn-secondary"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="apps" :loading="loading">
          <template #cell-app="{ row }">
            <div class="flex items-center gap-3">
              <div
                class="flex h-9 w-9 flex-shrink-0 items-center justify-center overflow-hidden rounded-lg bg-gray-100 dark:bg-dark-700"
              >
                <img
                  v-if="row.icon_url"
                  :src="row.icon_url"
                  :alt="row.client_name || row.client_id"
                  class="h-full w-full object-contain"
                  loading="lazy"
                />
                <span
                  v-else
                  class="text-sm font-semibold text-gray-500 dark:text-gray-300"
                  aria-hidden="true"
                >
                  {{ initials(row.client_name || row.client_id) }}
                </span>
              </div>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
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
                  <span
                    v-if="appTypeLabel(row.app_type)"
                    class="px-2 py-0.5 rounded text-xs bg-primary-50 dark:bg-primary-900/30 text-primary-700 dark:text-primary-300"
                  >
                    {{ appTypeLabel(row.app_type) }}
                  </span>
                </div>
                <div class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                  {{ row.client_id }}
                </div>
              </div>
            </div>
          </template>

          <template #cell-device="{ row }">
            <div class="text-sm text-gray-700 dark:text-gray-200">
              {{ row.device_name || t('authorizedApps.deviceUnknown') }}
            </div>
            <div
              v-if="row.last_used_ip"
              class="text-xs text-gray-500 dark:text-gray-400 mt-0.5 font-mono"
            >
              {{ row.last_used_ip }}
            </div>
          </template>

          <template #cell-group="{ row }">
            <span class="text-sm text-gray-700 dark:text-gray-200">
              {{ row.group_name || `#${row.group_id}` }}
            </span>
          </template>

          <template #cell-scopes="{ row }">
            <div class="flex flex-wrap gap-1">
              <span
                v-for="scope in row.scopes"
                :key="scope"
                class="inline-flex items-center px-2 py-0.5 rounded text-xs bg-primary-50 dark:bg-primary-900/30 text-primary-700 dark:text-primary-300"
                :title="scopeTooltip(scope)"
              >
                {{ scopeLabel(scope) }}
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
              <template v-else>{{ t('authorizedApps.lastUsedNever') }}</template>
            </span>
          </template>

          <template #cell-tokens="{ row }">
            <span
              class="text-xs text-gray-600 dark:text-gray-300"
              :title="t('authorizedApps.tokenCount', {
                access: row.active_access_token_count,
                refresh: row.active_refresh_token_count
              })"
            >
              {{ t('authorizedApps.tokenCount', {
                access: row.active_access_token_count,
                refresh: row.active_refresh_token_count
              }) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex flex-col items-end gap-1">
              <button
                type="button"
                :disabled="revokingGrantId === row.grant_id"
                @click="confirmRevokeDevice(row)"
                class="text-sm text-red-600 dark:text-red-400 hover:text-red-700 dark:hover:text-red-300 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500 focus-visible:ring-offset-1 dark:focus-visible:ring-offset-dark-800 rounded"
              >
                {{ t('authorizedApps.revokeDevice') }}
              </button>
              <button
                type="button"
                :disabled="revokingClientId === row.client_id"
                @click="confirmRevokeAll(row)"
                class="text-xs text-gray-500 dark:text-gray-400 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500 focus-visible:ring-offset-1 dark:focus-visible:ring-offset-dark-800 rounded"
              >
                {{ t('authorizedApps.revokeAllForClient') }}
              </button>
            </div>
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
      :show="dialogMode === 'device'"
      :title="t('authorizedApps.revokeDeviceConfirmTitle')"
      :message="t('authorizedApps.revokeDeviceConfirmMessage', {
        name: pendingApp?.client_name || pendingApp?.client_id || '',
        device: pendingApp?.device_name || t('authorizedApps.deviceUnknown')
      })"
      :confirm-text="t('authorizedApps.revokeDevice')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="handleRevokeDevice"
      @cancel="closeDialog"
    />

    <ConfirmDialog
      :show="dialogMode === 'all'"
      :title="t('authorizedApps.revokeAllConfirmTitle', {
        name: pendingApp?.client_name || pendingApp?.client_id || ''
      })"
      :message="t('authorizedApps.revokeAllConfirmMessage', {
        name: pendingApp?.client_name || pendingApp?.client_id || '',
        count: clientSessionCount(pendingApp)
      })"
      :confirm-text="t('authorizedApps.revokeAllForClient')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="handleRevokeAll"
      @cancel="closeDialog"
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
import { describeScope } from '@/utils/scopes'
import type { AuthorizedAppGrant } from '@/types'
import type { Column } from '@/components/common/types'

type DialogMode = 'none' | 'device' | 'all'

const APP_TYPE_KEYS: Record<string, string> = {
  web: 'authorizedApps.appType.web',
  cli: 'authorizedApps.appType.cli',
  mobile: 'authorizedApps.appType.mobile',
  desktop: 'authorizedApps.appType.desktop',
  chat: 'authorizedApps.appType.chat',
  image: 'authorizedApps.appType.image'
}

const { t } = useI18n()
const appStore = useAppStore()

const apps = ref<AuthorizedAppGrant[]>([])
const loading = ref(false)
const dialogMode = ref<DialogMode>('none')
const pendingApp = ref<AuthorizedAppGrant | null>(null)
const revokingGrantId = ref<string | null>(null)
const revokingClientId = ref<string | null>(null)

const columns = computed<Column[]>(() => [
  { key: 'app', label: t('authorizedApps.columns.app'), sortable: false },
  { key: 'device', label: t('authorizedApps.columns.device'), sortable: false },
  { key: 'group', label: t('authorizedApps.columns.group'), sortable: false },
  { key: 'scopes', label: t('authorizedApps.columns.scopes'), sortable: false },
  { key: 'first_authorized_at', label: t('authorizedApps.columns.firstAuthorized'), sortable: false },
  { key: 'last_used_at', label: t('authorizedApps.columns.lastUsed'), sortable: false },
  { key: 'tokens', label: t('authorizedApps.columns.tokens'), sortable: false },
  { key: 'actions', label: t('common.actions'), sortable: false }
])

const initials = (label: string): string => {
  const trimmed = label.trim()
  if (!trimmed) return '?'
  const parts = trimmed.split(/[\s_\-./:]+/).filter(Boolean)
  if (parts.length === 0) return trimmed.slice(0, 2).toUpperCase()
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

const appTypeLabel = (appType: string): string => {
  if (!appType) return ''
  const key = APP_TYPE_KEYS[appType]
  return key ? t(key) : appType
}

const scopeLabel = (scope: string): string => describeScope(scope, t).name

const scopeTooltip = (scope: string): string => {
  const { description } = describeScope(scope, t)
  return `${scope} — ${description}`
}

const clientSessionCount = (app: AuthorizedAppGrant | null): number => {
  if (!app) return 0
  return apps.value.filter((row) => row.client_id === app.client_id).length
}

const loadApps = async (): Promise<void> => {
  loading.value = true
  try {
    apps.value = await oauthGrantsAPI.listAuthorizedApps()
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : t('authorizedApps.loadFailed')
    appStore.showError(message)
  } finally {
    loading.value = false
  }
}

const closeDialog = (): void => {
  dialogMode.value = 'none'
  pendingApp.value = null
}

const confirmRevokeDevice = (app: AuthorizedAppGrant): void => {
  pendingApp.value = app
  dialogMode.value = 'device'
}

const confirmRevokeAll = (app: AuthorizedAppGrant): void => {
  pendingApp.value = app
  dialogMode.value = 'all'
}

const handleRevokeDevice = async (): Promise<void> => {
  const target = pendingApp.value
  if (!target) return
  revokingGrantId.value = target.grant_id
  try {
    await oauthGrantsAPI.revokeAuthorizedApp(target.grant_id)
    apps.value = apps.value.filter((row) => row.grant_id !== target.grant_id)
    appStore.showSuccess(t('authorizedApps.revokedOne'))
  } catch (error: unknown) {
    // Check for step-up auth required (403 with error: "step_up_required")
    const apiError = error as { status?: number; error?: string; message?: string }
    if (apiError.status === 403 && apiError.error === 'step_up_required') {
      appStore.showError(t('authorizedApps.stepUpRequired'))
      // Redirect to login after a short delay
      setTimeout(() => {
        window.location.href = '/auth/login?next=' + encodeURIComponent(window.location.pathname)
      }, 2000)
    } else {
      const message = error instanceof Error ? error.message : t('authorizedApps.revokeFailed')
      appStore.showError(message)
    }
  } finally {
    revokingGrantId.value = null
    closeDialog()
  }
}

const handleRevokeAll = async (): Promise<void> => {
  const target = pendingApp.value
  if (!target) return
  revokingClientId.value = target.client_id
  try {
    const { revoked } = await oauthGrantsAPI.revokeAuthorizedAppsForClient(target.client_id)
    apps.value = apps.value.filter((row) => row.client_id !== target.client_id)
    appStore.showSuccess(t('authorizedApps.revoked', { count: revoked }))
  } catch (error: unknown) {
    // Check for step-up auth required (403 with error: "step_up_required")
    const apiError = error as { status?: number; error?: string; message?: string }
    if (apiError.status === 403 && apiError.error === 'step_up_required') {
      appStore.showError(t('authorizedApps.stepUpRequired'))
      // Redirect to login after a short delay
      setTimeout(() => {
        window.location.href = '/auth/login?next=' + encodeURIComponent(window.location.pathname)
      }, 2000)
    } else {
      const message = error instanceof Error ? error.message : t('authorizedApps.revokeFailed')
      appStore.showError(message)
    }
  } finally {
    revokingClientId.value = null
    closeDialog()
  }
}

onMounted(() => {
  loadApps()
})
</script>
