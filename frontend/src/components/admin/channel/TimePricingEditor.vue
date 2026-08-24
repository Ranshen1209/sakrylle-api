<template>
  <div class="mt-3 border-t border-gray-200 pt-3 dark:border-dark-600">
    <div class="flex items-center justify-between gap-3">
      <div>
        <div class="text-xs font-medium text-gray-600 dark:text-gray-300">
          {{ t('admin.channels.form.timePricing') }}
        </div>
        <div class="mt-0.5 text-xs text-gray-400">
          {{ t('admin.channels.form.timePricingHint') }}
        </div>
      </div>
      <button
        type="button"
        data-testid="add-time-version"
        class="inline-flex h-8 items-center gap-1 rounded border border-primary-300 px-2 text-xs text-primary-600 hover:bg-primary-50 dark:border-primary-700 dark:text-primary-400 dark:hover:bg-primary-900/20"
        @click="addVersion"
      >
        <Icon name="plus" size="xs" />
        {{ t('admin.channels.form.addTimeVersion') }}
      </button>
    </div>

    <div v-if="versions.length === 0" class="mt-2 text-xs text-gray-400">
      {{ t('admin.channels.form.noTimeVersions') }}
    </div>

    <div
      v-for="(version, versionIndex) in versions"
      :key="versionIndex"
      class="mt-3 border-l-2 border-primary-300 pl-3 dark:border-primary-700"
    >
      <div class="flex items-center justify-between">
        <span class="text-xs font-medium text-gray-600 dark:text-gray-300">
          {{ t('admin.channels.form.timeVersion', { index: versionIndex + 1 }) }}
        </span>
        <button
          type="button"
          class="rounded p-1 text-gray-400 hover:text-red-500"
          :title="t('common.delete')"
          @click="removeVersion(versionIndex)"
        >
          <Icon name="trash" size="xs" />
        </button>
      </div>

      <div class="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-4">
        <label class="text-xs text-gray-400">
          {{ t('admin.channels.form.effectiveFrom') }}
          <input
            :value="version.effective_from"
            type="datetime-local"
            class="input mt-0.5 text-sm"
            @input="updateVersionField(versionIndex, 'effective_from', inputValue($event))"
          />
        </label>
        <label class="text-xs text-gray-400">
          {{ t('admin.channels.form.effectiveUntil') }}
          <input
            :value="version.effective_until || ''"
            type="datetime-local"
            class="input mt-0.5 text-sm"
            @input="updateVersionField(versionIndex, 'effective_until', inputValue($event) || null)"
          />
        </label>
        <label class="text-xs text-gray-400">
          {{ t('admin.channels.form.timezone') }}
          <input
            :value="version.timezone"
            list="pricing-timezones"
            class="input mt-0.5 text-sm"
            @input="updateVersionField(versionIndex, 'timezone', inputValue($event))"
          />
        </label>
        <label class="text-xs text-gray-400">
          {{ t('admin.channels.form.defaultMultiplier') }}
          <input
            :value="version.default_multiplier"
            type="number"
            min="0"
            step="0.01"
            class="input mt-0.5 text-sm"
            @input="updateVersionField(versionIndex, 'default_multiplier', inputValue($event))"
          />
        </label>
      </div>
      <datalist id="pricing-timezones">
        <option value="Asia/Shanghai" />
        <option value="UTC" />
      </datalist>

      <div class="mt-2 text-xs font-medium text-gray-500 dark:text-gray-400">
        {{ t('admin.channels.form.versionBasePrices') }}
        <span class="font-normal text-gray-400">￥/MTok</span>
      </div>
      <div class="mt-1 grid grid-cols-2 gap-2 sm:grid-cols-6">
        <label v-for="field in priceFields" :key="field.key" class="text-xs text-gray-400">
          {{ t(field.label) }}
          <input
            :value="version[field.key] ?? ''"
            type="number"
            min="0"
            step="any"
            class="input mt-0.5 text-sm"
            :placeholder="t('admin.channels.form.inheritStaticPrice')"
            @input="updateVersionField(versionIndex, field.key, inputValue($event) || null)"
          />
        </label>
      </div>

      <div class="mt-3 flex items-center justify-between">
        <span class="text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t('admin.channels.form.peakWindows') }}
        </span>
        <button type="button" class="inline-flex items-center gap-1 text-xs text-primary-600 hover:text-primary-700" @click="addWindow(versionIndex)">
          <Icon name="plus" size="xs" />
          {{ t('admin.channels.form.addWindow') }}
        </button>
      </div>

      <div v-for="(window, windowIndex) in version.windows" :key="windowIndex" class="mt-2 border-t border-gray-200 pt-2 dark:border-dark-600">
        <div class="grid grid-cols-2 gap-2 sm:grid-cols-[minmax(90px,1fr)_90px_90px_90px_32px]">
          <label class="text-xs text-gray-400">
            {{ t('admin.channels.form.periodLabel') }}
            <input :value="window.label" class="input mt-0.5 text-sm" @input="updateWindowField(versionIndex, windowIndex, 'label', inputValue($event))" />
          </label>
          <label class="text-xs text-gray-400">
            {{ t('admin.channels.form.startTime') }}
            <input :value="formatMinute(window.start_minute)" class="input mt-0.5 text-sm" placeholder="09:00" @change="updateWindowMinute(versionIndex, windowIndex, 'start_minute', inputValue($event))" />
          </label>
          <label class="text-xs text-gray-400">
            {{ t('admin.channels.form.endTime') }}
            <input :value="formatMinute(window.end_minute)" class="input mt-0.5 text-sm" placeholder="12:00" @change="updateWindowMinute(versionIndex, windowIndex, 'end_minute', inputValue($event))" />
          </label>
          <label class="text-xs text-gray-400">
            {{ t('admin.channels.form.multiplier') }}
            <input :value="window.multiplier" type="number" min="0" step="0.01" class="input mt-0.5 text-sm" @input="updateWindowField(versionIndex, windowIndex, 'multiplier', inputValue($event))" />
          </label>
          <button type="button" class="mt-4 rounded p-1 text-gray-400 hover:text-red-500" :title="t('common.delete')" @click="removeWindow(versionIndex, windowIndex)">
            <Icon name="trash" size="xs" />
          </button>
        </div>

        <div class="mt-2 flex flex-wrap gap-1">
          <label
            v-for="day in weekdays"
            :key="day.bit"
            class="inline-flex cursor-pointer items-center gap-1 rounded border px-1.5 py-1 text-xs"
            :class="(window.weekdays & day.bit) !== 0 ? 'border-primary-300 bg-primary-50 text-primary-700 dark:border-primary-700 dark:bg-primary-900/20 dark:text-primary-300' : 'border-gray-200 text-gray-400 dark:border-dark-600'"
          >
            <input class="sr-only" type="checkbox" :checked="(window.weekdays & day.bit) !== 0" @change="toggleWeekday(versionIndex, windowIndex, day.bit)" />
            {{ t(day.label) }}
          </label>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { PricingTimeVersionFormEntry, PricingTimeWindowFormEntry } from './types'

