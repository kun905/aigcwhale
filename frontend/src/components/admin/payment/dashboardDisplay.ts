import type { CurrencyAmounts, DailyPaymentStats, DashboardStats } from '@/types/payment'

// Set to false to restore API values and hide the demo indicator.
export const PAYMENT_DASHBOARD_DEMO_ENABLED: boolean = true
export const PAYMENT_DASHBOARD_DEMO_TOTALS: Readonly<Record<number, number>> = {
  7: 1521,
  30: 10969,
  90: 32907,
}

// Largest-remainder allocation keeps every displayed breakdown exact to the cent.
function allocateAmounts(amounts: number[], total: number): number[] {
  const sum = amounts.reduce((value, amount) => value + amount, 0)
  if (sum <= 0) return amounts.map(() => 0)
  const totalCents = Math.round(total * 100)
  const shares = amounts.map(amount => amount / sum * totalCents)
  const cents = shares.map(Math.floor)
  const remainder = totalCents - cents.reduce((value, amount) => value + amount, 0)
  const indices = amounts.map((_, index) => index)
    .sort((left, right) => (shares[right] - cents[right]) - (shares[left] - cents[left]))
  for (let index = 0; index < remainder; index++) cents[indices[index]]++
  return cents.map(amount => amount / 100)
}

function allocateBreakdown<T extends { amount: CurrencyAmounts }>(rows: T[], total: number): T[] {
  const amounts = allocateAmounts(rows.map(row => row.amount.CNY || 0), total)
  return rows.map((row, index) => ({
    ...row,
    amount: 'CNY' in row.amount ? { ...row.amount, CNY: amounts[index] } : { ...row.amount },
  }))
}

function allocateDailySeries(rows: DailyPaymentStats[], days: number, target: number): DailyPaymentStats[] {
  const periods = [7, 30, 90].filter(period => period <= days)
  const segments = periods.map((period, index) => {
    const previous = periods[index - 1] || 0
    return {
      rows: rows.slice(days - period, days - previous),
      total: PAYMENT_DASHBOARD_DEMO_TOTALS[period] - (PAYMENT_DASHBOARD_DEMO_TOTALS[previous] || 0),
    }
  })
  // Match overlapping windows when all historical segments have source revenue.
  if (rows.length === days && segments.every(segment => segment.rows.some(row => (row.amount.CNY || 0) > 0))) {
    return segments.reverse().flatMap(segment => allocateBreakdown(segment.rows, segment.total))
  }
  return allocateBreakdown(rows, target)
}

export function paymentDashboardDisplay(
  source: DashboardStats,
  days = 30,
  enabled = PAYMENT_DASHBOARD_DEMO_ENABLED,
): DashboardStats {
  const target = PAYMENT_DASHBOARD_DEMO_TOTALS[days]
  const originalTotal = source.total_amount.CNY
  if (!enabled || !target || !Number.isFinite(originalTotal) || originalTotal <= 0 || source.total_count <= 0) {
    return source
  }

  const factor = target / originalTotal
  const dailySeries = allocateDailySeries(source.daily_series || [], days, target)
  const todayAmount = { ...source.today_amount }
  if ('CNY' in todayAmount) {
    todayAmount.CNY = Number((todayAmount.CNY * factor).toFixed(2))
    const lastDay = source.daily_series?.at(-1)
    if (lastDay && lastDay.amount.CNY === source.today_amount.CNY && lastDay.count === source.today_count) {
      todayAmount.CNY = dailySeries.at(-1)!.amount.CNY
    }
  }

  const averageAmount = { ...source.avg_amount }
  // Mixed-currency order counts are not exposed; preserve the API's per-currency basis.
  averageAmount.CNY = Object.keys(source.total_amount).length === 1
    ? target / source.total_count
    : (source.avg_amount.CNY || 0) * factor

  const topUsers = { ...source.top_users }
  if (topUsers.CNY?.length) {
    const rankedTotal = topUsers.CNY.reduce((sum, user) => sum + user.amount, 0)
    // The API returns only the top users, who may account for less than the total.
    const amounts = allocateAmounts(topUsers.CNY.map(user => user.amount), Math.min(target, rankedTotal * factor))
    topUsers.CNY = topUsers.CNY.map((user, index) => ({ ...user, amount: amounts[index] }))
  }

  return {
    ...source,
    today_amount: todayAmount,
    total_amount: { ...source.total_amount, CNY: target },
    avg_amount: averageAmount,
    daily_series: dailySeries,
    payment_methods: allocateBreakdown(source.payment_methods || [], target),
    top_users: topUsers,
  }
}
