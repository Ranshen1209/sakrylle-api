import { describe, expect, it } from 'vitest'
import { currencySymbol, formatPaymentAmount } from '../currency'

describe('formatPaymentAmount', () => {
  it('uses Sakrylle display currency while preserving currency fraction digits', () => {
    expect(formatPaymentAmount(100, 'JPY', 'en-US')).toBe('￥100')
    expect(formatPaymentAmount(100, 'JPY', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'KRW', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'HKD', 'en-US')).toBe('￥100.00')
  })
})

describe('currencySymbol', () => {
  it('uses the Sakrylle display-only symbol for every stored currency', () => {
    expect(currencySymbol('USD')).toBe('￥')
    expect(currencySymbol('cny')).toBe('￥')
    expect(currencySymbol('EUR')).toBe('￥')
    expect(currencySymbol('')).toBe('￥')
    expect(currencySymbol('XYZ')).toBe('￥')
  })
})
