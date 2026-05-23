/**
 * Tests for the model-plaza data shaping.
 *
 * Each plaza row is one (platform, model, group) — two groups granting access
 * to the same model produce two rows so the user sees a separate price card
 * per access path.
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
  it('produces one row per (model, group) combination', () => {
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

    const result = flattenChannelsToPlaza(channels, {})

    expect(result).toHaveLength(2)
    expect(result.map((m) => m.group.name).sort()).toEqual(['GPT-Plus', 'GPT-Pro'])
    expect(result.every((m) => m.name === 'gpt-5.4-mini')).toBe(true)
    // Cards are sorted by ascending rate within the same model — cheapest first.
    expect(result[0]?.group.effectiveRate).toBe(0.2)
    expect(result[1]?.group.effectiveRate).toBe(0.4)
  })

  it('honors per-user group rate overrides over the default multiplier', () => {
    const channels: UserAvailableChannel[] = [
      {
        name: 'GPT',
        description: '',
        platforms: [
          {
            platform: 'openai',
            groups: [group(3, 'GPT-Pro', 'openai', 0.4)],
            supported_models: [model('gpt-5.4-mini', 'openai', 0.0000005)],
          },
        ],
      },
    ]

    const [m] = flattenChannelsToPlaza(channels, { 3: 0.1 })
    expect(m?.group.userRate).toBe(0.1)
    expect(m?.group.effectiveRate).toBe(0.1)
    expect(m?.group.defaultRate).toBe(0.4)
  })

  it('deduplicates the same group across multiple channels', () => {
    // Same model + same group exposed by two channels — should still produce
    // a single row, not duplicate the user's view.
    const sharedGroup = group(2, 'Claude-Kiro', 'anthropic', 0.2)
    const channels: UserAvailableChannel[] = [
      {
        name: 'A',
        description: '',
        platforms: [
          {
            platform: 'anthropic',
            groups: [sharedGroup],
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
            groups: [sharedGroup],
            supported_models: [model('claude-haiku-4-5', 'anthropic', 0.0000001)],
          },
        ],
      },
    ]

    const result = flattenChannelsToPlaza(channels, {})
    expect(result).toHaveLength(1)
    expect(result[0]?.channels).toEqual(['A', 'B'])
  })

  it('keeps pricing when the first channel has none and a later one fills it', () => {
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

    const result = flattenChannelsToPlaza(channels, {})
    // Both rows share the same model and therefore the same pricing record.
    expect(result.every((m) => m.pricing?.input_price === 0.0000005)).toBe(true)
  })

  it('sorts entries by platform, then model name, then ascending rate', () => {
    const channels: UserAvailableChannel[] = [
      {
        name: 'Mixed',
        description: '',
        platforms: [
          {
            platform: 'openai',
            groups: [
              group(3, 'GPT-Pro', 'openai', 0.4),
              group(4, 'GPT-Plus', 'openai', 0.2),
            ],
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
    expect(
      result.map((m) => `${m.platform}/${m.name}/${m.group.name}`),
    ).toEqual([
      'anthropic/claude-haiku-4-5/Claude-Kiro',
      'openai/gpt-5.4/GPT-Plus',
      'openai/gpt-5.4/GPT-Pro',
      'openai/gpt-5.5/GPT-Plus',
      'openai/gpt-5.5/GPT-Pro',
    ])
  })
})

describe('formatPrice', () => {
  it('returns "-" for null', () => {
    expect(formatPrice(null, 1_000_000)).toBe('-')
  })

  it('strips trailing IEEE noise', () => {
    // 0.0000005 × 1_000_000 = 0.5, but JS may produce "0.5000000000".
    expect(formatPrice(0.0000005, 1_000_000)).toBe('￥0.5')
  })

  it('renders zero cleanly', () => {
    expect(formatPrice(0, 1_000_000)).toBe('￥0')
  })

  it('keeps full precision for small values', () => {
    expect(formatPrice(0.0000001, 1_000_000)).toBe('￥0.1')
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
