export const DEFAULT_PAYMENT_CURRENCY = 'CNY'
export const SAKRYLLE_DISPLAY_CURRENCY_SYMBOL = '￥'

export function normalizePaymentCurrency(currency?: string | null): string {
  const normalized = String(currency || '').trim().toUpperCase()
  return /^[A-Z]{3}$/.test(normalized) ? normalized : DEFAULT_PAYMENT_CURRENCY
}

export function currencySymbol(_currency?: string | null): string {
  return SAKRYLLE_DISPLAY_CURRENCY_SYMBOL
}

function paymentCurrencyFractionDigits(currency: string): number {
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency,
    }).resolvedOptions().maximumFractionDigits ?? 2
  } catch {
    return 2
  }
}

export function formatPaymentAmount(amount: number, currency?: string | null, locale?: string): string {
  const normalized = normalizePaymentCurrency(currency)
  const fractionDigits = paymentCurrencyFractionDigits(normalized)
  const numericAmount = Number.isFinite(amount) ? amount : 0
  try {
    const formatted = new Intl.NumberFormat(locale || undefined, {
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
    }).format(numericAmount)
    return `${SAKRYLLE_DISPLAY_CURRENCY_SYMBOL}${formatted}`
  } catch {
    return `${SAKRYLLE_DISPLAY_CURRENCY_SYMBOL}${numericAmount.toFixed(fractionDigits)}`
  }
}
