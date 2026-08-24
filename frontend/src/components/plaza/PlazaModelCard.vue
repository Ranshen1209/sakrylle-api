<template>
  <article
    class="group relative flex flex-col gap-3 overflow-hidden rounded-2xl border bg-white p-5 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg dark:bg-dark-800"
    :class="[platformBorderClass(model.platform)]"
  >
    <header class="flex items-start justify-between gap-3">
      <div class="flex min-w-0 items-center gap-3">
        <div
          class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl border bg-white shadow-sm dark:bg-dark-900"
          :class="[platformBorderClass(model.platform)]"
        >
          <PlatformIcon :platform="model.platform as GroupPlatform" size="md" />
        </div>
        <div class="min-w-0">
          <h3
            class="truncate text-base font-semibold text-gray-900 dark:text-white"
            :title="model.name"
          >
            {{ model.name }}
          </h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            <span class="uppercase tracking-wide">{{ model.platform || '—' }}</span>
            <span v-if="rateLabel" class="mx-1.5 text-gray-300 dark:text-gray-600">·</span>
            <span v-if="rateLabel" :class="platformTextClass(model.platform)">
              {{ rateLabel }}
            </span>
          </p>
          <div
            v-if="model.pricing?.time_versions?.length"
            class="mt-1 flex flex-wrap items-center gap-1 text-[10px] text-gray-500 dark:text-dark-400"
          >
            <span class="rounded bg-emerald-50 px-1.5 py-0.5 font-medium text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300">
              {{ displayedTimePeriod }}
            </span>
            <span>{{ compactTimeWindows }}</span>
          </div>
        </div>
      </div>

      <button
        type="button"
        class="flex-shrink-0 rounded-lg p-1.5 text-gray-400 opacity-0 transition group-hover:opacity-100 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
        :title="copyTooltip"
        :aria-label="copyTooltip"
        @click="onCopy"
      >
        <Icon :name="copied ? 'check' : 'copy'" size="sm" />
      </button>
    </header>

    <!-- Pricing block -->
    <section v-if="model.pricing" class="space-y-1.5 rounded-xl bg-gray-50/70 p-3 dark:bg-dark-900/50">
      <template v-if="isToken">
        <PlazaPriceRow
          :label="t('plaza.pricing.input')"
          :value="longContextValue(displayedTimePrice('input_price'), 'input_price')"
          :rate="effectiveRate"
          :scale="perMillionScale"
          :unit="t('plaza.pricing.unitPerMillion')"
          :show-original="showOriginal"
        />
        <PlazaPriceRow
          v-if="model.pricing.image_input_ratio != null && displayedTimePrice('input_price') != null"
          :label="t('plaza.pricing.imageInput')"
          :value="longContextValue(imageInputPrice, 'input_price')"
          :rate="effectiveRate"
          :scale="perMillionScale"
          :unit="t('plaza.pricing.unitPerMillion')"
          :show-original="showOriginal"
        />
        <PlazaPriceRow
          :label="t('plaza.pricing.output')"
          :value="longContextValue(displayedTimePrice('output_price'), 'output_price')"
          :rate="effectiveRate"
          :scale="perMillionScale"
          :unit="t('plaza.pricing.unitPerMillion')"
          :show-original="showOriginal"
        />
        <PlazaPriceRow
          v-if="displayedTimePrice('cache_read_price') != null"
          :label="t('plaza.pricing.cacheRead')"
          :value="longContextValue(displayedTimePrice('cache_read_price'), 'cache_read_price')"
          :rate="effectiveRate"
          :scale="perMillionScale"
          :unit="t('plaza.pricing.unitPerMillion')"
          :show-original="showOriginal"
        />
        <PlazaPriceRow
          v-if="displayedTimePrice('cache_write_price') != null"
          :label="t('plaza.pricing.cacheWrite')"
          :value="longContextValue(displayedTimePrice('cache_write_price'), 'cache_write_price')"
          :rate="effectiveRate"
          :scale="perMillionScale"
          :unit="t('plaza.pricing.unitPerMillion')"
          :show-original="showOriginal"
        />
      </template>
      <template v-else-if="isPerRequest">
        <PlazaPriceRow
          :label="t('plaza.pricing.perRequest')"
          :value="model.pricing.per_request_price"
          :rate="effectiveRate"
          :scale="1"
          :unit="t('plaza.pricing.unitPerRequest')"
          :show-original="showOriginal"
        />
      </template>
      <template v-else-if="isImage">
        <PlazaPriceRow
          :label="t('plaza.pricing.image')"
          :value="model.pricing.per_request_price ?? displayedTimePrice('image_output_price')"
          :rate="effectiveRate"
          :scale="1"
          :unit="t('plaza.pricing.unitPerRequest')"
          :show-original="showOriginal"
        />
      </template>
    </section>
    <section v-else class="rounded-xl bg-gray-50 p-3 text-xs text-gray-500 dark:bg-dark-900/50 dark:text-gray-400">
      {{ t('plaza.noPricing') }}
    </section>

    <!-- Footer: billing badge + this card's group -->
    <footer class="flex flex-wrap items-center gap-1.5">
      <span
        v-if="longContext && longContextInterval"
        class="inline-flex items-center rounded-md bg-amber-100 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
      >
        {{ t('plaza.longContextBadge', { threshold: formatThreshold(longContextInterval.min_tokens) }) }}
      </span>
      <span
        class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium"
        :class="[billingBadgeClass]"
      >
        {{ billingLabel }}
      </span>
      <GroupBadge
        :name="model.group.name"
        :platform="model.group.platform as GroupPlatform"
        :subscription-type="model.group.subscriptionType as SubscriptionType"
        :rate-multiplier="model.group.defaultRate"
        :user-rate-multiplier="model.group.userRate"
        always-show-rate
      />
    </footer>
  </article>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import PlazaPriceRow from './PlazaPriceRow.vue'
