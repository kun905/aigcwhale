import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DashboardStats } from '@/types/payment'
import AdminPaymentDashboardView from '../AdminPaymentDashboardView.vue'

const { getDashboard, showError } = vi.hoisted(() => ({
  getDashboard: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: { getDashboard },
  default: { getDashboard },
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<div><slot /></div>' },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

function statsForDays(days: number): DashboardStats {
  return {
    today_amount: { CNY: 1 },
    total_amount: { CNY: days },
    avg_amount: { CNY: 1 },
    today_count: 1,
    total_count: days,
    daily_series: [{ date: '2026-09-17', amount: { CNY: days }, count: days }],
    payment_methods: [{ type: 'alipay', amount: { CNY: days }, count: days }],
    top_users: { CNY: [{ user_id: 1, email: 'example@example.test', amount: days }] },
  }
}

function render() {
  return mount(AdminPaymentDashboardView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        OrderStatsCards: true,
        DailyRevenueChart: true,
        Icon: true,
        LoadingSpinner: true,
      },
    },
  })
}

function money(amount: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency: 'CNY' }).format(amount)
}

describe('AdminPaymentDashboardView demo amounts', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getDashboard.mockImplementation(async (days: number) => ({ data: statsForDays(days) }))
  })

  it('renders the requested totals and corresponding breakdowns for each period and refresh', async () => {
    const wrapper = render()
    await flushPromises()

    for (const [index, days, target] of [[1, 30, 10969], [0, 7, 1521], [2, 90, 32907]] as const) {
      await wrapper.findAll('button')[index].trigger('click')
      await flushPromises()

      expect(getDashboard).toHaveBeenLastCalledWith(days)
      const stats = wrapper.findComponent({ name: 'OrderStatsCards' }).props('stats')
      expect(stats.total_amount.CNY).toBe(target)
      expect(stats.total_count).toBe(days)
      expect(wrapper.findComponent({ name: 'DailyRevenueChart' }).props('data')[0]).toEqual({
        date: '2026-09-17', amount: { CNY: target }, count: days,
      })
      // Payment method breakdown and ranking both render the scaled amount.
      expect(wrapper.text().split(money(target))).toHaveLength(3)
    }

    await wrapper.get('button[title="common.refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.findComponent({ name: 'OrderStatsCards' }).props('stats').total_amount.CNY).toBe(32907)

    const indicator = wrapper.get('span[tabindex="0"]')
    expect(indicator.text()).toBe('demo')
    expect(indicator.classes()).toContain('text-[10px]')
    expect(indicator.classes()).toContain('opacity-50')
    expect(indicator.attributes('title')).toBe('payment.admin.displayDemoHint')
    expect(indicator.attributes('aria-label')).toBe('payment.admin.displayDemoHint')
    wrapper.unmount()
  })

  it('does not mutate the original API response', async () => {
    const original = statsForDays(30)
    getDashboard.mockResolvedValue({ data: original })
    const wrapper = render()
    await flushPromises()
    expect(original).toEqual(statsForDays(30))
    wrapper.unmount()
  })

  it('ignores an older response when the range is changed quickly', async () => {
    let resolveFirst!: (value: { data: DashboardStats }) => void
    getDashboard.mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
    const wrapper = render()
    await wrapper.findAll('button')[2].trigger('click')
    await flushPromises()
    resolveFirst({ data: statsForDays(30) })
    await flushPromises()

    expect(wrapper.findComponent({ name: 'OrderStatsCards' }).props('stats').total_amount.CNY).toBe(32907)
    wrapper.unmount()
  })

  it('clears the previous range if the newly selected range fails to load', async () => {
    const wrapper = render()
    await flushPromises()
    getDashboard.mockRejectedValueOnce(new Error('Unavailable'))
    await wrapper.findAll('button')[0].trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledOnce()
    expect(wrapper.findComponent({ name: 'OrderStatsCards' }).exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'LoadingSpinner' }).exists()).toBe(false)
    wrapper.unmount()
  })
})
