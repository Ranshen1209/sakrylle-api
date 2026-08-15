<template>
  <Teleport to="body">
    <Transition name="qq-warning-fade">
      <div
        v-if="visible"
        class="fixed inset-0 z-[140] flex items-center justify-center overflow-y-auto bg-gray-950/60 p-4 backdrop-blur-sm"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleId"
      >
        <div class="w-full max-w-[480px] overflow-hidden rounded-2xl bg-white shadow-2xl ring-1 ring-black/10 dark:bg-dark-900 dark:ring-white/10">
          <div class="px-6 pb-2 pt-6">
            <div class="flex items-start gap-4">
              <span class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-xl bg-amber-50 text-amber-600 ring-1 ring-amber-100 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/20">
                <Icon name="exclamationTriangle" size="md" />
              </span>
              <div class="min-w-0 flex-1">
                <h2
                  :id="titleId"
                  class="text-lg font-bold leading-snug text-gray-950 dark:text-white"
                >
                  {{ t('auth.qqEmailWarning.title') }}
                </h2>
                <p class="mt-2 text-[13px] leading-6 text-gray-600 dark:text-dark-300">
                  {{ t('auth.qqEmailWarning.body') }}
                </p>
              </div>
            </div>

            <div class="mt-5 rounded-lg bg-gray-50 px-4 py-3 dark:bg-dark-800/60">
              <p class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-dark-400">
                {{ t('auth.qqEmailWarning.recommendedLabel') }}
              </p>
              <p class="mt-1.5 text-sm font-medium text-gray-800 dark:text-dark-100">
                Outlook · Gmail · 163 · iCloud
              </p>
            </div>
          </div>

          <div class="flex flex-col-reverse gap-2 border-t border-gray-100 bg-gray-50/60 px-6 py-4 dark:border-dark-800 dark:bg-dark-900/60 sm:flex-row sm:justify-end">
            <button
              type="button"
              class="inline-flex h-10 items-center justify-center rounded-lg border border-gray-200 bg-white px-4 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100 dark:hover:bg-dark-800"
              @click="onContinue"
            >
              {{ t('auth.qqEmailWarning.continueAnyway') }}
            </button>
            <button
              type="button"
              class="inline-flex h-10 items-center justify-center rounded-lg bg-primary-600 px-4 text-sm font-medium text-white shadow-sm transition hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:focus:ring-offset-dark-900"
              @click="onChange"
            >
              {{ t('auth.qqEmailWarning.changeEmail') }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

interface Props {
  visible: boolean
}

defineProps<Props>()

const emit = defineEmits<{
  (e: 'continue'): void
  (e: 'change-email'): void
}>()

const { t } = useI18n()

const titleId = computed(
  () => `qq-email-warning-title-${Math.random().toString(36).slice(2, 8)}`
)

function onContinue(): void {
  emit('continue')
}

function onChange(): void {
  emit('change-email')
}
</script>

<style scoped>
.qq-warning-fade-enter-active,
.qq-warning-fade-leave-active {
  transition: opacity 0.18s ease;
}

.qq-warning-fade-enter-from,
.qq-warning-fade-leave-to {
  opacity: 0;
}
</style>
