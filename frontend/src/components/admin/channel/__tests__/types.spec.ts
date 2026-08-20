import { describe, expect, it } from 'vitest'
import {
  apiIntervalsToForm,
  apiTimePricingToForm,
  createDefaultTimePricingForm,
  formTimeVersionsToAPI,
  toZonedDatetimeLocal,
  formIntervalsToAPI,
  formTimePricingToAPI,
  isValidPositiveMultiplier,
  validateIntervals,
  validateTimeVersions,
  validateTimePricing,
  validateTimePricingCompatibility,
  type IntervalFormEntry,
  type PricingTimeVersionFormEntry,
  type TimePricingFormEntry,
  type TimePricingPeriodFormEntry,
} from '../types'

describe('interval multiplier conversion', () => {
  it('preserves component multipliers without MTok conversion', () => {
    const form = apiIntervalsToForm([{
      min_tokens: 272000,
      max_tokens: null,
      tier_label: '',
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_read_price: null,
      input_multiplier: 2,
      output_multiplier: 1.5,
      cache_write_multiplier: 2,
      cache_read_multiplier: 2,
      per_request_price: null,
      sort_order: 0,
    }])

    expect(form[0].input_multiplier).toBe(2)
    expect(form[0].output_multiplier).toBe(1.5)
    expect(formIntervalsToAPI(form)[0]).toMatchObject({
      input_multiplier: 2,
      output_multiplier: 1.5,
      cache_write_multiplier: 2,
      cache_read_multiplier: 2,
    })
  })
})

describe('positive multiplier validation', () => {
  it('accepts empty and positive values but rejects zero and negative values', () => {
    expect(isValidPositiveMultiplier(null)).toBe(true)
    expect(isValidPositiveMultiplier('')).toBe(true)
    expect(isValidPositiveMultiplier('0.5')).toBe(true)
    expect(isValidPositiveMultiplier(0)).toBe(false)
    expect(isValidPositiveMultiplier(-1)).toBe(false)
  })

  it('rejects a zero interval multiplier', () => {
    expect(validateIntervals([
      makeInterval({ min_tokens: 100, input_multiplier: 0 }),
    ], 'token', t)).toContain('multiplierPositive')
  })
})

function makeInterval(over: Partial<IntervalFormEntry>): IntervalFormEntry {
  return {
    min_tokens: 0,
    max_tokens: null,
    tier_label: '',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    input_multiplier: null,
    output_multiplier: null,
    cache_write_multiplier: null,
    cache_read_multiplier: null,
    per_request_price: null,
    sort_order: 0,
    ...over,
  }
}

function makeTimeVersion(over: Partial<PricingTimeVersionFormEntry> = {}): PricingTimeVersionFormEntry {
  return {
    effective_from: '2026-08-17T00:00',
    effective_until: null,
    timezone: 'Asia/Shanghai',
    default_multiplier: 0.5,
    input_price: 3,
    output_price: 9,
    cache_write_price: null,
    cache_read_price: 0.1,
    image_input_price: null,
    image_output_price: null,
    sort_order: 0,
    windows: [
      { label: 'peak', weekdays: 127, start_minute: 540, end_minute: 720, multiplier: 1, sort_order: 0 },
      { label: 'peak', weekdays: 127, start_minute: 840, end_minute: 1080, multiplier: 1, sort_order: 1 },
    ],
    ...over,
  }
}

function t(key: string, params?: Record<string, unknown>): string {
  return `${key}${params ? ` ${JSON.stringify(params)}` : ''}`
}

describe('validateIntervals', () => {
  describe('token mode', () => {
    it('rejects unbounded interval that is not last', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: null, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: 500000, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('unboundedLast')
    })

    it('accepts unbounded interval at the end', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: 200000, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: null, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toBeNull()
    })

    it('rejects overlapping intervals', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: 250000, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: 500000, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('overlap')
    })

    it('rejects unbounded interval in token mode', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: null, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 100, max_tokens: 200, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('unboundedLast')
    })
  })

  describe('image / per_request mode', () => {
    it('allows multiple unbounded tiers identified by label', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', per_request_price: 0.04 }),
        makeInterval({ tier_label: '2K', per_request_price: 0.06 }),
        makeInterval({ tier_label: '4K', per_request_price: 0.08 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toBeNull()
      expect(validateIntervals(intervals, 'per_request', t)).toBeNull()
    })

    it('still rejects negative prices', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', per_request_price: -1 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toContain('negativePrice')
    })

    it('still rejects max <= min on a single tier', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', min_tokens: 100, max_tokens: 50, per_request_price: 0.04 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toContain('maxGreaterThanMin')
    })
  })
})

