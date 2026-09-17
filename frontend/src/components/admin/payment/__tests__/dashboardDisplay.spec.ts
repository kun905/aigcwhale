import { describe, expect, it } from 'vitest'
import type { DashboardStats } from '@/types/payment'
import { PAYMENT_DASHBOARD_DISPLAY_MULTIPLIER, paymentDashboardDisplay } from '../dashboardDisplay'

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

describe('payment dashboard display', () => {
  it('scales every monetary breakdown, but not counts, dates, users or ranking order', () => {
    const source = statsFactory()
    const display = paymentDashboardDisplay(source)

    expect(PAYMENT_DASHBOARD_DISPLAY_MULTIPLIER).toBe(50)
    expect(display.today_amount.CNY).toBeCloseTo(3018)
    expect(display.total_amount.CNY).toBeCloseTo(49294)
    expect(display.today_count).toBe(2)
    expect(display.total_count).toBe(30)
    expect(display.daily_series.map(day => day.count)).toEqual([28, 2])
    expect(display.daily_series.map(day => day.date)).toEqual(source.daily_series.map(day => day.date))
    expect(display.daily_series[1].amount.CNY).toBeCloseTo(3018)
    expect(display.daily_series.reduce((sum, day) => sum + day.amount.CNY, 0)).toBeCloseTo(49294)
    expect(display.payment_methods.map(method => method.count)).toEqual([13, 17])
    expect(display.payment_methods[0].amount.CNY).toBeCloseTo(15593)
    expect(display.payment_methods[1].amount.CNY).toBeCloseTo(33701)
    expect(display.top_users.CNY).toEqual([
      { user_id: 1, email: 'first@example.test', amount: 20120 },
      { user_id: 2, email: 'second@example.test', amount: 10311.5 },
    ])
  })

  it('recomputes the single-currency average before display rounding', () => {
    expect(paymentDashboardDisplay(statsFactory()).avg_amount.CNY.toFixed(2)).toBe('1643.13')
  })

  it('makes the new display five times the previous 10x display', () => {
    const source = statsFactory()
    const previous = paymentDashboardDisplay(source, 10)
    const current = paymentDashboardDisplay(source)
    expect(current.total_amount.CNY).toBeCloseTo(previous.total_amount.CNY * 5)
    expect(current.today_amount.CNY).toBeCloseTo(previous.today_amount.CNY * 5)
    expect(current.avg_amount.CNY).toBeCloseTo(previous.avg_amount.CNY * 5)
    expect(current.top_users.CNY[0].amount).toBeCloseTo(previous.top_users.CNY[0].amount * 5)
  })

  it('never modifies API data or compounds the multiplier on repeated rendering', () => {
    const source = statsFactory()
    const original = JSON.parse(JSON.stringify(source))
    const first = paymentDashboardDisplay(source)
    const second = paymentDashboardDisplay(source)

    expect(source).toEqual(original)
    expect(second).toEqual(first)
    expect(first).not.toBe(source)
    expect(first.top_users.CNY[0]).not.toBe(source.top_users.CNY[0])
    expect(first.daily_series[0]).not.toBe(source.daily_series[0])
    expect(first.payment_methods[0]).not.toBe(source.payment_methods[0])
  })

  it('keeps currencies separate without using the overall order count for mixed-currency averages', () => {
    const source = statsFactory()
    source.total_amount = { CNY: 15, USD: 10 }
    source.today_amount = { CNY: 10, USD: 10 }
    source.avg_amount = { CNY: 7.5, USD: 10 }
    source.total_count = 3
    source.daily_series = [{ date: '2026-09-17', amount: { CNY: 15, USD: 10 }, count: 3 }]
    source.payment_methods = [{ type: 'stripe', amount: { CNY: 15, USD: 10 }, count: 3 }]
    source.top_users.USD = [{ user_id: 1, email: 'first@example.test', amount: 10 }]

    const display = paymentDashboardDisplay(source)
    expect(display.total_amount).toEqual({ CNY: 750, USD: 500 })
    expect(display.today_amount).toEqual({ CNY: 500, USD: 500 })
    expect(display.avg_amount).toEqual({ CNY: 375, USD: 500 })
    expect(display.daily_series[0].amount).toEqual({ CNY: 750, USD: 500 })
    expect(display.payment_methods[0].amount).toEqual({ CNY: 750, USD: 500 })
    expect(display.top_users.USD[0].amount).toBe(500)
  })

  it('restores the exact original values when the multiplier is 1', () => {
    const source = statsFactory()
    expect(paymentDashboardDisplay(source, 1)).toBe(source)
    expect(paymentDashboardDisplay(source, 1).avg_amount.CNY).toBe(32.86)
  })

  it('supports empty periods, zero totals and absent optional breakdowns', () => {
    const empty = {
      today_amount: {}, total_amount: { CNY: 0 }, avg_amount: { CNY: 0 },
      today_count: 0, total_count: 0,
      daily_series: null, payment_methods: null, top_users: null,
    } as unknown as DashboardStats
    expect(paymentDashboardDisplay(empty)).toEqual({
      ...empty, daily_series: [], payment_methods: [], top_users: {},
    })
  })
})
