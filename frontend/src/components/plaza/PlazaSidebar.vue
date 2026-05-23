<template>
  <aside
    class="flex flex-col gap-5 rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800"
  >
    <header class="flex items-center justify-between">
      <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('plaza.filters.title') }}</h2>
      <button
        type="button"
        class="rounded-md border border-gray-200 bg-white px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-300 dark:hover:bg-dark-700"
        @click="onReset"
      >
        {{ t('plaza.filters.reset') }}
      </button>
    </header>

    <!-- Platform / Provider -->
    <section class="space-y-2">
      <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
        {{ t('plaza.filters.platform') }}
      </h3>
      <div class="grid grid-cols-2 gap-1.5">
        <FilterPill
          :active="platform === ''"
          :label="t('plaza.filters.all')"
          :count="models.length"
          @click="emit('update:platform', '')"
        />
        <FilterPill
          v-for="facet in platformFacets"
          :key="facet.value"
          :active="platform === facet.value"
          :label="facet.label"
          :count="facet.count"
          @click="emit('update:platform', facet.value)"
        />
      </div>
    </section>

    <!-- Available groups (key value-prop of the page) -->
    <section class="space-y-2">
      <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
        {{ t('plaza.filters.group') }}
      </h3>
      <div class="grid grid-cols-2 gap-1.5">
        <FilterPill
          :active="group === ''"
          :label="t('plaza.filters.all')"
          :count="models.length"
          @click="emit('update:group', '')"
        />
        <FilterPill
          v-for="facet in groupFacets"
          :key="facet.value"
          :active="group === facet.value"
          :label="facet.label"
          :count="facet.count"
          :suffix="facet.rateLabel"
          @click="emit('update:group', facet.value)"
        />
      </div>
    </section>

    <!-- Billing mode -->
    <section class="space-y-2">
      <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
        {{ t('plaza.filters.billing') }}
      </h3>
      <div class="grid grid-cols-2 gap-1.5">
        <FilterPill
          :active="billing === ''"
          :label="t('plaza.filters.all')"
          :count="models.length"
          @click="emit('update:billing', '')"
        />
        <FilterPill
          v-for="facet in billingFacets"
          :key="facet.value"
          :active="billing === facet.value"
          :label="facet.label"
          :count="facet.count"
          @click="emit('update:billing', facet.value)"
        />
      </div>
    </section>
  </aside>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlazaModel } from '@/utils/modelPlaza'
import {
  BILLING_MODE_TOKEN,
  BILLING_MODE_PER_REQUEST,
  BILLING_MODE_IMAGE,
  type BillingMode,
} from '@/constants/channel'

const props = defineProps<{
  models: PlazaModel[]
  platform: string
  group: string
  billing: string
}>()

const emit = defineEmits<{
  (e: 'update:platform', value: string): void
  (e: 'update:group', value: string): void
  (e: 'update:billing', value: string): void
  (e: 'reset'): void
}>()

const { t } = useI18n()

// ── Inline FilterPill ──────────────────────────────────────────────
// Defined inline (render fn) to keep the plaza folder small. Controlled
// chip with active/inactive styling, count badge, and an optional discount
// suffix like "0.2x".
const FilterPill = (p: {
  active: boolean
  label: string
  count: number
  suffix?: string
  onClick?: () => void
}) =>
  h(
    'button',
    {
      type: 'button',
      class: [
        'flex items-center justify-between gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs transition-colors',
        p.active
          ? 'border-primary-400 bg-primary-50 text-primary-700 dark:border-primary-500 dark:bg-primary-900/30 dark:text-primary-200'
          : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-300 dark:hover:bg-dark-700',
      ],
      onClick: p.onClick,
    },
    [
      h('span', { class: 'min-w-0 flex-1 truncate text-left' }, p.label),
      p.suffix
        ? h(
            'span',
            { class: 'text-[10px] font-medium text-gray-400 dark:text-gray-500' },
            p.suffix,
          )
        : null,
      h(
        'span',
        {
          class: [
            'flex-shrink-0 rounded px-1 text-[10px] font-medium',
            p.active
              ? 'bg-primary-200/60 text-primary-800 dark:bg-primary-800/60 dark:text-primary-100'
              : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400',
          ],
        },
        String(p.count),
      ),
    ],
  )

// ── Facets ─────────────────────────────────────────────────────────
interface Facet {
  value: string
  label: string
  count: number
  rateLabel?: string
}

const platformFacets = computed<Facet[]>(() => {
  const counts = new Map<string, number>()
  for (const m of props.models) {
    if (!m.platform) continue
    counts.set(m.platform, (counts.get(m.platform) ?? 0) + 1)
  }
  return [...counts.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([value, count]) => ({ value, label: value, count }))
})

const groupFacets = computed<Facet[]>(() => {
  // Aggregate by group display name so two ids with the same name (rare but
  // possible across platforms) collapse into one filter row.
  const counts = new Map<string, { count: number; rate: number }>()
  for (const m of props.models) {
    for (const g of m.groups) {
      const prev = counts.get(g.name)
      if (prev) {
        prev.count += 1
        // Track the *minimum* rate so the suffix shows best-case discount.
        if (g.effectiveRate < prev.rate) prev.rate = g.effectiveRate
      } else {
        counts.set(g.name, { count: 1, rate: g.effectiveRate })
      }
    }
  }
  return [...counts.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([value, { count, rate }]) => ({
      value,
      label: value,
      count,
      rateLabel: formatRate(rate),
    }))
})

const billingLabels: Record<BillingMode, string> = {
  [BILLING_MODE_TOKEN]: t('plaza.billing.token'),
  [BILLING_MODE_PER_REQUEST]: t('plaza.billing.perRequest'),
  [BILLING_MODE_IMAGE]: t('plaza.billing.image'),
}

const billingFacets = computed<Facet[]>(() => {
  const counts = new Map<string, number>()
  for (const m of props.models) {
    const mode = m.pricing?.billing_mode ?? BILLING_MODE_TOKEN
    counts.set(mode, (counts.get(mode) ?? 0) + 1)
  }
  return [...counts.entries()].map(([value, count]) => ({
    value,
    label: billingLabels[value as BillingMode] ?? value,
    count,
  }))
})

function formatRate(rate: number): string {
  if (rate >= 0.999 && rate <= 1.001) return ''
  return `${rate.toFixed(2).replace(/\.?0+$/, '')}x`
}

function onReset() {
  emit('update:platform', '')
  emit('update:group', '')
  emit('update:billing', '')
  emit('reset')
}
</script>
