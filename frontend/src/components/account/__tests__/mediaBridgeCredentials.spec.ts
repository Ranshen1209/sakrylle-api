import { describe, expect, it } from 'vitest'
import {
  applyMediaBridgeCredentials,
  createMediaBridgeForm,
  readMediaBridgeCredentials,
  validateMediaBridgeForm
} from '../mediaBridgeCredentials'

describe('mediaBridgeCredentials', () => {
  it('hydrates string-backed flags, timing values, and synth calibration', () => {
    const form = readMediaBridgeCredentials({
      image_models: 'gpt-image-2-async, gpt-image-2',
      image_default_size: '1024x1024',
      async_enabled: 'TRUE',
      async_base_url: 'https://cdn.example.com',
      async_image_host_suffix: 'example.com',
      poll_interval_ms: '2500',
      max_wait_ms: 180000,
      async_image_synth: {
        output_token_table: {
          '1K': { low: 101, medium: 102, high: 103 },
          '2K': { low: 201, medium: 202, high: 203 },
          '4K': { low: 401, medium: 402, high: 403 }
        },
        ref_image_tokens: { '1K': 11, '2K': 22, '4K': 44 },
        image_input_ratio: 2.25
      },
      video_enabled: 'true',
      video_models: 'agnes-video-v2.0',
      video_submit_path: '/videos',
      video_poll_path: '/tasks',
      video_poll_interval_ms: '4500',
      video_max_wait_ms: '500000',
      video_default_seconds: '12.5',
      video_host_suffix: 'media.example.com'
    })

    expect(form.asyncEnabled).toBe(true)
    expect(form.asyncPollIntervalMs).toBe(2500)
    expect(form.outputTokenTable['4K'].high).toBe(403)
    expect(form.refImageTokens['2K']).toBe(22)
    expect(form.imageInputRatio).toBe(2.25)
    expect(form.videoEnabled).toBe(true)
    expect(form.videoDefaultSeconds).toBe(12.5)
  })

  it('serializes runtime flags and top-level numeric values as strings', () => {
    const form = createMediaBridgeForm()
    form.imageModels = ' model-a, model-b\nmodel-c '
    form.imageDefaultSize = ' 1024x1024 '
    form.asyncEnabled = true
    form.asyncBaseUrl = 'https://cdn.example.com/'
    form.asyncImageHostSuffix = 'example.com'
    form.videoEnabled = true
    form.videoModels = 'video-a, video-b'
    form.videoHostSuffix = 'video.example.com, storage.example.com'

    const credentials: Record<string, unknown> = {
      api_key: 'redacted-preserved-by-backend',
      base_url: 'https://api.example.com',
      custom_key: 'untouched'
    }
    applyMediaBridgeCredentials(credentials, form)

    expect(credentials).toMatchObject({
      image_models: 'model-a,model-b,model-c',
      image_default_size: '1024x1024',
      async_enabled: 'true',
      async_base_url: 'https://cdn.example.com',
      async_image_host_suffix: 'example.com',
      poll_interval_ms: '3000',
      max_wait_ms: '240000',
      video_enabled: 'true',
      video_models: 'video-a,video-b',
      video_poll_interval_ms: '5000',
      video_max_wait_ms: '600000',
      video_default_seconds: '18.375',
      video_host_suffix: 'video.example.com,storage.example.com',
      custom_key: 'untouched'
    })
    expect(credentials.async_image_synth).toEqual({
      output_token_table: form.outputTokenTable,
      ref_image_tokens: form.refImageTokens,
      image_input_ratio: 1.6
    })
  })

  it('removes all bridge-owned keys when both bridges are disabled', () => {
    const credentials: Record<string, unknown> = {
      async_enabled: 'true',
      async_base_url: 'https://old.example.com',
      async_image_synth: { old: true },
      video_enabled: 'true',
      video_models: 'old-video',
      video_default_seconds: '9',
      unrelated: 'keep'
    }
    applyMediaBridgeCredentials(credentials, createMediaBridgeForm())

    expect(credentials).toEqual({ unrelated: 'keep' })
  })

  it('validates security boundaries and fail-closed billing calibration', () => {
    const form = createMediaBridgeForm()
    form.asyncEnabled = true
    expect(validateMediaBridgeForm(form)).toBe('asyncBaseUrlRequired')
    form.asyncBaseUrl = 'file:///tmp/task'
    expect(validateMediaBridgeForm(form)).toBe('asyncBaseUrlInvalid')
    form.asyncBaseUrl = 'https://cdn.example.com'
    expect(validateMediaBridgeForm(form)).toBe('asyncHostSuffixRequired')
    form.asyncImageHostSuffix = 'example.com'
    form.outputTokenTable['2K'].high = 0
    expect(validateMediaBridgeForm(form)).toBe('asyncTokenTableInvalid')

    form.outputTokenTable['2K'].high = 14281
    form.asyncEnabled = false
    form.videoEnabled = true
    expect(validateMediaBridgeForm(form)).toBe('videoModelsRequired')
    form.videoModels = 'video-a'
    form.videoSubmitPath = 'videos'
    expect(validateMediaBridgeForm(form)).toBe('videoPathsInvalid')
    form.videoSubmitPath = '/videos'
    expect(validateMediaBridgeForm(form)).toBe('videoHostSuffixRequired')
  })
})
