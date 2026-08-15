import type {
  BillingMode,
  PricingInterval,
  PricingTimeVersion,
} from '@/api/admin/channels'

type TranslateFn = (key: string, params?: Record<string, unknown>) => string

export interface IntervalFormEntry {
  min_tokens: number
  max_tokens: number | null
  tier_label: string
  input_price: number | string | null
  output_price: number | string | null
  cache_write_price: number | string | null
  cache_read_price: number | string | null
  per_request_price: number | string | null
  sort_order: number
}

export interface PricingFormEntry {
  models: string[]
  billing_mode: BillingMode
  input_price: number | string | null
  output_price: number | string | null
  cache_write_price: number | string | null
  cache_read_price: number | string | null
  image_input_price: number | string | null
  image_output_price: number | string | null
  per_request_price: number | string | null
  intervals: IntervalFormEntry[]
  time_versions: PricingTimeVersionFormEntry[]
}

export interface PricingTimeWindowFormEntry {
  label: string
  weekdays: number
  start_minute: number
  end_minute: number
  multiplier: number | string
  sort_order: number
}

export interface PricingTimeVersionFormEntry {
  effective_from: string
  effective_until: string | null
  timezone: string
  default_multiplier: number | string
  input_price: number | string | null
  output_price: number | string | null
  cache_write_price: number | string | null
  cache_read_price: number | string | null
  image_input_price: number | string | null
  image_output_price: number | string | null
  sort_order: number
  windows: PricingTimeWindowFormEntry[]
}

// 价格转换：后端存 per-token，前端显示 per-MTok (￥/1M tokens)
const MTOK = 1_000_000

export function toNullableNumber(val: number | string | null | undefined): number | null {
  if (val === null || val === undefined || val === '') return null
  const num = Number(val)
  return isNaN(num) ? null : num
}

/** 前端显示值($/MTok) → 后端存储值(per-token) */
export function mTokToPerToken(val: number | string | null | undefined): number | null {
  const num = toNullableNumber(val)
  return num === null ? null : parseFloat((num / MTOK).toPrecision(10))
}

/** 后端存储值(per-token) → 前端显示值($/MTok) */
export function perTokenToMTok(val: number | null | undefined): number | null {
  if (val === null || val === undefined) return null
  // toPrecision(10) 消除 IEEE 754 浮点乘法精度误差，如 5e-8 * 1e6 = 0.04999...96 → 0.05
  return parseFloat((val * MTOK).toPrecision(10))
}

export function apiIntervalsToForm(intervals: PricingInterval[]): IntervalFormEntry[] {
  return (intervals || []).map(iv => ({
    min_tokens: iv.min_tokens,
    max_tokens: iv.max_tokens,
    tier_label: iv.tier_label || '',
    input_price: perTokenToMTok(iv.input_price),
    output_price: perTokenToMTok(iv.output_price),
    cache_write_price: perTokenToMTok(iv.cache_write_price),
    cache_read_price: perTokenToMTok(iv.cache_read_price),
    per_request_price: iv.per_request_price,
    sort_order: iv.sort_order
  }))
}

export function formIntervalsToAPI(intervals: IntervalFormEntry[]): PricingInterval[] {
  return (intervals || []).map(iv => ({
    min_tokens: iv.min_tokens,
    max_tokens: iv.max_tokens,
    tier_label: iv.tier_label,
    input_price: mTokToPerToken(iv.input_price),
    output_price: mTokToPerToken(iv.output_price),
    cache_write_price: mTokToPerToken(iv.cache_write_price),
    cache_read_price: mTokToPerToken(iv.cache_read_price),
    per_request_price: toNullableNumber(iv.per_request_price),
    sort_order: iv.sort_order
  }))
}