type TimePriceField = 'input_price' | 'output_price' | 'cache_write_price' | 'cache_read_price' | 'image_input_price' | 'image_output_price'

const { t } = useI18n()
const props = defineProps<{
  versions: PricingTimeVersionFormEntry[]
  platform?: string
}>()
const emit = defineEmits<{ update: [versions: PricingTimeVersionFormEntry[]] }>()

const priceFields: { key: TimePriceField, label: string }[] = [
  { key: 'input_price', label: 'admin.channels.form.inputPrice' },
  { key: 'output_price', label: 'admin.channels.form.outputPrice' },
  { key: 'cache_write_price', label: 'admin.channels.form.cacheWritePrice' },
  { key: 'cache_read_price', label: 'admin.channels.form.cacheReadPrice' },
  { key: 'image_input_price', label: 'admin.channels.form.imageInputPrice' },
  { key: 'image_output_price', label: 'admin.channels.form.imageTokenPrice' },
]

const weekdays = [
  { bit: 1, label: 'admin.channels.weekdays.mon' },
  { bit: 2, label: 'admin.channels.weekdays.tue' },
  { bit: 4, label: 'admin.channels.weekdays.wed' },
  { bit: 8, label: 'admin.channels.weekdays.thu' },
  { bit: 16, label: 'admin.channels.weekdays.fri' },
  { bit: 32, label: 'admin.channels.weekdays.sat' },
  { bit: 64, label: 'admin.channels.weekdays.sun' },
]

