import type { UserPricingTimeVersion, UserPricingTimeWindow } from '@/api/channels'

export function formatPricingMinute(minute: number): string {
  if (minute === 1440) return '24:00'
  return `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
}

export function formatPricingWeekdays(mask: number, locale = 'zh-CN'): string {
  const chinese = locale.toLowerCase().startsWith('zh')
  if (mask === 127) return chinese ? '每日' : 'Daily'
  const dayLabels = chinese
    ? ['一', '二', '三', '四', '五', '六', '日']
    : ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
  const selected = dayLabels.filter((_, index) => (mask & (1 << index)) !== 0)
  return chinese ? `周${selected.join('、')}` : selected.join(', ')
}

export function formatPricingWindow(window: UserPricingTimeWindow, locale = 'zh-CN'): string {
  return `${formatPricingWeekdays(window.weekdays, locale)} ${formatPricingMinute(window.start_minute)}-${formatPricingMinute(window.end_minute)}`
}

export function formatPricingVersionDate(version: UserPricingTimeVersion, locale?: string): string {
  const date = new Date(version.effective_from)
  if (Number.isNaN(date.getTime())) return version.effective_from
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  }).format(date)
}