describe('time pricing', () => {
  it('accepts the DeepSeek two-window schedule and converts MTok prices', () => {
    const version = makeTimeVersion()
    expect(validateTimeVersions([version], false, t)).toBeNull()
    const [api] = formTimeVersionsToAPI([version])
    expect(api.input_price).toBe(0.000003)
    expect(api.output_price).toBe(0.000009)
    expect(api.cache_read_price).toBe(0.0000001)
    expect(api.default_multiplier).toBe(0.5)
    expect(api.windows).toHaveLength(2)
    expect(api.effective_from).toBe('2026-08-16T16:00:00.000Z')
    expect(toZonedDatetimeLocal(api.effective_from, 'Asia/Shanghai')).toBe('2026-08-17T00:00')
  })

  it('rejects overlapping windows on shared weekdays', () => {
    const version = makeTimeVersion({
      windows: [
        { label: 'peak', weekdays: 127, start_minute: 540, end_minute: 720, multiplier: 1, sort_order: 0 },
        { label: 'peak', weekdays: 1, start_minute: 600, end_minute: 780, multiplier: 1, sort_order: 1 },
      ],
    })
    expect(validateTimeVersions([version], false, t)).toContain('windowOverlap')
  })

  it('rejects context intervals and overlapping effective ranges', () => {
    expect(validateTimeVersions([makeTimeVersion()], true, t)).toContain('intervalsConflict')
    const first = makeTimeVersion({ effective_until: '2026-08-18T00:00' })
    const second = makeTimeVersion({ effective_from: '2026-08-17T12:00' })
    expect(validateTimeVersions([first, second], false, t)).toContain('versionOverlap')
  })

  it('rejects enabling both scheduling formats on one price card', () => {
    expect(validateTimePricingCompatibility(
      [makeTimeVersion()],
      {
        timezone: 'Asia/Shanghai',
        periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }],
      },
      t,
    )).toContain('pricingSystemsConflict')
    expect(validateTimePricingCompatibility([makeTimeVersion()], createDefaultTimePricingForm(), t)).toBeNull()
  })

  it('uses a disabled Shanghai default', () => {
    const form = createDefaultTimePricingForm()
    expect(form).toEqual({ timezone: 'Asia/Shanghai', periods: [] })
    expect(formTimePricingToAPI(form)).toBeNull()
  })

  it('round-trips and formats multiplier', () => {
    const form = apiTimePricingToForm({
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00', end_time: '12:00', multiplier: 2 }],
    })
    expect(form.periods[0]).toEqual({
      start_time: '09:00:00',
      end_time: '12:00:00',
      multiplier: '2.00',
    })
    expect(formTimePricingToAPI(form)).toEqual({
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: 2 }],
    })
  })

  it.each([
    ['separated', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }, { start_time: '14:00:00', end_time: '18:00:00', multiplier: '2.00' }], null],
    ['adjacent', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }, { start_time: '12:00:00', end_time: '14:00:00', multiplier: '1.50' }], null],
    ['midnight split', [{ start_time: '22:00:00', end_time: '00:00:00', multiplier: '2.00' }, { start_time: '00:00:00', end_time: '02:00:00', multiplier: '2.00' }], null],
    ['overlap by one second', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }, { start_time: '11:59:59', end_time: '14:00:00', multiplier: '2.00' }], 'overlap'],
    ['cross midnight', [{ start_time: '22:00:00', end_time: '02:00:00', multiplier: '2.00' }], 'range'],
    ['equal midnight', [{ start_time: '00:00:00', end_time: '00:00:00', multiplier: '2.00' }], 'range'],
    ['missing seconds', [{ start_time: '09:00', end_time: '12:00', multiplier: '2.00' }], 'format'],
    ['zero', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '0.00' }], 'multiplier'],
    ['three decimals', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '1.001' }], 'multiplier'],
  ])('%s', (_name, periods, errorKey) => {
    const result = validateTimePricing({
      timezone: 'Asia/Shanghai',
      periods: periods as TimePricingPeriodFormEntry[],
    }, t)
    if (errorKey === null) expect(result).toBeNull()
    else expect(result).toContain(String(errorKey))
  })

  it('rejects non-IANA timezone', () => {
    expect(validateTimePricing({
      timezone: 'UTC+8',
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }],
    }, t)).toContain('timezone')
  })

  it.each([
    ['missing', undefined],
    ['blank', '   '],
  ])('rejects a %s timezone without throwing during conversion', (_name, timezone) => {
    const form = {
      timezone,
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }],
    } as unknown as TimePricingFormEntry

    expect(validateTimePricing(form, t)).toContain('timezone')
    expect(() => formTimePricingToAPI(form)).not.toThrow()
    expect(formTimePricingToAPI(form)?.timezone).toBe('')
  })
})
