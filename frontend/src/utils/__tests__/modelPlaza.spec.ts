/**
 * Tests for the model-plaza data shaping.
 *
 * The transformation matters because pricing decisions surface from it: if
 * `bestRate` or group merging is wrong, users see misleading prices.
 */

import { describe, expect, it } from 'vitest'
import {
  applyRate,
  flattenChannelsToPlaza,
  formatPrice,
} from '../modelPlaza'
import type { UserAvailableChannel } from '@/api/channels'

function group(
  id: number,
  name: string,
  platform: string,
  rate: number,
  isExclusive = false,
) {
  return {
    id,
    name,
    platform,
    subscription_type: 'standard',
    rate_multiplier: rate,
    is_exclusive: isExclusive,
  }
}

function model(name: string, platform: string, input?: number, output?: number) {
  return {
    name,
    platform,
    pricing:
      input != null
        ? {
            billing_mode: 'token' as const,
            input_price: input,
            output_price: output ?? input * 3,
            cache_write_price: null,
            cache_read_price: null,
            image_output_price: null,
            per_request_price: null,
            intervals: [],
          }
        : null,
  }
}

describe('flattenChannelsToPlaza', () => {
  it('produces one entry per (platform, name) regardless of channel count', () => {
    const channels: UserAvailableChannel[] = [
      {
        name: 'Claude-Kiro',
        description: '',
        platforms: [
          {
            platform: 'anthropic',
            groups: [group(2, 'Claude-Kiro', 'anthropic', 0.2)],
            supported_models: [model('claude-haiku-4-5', 'anthropic', 0.0000001)],
          },
        ],
      },
      {
        name: 'Claude-Backup',
        description: '',
        platforms: [
          {
            platform: 'anthropic',
            groups: [group(7, 'Claude-Plus', 'anthropic', 0.5)],
            supported_models: [model('claude-haiku-4-5', 'anthropic', 0.0000001)],
          },
        ],
      },
    ]

    const result = flattenChannelsToPlaza(channels, {})

    expect(result).toHaveLength(1)
    expect(result[0]?.name).toBe('claude-haiku-4-5')
    expect(result[0]?.channels).toEqual(['Claude-Kiro', 'Claude-Backup'])
    expect(result[0]?.groups.map((g) => g.id).sort()).toEqual([2, 7])
  })

  it('uses the smallest effective rate as bestRate', () => {
    const channels: UserAvailableChannel[] = [
      {
        name: 'GPT',
        description: '',
        platforms: [
          {
            platform: 'openai',
            groups: [
              group(3, 'GPT-Pro', 'openai', 0.4),
              group(4, 'GPT-Plus', 'openai', 0.2),
            ],
            supported_models: [model('gpt-5.4-mini', 'openai', 0.0000005)],
          },
        ],
      },
    ]

    const [m] = flattenChannelsToPlaza(channels, {})
    expect(m?.bestRate).toBe(0.2)
  })

  it('honors per-user group rate overrides over the default multiplier', () => {
    const channels: UserAvailableChannel[] = [
      {
        name: 'GPT',
        description: '',
        platforms: [
          {
            platform: 'openai',
            // Default rate 0.4; the user's override is 0.1.
            groups: [group(3, 'GPT-Pro', 'openai', 0.4)],
            supported_models: [model('gpt-5.4-mini', 'openai', 0.0000005)],
          },
        ],
      },
    ]

    const [m] = flattenChannelsToPlaza(channels, { 3: 0.1 })
    expect(m?.bestRate).toBe(0.1)
    expect(m?.groups[0]?.userRate).toBe(0.1)
    expect(m?.groups[0]?.effectiveRate).toBe(0.1)
    expect(m?.groups[0]?.defaultRate).toBe(0.4)
  })

  it('drops duplicate groups when the same (platform, model) appears across channels', () => {
    const sameGroup = group(2, 'Claude-Kiro', 'anthropic', 0.2)
    const channels: UserAvailableChannel[] = [
      {
        name: 'A',
        description: '',
        platforms: [
          {
            platform: 'anthropic',
            groups: [sameGroup],
            supported_models: [model('claude-haiku-4-5', 'anthropic', 0.0000001)],
          },
        ],
      },
      {
        name: 'B',
        description: '',
        platforms: [
          {
            platform: 'anthropic',
            groups: [sameGroup],
            supported_models: [model('claude-haiku-4-5', 'anthropic', 0.0000001)],
          },
        ],
      },
    ]

    const [m] = flattenChannelsToPlaza(channels, {})
    expect(m?.groups).toHaveLength(1)
    expect(m?.channels).toEqual(['A', 'B'])
  })

  it('keeps pricing when the first channel has it null and a later one fills it', () => {
    const channels: UserAvailableChannel[] = [
      {
        name: 'NoPricing',
        description: '',
        platforms: [
          {
            platform: 'openai',
            groups: [group(3, 'GPT-Pro', 'openai', 0.4)],
            supported_models: [model('gpt-5.4-mini', 'openai')],
          },
        ],
      },
      {
        name: 'WithPricing',
        description: '',
        platforms: [
          {
            platform: 'openai',
            groups: [group(4, 'GPT-Plus', 'openai', 0.2)],
            supported_models: [model('gpt-5.4-mini', 'openai', 0.0000005)],
          },
        ],
      },
    ]

    const [m] = flattenChannelsToPlaza(channels, {})
    expect(m?.pricing?.input_price).toBe(0.0000005)
  })

  it('sorts entries by platform then name for stable rendering', () => {
    const channels: UserAvailableChannel[] = [
      {
        name: 'Mixed',
        description: '',
        platforms: [
          {
            platform: 'openai',
            groups: [group(3, 'GPT-Pro', 'openai', 0.4)],
            supported_models: [
              model('gpt-5.5', 'openai', 0.0000005),
              model('gpt-5.4', 'openai', 0.0000003),
            ],
          },
          {
            platform: 'anthropic',
            groups: [group(2, 'Claude-Kiro', 'anthropic', 0.2)],
            supported_models: [model('claude-haiku-4-5', 'anthropic', 0.0000001)],
          },
        ],
      },
    ]

    const result = flattenChannelsToPlaza(channels, {})
    expect(result.map((m) => `${m.platform}/${m.name}`)).toEqual([
      'anthropic/claude-haiku-4-5',
      'openai/gpt-5.4',
      'openai/gpt-5.5',
    ])
  })
})

describe('formatPrice', () => {
  it('returns "-" for null', () => {
    expect(formatPrice(null, 1_000_000)).toBe('-')
  })

  it('strips trailing IEEE noise', () => {
    // 0.0000005 × 1_000_000 = 0.5, but JS may produce "0.5000000000".
    expect(formatPrice(0.0000005, 1_000_000)).toBe('$0.5')
  })

  it('renders zero cleanly', () => {
    expect(formatPrice(0, 1_000_000)).toBe('$0')
  })

  it('keeps full precision for small values', () => {
    expect(formatPrice(0.0000001, 1_000_000)).toBe('$0.1')
  })
})

describe('applyRate', () => {
  it('returns null when value is null', () => {
    expect(applyRate(null, 0.2)).toBeNull()
  })

  it('multiplies by the rate', () => {
    expect(applyRate(0.0000005, 0.2)).toBeCloseTo(0.0000001, 12)
  })
})
