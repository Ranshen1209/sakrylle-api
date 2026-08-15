export const MEDIA_BRIDGE_TIERS = ['1K', '2K', '4K'] as const
export const MEDIA_BRIDGE_QUALITIES = ['low', 'medium', 'high'] as const

export type MediaBridgeTier = (typeof MEDIA_BRIDGE_TIERS)[number]
export type MediaBridgeQuality = (typeof MEDIA_BRIDGE_QUALITIES)[number]

export interface MediaBridgeForm {
  imageModels: string
  imageDefaultSize: string
  asyncEnabled: boolean
  asyncBaseUrl: string
  asyncImageHostSuffix: string
  asyncPollIntervalMs: number
  asyncMaxWaitMs: number
  outputTokenTable: Record<MediaBridgeTier, Record<MediaBridgeQuality, number>>
  refImageTokens: Record<MediaBridgeTier, number>
  imageInputRatio: number
  videoEnabled: boolean
  videoModels: string
  videoSubmitPath: string
  videoPollPath: string
  videoPollIntervalMs: number
  videoMaxWaitMs: number
  videoDefaultSeconds: number
  videoHostSuffix: string
}

export type MediaBridgeValidationError =
  | 'asyncBaseUrlRequired'
  | 'asyncBaseUrlInvalid'
  | 'asyncHostSuffixRequired'
  | 'asyncTimingInvalid'
  | 'asyncTokenTableInvalid'
  | 'videoModelsRequired'
  | 'videoPathsInvalid'
  | 'videoHostSuffixRequired'
  | 'videoTimingInvalid'

const DEFAULT_OUTPUT_TOKEN_TABLE: MediaBridgeForm['outputTokenTable'] = {
  '1K': { low: 196, medium: 1756, high: 7023 },
  '2K': { low: 397, medium: 3571, high: 14281 },
  '4K': { low: 367, medium: 3299, high: 13195 }
}

const DEFAULT_REF_IMAGE_TOKENS: MediaBridgeForm['refImageTokens'] = {
  '1K': 1024,
  '2K': 1521,
  '4K': 1508
}

const asRecord = (value: unknown): Record<string, unknown> | undefined =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined

const readString = (value: unknown, fallback = ''): string =>
  typeof value === 'string' ? value : fallback

const readPositiveNumber = (value: unknown, fallback: number): number => {
  const parsed = typeof value === 'number' ? value : Number.parseFloat(readString(value))
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback
}

const isEnabled = (value: unknown): boolean =>
  typeof value === 'string' && value.trim().toLowerCase() === 'true'

const normalizeCommaList = (value: string): string =>
  value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean)
    .join(',')

const setTrimmedOrDelete = (
  credentials: Record<string, unknown>,
  key: string,
  value: string
): void => {
  const normalized = value.trim()
  if (normalized) {
    credentials[key] = normalized
  } else {
    delete credentials[key]
  }
}

export function createMediaBridgeForm(): MediaBridgeForm {
  return {
    imageModels: '',
    imageDefaultSize: '',
    asyncEnabled: false,
    asyncBaseUrl: '',
    asyncImageHostSuffix: '',
    asyncPollIntervalMs: 3000,
    asyncMaxWaitMs: 240000,
    outputTokenTable: {
      '1K': { ...DEFAULT_OUTPUT_TOKEN_TABLE['1K'] },
      '2K': { ...DEFAULT_OUTPUT_TOKEN_TABLE['2K'] },
      '4K': { ...DEFAULT_OUTPUT_TOKEN_TABLE['4K'] }
    },
    refImageTokens: { ...DEFAULT_REF_IMAGE_TOKENS },
    imageInputRatio: 1.6,
    videoEnabled: false,
    videoModels: '',
    videoSubmitPath: '/v1/videos',
    videoPollPath: '/agnesapi',
    videoPollIntervalMs: 5000,
    videoMaxWaitMs: 600000,
    videoDefaultSeconds: 18.375,
    videoHostSuffix: ''
  }
}

export function readMediaBridgeCredentials(
  credentials: Record<string, unknown> | undefined
): MediaBridgeForm {
  const form = createMediaBridgeForm()
  if (!credentials) return form

  form.imageModels = readString(credentials.image_models)
  form.imageDefaultSize = readString(credentials.image_default_size)
  form.asyncEnabled = isEnabled(credentials.async_enabled)
  form.asyncBaseUrl = readString(credentials.async_base_url)
  form.asyncImageHostSuffix = readString(credentials.async_image_host_suffix)
  form.asyncPollIntervalMs = readPositiveNumber(
    credentials.poll_interval_ms,
    form.asyncPollIntervalMs
  )
  form.asyncMaxWaitMs = readPositiveNumber(credentials.max_wait_ms, form.asyncMaxWaitMs)

  const synth = asRecord(credentials.async_image_synth)
  const outputTable = asRecord(synth?.output_token_table)
  for (const tier of MEDIA_BRIDGE_TIERS) {
    const row = asRecord(outputTable?.[tier])
    for (const quality of MEDIA_BRIDGE_QUALITIES) {
      form.outputTokenTable[tier][quality] = readPositiveNumber(
        row?.[quality],
        form.outputTokenTable[tier][quality]
      )
    }
  }
  const refTokens = asRecord(synth?.ref_image_tokens)
  for (const tier of MEDIA_BRIDGE_TIERS) {
    form.refImageTokens[tier] = readPositiveNumber(
      refTokens?.[tier],
      form.refImageTokens[tier]
    )
  }
  form.imageInputRatio = readPositiveNumber(
    synth?.image_input_ratio,
    form.imageInputRatio
  )

  form.videoEnabled = isEnabled(credentials.video_enabled)
  form.videoModels = readString(credentials.video_models)
  form.videoSubmitPath = readString(credentials.video_submit_path, form.videoSubmitPath)
  form.videoPollPath = readString(credentials.video_poll_path, form.videoPollPath)
  form.videoPollIntervalMs = readPositiveNumber(
    credentials.video_poll_interval_ms,
    form.videoPollIntervalMs
  )
  form.videoMaxWaitMs = readPositiveNumber(
    credentials.video_max_wait_ms,
    form.videoMaxWaitMs
  )
  form.videoDefaultSeconds = readPositiveNumber(
    credentials.video_default_seconds,
    form.videoDefaultSeconds
  )
  form.videoHostSuffix = readString(credentials.video_host_suffix)
  return form
}

