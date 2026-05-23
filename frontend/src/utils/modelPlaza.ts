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
  UserSupportedModelPricing,
} from '@/api/channels'

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

/** Format the original USD price as displayed text, scaled by `scale`. */
export function formatPrice(value: number | null, scale: number): string {
  if (value == null) return '-'
  const scaled = value * scale
  if (scaled === 0) return '$0'
  // toPrecision(6) is plenty for prices like 0.00075 → "$0.00075", and the
  // strip removes IEEE 754 trailing-zero noise.
  return `$${scaled.toPrecision(6).replace(/\.?0+$/, '')}`
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
  pricing: UserSupportedModelPricing | null
  channels: string[]
  groupsById: Map<number, PlazaGroupAccess>
}

/**
 * Flatten a channel-centric API response into a model-centric plaza view.
 *
 * Every (platform, model, group) combination becomes one entry. Pricing is
 * taken from the first channel that defines pricing for that model — pricing
 * is normally shared across channels (it's stored on `channel_model_pricing`
 * as the official upstream rate), so first-wins is stable in practice.
 */
export function flattenChannelsToPlaza(
  channels: UserAvailableChannel[],
  userGroupRates: Record<number, number>,
): PlazaModel[] {
  const aggregates = new Map<string, ModelAggregate>()

  for (const ch of channels) {
    for (const section of ch.platforms) {
      for (const model of section.supported_models) {
        const platform = model.platform || section.platform
        const modelKey = `${platform}::${model.name}`
        const agg = aggregates.get(modelKey) ?? {
          pricing: null,
          channels: [],
          groupsById: new Map<number, PlazaGroupAccess>(),
        }
        if (!agg.channels.includes(ch.name)) agg.channels.push(ch.name)
        if (agg.pricing == null && model.pricing != null) agg.pricing = model.pricing
        for (const g of section.groups) {
          if (agg.groupsById.has(g.id)) continue
          const userRate = Object.prototype.hasOwnProperty.call(userGroupRates, g.id)
            ? userGroupRates[g.id]
            : null
          agg.groupsById.set(g.id, {
            id: g.id,
            name: g.name,
            platform: g.platform,
            subscriptionType: g.subscription_type || 'standard',
            isExclusive: g.is_exclusive,
            defaultRate: g.rate_multiplier,
            userRate,
            effectiveRate: userRate ?? g.rate_multiplier,
          })
        }
        aggregates.set(modelKey, agg)
      }
    }
  }

  const out: PlazaModel[] = []
  for (const [modelKey, agg] of aggregates) {
    const [platform, name] = splitModelKey(modelKey)
    for (const group of agg.groupsById.values()) {
      out.push({
        id: `${modelKey}::${group.id}`,
        name,
        platform,
        pricing: agg.pricing,
        channels: [...agg.channels],
        group,
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
