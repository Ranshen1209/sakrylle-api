<template>
  <div class="flex items-baseline justify-between gap-3 text-sm">
    <span class="text-gray-500 dark:text-gray-400">{{ label }}</span>
    <span class="flex items-baseline gap-2 font-mono">
      <span
        v-if="showOriginal && originalText !== '-'"
        class="text-xs text-gray-400 line-through dark:text-gray-500"
      >
        {{ originalText }}
      </span>
      <span class="text-gray-900 dark:text-gray-100">{{ effectiveText }}</span>
      <span v-if="unit" class="text-xs text-gray-400 dark:text-gray-500">{{ unit }}</span>
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { applyRate, formatPrice } from '@/utils/modelPlaza'

const props = withDefaults(
  defineProps<{
    label: string
    /** Original (un-discounted) USD price per token / per request. */
    value: number | null
    /** Effective rate multiplier in [0,1+]; 1 means no discount. */
    rate: number
    /** Display scale: 1_000_000 for /1M tokens, 1 for per-request. */
    scale: number
    /** Unit suffix shown after the effective price (e.g. "/ 1M tokens"). */
    unit?: string
    /** Show original price with strikethrough next to the effective one. */
    showOriginal?: boolean
  }>(),
  { unit: '', showOriginal: true },
)

const effectiveText = computed(() => formatPrice(applyRate(props.value, props.rate), props.scale))
const originalText = computed(() => formatPrice(props.value, props.scale))
</script>
