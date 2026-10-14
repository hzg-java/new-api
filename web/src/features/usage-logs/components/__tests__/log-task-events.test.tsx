import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { UsageLog } from '../../data/schema'
import type { TaskEventsResponse } from '../../types'
import { DetailsDialog } from '../dialogs/details-dialog'

const log: UsageLog = {
  id: 1,
  user_id: 42,
  created_at: 1,
  type: 2,
  content: '',
  username: 'owner',
  token_name: '',
  model_name: 'video',
  quota: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  use_time: 0,
  is_stream: false,
  channel: 0,
  channel_name: '',
  request_id: '',
  upstream_request_id: '',
  token_id: 1,
  group: '',
  ip: '',
  other: JSON.stringify({ task_id: 'public/task &1' }),
}
const history: TaskEventsResponse = {
  success: true,
  data: [
    {
      kind: 'request',
      timestamp: 1700000000,
      payload: '{"prompt":"request-canary"}',
    },
    {
      kind: 'accepted',
      timestamp: 1700000001,
      payload: '{"status":"accepted-canary"}',
    },
    {
      kind: 'result',
      timestamp: 1700000002,
      payload: '{"output":"result-canary"}',
    },
  ],
}
let client: QueryClient

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(['status'], {}, { updatedAt: Date.now() + 60_000 })
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'viewer', role: ROLE.ADMIN })
})
afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

function details(
  overrides: Partial<React.ComponentProps<typeof DetailsDialog>> = {}
) {
  return (
    <QueryClientProvider client={client}>
      <DetailsDialog
        log={log}
        isAdmin
        isRoot={false}
        open
        onOpenChange={() => {}}
        {...overrides}
      />
    </QueryClientProvider>
  )
}

