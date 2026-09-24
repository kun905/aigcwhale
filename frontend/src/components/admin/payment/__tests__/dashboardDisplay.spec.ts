import { describe, expect, it } from 'vitest'
import type { DashboardStats } from '@/types/payment'
import { PAYMENT_DASHBOARD_DEMO_TOTALS, paymentDashboardDisplay } from '../dashboardDisplay'

function statsFactory(): DashboardStats {
  return {
    today_amount: { CNY: 60.36 },
    total_amount: { CNY: 985.88 },
    avg_amount: { CNY: 32.86 },
    today_count: 2,
    total_count: 30,
    daily_series: [
      { date: '2026-09-16', amount: { CNY: 925.52 }, count: 28 },
      { date: '2026-09-17', amount: { CNY: 60.36 }, count: 2 },
    ],
    payment_methods: [
      { type: 'alipay', amount: { CNY: 311.86 }, count: 13 },
      { type: 'wxpay', amount: { CNY: 674.02 }, count: 17 },
    ],
    top_users: {
      CNY: [
        { user_id: 1, email: 'first@example.test', amount: 402.4 },
        { user_id: 2, email: 'second@example.test', amount: 206.23 },
      ],
    },
  }
}

const cents = (amount: number) => Math.round(amount * 100)

describe('payment dashboard demo display', () => {
  it.each([[7, 1521], [30, 10969], [90, 32907]])('reconciles the %i-day total, curve, methods and average to %i', (days, target) => {
    const source = statsFactory()
    const display = paymentDashboardDisplay(source, days, true)

    expect(PAYMENT_DASHBOARD_DEMO_TOTALS[days]).toBe(target)
    expect(display.total_amount.CNY).toBe(target)
    expect(display.daily_series.reduce((sum, day) => sum + cents(day.amount.CNY), 0)).toBe(target * 100)
    expect(display.payment_methods.reduce((sum, method) => sum + cents(method.amount.CNY), 0)).toBe(target * 100)
    expect(display.today_amount.CNY).toBe(display.daily_series[1].amount.CNY)
    expect(display.avg_amount.CNY).toBe(target / source.total_count)
    expect(display.today_count).toBe(2)
    expect(display.total_count).toBe(30)
    expect(display.daily_series.map(day => day.count)).toEqual([28, 2])
    expect(display.daily_series.map(day => day.date)).toEqual(source.daily_series.map(day => day.date))
    expect(display.payment_methods.map(method => method.count)).toEqual([13, 17])
    expect(display.top_users.CNY.map(({ user_id, email }) => ({ user_id, email }))).toEqual(source.top_users.CNY.map(({ user_id, email }) => ({ user_id, email })))
    expect(display.top_users.CNY[0].amount).toBeGreaterThan(display.top_users.CNY[1].amount)
    const rankedTotal = source.top_users.CNY.reduce((sum, user) => sum + user.amount, 0)
    expect(display.top_users.CNY.reduce((sum, user) => sum + cents(user.amount), 0)).toBe(cents(rankedTotal / source.total_amount.CNY * target))
  })

  it('allocates rounding remainders without changing zero days or ranking order', () => {
    const source = statsFactory()
    source.total_amount.CNY = 3
    source.total_count = 3
    source.daily_series = [0, 1, 1, 1].map((amount, index) => ({ date: `2026-09-${index + 10}`, amount: { CNY: amount }, count: amount }))
    source.payment_methods = [1, 1, 1].map((amount, index) => ({ type: `method${index}`, amount: { CNY: amount }, count: 1 }))
    source.top_users.CNY = [1, 1, 1].map((amount, index) => ({ user_id: index, email: `${index}@example.test`, amount }))

    const display = paymentDashboardDisplay(source, 30, true)
    expect(display.daily_series.map(day => day.amount.CNY)).toEqual([0, 3656.34, 3656.33, 3656.33])
    expect(display.payment_methods.map(method => method.amount.CNY)).toEqual([3656.34, 3656.33, 3656.33])
    expect(display.top_users.CNY.map(user => user.amount)).toEqual([3656.34, 3656.33, 3656.33])
  })

  it('keeps today and overlapping daily windows consistent across complete periods', () => {
    const source = statsFactory()
    source.today_amount = { CNY: 1 }
    source.today_count = 1
    const series = Array.from({ length: 90 }, (_, index) => ({
      date: new Date(Date.UTC(2026, 5, 20 + index)).toISOString().slice(0, 10),
      amount: { CNY: 1 }, count: 1,
    }))
    const displays = [7, 30, 90].map(days => paymentDashboardDisplay({
      ...source, total_amount: { CNY: days }, total_count: days, daily_series: series.slice(-days),
    }, days, true))
    expect(displays[1].daily_series.slice(-7)).toEqual(displays[0].daily_series)
    expect(displays[2].daily_series.slice(-30)).toEqual(displays[1].daily_series)
    expect(displays[1].today_amount).toEqual(displays[0].today_amount)
    expect(displays[2].today_amount).toEqual(displays[0].today_amount)
    for (const display of displays) {
      expect(display.daily_series.reduce((sum, day) => sum + cents(day.amount.CNY), 0)).toBe(cents(display.total_amount.CNY))
    }
  })

  it('preserves raw API data across refreshes and range switches', () => {
    const source = statsFactory()
    const original = JSON.parse(JSON.stringify(source))
    const first = paymentDashboardDisplay(source, 30, true)
    paymentDashboardDisplay(source, 7, true)
    const second = paymentDashboardDisplay(source, 30, true)

    expect(source).toEqual(original)
    expect(second).toEqual(first)
    expect(first).not.toBe(source)
    expect(first.top_users.CNY[0]).not.toBe(source.top_users.CNY[0])
    expect(first.daily_series[0]).not.toBe(source.daily_series[0])
    expect(first.payment_methods[0]).not.toBe(source.payment_methods[0])
  })

  it('leaves other currencies untouched and respects per-currency average counts', () => {
    const source = statsFactory()
    source.total_amount = { CNY: 15, USD: 10 }
    source.today_amount = { CNY: 10, USD: 10 }
    source.avg_amount = { CNY: 7.5, USD: 10 }
    source.total_count = 3
    source.daily_series = [{ date: '2026-09-17', amount: { CNY: 15, USD: 10 }, count: 3 }]
    source.payment_methods = [{ type: 'stripe', amount: { CNY: 15, USD: 10 }, count: 3 }]
    source.top_users.USD = [{ user_id: 1, email: 'first@example.test', amount: 10 }]

    const display = paymentDashboardDisplay(source, 30, true)
    expect(display.total_amount).toEqual({ CNY: 10969, USD: 10 })
    expect(display.today_amount.USD).toBe(10)
    expect(display.avg_amount).toEqual({ CNY: 5484.5, USD: 10 })
    expect(display.daily_series[0].amount).toEqual({ CNY: 10969, USD: 10 })
    expect(display.payment_methods[0].amount).toEqual({ CNY: 10969, USD: 10 })
    expect(display.top_users.USD).toEqual(source.top_users.USD)
  })

  it('restores all original values when demo display is disabled', () => {
    const source = statsFactory()
    expect(paymentDashboardDisplay(source, 30, false)).toBe(source)
  })

  it.each([7, 30, 90])('uses raw API values by default for %i days', days => {
    const source = statsFactory()
    expect(paymentDashboardDisplay(source, days)).toBe(source)
  })

  it.each([0, 14, 365])('leaves unsupported period %i unchanged', days => {
    const source = statsFactory()
    expect(paymentDashboardDisplay(source, days, true)).toBe(source)
  })

  it('does not fabricate revenue for empty periods or absent CNY', () => {
    const empty = {
      today_amount: {}, total_amount: { CNY: 0 }, avg_amount: { CNY: 0 },
      today_count: 0, total_count: 0,
      daily_series: null, payment_methods: null, top_users: null,
    } as unknown as DashboardStats
    expect(paymentDashboardDisplay(empty, 7, true)).toBe(empty)
    empty.total_amount = {}
    expect(paymentDashboardDisplay(empty, 30, true)).toBe(empty)
    empty.total_amount = { USD: 10 }
    empty.total_count = 1
    expect(paymentDashboardDisplay(empty, 90, true)).toBe(empty)
  })

  it('handles optional breakdowns missing from the response', () => {
    const source = { ...statsFactory(), daily_series: null, payment_methods: null, top_users: null } as unknown as DashboardStats
    const display = paymentDashboardDisplay(source, 30, true)
    expect(display.daily_series).toEqual([])
    expect(display.payment_methods).toEqual([])
    expect(display.top_users).toEqual({})
    expect(display.total_amount.CNY).toBe(10969)
  })
})