import {
  displayedTimePrice as getDisplayedTimePrice,
  displayedTimeVersion,
  type ModelPlazaTimePriceField,
} from '@/utils/modelPlaza'
import type { ModelPlazaPricingMode } from '@/api/modelPlaza'
import type { UserPricingInterval } from '@/api/channels'
import type { PlazaModel } from '@/utils/modelPlaza'
import type { GroupPlatform, SubscriptionType } from '@/types'
import { platformBorderClass, platformTextClass } from '@/utils/platformColors'
import { formatPricingMinute } from '@/utils/timePricing'
import {
  BILLING_MODE_TOKEN,
  BILLING_MODE_PER_REQUEST,
  BILLING_MODE_IMAGE,
} from '@/constants/channel'

const props = withDefaults(
  defineProps<{
    model: PlazaModel
    /** Show original strikethrough price beside the discounted price. */
    showOriginal?: boolean
    longContext?: boolean
    timePricingMode?: ModelPlazaPricingMode
  }>(),
  { showOriginal: true, longContext: false, timePricingMode: 'current' },
)

const { t } = useI18n()
const perMillionScale = 1_000_000
const effectiveRate = computed(() => props.model.group.effectiveRate)

const displayedTimePeriod = computed(() => {
  if (props.timePricingMode === 'peak') return t('modelPlaza.table.timePricingPeakPreview')
  if (props.timePricingMode === 'off_peak') return t('modelPlaza.table.timePricingOffPeakPreview')
  const resolution = props.model.pricing?.time_resolution
  if (!resolution) return t('modelPlaza.table.timePricingUpcoming')
  if (resolution.period_label === 'peak') return t('modelPlaza.table.timePricingPeak')
  if (resolution.period_label === 'off_peak') return t('modelPlaza.table.timePricingOffPeak')
  return resolution.period_label
})

const compactTimeWindows = computed(() => {
  const version = displayedTimeVersion(props.model.pricing)
  if (!version) return ''
  return version.windows
    .map((window) => `${formatPricingMinute(window.start_minute)}-${formatPricingMinute(window.end_minute)}`)
    .join(' / ')
})

const imageInputPrice = computed(() => {
  const pricing = props.model.pricing
  const inputPrice = displayedTimePrice('input_price')
  if (pricing == null || inputPrice == null || pricing.image_input_ratio == null) return null
  return inputPrice * pricing.image_input_ratio
})

const billingMode = computed(() => props.model.pricing?.billing_mode ?? BILLING_MODE_TOKEN)
const isToken = computed(() => billingMode.value === BILLING_MODE_TOKEN)
const isPerRequest = computed(() => billingMode.value === BILLING_MODE_PER_REQUEST)
const isImage = computed(() => billingMode.value === BILLING_MODE_IMAGE)

type IntervalPriceField = 'input_price' | 'output_price' | 'cache_write_price' | 'cache_read_price'

const longContextInterval = computed<UserPricingInterval | null>(() => {
  const intervals = [...(props.model.pricing?.intervals ?? [])]
    .sort((a, b) => a.min_tokens - b.min_tokens)
  if (intervals.length < 2) return null
  return intervals[intervals.length - 1] ?? null
})

function longContextValue(value: number | null, field: IntervalPriceField): number | null {
  const pricing = props.model.pricing
  const intervalValue = longContextInterval.value?.[field]
  if (value == null || !props.longContext || pricing == null || intervalValue == null) return value

  // Time previews expose absolute prices. Preserve their multiplier when
  // switching the card to the corresponding long-context tier.
  const baseValue = pricing[field]
  if (baseValue != null && baseValue !== 0) return intervalValue * (value / baseValue)
  return intervalValue
}

function displayedTimePrice(field: ModelPlazaTimePriceField): number | null {
  return getDisplayedTimePrice(props.model.pricing, props.timePricingMode, field) ?? null
}

function formatThreshold(value: number | undefined): string {
  if (value == null) return '272K'
  return value >= 1000 && value % 1000 === 0 ? `${value / 1000}K` : String(value)
}

const billingLabel = computed(() => {
  if (isPerRequest.value) return t('plaza.billing.perRequest')
  if (isImage.value) return t('plaza.billing.image')
  return t('plaza.billing.token')
})

const billingBadgeClass = computed(() => {
  if (isPerRequest.value) return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
  if (isImage.value) return 'bg-pink-100 text-pink-700 dark:bg-pink-900/40 dark:text-pink-300'
  return 'bg-primary-100 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300'
})

const rateLabel = computed(() => {
  const rate = effectiveRate.value
  // Skip the badge when there is no discount (rate == 1) — shouting "1x" adds
  // noise without information.
  if (rate >= 0.999 && rate <= 1.001) return ''
  // toFixed(2) then strip trailing zero turns 0.20 → "0.2", 1.50 → "1.5".
  const text = rate.toFixed(2).replace(/\.?0+$/, '')
  return `${text}x`
})

const copied = ref(false)
const copyTooltip = computed(() => t('plaza.copy', { name: props.model.name }))

async function onCopy() {
  try {
    await navigator.clipboard.writeText(props.model.name)
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 1500)
  } catch {
    // Clipboard may be blocked (insecure context, permissions). Fail silent.
  }
}
</script>
