<template>
  <AppLayout>
    <!-- Hero banner -->
    <section
      class="relative mb-6 overflow-hidden rounded-2xl border border-primary-200/60 bg-gradient-to-br from-primary-500 via-primary-600 to-primary-700 p-6 text-white shadow-md dark:border-primary-700/60"
    >
      <div class="pointer-events-none absolute inset-0 opacity-30">
        <div class="absolute -right-20 -top-20 h-60 w-60 rounded-full bg-white/30 blur-3xl" />
        <div class="absolute -bottom-32 -left-10 h-72 w-72 rounded-full bg-white/20 blur-3xl" />
      </div>
      <div class="relative flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 class="flex items-center gap-3 text-2xl font-bold">
            {{ t('plaza.title') }}
            <span class="rounded-md bg-white/20 px-2 py-0.5 text-xs font-medium uppercase tracking-wide">
              {{ t('plaza.modelCount', { count: filteredModels.length }) }}
            </span>
          </h1>
          <p class="mt-1.5 text-sm text-primary-100">{{ t('plaza.description') }}</p>
        </div>
      </div>
    </section>

    <div class="grid grid-cols-1 gap-6 lg:grid-cols-[260px_minmax(0,1fr)]">
      <PlazaSidebar
        :models="plazaModels"
        :platform="filterPlatform"
        :group="filterGroup"
        :billing="filterBilling"
        @update:platform="filterPlatform = $event"
        @update:group="filterGroup = $event"
        @update:billing="filterBilling = $event"
      />

      <div class="flex min-w-0 flex-col gap-4">
        <!-- Toolbar -->
        <div
          class="flex flex-col gap-3 rounded-2xl border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800 sm:flex-row sm:items-center sm:gap-4"
        >
          <div class="relative flex-1">
            <Icon
              name="search"
              size="md"
              class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
            />
            <input
              v-model="searchQuery"
              type="text"
              :placeholder="t('plaza.searchPlaceholder')"
              class="input pl-10"
            />
          </div>

          <label
            class="flex flex-shrink-0 cursor-pointer items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-100 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-200 dark:hover:bg-dark-700"
          >
            <input v-model="showOriginal" type="checkbox" class="h-3.5 w-3.5 accent-primary-500" />
            {{ t('plaza.showOriginal') }}
          </label>

          <div
            v-if="hasLongContextPricing"
            class="inline-flex h-9 flex-shrink-0 items-center rounded-lg border border-gray-200 bg-gray-50 p-0.5 text-xs font-medium dark:border-dark-700 dark:bg-dark-900"
            role="group"
            :aria-label="t('plaza.longContext')"
          >
            <button
              type="button"
              class="h-8 rounded-md px-3 transition-colors"
              :class="pricingMode === 'standard' ? pricingModeActiveClass : pricingModeInactiveClass"
              :aria-pressed="pricingMode === 'standard'"
              @click="pricingMode = 'standard'"
            >
              {{ t('plaza.standard') }}
            </button>
            <button
              type="button"
              class="h-8 rounded-md px-3 transition-colors"
              :class="pricingMode === 'long_context' ? pricingModeActiveClass : pricingModeInactiveClass"
              :aria-pressed="pricingMode === 'long_context'"
              @click="pricingMode = 'long_context'"
            >
              {{ t('plaza.longContext') }}
            </button>
          </div>

          <div
            v-if="hasTimePricing"
            class="inline-flex min-h-9 flex-shrink-0 items-center gap-0.5 rounded-md border border-gray-200 bg-gray-50 p-0.5 text-xs font-medium dark:border-dark-600 dark:bg-dark-900"
            role="group"
            :aria-label="t('modelPlaza.table.timePricingMode')"
          >
            <Icon name="clock" size="xs" class="mx-1.5 h-3.5 w-3.5 text-gray-500 dark:text-dark-400" />
            <button
              v-for="mode in timePricingModes"
              :key="mode.value"
              type="button"
              class="min-h-8 rounded px-2.5 transition-colors"
              :class="timePricingMode === mode.value
                ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
                : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100'"
              :aria-pressed="timePricingMode === mode.value"
              @click="timePricingMode = mode.value"
            >
              {{ t(mode.label) }}
            </button>
          </div>

          <button
            type="button"
            class="flex flex-shrink-0 items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-3 py-2 text-xs font-medium text-gray-700 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-200 dark:hover:bg-dark-700"
            :disabled="loading"
            @click="loadAll"
          >
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh', 'Refresh') }}
          </button>
        </div>

        <!-- Loading -->
        <div
          v-if="loading && plazaModels.length === 0"
          class="flex flex-col items-center justify-center gap-3 rounded-2xl border border-gray-200 bg-white py-20 dark:border-dark-700 dark:bg-dark-800"
        >
          <Icon name="refresh" size="xl" class="animate-spin text-primary-500" />
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading', 'Loading...') }}</p>
        </div>

        <!-- Empty -->
        <div
          v-else-if="filteredModels.length === 0"
          class="flex flex-col items-center justify-center gap-3 rounded-2xl border border-gray-200 bg-white py-20 dark:border-dark-700 dark:bg-dark-800"
        >
          <Icon name="inbox" size="xl" class="text-gray-300" />
          <p class="text-sm text-gray-500 dark:text-gray-400">
            {{ plazaModels.length === 0 ? t('plaza.empty') : t('plaza.noMatch') }}
          </p>
        </div>

        <!-- Cards grid -->
        <div v-else class="plaza-model-grid grid gap-4">
          <PlazaModelCard
            v-for="model in filteredModels"
            :key="model.id"
            :model="model"
            :show-original="showOriginal"
            :long-context="pricingMode === 'long_context'"
            :time-pricing-mode="timePricingMode"
          />
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import PlazaSidebar from '@/components/plaza/PlazaSidebar.vue'
import PlazaModelCard from '@/components/plaza/PlazaModelCard.vue'
import userChannelsAPI, { type UserAvailableChannel } from '@/api/channels'
import userGroupsAPI from '@/api/groups'
import type { ModelPlazaPricingMode } from '@/api/modelPlaza'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { flattenChannelsToPlaza, type PlazaModel } from '@/utils/modelPlaza'
import { BILLING_MODE_TOKEN } from '@/constants/channel'

