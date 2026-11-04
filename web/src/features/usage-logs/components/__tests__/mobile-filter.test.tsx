/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import {
  render,
  screen,
  waitFor,
  within,
  fireEvent,
  cleanup,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { Route as UsageLogsRoute } from '@/routes/_authenticated/usage-logs/$section'
import { useAuthStore } from '@/stores/auth-store'

import { buildSearchParams } from '../../lib/filter'
import { buildApiParams, fetchLogsByCategory } from '../../lib/utils'
import { CommonLogsFilterBar } from '../common-logs-filter-bar'
import { CompactDateTimeRangePicker } from '../compact-date-time-range-picker'
import { LogsFilterToolbar } from '../logs-filter-toolbar'
import { TaskLogsFilterBar } from '../task-logs-filter-bar'
import { UsageLogsProvider } from '../usage-logs-provider'

const captureDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'setPointerCapture'
)
beforeEach(() => {
  Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', {
    configurable: true,
    value: vi.fn(),
  })
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

function Fixture(props: { task?: boolean }) {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <UsageLogsProvider>
      {props.task ? (
        <TaskLogsFilterBar table={table} logCategory='task' />
      ) : (
        <CommonLogsFilterBar table={table} />
      )}
    </UsageLogsProvider>
  )
}
async function renderMobileFilter(task = false, mobile = true) {
  const original = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...original(query),
    matches: mobile && query === '(max-width: 640px)',
  }))
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/user/self/groups') {
      return { data: { success: true, data: {} } }
    }
    if (url === '/api/group/') {
      return { data: { success: true, data: [] } }
    }
    return {
      data: { success: true, data: { quota: 0, rpm: 0, tpm: 0 } },
    }
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: () => <Fixture task={task} />,
    validateSearch: UsageLogsRoute.options.validateSearch,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({
      initialEntries: [
        task
          ? '/usage-logs/task?page=3&filter=task-1&model=client-model&status=QUEUED'
          : '/usage-logs/common?page=3&type=%5B%222%22%5D&group=default&requestId=old&upstreamRequestId=old-upstream',
      ],
    }),
  })
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('button', { name: mobile ? 'Filter' : 'Search' })
  return router
}
afterEach(async () => {
  if (captureDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'setPointerCapture',
      captureDescriptor
    )
  } else Reflect.deleteProperty(HTMLElement.prototype, 'setPointerCapture')
  cleanup()
  vi.restoreAllMocks()
  vi.useRealTimers()
  useAuthStore.getState().auth.setUser(null)
  await i18next.changeLanguage('en')
})

it('applies the selected mobile date range directly and resets pagination while retaining filters', async () => {
  const router = await renderMobileFilter()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /^\d{4}-\d{2}/ }))
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-09-07T09:30' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-09-08T17:45' },
  })
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      page: 1,
      group: 'default',
      type: ['2'],
      startTime: new Date('2026-09-07T09:30').getTime(),
      endTime: new Date('2026-09-08T17:45').getTime(),
    })
  )
})

it('applies mobile drawer filters only when Search is pressed', async () => {
  const router = await renderMobileFilter()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Filter' }))
  const dialog = await screen.findByRole('dialog', { name: 'Filter' })
  await user.type(
    within(dialog).getByPlaceholderText('Model Name'),
    'gemini-3.7-flash'
  )
  expect(router.state.location.search).not.toHaveProperty('model')
  await user.click(within(dialog).getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      page: 1,
      model: 'gemini-3.7-flash',
    })
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('dialog', { name: 'Filter' })
    ).not.toBeInTheDocument()
  )
})

it.each([true, false])(
  'combines task model/status/ID filters and resets them (mobile=%s)',
  async (mobile) => {
    const router = await renderMobileFilter(true, mobile)
    const user = userEvent.setup()
    if (mobile) await user.click(screen.getByRole('button', { name: 'Filter' }))
    expect(screen.getByRole('textbox', { name: 'Model Name' })).toHaveValue(
      'client-model'
    )
    expect(screen.getByRole('textbox', { name: 'Task ID' })).toHaveValue(
      'task-1'
    )
    await user.click(screen.getByRole('combobox', { name: 'Status' }))
    const options = await screen.findAllByRole('option')
    expect(options.map((option) => option.textContent)).toEqual([
      'All',
      'Not Started',
      'Submitted',
      'Queued',
      'In Progress',
      'Success',
      'Failed',
      'Unknown',
    ])
    await user.click(screen.getByRole('option', { name: /^Success$/ }))
    const scope = mobile
      ? within(screen.getByRole('dialog', { name: 'Filter' }))
      : screen
    await user.click(scope.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        page: 1,
        filter: 'task-1',
        model: 'client-model',
        status: 'SUCCESS',
      })
    )
    await fetchLogsByCategory({
      logCategory: 'task',
      isAdmin: false,
      page: 1,
      pageSize: 20,
      searchParams: router.state.location.search,
      columnFilters: [],
    })
    expect(vi.mocked(api.get).mock.calls.at(-1)?.[0]).toContain(
      'model_name=client-model'
    )
    expect(vi.mocked(api.get).mock.calls.at(-1)?.[0]).toContain(
      'status=SUCCESS'
    )
    if (mobile) await user.click(screen.getByRole('button', { name: 'Filter' }))
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    await waitFor(() => {
      expect(router.state.location.search).not.toHaveProperty('model')
      expect(router.state.location.search).not.toHaveProperty('status')
      expect(router.state.location.search).not.toHaveProperty('filter')
    })
  }
)

