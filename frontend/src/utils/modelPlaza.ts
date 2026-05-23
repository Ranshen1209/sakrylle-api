/**
 * Model-plaza data shaping.
 *
 * The user-facing /channels/available endpoint is channel-centric: each row is
 * a channel that lists the platforms it carries, the groups inside each
 * platform, and the models supported on each platform.
 *
 * The plaza view inverts this: every unique (platform, model) becomes one
 * entry, and the entry collects every group through which the user can reach
 * that model. The "best rate" is whichever group gives the lowest effective
 * multiplier (user override → default), so the prominent price reflects the
 * cheapest path the user can actually take today.
 */

import type {
  UserAvailableChannel,
  UserAvailableGroup,
  UserSupportedModel,
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

/** One row in the plaza: one model, all groups that can access it. */
export interface PlazaModel {
  /** Stable id (`${platform}::${name}`) suitable for v-for keys and routing. */
  id: string
  name: string
  platform: string
  pricing: UserSupportedModelPricing | null
  /** All channels that expose this (platform, model) combo. */
  channels: string[]
  /** Every group through which the user can hit this model. */
  groups: PlazaGroupAccess[]
  /** Lowest effective rate across `groups`; 1 if none (defensive). */
  bestRate: number
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

/**
 * Flatten a channel-centric API response into a model-centric plaza view.
 *
 * Resolution rules:
 * - One entry per unique `(platform, name)` across all channels and platforms.
 * - Pricing is taken from the first channel that defines pricing for that
 *   model. Pricing is normally shared across channels (it's stored on
 *   `channel_model_pricing` as the official upstream rate), so first-wins is
 *   stable in practice. If no channel has pricing, `pricing` is null.
 * - `groups` aggregates every group from every channel section that supports
 *   this model. Duplicates by group id are collapsed.
 * - `bestRate` is the minimum effective rate across `groups`.
 */
export function flattenChannelsToPlaza(
  channels: UserAvailableChannel[],
  userGroupRates: Record<number, number>,
): PlazaModel[] {
  const byKey = new Map<string, PlazaModel>()

  for (const ch of channels) {
    for (const section of ch.platforms) {
      for (const model of section.supported_models) {
        const key = `${model.platform || section.platform}::${model.name}`
        const existing = byKey.get(key)
        if (existing) {
          if (!existing.channels.includes(ch.name)) {
            existing.channels.push(ch.name)
          }
          mergeGroups(existing.groups, section.groups, userGroupRates)
          if (existing.pricing == null && model.pricing != null) {
            existing.pricing = model.pricing
          }
        } else {
          byKey.set(key, buildEntry(key, ch.name, section.groups, model, userGroupRates))
        }
      }
    }
  }

  for (const entry of byKey.values()) {
    entry.bestRate = computeBestRate(entry.groups)
  }

  return [...byKey.values()].sort(comparePlazaModels)
}

function buildEntry(
  id: string,
  channelName: string,
  groups: UserAvailableGroup[],
  model: UserSupportedModel,
  userGroupRates: Record<number, number>,
): PlazaModel {
  const accesses: PlazaGroupAccess[] = []
  mergeGroups(accesses, groups, userGroupRates)
  return {
    id,
    name: model.name,
    platform: model.platform || (groups[0]?.platform ?? ''),
    pricing: model.pricing,
    channels: [channelName],
    groups: accesses,
    bestRate: 1,
  }
}

function mergeGroups(
  target: PlazaGroupAccess[],
  source: UserAvailableGroup[],
  userGroupRates: Record<number, number>,
): void {
  for (const g of source) {
    if (target.some((t) => t.id === g.id)) continue
    const userRate =
      Object.prototype.hasOwnProperty.call(userGroupRates, g.id) ? userGroupRates[g.id] : null
    const effective = userRate ?? g.rate_multiplier
    target.push({
      id: g.id,
      name: g.name,
      platform: g.platform,
      subscriptionType: g.subscription_type || 'standard',
      isExclusive: g.is_exclusive,
      defaultRate: g.rate_multiplier,
      userRate,
      effectiveRate: effective,
    })
  }
}

function computeBestRate(groups: PlazaGroupAccess[]): number {
  if (groups.length === 0) return 1
  let best = Number.POSITIVE_INFINITY
  for (const g of groups) {
    if (g.effectiveRate < best) best = g.effectiveRate
  }
  return Number.isFinite(best) ? best : 1
}

/** Stable sort: platform asc, then model name asc. */
function comparePlazaModels(a: PlazaModel, b: PlazaModel): number {
  if (a.platform !== b.platform) return a.platform.localeCompare(b.platform)
  return a.name.localeCompare(b.name)
}