export function apiTimeVersionsToForm(versions: PricingTimeVersion[]): PricingTimeVersionFormEntry[] {
  return (versions || []).map(version => ({
    effective_from: toZonedDatetimeLocal(version.effective_from, version.timezone),
    effective_until: version.effective_until ? toZonedDatetimeLocal(version.effective_until, version.timezone) : null,
    timezone: version.timezone || 'Asia/Shanghai',
    default_multiplier: version.default_multiplier,
    input_price: perTokenToMTok(version.input_price),
    output_price: perTokenToMTok(version.output_price),
    cache_write_price: perTokenToMTok(version.cache_write_price),
    cache_read_price: perTokenToMTok(version.cache_read_price),
    image_input_price: perTokenToMTok(version.image_input_price),
    image_output_price: perTokenToMTok(version.image_output_price),
    sort_order: version.sort_order,
    windows: (version.windows || []).map(window => ({ ...window })),
  }))
}

export function formTimeVersionsToAPI(versions: PricingTimeVersionFormEntry[]): PricingTimeVersion[] {
  return (versions || []).map(version => ({
    effective_from: zonedDatetimeLocalToISO(version.effective_from, version.timezone),
    effective_until: version.effective_until ? zonedDatetimeLocalToISO(version.effective_until, version.timezone) : null,
    timezone: version.timezone.trim(),
    default_multiplier: Number(version.default_multiplier),
    input_price: mTokToPerToken(version.input_price),
    output_price: mTokToPerToken(version.output_price),
    cache_write_price: mTokToPerToken(version.cache_write_price),
    cache_read_price: mTokToPerToken(version.cache_read_price),
    image_input_price: mTokToPerToken(version.image_input_price),
    image_output_price: mTokToPerToken(version.image_output_price),
    sort_order: version.sort_order,
    windows: (version.windows || []).map(window => ({
      ...window,
      multiplier: Number(window.multiplier),
    })),
  }))
}

