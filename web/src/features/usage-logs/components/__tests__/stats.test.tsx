import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { formatLogQuota } from '@/lib/format'
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
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
      const query = new URL(String(url), 'http://localhost').searchParams
      const empty = query.get('request_id') === 'missing'
      return {
        data: {
          success: true,
          data: {
            quota: empty ? 0 : 500000,
            rpm: empty ? 0 : 2,
            tpm: empty ? 0 : 50,
            total_tokens: empty ? 0 : 12345,
          },
        },
      }
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
        initialEntries: [
          '/usage-logs/common?model=model-a&requestId=request-selected&type=%5B%222%22%5D&token=key-a&group=default&username=owner&channel=2&startTime=1730000000000&endTime=1730086400000&page=3&pageSize=1',
        ],
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
    expect(screen.getByText(formatLogQuota(500000))).toBeVisible()
    const query = new URL(String(get.mock.calls[0][0]), 'http://localhost')
      .searchParams
    expect(Object.fromEntries(query)).toMatchObject({
      type: '2',
      model_name: 'model-a',
      request_id: 'request-selected',
      token_name: 'key-a',
      group: 'default',
      start_timestamp: '1730000000',
      end_timestamp: '1730086400',
    })
    expect(query.get('username')).toBe(role === 10 ? 'owner' : null)
    expect(query.get('channel')).toBe(role === 10 ? '2' : null)
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

    await act(async () => {
      await router.navigate({
        to: '/usage-logs/$section',
        params: { section: 'common' },
        search: { ...router.state.location.search, requestId: 'missing' },
      })
    })
    await waitFor(() => {
      expect(screen.queryByText('12,345')).not.toBeInTheDocument()
      expect(screen.getByText(formatLogQuota(0))).toBeVisible()
    })
    expect(
      new URL(
        String(get.mock.calls.at(-1)?.[0]),
        'http://localhost'
      ).searchParams.get('request_id')
    ).toBe('missing')
  }
)