it.each([
  { mobile: true, admin: false },
  { mobile: true, admin: true },
  { mobile: false, admin: false },
  { mobile: false, admin: true },
])(
  'queries an exact request ID while retaining filters and resets it (mobile=$mobile admin=$admin)',
  async ({ mobile, admin }) => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: admin ? 'admin' : 'user',
      role: admin ? 10 : 1,
    })
    const router = await renderMobileFilter(false, mobile)
    const user = userEvent.setup()
    if (mobile) await user.click(screen.getByRole('button', { name: 'Filter' }))
    const input = screen.getByRole('textbox', { name: 'Request ID' })
    expect(input).toHaveValue('old')
    expect(
      screen.queryByPlaceholderText('Upstream Request ID')
    ).not.toBeInTheDocument()
    await user.clear(input)
    await user.type(input, '  request-20261029-exact  ')
    expect(router.state.location.search).toMatchObject({
      page: 3,
      requestId: 'old',
    })
    if (mobile) {
      await user.click(
        within(screen.getByRole('dialog', { name: 'Filter' })).getByRole(
          'button',
          { name: 'Search' }
        )
      )
    } else {
      await user.keyboard('{Enter}')
    }
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        page: 1,
        requestId: 'request-20261029-exact',
        group: 'default',
        type: ['2'],
      })
    )
    const config = {
      logCategory: 'common' as const,
      isAdmin: admin,
      page: 1,
      pageSize: 20,
      searchParams: router.state.location.search,
      columnFilters: [],
    }
    await fetchLogsByCategory(config)
    const url = new URL(
      String(vi.mocked(api.get).mock.calls.at(-1)?.[0]),
      'http://localhost'
    )
    expect(url.pathname).toBe(admin ? '/api/log' : '/api/log/self')
    expect(url.searchParams.get('request_id')).toBe('request-20261029-exact')
    expect(url.searchParams.get('group')).toBe('default')
    expect(url.searchParams.get('type')).toBe('2')
    expect(url.searchParams.has('upstream_request_id')).toBe(false)
    await waitFor(() => {
      const statsCalls = vi
        .mocked(api.get)
        .mock.calls.filter(([path]) => path.includes('/stat?'))
      expect(statsCalls.length).toBeGreaterThan(0)
      for (const [path] of statsCalls) {
        expect(path).not.toContain('request_id=')
      }
    })
    if (mobile) await user.click(screen.getByRole('button', { name: 'Filter' }))
    expect(screen.getByRole('textbox', { name: 'Request ID' })).toHaveValue(
      'request-20261029-exact'
    )
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    await waitFor(() =>
      expect(router.state.location.search.requestId).toBeFalsy()
    )
    await fetchLogsByCategory({
      ...config,
      searchParams: router.state.location.search,
    })
    expect(vi.mocked(api.get).mock.calls.at(-1)?.[0]).not.toContain(
      'request_id='
    )
  }
)

it('restores the request ID draft when the URL changes without losing it on pagination', async () => {
  const router = await renderMobileFilter(false, false)
  const user = userEvent.setup()
  const input = screen.getByRole('textbox', { name: 'Request ID' })
  expect(input).toHaveValue('old')
  await user.clear(input)
  await user.type(input, 'unsent-draft')
  await router.navigate({
    to: '/usage-logs/$section',
    params: { section: 'common' },
    search: { ...router.state.location.search, page: 4 },
  })
  expect(input).toHaveValue('unsent-draft')
  await router.navigate({
    to: '/usage-logs/$section',
    params: { section: 'common' },
    search: { ...router.state.location.search, requestId: 'linked-request' },
  })
  await waitFor(() => expect(input).toHaveValue('linked-request'))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      page: 1,
      requestId: 'linked-request',
    })
  )
})

it('restores the request ID after Search followed by browser Back and Forward', async () => {
  const router = await renderMobileFilter(false, false)
  const user = userEvent.setup()
  const input = screen.getByRole('textbox', { name: 'Request ID' })
  await user.clear(input)
  await user.type(input, 'new-request')
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search.requestId).toBe('new-request')
  )
  router.history.back()
  await waitFor(() => {
    expect(router.state.location.search.requestId).toBe('old')
    expect(input).toHaveValue('old')
  })
  router.history.forward()
  await waitFor(() => {
    expect(router.state.location.search.requestId).toBe('new-request')
    expect(input).toHaveValue('new-request')
  })
})