export function toZonedDatetimeLocal(value: string, timezone: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const parts = zonedDateParts(date, timezone)
  return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`
}

export function zonedDatetimeLocalToISO(value: string, timezone: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value)
  if (!match) throw new RangeError('invalid datetime-local value')
  const desiredWallTime = Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]), Number(match[4]), Number(match[5]))
  let instant = desiredWallTime
  // Two passes handle offsets that differ around daylight-saving transitions.
  for (let pass = 0; pass < 2; pass++) {
    const parts = zonedDateParts(new Date(instant), timezone)
    const representedWallTime = Date.UTC(
      Number(parts.year), Number(parts.month) - 1, Number(parts.day),
      Number(parts.hour), Number(parts.minute),
    )
    instant += desiredWallTime - representedWallTime
  }
  return new Date(instant).toISOString()
}

function zonedDateParts(date: Date, timezone: string): Record<'year' | 'month' | 'day' | 'hour' | 'minute', string> {
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone,
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  })
  const result = {} as Record<'year' | 'month' | 'day' | 'hour' | 'minute', string>
  for (const part of formatter.formatToParts(date)) {
    if (part.type === 'year' || part.type === 'month' || part.type === 'day' || part.type === 'hour' || part.type === 'minute') {
      result[part.type] = part.value
    }
  }
  return result
}

function timeVersionEpoch(version: PricingTimeVersionFormEntry, field: 'effective_from' | 'effective_until'): number | null {
  const value = version[field]
  if (!value) return null
  try {
    return Date.parse(zonedDatetimeLocalToISO(value, version.timezone))
  } catch {
    return Number.NaN
  }
}

export function validateTimeVersions(
  versions: PricingTimeVersionFormEntry[],
  hasIntervals: boolean,
  t: TranslateFn,
): string | null {
  if (!versions || versions.length === 0) return null
  if (hasIntervals) return t('admin.channels.timePricingValidation.intervalsConflict')

  for (let i = 0; i < versions.length; i++) {
    try {
      new Intl.DateTimeFormat('en', { timeZone: versions[i].timezone }).format()
    } catch {
      return t('admin.channels.timePricingValidation.timezone', { index: i + 1 })
    }
  }

  const sorted = [...versions].sort((a, b) => (timeVersionEpoch(a, 'effective_from') ?? 0) - (timeVersionEpoch(b, 'effective_from') ?? 0))
  for (let i = 0; i < sorted.length; i++) {
    const version = sorted[i]
    const from = timeVersionEpoch(version, 'effective_from')
    const until = timeVersionEpoch(version, 'effective_until')
    if (!version.effective_from || from == null || Number.isNaN(from)) {
      return t('admin.channels.timePricingValidation.effectiveFrom', { index: i + 1 })
    }
    if (version.default_multiplier === '' || !Number.isFinite(Number(version.default_multiplier)) || Number(version.default_multiplier) < 0) {
      return t('admin.channels.timePricingValidation.multiplier', { index: i + 1 })
    }
    if (until != null && (Number.isNaN(until) || until <= from)) {
      return t('admin.channels.timePricingValidation.effectiveUntil', { index: i + 1 })
    }
    if (i > 0) {
      const previousUntil = timeVersionEpoch(sorted[i - 1], 'effective_until')
      if (previousUntil == null || previousUntil > from) {
        return t('admin.channels.timePricingValidation.versionOverlap')
      }
    }
    for (let w = 0; w < version.windows.length; w++) {
      const window = version.windows[w]
      if (window.weekdays < 1 || window.start_minute < 0 || window.end_minute > 1440 || window.start_minute >= window.end_minute) {
        return t('admin.channels.timePricingValidation.windowRange', { index: i + 1, window: w + 1 })
      }
      if (window.multiplier === '' || !Number.isFinite(Number(window.multiplier)) || Number(window.multiplier) < 0) {
        return t('admin.channels.timePricingValidation.windowMultiplier', { index: i + 1, window: w + 1 })
      }
      for (let previous = 0; previous < w; previous++) {
        const other = version.windows[previous]
        if ((window.weekdays & other.weekdays) !== 0 && window.start_minute < other.end_minute && other.start_minute < window.end_minute) {
          return t('admin.channels.timePricingValidation.windowOverlap', { index: i + 1 })
        }
      }
    }
  }
  return null
}

// ── 模型模式冲突检测 ──────────────────────────────────────

interface ModelPattern {
  pattern: string
  prefix: string  // lowercase, 通配符去掉尾部 *
  wildcard: boolean
}

function toModelPattern(model: string): ModelPattern {
  const lower = model.toLowerCase()
  const wildcard = lower.endsWith('*')
  return {
    pattern: model,
    prefix: wildcard ? lower.slice(0, -1) : lower,
    wildcard,
  }
}

function patternsConflict(a: ModelPattern, b: ModelPattern): boolean {
  if (!a.wildcard && !b.wildcard) return a.prefix === b.prefix
  if (a.wildcard && !b.wildcard) return b.prefix.startsWith(a.prefix)
  if (!a.wildcard && b.wildcard) return a.prefix.startsWith(b.prefix)
  // 双通配符：任一前缀是另一前缀的前缀即冲突
  return a.prefix.startsWith(b.prefix) || b.prefix.startsWith(a.prefix)
}

/** 检测模型模式列表中的冲突，返回冲突的两个模式名；无冲突返回 null */
export function findModelConflict(models: string[]): [string, string] | null {
  const patterns = models.map(toModelPattern)
  for (let i = 0; i < patterns.length; i++) {
    for (let j = i + 1; j < patterns.length; j++) {
      if (patternsConflict(patterns[i], patterns[j])) {
        return [patterns[i].pattern, patterns[j].pattern]
      }
    }
  }
  return null
}

// ── 区间校验 ──────────────────────────────────────────────

/** 校验区间列表的合法性，返回错误消息；通过则返回 null
 *
 * mode 决定区间语义：
 * - token：区间是上下文 token 数分段 (min, max]，不能重叠，无上限段必须放最后
 * - per_request / image：区间是按 tier_label 分层（1K/2K/4K 等），后端按 label
 *   匹配，不依赖 min/max，因此跳过重叠 / last-unlimited 校验
 */
export function validateIntervals(
  intervals: IntervalFormEntry[],
  mode: BillingMode,
  t: TranslateFn,
): string | null {
  if (!intervals || intervals.length === 0) return null

  // 按 min_tokens 排序（不修改原数组）
  const sorted = [...intervals].sort((a, b) => a.min_tokens - b.min_tokens)

  for (let i = 0; i < sorted.length; i++) {
    const err = validateSingleInterval(sorted[i], i, t)
    if (err) return err
  }

  // per_request / image 模式按 tier_label 匹配，不做 token 区间重叠校验
  if (mode !== 'token') return null
  return checkIntervalOverlap(sorted, t)
}

function intervalValidationMessage(
  t: TranslateFn,
  key: string,
  params: Record<string, unknown>,
): string {
  return t(`admin.channels.intervalValidation.${key}`, params)
}

function intervalPriceLabel(t: TranslateFn, key: string): string {
  return t(`admin.channels.intervalValidation.price.${key}`)
}

function validateSingleInterval(iv: IntervalFormEntry, idx: number, t: TranslateFn): string | null {
  const index = idx + 1
  if (iv.min_tokens < 0) {
    return intervalValidationMessage(
      t,
      'negativeMin',
      { index, value: iv.min_tokens },
    )
  }
  if (iv.max_tokens != null) {
    if (iv.max_tokens <= 0) {
      return intervalValidationMessage(
        t,
        'maxPositive',
        { index, value: iv.max_tokens },
      )
    }
    if (iv.max_tokens <= iv.min_tokens) {
      return intervalValidationMessage(
        t,
        'maxGreaterThanMin',
        { index, max: iv.max_tokens, min: iv.min_tokens },
      )
    }
  }
  return validateIntervalPrices(iv, idx, t)
}

function validateIntervalPrices(iv: IntervalFormEntry, idx: number, t: TranslateFn): string | null {
  const index = idx + 1
  const prices: [string, number | string | null][] = [
    ['inputPrice', iv.input_price],
    ['outputPrice', iv.output_price],
    ['cacheWritePrice', iv.cache_write_price],
    ['cacheReadPrice', iv.cache_read_price],
    ['perRequestPrice', iv.per_request_price],
  ]
  for (const [key, val] of prices) {
    if (val != null && val !== '' && Number(val) < 0) {
      const field = intervalPriceLabel(t, key)
      return intervalValidationMessage(
        t,
        'negativePrice',
        { index, field },
      )
    }
  }
  return null
}

function checkIntervalOverlap(sorted: IntervalFormEntry[], t: TranslateFn): string | null {
  for (let i = 0; i < sorted.length; i++) {
    // 无上限区间必须是最后一个
    if (sorted[i].max_tokens == null && i < sorted.length - 1) {
      return intervalValidationMessage(
        t,
        'unboundedLast',
        { index: i + 1 },
      )
    }
    if (i === 0) continue
    const prev = sorted[i - 1]
    // (min, max] 语义：前一个区间上界 > 当前区间下界则重叠
    if (prev.max_tokens == null || prev.max_tokens > sorted[i].min_tokens) {
      const prevMax = prev.max_tokens == null ? '∞' : String(prev.max_tokens)
      return intervalValidationMessage(
        t,
        'overlap',
        { previousIndex: i, currentIndex: i + 1, previousMax: prevMax, currentMin: sorted[i].min_tokens },
      )
    }
  }
  return null
}

/** 平台对应的模型 tag 样式（背景+文字） */
export function getPlatformTagClass(platform: string): string {
  switch (platform) {
    case 'anthropic': return 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
    case 'openai': return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
    case 'gemini': return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
    case 'antigravity': return 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
    case 'grok': return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300'
    default: return 'bg-gray-100 text-gray-700 dark:bg-gray-900/30 dark:text-gray-400'
  }
}

/** 平台对应的模型文字色（仅 text-*，用于 input/text 场景）— 与 getPlatformTagClass 同色系 */
export function getPlatformTextClass(platform: string): string {
  switch (platform) {
    case 'anthropic': return 'text-orange-700 dark:text-orange-400'
    case 'openai': return 'text-emerald-700 dark:text-emerald-400'
    case 'gemini': return 'text-blue-700 dark:text-blue-400'
    case 'antigravity': return 'text-purple-700 dark:text-purple-400'
    case 'grok': return 'text-slate-700 dark:text-slate-300'
    default: return ''
  }
}