const { t } = useI18n()
const appStore = useAppStore()

// Raw API state
const channels = ref<UserAvailableChannel[]>([])
const userGroupRates = ref<Record<number, number>>({})
const loading = ref(false)

// UI state
const searchQuery = ref('')
const filterPlatform = ref('')
const filterGroup = ref('')
const filterBilling = ref('')
const showOriginal = ref(true)
const pricingMode = ref<'standard' | 'long_context'>('standard')
const timePricingMode = ref<ModelPlazaPricingMode>('current')
const timePricingModes = [
  { value: 'current' as const, label: 'modelPlaza.table.timePricingModeCurrent' },
  { value: 'peak' as const, label: 'modelPlaza.table.timePricingModePeak' },
  { value: 'off_peak' as const, label: 'modelPlaza.table.timePricingModeOffPeak' }
]
const pricingModeActiveClass = 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
const pricingModeInactiveClass = 'text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-100'

// Derived
const plazaModels = computed<PlazaModel[]>(() =>
  flattenChannelsToPlaza(channels.value, userGroupRates.value),
)

const filteredModels = computed<PlazaModel[]>(() => {
  const q = searchQuery.value.trim().toLowerCase()
  return plazaModels.value.filter((m) => {
    if (filterPlatform.value && m.platform !== filterPlatform.value) return false
    if (filterGroup.value && m.group.name !== filterGroup.value) return false
    if (filterBilling.value) {
      const mode = m.pricing?.billing_mode ?? BILLING_MODE_TOKEN
      if (mode !== filterBilling.value) return false
    }
    if (q) {
      const hay = `${m.name} ${m.platform} ${m.channels.join(' ')}`.toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  })
})

const hasLongContextPricing = computed(() =>
  plazaModels.value.some((model) => (model.pricing?.intervals?.length ?? 0) > 1),
)
const hasTimePricing = computed(() =>
  filteredModels.value.some((model) => model.pricing?.time_versions?.length),
)

async function loadAll() {
  loading.value = true
  try {
    // User group rates failure must not block the plaza — without overrides,
    // PlazaGroupAccess.effectiveRate falls back to the group's default
    // multiplier, which is still a usable view of the prices.
    const [list, rates] = await Promise.all([
      userChannelsAPI.getAvailable(),
      userGroupsAPI.getUserGroupRates().catch((err: unknown) => {
        console.error('Failed to load user group rates:', err)
        return {} as Record<number, number>
      }),
    ])
    channels.value = list
    userGroupRates.value = rates
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

onMounted(loadAll)
</script>

<style scoped>
.plaza-model-grid {
  /* Keep cards readable while filling every available width. The min() guard
     lets a single card shrink to the viewport on narrow layouts. */
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 18rem), 1fr));
}
</style>