function inputValue(event: Event): string {
  return (event.target as HTMLInputElement).value
}

function cloneVersions(): PricingTimeVersionFormEntry[] {
  return props.versions.map(version => ({ ...version, windows: version.windows.map(window => ({ ...window })) }))
}

function addVersion() {
  emit('update', [...props.versions, {
    effective_from: '',
    effective_until: null,
    timezone: 'Asia/Shanghai',
    default_multiplier: 0.5,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    sort_order: props.versions.length,
    windows: [newWindow(540, 720, 0), newWindow(840, 1080, 1)],
  }])
}

function newWindow(startMinute = 540, endMinute = 720, sortOrder = 0): PricingTimeWindowFormEntry {
  const weekdaysMask = props.platform?.trim().toLowerCase() === 'deepseek' ? 31 : 127
  return { label: 'peak', weekdays: weekdaysMask, start_minute: startMinute, end_minute: endMinute, multiplier: 1, sort_order: sortOrder }
}

function removeVersion(index: number) {
  const versions = cloneVersions()
  versions.splice(index, 1)
  emit('update', versions.map((version, sortOrder) => ({ ...version, sort_order: sortOrder })))
}

function updateVersionField(index: number, field: keyof PricingTimeVersionFormEntry, value: PricingTimeVersionFormEntry[keyof PricingTimeVersionFormEntry]) {
  const versions = cloneVersions()
  Object.assign(versions[index], { [field]: value })
  emit('update', versions)
}

function addWindow(versionIndex: number) {
  const versions = cloneVersions()
  versions[versionIndex].windows.push(newWindow(540, 720, versions[versionIndex].windows.length))
  emit('update', versions)
}

function removeWindow(versionIndex: number, windowIndex: number) {
  const versions = cloneVersions()
  versions[versionIndex].windows.splice(windowIndex, 1)
  versions[versionIndex].windows.forEach((window, sortOrder) => { window.sort_order = sortOrder })
  emit('update', versions)
}

function updateWindowField(versionIndex: number, windowIndex: number, field: keyof PricingTimeWindowFormEntry, value: PricingTimeWindowFormEntry[keyof PricingTimeWindowFormEntry]) {
  const versions = cloneVersions()
  Object.assign(versions[versionIndex].windows[windowIndex], { [field]: value })
  emit('update', versions)
}

function toggleWeekday(versionIndex: number, windowIndex: number, bit: number) {
  const window = props.versions[versionIndex].windows[windowIndex]
  updateWindowField(versionIndex, windowIndex, 'weekdays', window.weekdays ^ bit)
}

function formatMinute(minute: number): string {
  if (minute === 1440) return '24:00'
  return `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
}

function parseMinute(value: string): number | null {
  const match = /^(\d{1,2}):(\d{2})$/.exec(value.trim())
  if (!match) return null
  const hour = Number(match[1])
  const minute = Number(match[2])
  if (minute > 59 || hour > 24 || (hour === 24 && minute !== 0)) return null
  return hour * 60 + minute
}

function updateWindowMinute(versionIndex: number, windowIndex: number, field: 'start_minute' | 'end_minute', value: string) {
  const minute = parseMinute(value)
  if (minute != null) updateWindowField(versionIndex, windowIndex, field, minute)
}
</script>
