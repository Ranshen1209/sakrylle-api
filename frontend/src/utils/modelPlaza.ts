/**
 * Model-plaza data shaping.
 *
 * The user-facing /channels/available endpoint is channel-centric: each row is
 * a channel that lists the platforms it carries, the groups inside each
 * platform, and the models supported on each platform.
 *
 * The plaza view inverts AND explodes this: every unique
 * (platform, model, group) becomes one entry, so each card on the page shows
 * exactly the price the user pays for that specific access path. Two groups
 * granting access to the same model produce two cards — that's intentional,
 * because the rate (and therefore the price) is different on each path.
 */

import type {
  UserAvailableChannel,
  UserPricingTimeVersion,
  UserSupportedModelPricing,
} from '@/api/channels'
import type { ModelPlazaPricingMode } from '@/api/modelPlaza'

export type ModelPlazaTimePriceField =
  | 'input_price'
  | 'output_price'
  | 'cache_write_price'
  | 'cache_write_1h_price'
  | 'cache_read_price'
  | 'image_input_price'
  | 'image_output_price'

/** Resolve the version card that was active when the backend calculated current pricing. */
export function displayedTimeVersion(
  pricing: UserSupportedModelPricing | null | undefined,
): UserPricingTimeVersion | undefined {
  const versions = pricing?.time_versions
  if (!versions?.length) return undefined
  const pricingAt = pricing?.time_resolution?.pricing_at
  if (!pricingAt) return versions[0]

  const instant = Date.parse(pricingAt)
  return [...versions]
    .filter((version) => {
      const from = Date.parse(version.effective_from)
      const until = version.effective_until ? Date.parse(version.effective_until) : Number.POSITIVE_INFINITY
      return from <= instant && instant < until
    })
    .sort((a, b) => Date.parse(b.effective_from) - Date.parse(a.effective_from))[0] || versions[0]
}

/** Return the price shown for the selected period without changing backend billing. */
export function displayedTimePrice(
  pricing: UserSupportedModelPricing | null | undefined,
  mode: ModelPlazaPricingMode | undefined,
  field: ModelPlazaTimePriceField,
): number | null | undefined {
  const current = pricing?.[field]
  if (mode == null || mode === 'current') return current

  const version = displayedTimeVersion(pricing)
  if (!version) return current

  let base = version[field]
  if (base == null && current != null) {
    const currentMultiplier = pricing?.time_resolution?.multiplier
    base = currentMultiplier != null && currentMultiplier !== 0
      ? current / currentMultiplier
      : current
  }
  if (base == null) return base

  if (mode === 'off_peak') return base * version.default_multiplier
  const peakMultiplier = version.windows.length
    ? Math.max(...version.windows.map((window) => window.multiplier))
    : 1
  return base * peakMultiplier
}

/** A group with the effective multiplier resolved (user override or default). */
export interface PlazaGroupAccess {
  id: number
  name: string
  platform: string
  subscriptionType: string
  isExclusive: boolean
  defaultRate: number
  /** User-specific override if any; otherwise null. */
  userRate: number | null
  /** Effective rate to use for pricing — userRate ?? defaultRate. */
  effectiveRate: number
}

/** One row in the plaza: one model viewed through one specific group. */
export interface PlazaModel {
  /** Stable id (`${platform}::${name}::${groupId}`) suitable for v-for keys. */
  id: string
  name: string
  platform: string
  pricing: UserSupportedModelPricing | null
  /** Channels that expose this (platform, model) combo. */
  channels: string[]
  /** The group this card represents — exactly one access path per card. */
  group: PlazaGroupAccess
}

/** Format the price as displayed text, scaled by `scale` (numeric value unchanged; rendered as ￥). */
export function formatPrice(value: number | null, scale: number): string {
  if (value == null) return '-'
  const scaled = value * scale
  if (scaled === 0) return '￥0'
  // toPrecision(6) is plenty for prices like 0.00075 → "￥0.00075", and the
  // strip removes IEEE 754 trailing-zero noise.
  return `￥${scaled.toPrecision(6).replace(/\.?0+$/, '')}`
}