const isValidHTTPURL = (value: string): boolean => {
  try {
    const parsed = new URL(value)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && Boolean(parsed.hostname)
  } catch {
    return false
  }
}

const isPositiveInteger = (value: number): boolean =>
  Number.isInteger(value) && value > 0

export function validateMediaBridgeForm(
  form: MediaBridgeForm
): MediaBridgeValidationError | null {
  if (form.asyncEnabled) {
    if (!form.asyncBaseUrl.trim()) return 'asyncBaseUrlRequired'
    if (!isValidHTTPURL(form.asyncBaseUrl.trim())) return 'asyncBaseUrlInvalid'
    if (!form.asyncImageHostSuffix.trim()) return 'asyncHostSuffixRequired'
    if (
      !isPositiveInteger(form.asyncPollIntervalMs) ||
      !isPositiveInteger(form.asyncMaxWaitMs)
    ) {
      return 'asyncTimingInvalid'
    }
    if (!Number.isFinite(form.imageInputRatio) || form.imageInputRatio <= 0) {
      return 'asyncTokenTableInvalid'
    }
    for (const tier of MEDIA_BRIDGE_TIERS) {
      if (!isPositiveInteger(form.refImageTokens[tier])) return 'asyncTokenTableInvalid'
      for (const quality of MEDIA_BRIDGE_QUALITIES) {
        if (!isPositiveInteger(form.outputTokenTable[tier][quality])) {
          return 'asyncTokenTableInvalid'
        }
      }
    }
  }

  if (form.videoEnabled) {
    if (!normalizeCommaList(form.videoModels)) return 'videoModelsRequired'
    if (!form.videoSubmitPath.trim().startsWith('/') || !form.videoPollPath.trim().startsWith('/')) {
      return 'videoPathsInvalid'
    }
    if (!normalizeCommaList(form.videoHostSuffix)) return 'videoHostSuffixRequired'
    if (
      !isPositiveInteger(form.videoPollIntervalMs) ||
      !isPositiveInteger(form.videoMaxWaitMs) ||
      !Number.isFinite(form.videoDefaultSeconds) ||
      form.videoDefaultSeconds <= 0
    ) {
      return 'videoTimingInvalid'
    }
  }
  return null
}

export function applyMediaBridgeCredentials(
  credentials: Record<string, unknown>,
  form: MediaBridgeForm
): void {
  const imageModels = normalizeCommaList(form.imageModels)
  setTrimmedOrDelete(credentials, 'image_models', imageModels)
  setTrimmedOrDelete(credentials, 'image_default_size', form.imageDefaultSize)

  for (const key of [
    'async_enabled',
    'async_base_url',
    'async_image_host_suffix',
    'poll_interval_ms',
    'max_wait_ms',
    'async_image_synth'
  ]) {
    delete credentials[key]
  }
  if (form.asyncEnabled) {
    credentials.async_enabled = 'true'
    credentials.async_base_url = form.asyncBaseUrl.trim().replace(/\/+$/, '')
    credentials.async_image_host_suffix = form.asyncImageHostSuffix.trim()
    credentials.poll_interval_ms = String(Math.trunc(form.asyncPollIntervalMs))
    credentials.max_wait_ms = String(Math.trunc(form.asyncMaxWaitMs))
    credentials.async_image_synth = {
      output_token_table: {
        '1K': { ...form.outputTokenTable['1K'] },
        '2K': { ...form.outputTokenTable['2K'] },
        '4K': { ...form.outputTokenTable['4K'] }
      },
      ref_image_tokens: { ...form.refImageTokens },
      image_input_ratio: form.imageInputRatio
    }
  }

  for (const key of [
    'video_enabled',
    'video_models',
    'video_submit_path',
    'video_poll_path',
    'video_poll_interval_ms',
    'video_max_wait_ms',
    'video_default_seconds',
    'video_host_suffix'
  ]) {
    delete credentials[key]
  }
  if (form.videoEnabled) {
    credentials.video_enabled = 'true'
    credentials.video_models = normalizeCommaList(form.videoModels)
    credentials.video_submit_path = form.videoSubmitPath.trim()
    credentials.video_poll_path = form.videoPollPath.trim()
    credentials.video_poll_interval_ms = String(Math.trunc(form.videoPollIntervalMs))
    credentials.video_max_wait_ms = String(Math.trunc(form.videoMaxWaitMs))
    credentials.video_default_seconds = String(form.videoDefaultSeconds)
    credentials.video_host_suffix = normalizeCommaList(form.videoHostSuffix)
  }
}
