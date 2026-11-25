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
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { Route as ChannelsRoute } from '@/routes/_authenticated/channels'

import { ChannelsProvider } from '../channels-provider'
import { ChannelsTable } from '../channels-table'

const clients: QueryClient[] = []

beforeEach(() => {
  localStorage.clear()
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  localStorage.clear()
})

async function renderChannels() {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    return {
      data: {
        success: true,
        data: {
          items: [
            {
              id: 9,
              name: 'new-channel',
              type: 1,
              status: 1,
              models: 'model-a',
              group: 'default',
              priority: 0,
              weight: 0,
            },
          ],
          total: 30,
          type_counts: { '1': 30 },
        },
      },
    }
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const channels = createRoute({
    getParentRoute: () => auth,
    path: '/channels/',
    validateSearch: ChannelsRoute.options.validateSearch,
    component: () => (
      <ChannelsProvider>
        <ChannelsTable />
      </ChannelsProvider>
    ),
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([channels])]),
    history: createMemoryHistory({ initialEntries: ['/channels/'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByText('new-channel')
  return { get, router }
}

it('requests server-side ID ordering by default across pagination and search', async () => {
  const { get, router } = await renderChannels()
  expect(get).toHaveBeenCalledWith('/api/channel', {
    params: expect.objectContaining({ id_sort: true, p: 1 }),
  })

  await act(async () => {
    await router.navigate({ to: '/channels', search: { page: 2 } })
  })
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/channel', {
      params: expect.objectContaining({ id_sort: true, p: 2 }),
    })
  )

  await userEvent.type(
    screen.getByPlaceholderText('Filter by name, ID, or key...'),
    'new-channel'
  )
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/channel/search', {
      params: expect.objectContaining({
        id_sort: true,
        keyword: 'new-channel',
        p: 1,
      }),
    })
  )
})

it('preserves an explicitly disabled ID-sort preference', async () => {
  localStorage.setItem('channels-id-sort', 'false')
  const { get } = await renderChannels()
  expect(get).toHaveBeenCalledWith('/api/channel', {
    params: expect.objectContaining({ id_sort: false }),
  })
})