/**
 * Discount the original price by the effective rate multiplier.
 * `rate=0.2` means "user pays 20% of the channel base price".
 */
export function applyRate(value: number | null, rate: number): number | null {
  if (value == null) return null
  return value * rate
}

interface ModelAggregate {
  channels: string[]
  // group id → that group's access path + the pricing of the channel serving it
  groups: Map<number, { access: PlazaGroupAccess; pricing: UserSupportedModelPricing | null }>
}

/**
 * Flatten a channel-centric API response into a model-centric plaza view.
 *
 * Every (platform, model, group) becomes one entry. Pricing is taken PER GROUP
 * from the channel that actually serves that group — NOT first-channel-wins —
 * so a model name carried by two channels with different billing (e.g. one
 * per-image, one token) shows the correct price for each access path.
 */
export function flattenChannelsToPlaza(
  channels: UserAvailableChannel[],
  userGroupRates: Record<number, number>,
): PlazaModel[] {
  const aggregates = new Map<string, ModelAggregate>()

  for (const ch of channels) {
    for (const section of ch.platforms) {
      for (const model of section.supported_models) {
        const platform = inferDisplayPlatform(model.name, model.platform || section.platform)
        const modelKey = `${platform}::${model.name}`
        const agg = aggregates.get(modelKey) ?? { channels: [] as string[], groups: new Map<number, { access: PlazaGroupAccess; pricing: UserSupportedModelPricing | null }>() }
        if (!agg.channels.includes(ch.name)) agg.channels.push(ch.name)
        for (const g of section.groups) {
          const existing = agg.groups.get(g.id)
          if (existing) {
            // group already seen via another channel; fill pricing only if missing
            if (existing.pricing == null && model.pricing != null) existing.pricing = model.pricing
            continue
          }
          const userRate = Object.prototype.hasOwnProperty.call(userGroupRates, g.id)
            ? userGroupRates[g.id]
            : null
          agg.groups.set(g.id, {
            access: {
              id: g.id,
              name: g.name,
              platform: inferDisplayPlatform(model.name, g.platform),
              subscriptionType: g.subscription_type || 'standard',
              isExclusive: g.is_exclusive,
              defaultRate: g.rate_multiplier,
              userRate,
              effectiveRate: userRate ?? g.rate_multiplier,
            },
            pricing: model.pricing ?? null,
          })
        }
        aggregates.set(modelKey, agg)
      }
    }
  }

  const out: PlazaModel[] = []
  for (const [modelKey, agg] of aggregates) {
    const [platform, name] = splitModelKey(modelKey)
    for (const { access, pricing } of agg.groups.values()) {
      out.push({
        id: `${modelKey}::${access.id}`,
        name,
        platform,
        pricing,
        channels: [...agg.channels],
        group: access,
      })
    }
  }
  return out.sort(comparePlazaModels)
}

function splitModelKey(key: string): [string, string] {
  const idx = key.indexOf('::')
  return idx < 0 ? ['', key] : [key.slice(0, idx), key.slice(idx + 2)]
}

/** Stable sort: platform asc, model name asc, then cheapest rate first. */
function comparePlazaModels(a: PlazaModel, b: PlazaModel): number {
  if (a.platform !== b.platform) return a.platform.localeCompare(b.platform)
  if (a.name !== b.name) return a.name.localeCompare(b.name)
  return a.group.effectiveRate - b.group.effectiveRate
}

/**
 * Infer the user-facing display platform from the model name.
 * The DB may store a protocol-level platform (e.g. 'anthropic' for DeepSeek
 * because the upstream uses Anthropic protocol), but the plaza should show
 * the actual provider brand.
 */
function inferDisplayPlatform(modelName: string, dbPlatform: string): string {
  if (modelName.startsWith('deepseek-')) return 'deepseek'
  return dbPlatform
}
