import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { Wallet } from '../index'

let client: QueryClient
let empty = false
beforeEach(() => {
  empty = false
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'wallet-user', role: 1 })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.startsWith('/api/log/self?')) {
      const page = new URLSearchParams(url.split('?')[1]).get('p')
      return {
        data: {
          success: true,
          data: {
            total: empty ? 0 : 11,
            items: empty
              ? []
              : [
                  {
                    id: 1,
                    user_id: 1,
                    content: `adjustment-page-${page}`,
                    created_at: 1700000000,
                    type: 1,
                    other: JSON.stringify({
                      op: {
                        action: 'user.quota_add',
                        params: { from: 500000, to: 1000000, quota: 500000 },
                      },
                    }),
                  },
                ],
          },
        },
      }
    }
    if (url === '/api/user/topup/info') {
      return {
        data: {
          success: true,
          data: { pay_methods: [], amount_options: [], discount: {} },
        },
      }
    }
    if (url === '/api/user/self') {
      return {
        data: {
          success: true,
          data: {
            id: 1,
            username: 'wallet-user',
            quota: 100,
            used_quota: 0,
            request_count: 0,
          },
        },
      }
    }
    if (url === '/api/user/aff') {
      return { data: { success: true, data: 'invite' } }
    }
    if (url === '/api/subscription/plans') {
      return { data: { success: true, data: [] } }
    }
    return { data: { success: true, data: {} } }
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: '1' },
  })
})
afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
})
async function renderWallet() {
  const router = createRouter({
    routeTree: createRootRoute({ component: Wallet }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
test('wallet replaces self-service funding with administrator instructions', async () => {
  await renderWallet()
  expect(
    await screen.findByText('Please contact the administrator to top up')
  ).toBeVisible()
  expect(screen.queryByText('Add Funds')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Redeem' })
  ).not.toBeInTheDocument()
  expect(api.post).not.toHaveBeenCalled()
})
test('wallet shows transaction records inline and supports pagination and refresh', async () => {
  await renderWallet()
  expect(await screen.findByText('Administrator manual top-up')).toBeVisible()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(
    screen.queryByPlaceholderText('Search by order number...')
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Payment Method')).not.toBeInTheDocument()
  expect(screen.getByRole('table')).toBeVisible()
  expect(
    screen.getAllByRole('columnheader').map((cell) => cell.textContent)
  ).toEqual(['Time', 'Type', 'Top-up description', 'Amount', 'Account balance'])
  expect(screen.getByRole('cell', { name: 'Top-up' })).toBeVisible()
  expect(screen.getByRole('cell', { name: '$1' })).toBeVisible()
  expect(screen.getByRole('cell', { name: '$2' })).toBeVisible()
  expect(api.get).toHaveBeenCalledWith(
    expect.stringMatching(/\/api\/log\/self\?.*type=1/)
  )
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  await userEvent
    .setup()
    .click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(api.get).toHaveBeenCalledWith(
      expect.stringMatching(/\/api\/log\/self\?p=2/)
    )
  )
  expect(await screen.findByText('Administrator manual top-up')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
  await userEvent.setup().click(screen.getByRole('combobox'))
  await userEvent.setup().click(screen.getByRole('option', { name: '20' }))
  await waitFor(() =>
    expect(api.get).toHaveBeenCalledWith(
      expect.stringMatching(/\/api\/log\/self\?p=1&page_size=20/)
    )
  )
  expect(await screen.findByText('Administrator manual top-up')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
  empty = true
  await userEvent.setup().click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('No billing records found')).toBeVisible()
  await waitFor(() =>
    expect(screen.queryByText('adjustment-page-2')).not.toBeInTheDocument()
  )
})
