<template>
  <details
    class="group border-t border-gray-200 pt-4 dark:border-dark-600"
    :open="model.asyncEnabled || model.videoEnabled"
    data-testid="media-bridge-settings"
  >
    <summary
      class="flex cursor-pointer list-none items-center justify-between gap-3 text-gray-800 dark:text-gray-100"
      data-testid="media-bridge-settings-summary"
    >
      <span class="flex min-w-0 items-center gap-2">
        <Icon name="cog" size="sm" class="shrink-0 text-primary-500" />
        <span>
          <span class="block text-sm font-medium">{{ t('admin.accounts.mediaBridge.title') }}</span>
          <span class="mt-0.5 block text-xs font-normal text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.mediaBridge.description') }}
          </span>
        </span>
      </span>
      <Icon
        name="chevronRight"
        size="sm"
        class="shrink-0 text-gray-400 transition-transform group-open:rotate-90"
      />
    </summary>

    <div class="mt-4 space-y-5">
      <section class="space-y-3">
        <h4 class="text-sm font-medium text-gray-800 dark:text-gray-100">
          {{ t('admin.accounts.mediaBridge.imageRouting') }}
        </h4>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.accounts.mediaBridge.imageModels') }}</label>
            <input
              v-model="model.imageModels"
              type="text"
              class="input"
              placeholder="agnes-image-2.0-flash,agnes-image-2.1-flash"
              data-testid="media-bridge-image-models"
            />
            <p class="input-hint">{{ t('admin.accounts.mediaBridge.commaSeparatedHint') }}</p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.mediaBridge.imageDefaultSize') }}</label>
            <input
              v-model="model.imageDefaultSize"
              type="text"
              class="input"
              placeholder="1024x1024"
              data-testid="media-bridge-image-default-size"
            />
          </div>
        </div>
      </section>

      <section class="space-y-4 border-t border-dashed border-gray-200 pt-4 dark:border-dark-600">
        <div class="flex items-center justify-between gap-4">
          <div class="min-w-0">
            <h4 class="text-sm font-medium text-gray-800 dark:text-gray-100">
              {{ t('admin.accounts.mediaBridge.asyncImage') }}
            </h4>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.mediaBridge.asyncImageHint') }}
            </p>
          </div>
          <Toggle v-model="model.asyncEnabled" data-testid="media-bridge-async-enabled" />
        </div>

        <div v-if="model.asyncEnabled" class="space-y-4" data-testid="media-bridge-async-fields">
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.asyncBaseUrl') }}</label>
              <input
                v-model="model.asyncBaseUrl"
                type="url"
                class="input"
                placeholder="https://cdn.12ai.org"
                data-testid="media-bridge-async-base-url"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.allowedImageHost') }}</label>
              <input
                v-model="model.asyncImageHostSuffix"
                type="text"
                class="input"
                placeholder="12ai.org"
                data-testid="media-bridge-async-host-suffix"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.pollIntervalMs') }}</label>
              <input
                v-model.number="model.asyncPollIntervalMs"
                type="number"
                min="1"
                step="1"
                class="input"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.maxWaitMs') }}</label>
              <input
                v-model.number="model.asyncMaxWaitMs"
                type="number"
                min="1"
                step="1"
                class="input"
              />
            </div>
          </div>

          <div>
            <div class="mb-2 flex flex-wrap items-end justify-between gap-2">
              <div>
                <label class="input-label mb-0">{{ t('admin.accounts.mediaBridge.outputTokenTable') }}</label>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.mediaBridge.outputTokenTableHint') }}
                </p>
              </div>
              <label class="w-full sm:w-52">
                <span class="input-label">{{ t('admin.accounts.mediaBridge.imageInputRatio') }}</span>
                <input
                  v-model.number="model.imageInputRatio"
                  type="number"
                  min="0.0001"
                  step="0.0001"
                  class="input"
                  data-testid="media-bridge-image-input-ratio"
                />
              </label>
            </div>
            <div class="overflow-x-auto">
              <table class="w-full min-w-[560px] table-fixed text-sm">
                <thead>
                  <tr class="text-left text-xs text-gray-500 dark:text-gray-400">
                    <th class="w-20 px-2 py-2 font-medium">{{ t('admin.accounts.mediaBridge.size') }}</th>
                    <th
                      v-for="quality in MEDIA_BRIDGE_QUALITIES"
                      :key="quality"
                      class="px-2 py-2 font-medium"
                    >
                      {{ t(`admin.accounts.mediaBridge.quality.${quality}`) }}
                    </th>
                    <th class="px-2 py-2 font-medium">{{ t('admin.accounts.mediaBridge.refImageTokens') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="tier in MEDIA_BRIDGE_TIERS"
                    :key="tier"
                    class="border-t border-gray-100 dark:border-dark-700"
                  >
                    <th class="px-2 py-2 text-left font-medium text-gray-700 dark:text-gray-200">{{ tier }}</th>
                    <td v-for="quality in MEDIA_BRIDGE_QUALITIES" :key="quality" class="px-2 py-2">
                      <input
                        v-model.number="model.outputTokenTable[tier][quality]"
                        type="number"
                        min="1"
                        step="1"
                        class="input min-w-0"
                        :data-testid="`media-bridge-output-${tier}-${quality}`"
                      />
                    </td>
                    <td class="px-2 py-2">
                      <input
                        v-model.number="model.refImageTokens[tier]"
                        type="number"
                        min="1"
                        step="1"
                        class="input min-w-0"
                        :data-testid="`media-bridge-ref-${tier}`"
                      />
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </section>

      <section class="space-y-4 border-t border-dashed border-gray-200 pt-4 dark:border-dark-600">
        <div class="flex items-center justify-between gap-4">
          <div class="min-w-0">
            <h4 class="text-sm font-medium text-gray-800 dark:text-gray-100">
              {{ t('admin.accounts.mediaBridge.video') }}
            </h4>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.mediaBridge.videoHint') }}
            </p>
          </div>
          <Toggle v-model="model.videoEnabled" data-testid="media-bridge-video-enabled" />
        </div>

        <div v-if="model.videoEnabled" class="space-y-3" data-testid="media-bridge-video-fields">
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div class="md:col-span-2">
              <label class="input-label">{{ t('admin.accounts.mediaBridge.videoModels') }}</label>
              <input
                v-model="model.videoModels"
                type="text"
                class="input"
                placeholder="agnes-video-v2.0"
                data-testid="media-bridge-video-models"
              />
              <p class="input-hint">{{ t('admin.accounts.mediaBridge.commaSeparatedHint') }}</p>
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.videoSubmitPath') }}</label>
              <input v-model="model.videoSubmitPath" type="text" class="input" placeholder="/v1/videos" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.videoPollPath') }}</label>
              <input v-model="model.videoPollPath" type="text" class="input" placeholder="/agnesapi" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.pollIntervalMs') }}</label>
              <input v-model.number="model.videoPollIntervalMs" type="number" min="1" step="1" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.maxWaitMs') }}</label>
              <input v-model.number="model.videoMaxWaitMs" type="number" min="1" step="1" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.videoDefaultSeconds') }}</label>
              <input v-model.number="model.videoDefaultSeconds" type="number" min="0.001" step="0.001" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.mediaBridge.allowedVideoHosts') }}</label>
              <input
                v-model="model.videoHostSuffix"
                type="text"
                class="input"
                placeholder="agnes-ai.space,googleapis.com"
                data-testid="media-bridge-video-host-suffix"
              />
              <p class="input-hint">{{ t('admin.accounts.mediaBridge.commaSeparatedHint') }}</p>
            </div>
          </div>
        </div>
      </section>
    </div>
  </details>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  MEDIA_BRIDGE_QUALITIES,
  MEDIA_BRIDGE_TIERS,
  type MediaBridgeForm
} from './mediaBridgeCredentials'

const model = defineModel<MediaBridgeForm>({ required: true })
const { t } = useI18n()
</script>
