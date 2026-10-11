import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createTable, getCoreRowModel } from '@tanstack/react-table'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { DataTableRowActions } from '../components/data-table-row-actions'
import { SubscriptionsPrimaryButtons } from '../components/subscriptions-primary-buttons'
import {
  SubscriptionsProvider,
  useSubscriptions,
} from '../components/subscriptions-provider'
import { subscriptionPlanSchema, type PlanRecord } from '../types'

const record: PlanRecord = {
  plan: subscriptionPlanSchema.parse({
    id: 1,
    title: 'Monthly plan',
    price_amount: 10,
    duration_unit: 'month',
    duration_value: 1,
    quota_reset_period: 'monthly',
    enabled: true,
    sort_order: 0,
    max_purchase_per_user: 1,
    total_amount: 100,
  }),
}

function Fixture() {
  const subscriptions = useSubscriptions()
  const table = createTable<PlanRecord>({
    data: [record],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
    state: {},
    onStateChange: () => {},
    renderFallbackValue: null,
  })
  return (
    <>
      <SubscriptionsPrimaryButtons />
      <DataTableRowActions row={table.getRowModel().rows[0]} />
      <output aria-label='Selected action'>
        {subscriptions.open ?? 'none'}
      </output>
      <output aria-label='Selected plan'>
        {subscriptions.currentRow?.plan.title ?? 'none'}
      </output>
    </>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
})

test.each([
  ['Create Plan', 'create', 'none'],
  ['Edit', 'update', 'Monthly plan'],
  ['Reset subscription quota', 'reset-subscriptions', 'Monthly plan'],
  ['Disable', 'toggle-status', 'Monthly plan'],
])(
  'without compliance options, %s opens its action',
  async (label, action, title) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <SubscriptionsProvider>
          <Fixture />
        </SubscriptionsProvider>
      </QueryClientProvider>
    )
    const button = screen.getByRole('button', { name: label })
    expect(button).toBeEnabled()
    await userEvent.setup().click(button)
    expect(screen.getByLabelText('Selected action')).toHaveTextContent(action)
    expect(screen.getByLabelText('Selected plan')).toHaveTextContent(title)
    client.clear()
  }
)
