import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { CommonLogsStats } from '../common-logs-stats'
import { UsageLogsProvider } from '../usage-logs-provider'

let client: QueryClient

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

test.each([1, 10])(
  'shows total tokens from the filtered statistics endpoint for role %s',
  async (role) => {
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    useAuthStore.getState().auth.setUser({ id: 1, username: 'viewer', role })
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: { quota: 0, rpm: 2, tpm: 50, total_tokens: 12345 },
      },
    })
    const root = createRootRoute()
    const auth = createRoute({
      getParentRoute: () => root,
      id: '_authenticated',
    })
    const logs = createRoute({
      getParentRoute: () => auth,
      path: '/usage-logs/$section',
      component: () => (
        <UsageLogsProvider>
          <CommonLogsStats />
        </UsageLogsProvider>
      ),
      validateSearch: (search: Record<string, unknown>) => search,
    })
    const router = createRouter({
      routeTree: root.addChildren([auth.addChildren([logs])]),
      history: createMemoryHistory({
        initialEntries: ['/usage-logs/common?model_name=model-a'],
      }),
    })
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    )
    expect(await screen.findByText('Total Tokens')).toBeVisible()
    expect(screen.getByText('12,345')).toBeVisible()
    const user = userEvent.setup()
    await user.hover(screen.getByText('RPM'))
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'RPM: Number of API requests in the last 60 seconds.'
    )
    await user.unhover(screen.getByText('RPM'))
    await user.hover(screen.getByText('TPM'))
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'TPM: Total input and output tokens processed in the last 60 seconds.'
    )
    expect(api.get).toHaveBeenCalledWith(
      expect.stringMatching(
        role === 10 ? /\/api\/log\/stat\?/ : /\/api\/log\/self\/stat\?/
      )
    )
  }
)
