import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { User } from '../../types'
import { DataTableRowActions } from '../data-table-row-actions'
import { UserTopupRecordsDrawer } from '../user-topup-records-drawer'
import { UsersProvider } from '../users-provider'

const target: User = {
  id: 42,
  username: 'recharge-owner',
  display_name: '',
  role: 1,
  status: 1,
  quota: 1000000,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}
let client: QueryClient
let empty = false
let failed = false
let legacy = false

function RowActions(props: { user: User }) {
  const table = useReactTable({
    data: [props.user],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return <DataTableRowActions row={table.getRowModel().rows[0]} />
}

function renderActions(user = target) {
  return render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <RowActions user={user} />
      </UsersProvider>
    </QueryClientProvider>
  )
}

async function openRecords() {
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
  await userEvent.click(
    screen.getByRole('menuitem', { name: 'Top-up records' })
  )
  return screen.findByRole('dialog', { name: /Top-up records/ })
}

beforeEach(() => {
  empty = false
  failed = false
  legacy = false
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 100 })
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/user/42/topup-logs') {
      if (failed) {
        return { data: { success: false, message: 'Read unavailable' } }
      }
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
                    user_id: 42,
                    type: 1,
                    created_at: 1700000000,
                    content: 'manual adjustment',
                    other: legacy
                      ? '{}'
                      : JSON.stringify({
                          op: {
                            action: 'user.quota_add',
                            params: { from: 500000, to: 1000000 },
                          },
                        }),
                  },
                ],
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})

it('loads only the selected user after opening and displays the wallet record columns', async () => {
  renderActions()
  expect(api.get).not.toHaveBeenCalledWith(
    '/api/user/42/topup-logs',
    expect.anything()
  )
  const drawer = await openRecords()
  expect(
    within(drawer).getByRole('heading', { name: /recharge-owner/ })
  ).toBeVisible()
  const table = await within(drawer).findByRole('table', {
    name: 'Top-up records',
  })
  expect(
    within(table)
      .getAllByRole('columnheader')
      .map((cell) => cell.textContent)
  ).toEqual(['Time', 'Type', 'Top-up description', 'Amount', 'Account balance'])
  expect(within(table).getByRole('cell', { name: '$1' })).toBeVisible()
  expect(within(table).getByRole('cell', { name: '$2' })).toBeVisible()
  expect(within(table).getByText('Administrator manual top-up')).toBeVisible()
  expect(api.get).toHaveBeenCalledWith('/api/user/42/topup-logs', {
    params: { p: 1, page_size: 10 },
  })
  expect(
    screen.queryByPlaceholderText('Search by order number...')
  ).not.toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
})

it('paginates the selected user and resets page size changes to page one', async () => {
  renderActions()
  const drawer = await openRecords()
  await within(drawer).findByRole('table')
  await userEvent.click(
    within(drawer).getByRole('button', { name: 'Go to next page' })
  )
  await waitFor(() =>
    expect(api.get).toHaveBeenCalledWith('/api/user/42/topup-logs', {
      params: { p: 2, page_size: 10 },
    })
  )
  expect(
    await within(drawer).findByRole('button', { name: 'Go to next page' })
  ).toBeDisabled()
  await userEvent.click(within(drawer).getByRole('combobox'))
  await userEvent.click(screen.getByRole('option', { name: '20' }))
  await waitFor(() =>
    expect(api.get).toHaveBeenCalledWith('/api/user/42/topup-logs', {
      params: { p: 1, page_size: 20 },
    })
  )
  empty = true
  await userEvent.click(within(drawer).getByRole('button', { name: 'Refresh' }))
  expect(
    await within(drawer).findByText('No billing records found')
  ).toBeVisible()
  expect(within(drawer).queryByRole('table')).not.toBeInTheDocument()
})

it('shows a failed query and allows refresh to recover without showing stale records', async () => {
  failed = true
  renderActions()
  const drawer = await openRecords()
  expect(await within(drawer).findByRole('alert')).toHaveTextContent(
    'Failed to load billing history'
  )
  expect(within(drawer).queryByRole('table')).not.toBeInTheDocument()
  failed = false
  await userEvent.click(within(drawer).getByRole('button', { name: 'Refresh' }))
  expect(await within(drawer).findByRole('table')).toBeVisible()
})

it('preserves unrecorded amounts in historical records', async () => {
  legacy = true
  renderActions()
  const drawer = await openRecords()
  const table = await within(drawer).findByRole('table')
  expect(within(table).getAllByText('Not recorded')).toHaveLength(2)
})

it('clears the previous user records while another user loads and restarts at page one', async () => {
  const response = {
    data: {
      success: true,
      data: {
        total: 1,
        items: [
          {
            id: 1,
            user_id: 43,
            type: 1,
            created_at: 1700000000,
            content: '',
            other: '{}',
          },
        ],
      },
    },
  }
  let resolveResponse!: (value: typeof response) => void
  const pending = new Promise<typeof response>((resolve) => {
    resolveResponse = resolve
  })
  const get = vi.mocked(api.get)
  const nextUser = { id: 43, username: 'another-recharge-owner' }
  const view = render(
    <QueryClientProvider client={client}>
      <UserTopupRecordsDrawer open onOpenChange={() => {}} user={target} />
    </QueryClientProvider>
  )
  await screen.findByRole('table')
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/user/42/topup-logs', {
      params: { p: 2, page_size: 10 },
    })
  )
  get.mockImplementationOnce(() => pending)
  view.rerender(
    <QueryClientProvider client={client}>
      <UserTopupRecordsDrawer open onOpenChange={() => {}} user={nextUser} />
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/user/43/topup-logs', {
      params: { p: 1, page_size: 10 },
    })
  )
  const drawer = screen.getByRole('dialog', { name: /another-recharge-owner/ })
  expect(within(drawer).queryByRole('table')).not.toBeInTheDocument()
  expect(drawer.querySelector('[aria-busy="true"]')).toBeInTheDocument()
  resolveResponse(response)
  const table = await within(drawer).findByRole('table')
  expect(within(table).getAllByText('Not recorded')).toHaveLength(2)
})

it('does not offer recharge records when the administrator cannot manage the target role', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  renderActions({ ...target, role: 100 })
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
  expect(
    screen.queryByRole('menuitem', { name: 'Top-up records' })
  ).not.toBeInTheDocument()
})
