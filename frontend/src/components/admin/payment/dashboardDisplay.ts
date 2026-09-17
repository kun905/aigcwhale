import type { CurrencyAmounts, DashboardStats } from '@/types/payment'

// Set to 1 to restore the original dashboard and hide the indicator.
export const PAYMENT_DASHBOARD_DISPLAY_MULTIPLIER: number = 50

export function paymentDashboardDisplay(
  source: DashboardStats,
  multiplier = PAYMENT_DASHBOARD_DISPLAY_MULTIPLIER,
): DashboardStats {
  if (multiplier === 1) return source

  const scaleAmount = (amount: number): number => Number((amount * multiplier).toFixed(2))
  const scaleAmounts = (amounts: CurrencyAmounts): CurrencyAmounts =>
    Object.fromEntries(Object.entries(amounts).map(([currency, amount]) => [currency, scaleAmount(amount)]))

  const totalAmount = scaleAmounts(source.total_amount)
  const averageAmount = scaleAmounts(source.avg_amount)
  const currencies = Object.keys(totalAmount)
  // The API rounds averages before returning them. For a single currency,
  // recalculate from the total to avoid magnifying that rounding error.
  if (currencies.length === 1 && source.total_count > 0) {
    averageAmount[currencies[0]] = totalAmount[currencies[0]] / source.total_count
  }

  return {
    ...source,
    today_amount: scaleAmounts(source.today_amount),
    total_amount: totalAmount,
    avg_amount: averageAmount,
    daily_series: (source.daily_series || []).map(day => ({
      ...day,
      amount: scaleAmounts(day.amount),
    })),
    payment_methods: (source.payment_methods || []).map(method => ({
      ...method,
      amount: scaleAmounts(method.amount),
    })),
    top_users: Object.fromEntries(
      Object.entries(source.top_users || {}).map(([currency, users]) => [
        currency,
        users.map(user => ({ ...user, amount: scaleAmount(user.amount) })),
      ]),
    ),
  }
}