describe('task events in consumption details', () => {
  test('error details use a wide desktop dialog while retaining mobile bounds', () => {
    render(details({ log: { ...log, type: 5, other: '{}' } }))
    const dialog = screen.getByRole('dialog')
    expect(dialog.classList.contains('sm:max-w-4xl')).toBe(true)
    expect(dialog.classList.contains('lg:max-w-5xl')).toBe(true)
    expect(dialog.classList.contains('max-sm:w-[calc(100vw-1.5rem)]')).toBe(true)
  })

  test('failed submission without task ID displays saved request and error response', () => {
    const get = vi.spyOn(api, 'get')
    render(
      details({
        log: {
          ...log,
          type: 5,
          other: JSON.stringify({
            admin_info: {
              task_submission: {
                request: { prompt: 'failed-request-canary', n: 0 },
                response: {
                  status_code: 400,
                  body: { error: 'failed-response-canary' },
                },
              },
            },
          }),
        },
      })
    )
    expect(
      screen.getByRole('textbox', { name: 'User Request' })
    ).toHaveTextContent('failed-request-canary')
    expect(screen.getByRole('textbox', { name: 'Response' })).toHaveTextContent(
      'failed-response-canary'
    )
    expect(get).not.toHaveBeenCalled()
  })

  test('historical failed task submission marks missing bodies as not recorded', () => {
    render(
      details({
        log: {
          ...log,
          type: 5,
          other: JSON.stringify({
            admin_info: { task_plugin: { key: 'video' } },
          }),
        },
      })
    )
    expect(screen.getAllByText('Not recorded')).toHaveLength(2)
  })

  test('unavailable failed submission bodies show the recording limitation', () => {
    render(
      details({
        log: {
          ...log,
          type: 5,
          other: JSON.stringify({
            admin_info: {
              task_submission: {
                request: { unavailable: 'size_limit_exceeded' },
                response: { unavailable: 'write_failed' },
              },
            },
          }),
        },
      })
    )
    expect(screen.getAllByText('Not recorded')).toHaveLength(2)
    expect(
      screen.getByRole('textbox', { name: 'User Request' })
    ).toHaveTextContent('size_limit_exceeded')
    expect(screen.getByRole('textbox', { name: 'Response' })).toHaveTextContent(
      'write_failed'
    )
  })

  test('a request business field named unavailable does not imply missing recording', () => {
    render(
      details({
        log: {
          ...log,
          type: 5,
          other: JSON.stringify({
            admin_info: {
              task_submission: {
                request: { unavailable: false, prompt: 'recorded-canary' },
                response: { body: { error: 'recorded-error' } },
              },
            },
          }),
        },
      })
    )
    expect(screen.queryByText('Not recorded')).not.toBeInTheDocument()
    expect(
      screen.getByRole('textbox', { name: 'User Request' })
    ).toHaveTextContent('recorded-canary')
  })

  test('ordinary error logs do not display task submission fields', () => {
    render(details({ log: { ...log, type: 5, other: '{}' } }))
    expect(screen.queryByText('Not recorded')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('textbox', { name: 'User Request' })
    ).not.toBeInTheDocument()
  })

  test('failed submission does not expose bodies with non-admin detail props', () => {
    render(
      details({
        isAdmin: false,
        log: {
          ...log,
          type: 5,
          other: JSON.stringify({
            admin_info: {
              task_submission: {
                request: { prompt: 'hidden-failure-canary' },
                response: { error: 'hidden-response-canary' },
              },
            },
          }),
        },
      })
    )
    expect(screen.queryByText(/hidden-failure-canary/)).not.toBeInTheDocument()
    expect(screen.queryByText(/hidden-response-canary/)).not.toBeInTheDocument()
  })

  test('failed submission bodies disappear when administrator role is revoked', () => {
    const failedLog = {
      ...log,
      type: 5,
      other: JSON.stringify({
        admin_info: {
          task_submission: {
            request: { prompt: 'private-failed-request' },
            response: { body: { error: 'private-failed-response' } },
          },
        },
      }),
    }
    render(details({ log: failedLog }))
    expect(
      screen.getByRole('textbox', { name: 'User Request' })
    ).toHaveTextContent('private-failed-request')
    act(() =>
      useAuthStore
        .getState()
        .auth.setUser({ id: 7, username: 'viewer', role: ROLE.USER })
    )
    expect(screen.queryByText(/private-failed-request/)).not.toBeInTheDocument()
    expect(
      screen.queryByText(/private-failed-response/)
    ).not.toBeInTheDocument()
  })

  test('strict mode remount does not reuse an aborted task event request', async () => {
    const previousAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => ({
      data: history,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
    try {
      render(<StrictMode>{details()}</StrictMode>)
      expect(
        await screen.findByRole('textbox', { name: 'User Request' })
      ).toHaveTextContent('request-canary')
      expect(screen.queryByText('Failed to load logs')).not.toBeInTheDocument()
    } finally {
      api.defaults.adapter = previousAdapter
    }
  })
  test.each([ROLE.ADMIN, ROLE.SUPER_ADMIN])(
    'role %s requests public task and owner and displays only request/accepted',
    async (role) => {
      useAuthStore.getState().auth.setUser({ id: 7, username: 'viewer', role })
      const get = vi.spyOn(api, 'get').mockResolvedValue({ data: history })
      render(details({ isRoot: role === ROLE.SUPER_ADMIN }))
      expect(
        await screen.findByRole('textbox', { name: 'User Request' })
      ).toHaveTextContent('request-canary')
      expect(
        screen.getByRole('textbox', { name: 'Submission Accepted' })
      ).toHaveTextContent('accepted-canary')
      const url = new URL(String(get.mock.calls[0][0]), 'http://localhost')
      expect(url.pathname).toBe('/api/log/task-events')
      expect(url.searchParams.get('task_id')).toBe('public/task &1')
      expect(url.searchParams.get('user_id')).toBe('42')
      expect(
        screen.queryByRole('textbox', { name: 'Task Result' })
      ).not.toBeInTheDocument()
      expect(screen.queryByText(/result-canary/)).not.toBeInTheDocument()
    }
  )

  test.each([
    { isAdmin: false },
    { open: false },
    { log: { ...log, other: '{}' } },
    { log: { ...log, other: '{"task_id":"  "}' } },
    { log: { ...log, other: '{"task_id":123}' } },
    { log: { ...log, type: 6 } },
    { log: { ...log, user_id: 0 } },
  ])(
    'does not request or show events for ineligible details %j',
    async (overrides) => {
      const get = vi.spyOn(api, 'get').mockResolvedValue({ data: history })
      render(details(overrides))
      await act(async () => {})
      expect(get).not.toHaveBeenCalled()
      expect(
        screen.queryByRole('region', { name: 'Full Chain Events' })
      ).not.toBeInTheDocument()
    }
  )

  test('ordinary viewer cannot request even with stale administrator props', async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 7, username: 'viewer', role: ROLE.USER })
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: history })
    render(details())
    await act(async () => {})
    expect(get).not.toHaveBeenCalled()
    expect(screen.queryByText('Full Chain Events')).not.toBeInTheDocument()
  })

  test.each([{ data: [] }, { data: history.data?.slice(0, 1) ?? [] }])(
    'missing historical nodes are marked not recorded: %j',
    async ({ data }) => {
      vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data } })
      render(details())
      expect(await screen.findAllByText('Not recorded')).toHaveLength(
        data.length === 0 ? 2 : 1
      )
      expect(screen.getAllByText('Submission Accepted').length).toBeGreaterThan(
        0
      )
      expect(screen.getAllByText('User Request').length).toBeGreaterThan(0)
    }
  )

  test.each(['business', 'network'])(
    'shows error instead of empty history on %s failure',
    async (failure) => {
      const get = vi.spyOn(api, 'get')
      if (failure === 'business') {
        get.mockResolvedValue({ data: { success: false, message: 'denied' } })
      } else get.mockRejectedValue(new Error('offline'))
      render(details())
      expect(await screen.findByText('Failed to load logs')).toBeInTheDocument()
      expect(screen.queryByText('Not recorded')).not.toBeInTheDocument()
    }
  )

  test('record and owner switches show loading without previous payloads, including late responses', async () => {
    let resolveOld!: (value: { data: TaskEventsResponse }) => void
    const old = new Promise<{ data: TaskEventsResponse }>((resolve) => {
      resolveOld = resolve
    })
    const get = vi
      .spyOn(api, 'get')
      .mockReturnValueOnce(old)
      .mockResolvedValueOnce({ data: { success: true, data: [] } })
    const view = render(details())
    expect(screen.getByText('Loading')).toBeInTheDocument()
    view.rerender(
      details({ log: { ...log, user_id: 43, other: '{"task_id":"next"}' } })
    )
    expect(await screen.findAllByText('Not recorded')).toHaveLength(2)
    await act(async () => {
      resolveOld({ data: history })
      await old
    })
    expect(screen.queryByText(/request-canary/)).not.toBeInTheDocument()
    expect(
      new URL(
        String(get.mock.calls[1][0]),
        'http://localhost'
      ).searchParams.get('user_id')
    ).toBe('43')
  })

  test('role downgrade hides cached events immediately and viewer changes cannot reuse them', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValueOnce({ data: history })
      .mockReturnValue(new Promise(() => {}))
    render(details())
    await screen.findByRole('textbox', { name: 'User Request' })
    act(() => {
      useAuthStore
        .getState()
        .auth.setUser({ id: 7, username: 'viewer', role: ROLE.USER })
    })
    expect(screen.queryByText('Full Chain Events')).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(1)
    act(() => {
      useAuthStore
        .getState()
        .auth.setUser({ id: 8, username: 'other-admin', role: ROLE.ADMIN })
    })
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
    expect(screen.getByText('Loading')).toBeInTheDocument()
    expect(
      screen.queryByRole('textbox', { name: 'User Request' })
    ).not.toBeInTheDocument()
  })
})