it.each(['', '   ', undefined])(
  'omits an empty request ID (%j) and never forwards upstream ID filters',
  (requestId) => {
    const filters = {
      model: 'client-model',
      requestId,
      upstreamRequestId: 'old-upstream',
    }
    expect(buildSearchParams(filters, 'common')).toEqual({
      model: 'client-model',
    })
    const params = buildApiParams({
      page: 1,
      pageSize: 20,
      isAdmin: false,
      searchParams: filters,
    })
    expect(params).not.toHaveProperty('request_id')
    expect(params).not.toHaveProperty('upstream_request_id')
  }
)

it('keeps all quick actions visible without opening a menu', async () => {
  await renderMobileFilter()
  const user = userEvent.setup()
  for (const name of ['Hide', 'Filter', 'Search', 'View']) {
    expect(screen.getByRole('button', { name })).toBeVisible()
  }
  expect(screen.queryByRole('button', { name: 'More' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Hide' }))
  expect(screen.getByRole('button', { name: 'Show' })).toBeVisible()
  screen.getByRole('button', { name: 'Show' }).focus()
  for (const name of ['Filter', 'Search', 'View']) {
    await user.tab()
    expect(screen.getByRole('button', { name })).toHaveFocus()
  }
})

function LoadingFixture(props: { loading: boolean; onSearch: () => void }) {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <LogsFilterToolbar
      table={table}
      compactMobile
      mobilePinnedFilters={<span>Date Range</span>}
      primaryFilters={null}
      hasActiveFilters={false}
      onReset={() => {}}
      onSearch={props.onSearch}
      searchLoading={props.loading}
    />
  )
}

it('keeps Search visible while loading and prevents repeated searches', async () => {
  const original = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...original(query),
    matches: query === '(max-width: 640px)',
  }))
  const onSearch = vi.fn()
  const user = userEvent.setup()
  const view = render(<LoadingFixture loading onSearch={onSearch} />)
  const search = screen.getByRole('button', { name: 'Search' })
  expect(search).toBeVisible()
  expect(search).toBeDisabled()
  expect(search).toHaveAttribute('aria-busy', 'true')
  await user.click(search)
  expect(onSearch).not.toHaveBeenCalled()
  view.rerender(<LoadingFixture loading={false} onSearch={onSearch} />)
  expect(search).toBeEnabled()
  await user.click(search)
  expect(onSearch).toHaveBeenCalledTimes(1)
})

it('collapses only date and statistics while keeping the right-hand quick actions visible', async () => {
  await renderMobileFilter()
  const user = userEvent.setup()
  const date =
    screen
      .getByRole('button', { name: /^\d{4}-\d{2}/ })
      .getAttribute('aria-label') ?? ''
  expect(await screen.findByText('Usage')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Collapse' }))
  expect(screen.getByRole('button', { name: 'Expand' })).toHaveAttribute(
    'aria-expanded',
    'false'
  )
  expect(screen.queryByRole('button', { name: date })).not.toBeInTheDocument()
  expect(screen.queryByText('Usage')).not.toBeInTheDocument()
  const actions = screen.getByRole('group', { name: 'Actions' })
  for (const name of ['Hide', 'Filter', 'Search', 'View']) {
    expect(within(actions).getByRole('button', { name })).toBeVisible()
  }
  expect(
    within(actions).queryByRole('button', { name: 'Expand' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Expand' }))
  expect(screen.getByRole('button', { name: 'Collapse' })).toHaveAttribute(
    'aria-expanded',
    'true'
  )
  expect(screen.getByRole('button', { name: date })).toBeVisible()
  expect(await screen.findByText('Usage')).toBeVisible()
})

it.each([
  { language: 'zh', resources: zh.translation },
  { language: 'zh-TW', resources: zhTW.translation },
])(
  'labels the calendar-month preset as 本月 in $language and selects the complete month',
  async ({ language, resources }) => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date(2026, 8, 8, 12))
    i18next.addResourceBundle(language, 'translation', resources, true, true)
    await i18next.changeLanguage(language)
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <CompactDateTimeRangePicker
        start={new Date(2026, 7, 10)}
        end={new Date(2026, 8, 8)}
        onChange={onChange}
      />
    )
    await user.click(screen.getByRole('button', { name: /^2026/ }))
    const preset = screen.getByRole('button', { name: '本月' })
    expect(preset).toBeVisible()
    await user.click(preset)
    expect(onChange).toHaveBeenCalledWith({
      start: new Date(2026, 8, 1),
      end: new Date(2026, 8, 30, 23, 59, 59, 999),
    })
  }
)
