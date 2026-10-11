import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  getBillingSectionContent,
  type BillingSectionId,
} from '../billing/section-registry'
import { SettingsPageProvider } from '../components/settings-page-context'
import type { BillingSettings } from '../types'

const settings = {
  QuotaForNewUser: 0,
  QuotaForInviter: 0,
  QuotaForInvitee: 0,
  TopUpLink: '',
  'quota_setting.trust_quota_usd': 10,
  'quota_setting.pre_consume_multiplier': 1,
  'quota_setting.enable_free_model_pre_consume': true,
  PayAddress: '',
  EpayId: '',
  EpayKey: '',
  Price: 7,
  MinTopUp: 1,
  CustomCallbackAddress: '',
  PayMethods: '[]',
  'payment_setting.amount_options': '[]',
  'payment_setting.amount_discount': '{}',
  StripeApiSecret: '',
  StripeWebhookSecret: '',
  StripePriceId: '',
  StripeUnitPrice: 1,
  StripeMinTopUp: 1,
  StripePromotionCodesEnabled: false,
  CreemApiKey: '',
  CreemWebhookSecret: '',
  CreemTestMode: false,
  CreemProducts: '[]',
} as BillingSettings

async function renderSection(section: BillingSectionId) {
  function Fixture() {
    const [container, setContainer] = useState<HTMLDivElement | null>(null)
    return (
      <>
        <div ref={setContainer} />
        <SettingsPageProvider actionsContainer={container}>
          {getBillingSectionContent(section, settings)}
        </SettingsPageProvider>
      </>
    )
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({ component: Fixture }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
})

test('without compliance options payment controls accept changes and save', async () => {
  await renderSection('payment')
  const input = await screen.findByRole('spinbutton', {
    name: 'Price (local currency / USD)',
  })
  const user = userEvent.setup()
  expect(
    screen.queryByRole('button', { name: 'Confirm compliance' })
  ).not.toBeInTheDocument()
  await user.click(input)
  await user.keyboard('{Control>}a{/Control}8')
  await user.click(screen.getByRole('button', { name: 'Save all settings' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'Price',
      value: 8,
    })
  )
  expect(
    screen.queryByRole('button', { name: 'Confirm compliance' })
  ).not.toBeInTheDocument()
})

test('without compliance options invitation rewards save without a lock hint', async () => {
  await renderSection('quota')
  const input = await screen.findByRole('spinbutton', {
    name: 'Inviter Reward',
  })
  const user = userEvent.setup()
  await user.clear(input)
  await user.type(input, '100')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'QuotaForInviter',
      value: 100,
    })
  )
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})
