import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import {
  act,
  fireEvent,
  render as renderUI,
  screen,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next, { createInstance } from 'i18next'
import type { ReactElement } from 'react'
import { I18nextProvider, initReactI18next, setI18n } from 'react-i18next'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import type { TaskLog } from '../../types'
import { useTaskLogsColumns } from '../columns/task-logs-columns'
import { TaskDetailsDialog } from '../dialogs/task-details-dialog'
import { UsageLogsMobileList } from '../usage-logs-mobile-card'

const log: TaskLog = {
  id: 1,
  user_id: 7,
  platform: 'private-platform',
  task_id: 'task-public-id',
  action: 'text_to_image',
  channel_id: 357,
  group: 'default',
  quota: 0,
  submit_time: 1700000000,
  created_at: 1600000000,
  finish_time: 1700000060,
  status: 'IN_PROGRESS',
  properties: {
    origin_model_name: 'public-model',
    upstream_model_name: 'private-model',
  },
  admin_info: { task_plugin: { key: 'private-plugin', name: 'Admin plugin' } },
}

function TaskTable(props: {
  log: TaskLog
  isAdmin: boolean
  mobile?: boolean
}) {
  const columns = useTaskLogsColumns(props.isAdmin, false)
  const table = useReactTable({
    data: [props.log],
    columns,
    getCoreRowModel: getCoreRowModel(),
    state: {
      columnVisibility: { user: false, channel_id: false, artifacts: false },
    },
  })
  if (props.mobile) {
    return <UsageLogsMobileList table={table} logCategory='task' />
  }
  return (
    <table>
      <thead>
        {table.getHeaderGroups().map((group) => (
          <tr key={group.id}>
            {group.headers.map((header) => (
              <th key={header.id}>
                {flexRender(
                  header.column.columnDef.header,
                  header.getContext()
                )}
              </th>
            ))}
          </tr>
        ))}
      </thead>
      <tbody>
        {table.getRowModel().rows.map((row) => (
          <tr key={row.id}>
            {row.getVisibleCells().map((cell) => (
              <td key={cell.id}>
                {flexRender(cell.column.columnDef.cell, cell.getContext())}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function render(element: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return renderUI(
    <QueryClientProvider client={client}>{element}</QueryClientProvider>
  )
}

afterEach(() => {
  vi.restoreAllMocks()
  setI18n(i18next)
})

describe('task log presentation', () => {
  test.each(['NOT_START', 'SUBMITTED', 'QUEUED', 'IN_PROGRESS', 'UNKNOWN'])(
    'hides estimated cost for %s tasks',
    (status) => {
      render(
        <TaskTable log={{ ...log, status, quota: 12500 }} isAdmin={false} />
      )
      const headers = screen.getAllByRole('columnheader')
      const cost =
        screen.getAllByRole('cell')[
          headers.findIndex((header) => header.textContent === 'Cost')
        ]
      expect(cost).toHaveTextContent('—')
      expect(screen.queryByText('$0.025')).not.toBeInTheDocument()
    }
  )

  test.each(['SUCCESS', 'FAILURE'])(
    'shows final cost for %s tasks',
    (status) => {
      render(
        <TaskTable log={{ ...log, status, quota: 12500 }} isAdmin={false} />
      )
      expect(screen.getByText('$0.025')).toBeVisible()
    }
  )
  test.each(['zhCN', 'zhTW', 'en', 'fr', 'ru', 'ja', 'vi', 'invalid_locale'])(
    'given language %s then switching to French, numbers follow the active locale',
    async (language) => {
      const i18n = createInstance()
      await i18n
        .use(initReactI18next)
        .init({ lng: language, fallbackLng: false, resources: {} })
      render(
        <I18nextProvider i18n={i18n}>
          <TaskTable
            log={{
              ...log,
              video_info: { duration_seconds: 12.5, consumed_tokens: 12345 },
            }}
            isAdmin={false}
          />
        </I18nextProvider>
      )
      const cells = screen.getAllByRole('cell')
      const headers = screen.getAllByRole('columnheader')
      const tokens =
        cells[
          headers.findIndex(
            (header) => header.textContent === 'Consumed Tokens'
          )
        ]
      const duration =
        cells[
          headers.findIndex(
            (header) => header.textContent === 'Duration (seconds)'
          )
        ]
      const locales: Record<string, string | undefined> = {
        zhCN: 'zh-CN',
        zhTW: 'zh-TW',
        en: 'en',
        fr: 'fr',
        ru: 'ru',
        ja: 'ja',
        vi: 'vi',
      }
      expect(tokens.textContent).toBe(
        new Intl.NumberFormat(locales[language]).format(12345)
      )
      expect(duration).toHaveTextContent(
        new Intl.NumberFormat(locales[language]).format(12.5)
      )
      await act(async () => {
        await i18n.changeLanguage('fr')
      })
      expect(tokens.textContent).toBe('12 345')
      expect(duration).toHaveTextContent('12,5')
    }
  )

  test.each([false, true])(
    'orders visible task columns for admin=%s',
    (isAdmin) => {
      render(<TaskTable log={log} isAdmin={isAdmin} />)
      expect(
        screen.getAllByRole('columnheader').map((header) => header.textContent)
      ).toEqual([
        'Task ID',
        'Model',
        'Cost',
        'Duration',
        'Status',
        'Progress',
        'Resolution',
        'Duration (seconds)',
        'Has Reference Video',
        'Consumed Tokens',
        ...(isAdmin ? ['Plugin'] : []),
        'Created At',
        'Finished At',
        'Details',
      ])
    }
  )
  test.each([
    [undefined, ['-', '-', '-', '-']],
    [{}, ['-', '-', '-', '-']],
    [
      {
        resolution: '1080p',
        duration_seconds: 12.5,
        has_reference_video: true,
        consumed_tokens: 12345,
      },
      ['1080p', '12.5', 'Yes', '12,345'],
    ],
    [
      { duration_seconds: 0, has_reference_video: false, consumed_tokens: 0 },
      ['-', '0', 'No', '0'],
    ],
  ])(
    'given video_info=%j, renders metadata without losing zero or false',
    (video_info, expected) => {
      render(<TaskTable log={{ ...log, video_info }} isAdmin={false} />)
      const headers = screen.getAllByRole('columnheader')
      const cells = screen.getAllByRole('cell')
      const labels = [
        'Resolution',
        'Duration (seconds)',
        'Has Reference Video',
        'Consumed Tokens',
      ]
      labels.forEach((label, index) => {
        const cellIndex = headers.findIndex(
          (header) => header.textContent === label
        )
        expect(cellIndex).toBeGreaterThanOrEqual(0)
        expect(cells[cellIndex]).toHaveTextContent(expected[index])
      })
    }
  )

  test.each([false, true])(
    'given admin=%s, mobile displays video metadata',
    (isAdmin) => {
      render(
        <TaskTable
          log={{
            ...log,
            video_info: {
              resolution: '720p',
              duration_seconds: 8,
              has_reference_video: false,
              consumed_tokens: 12345,
            },
          }}
          isAdmin={isAdmin}
          mobile
        />
      )
      for (const text of [
        'Resolution',
        'Duration (seconds)',
        'Has Reference Video',
        'Consumed Tokens',
        '720p',
        '8',
        'No',
        '12,345',
      ]) {
        expect(screen.getByText(text)).toBeVisible()
      }
    }
  )

  test.each([
    [true, log.properties, 'private-model'],
    [false, log.properties, 'public-model'],
    [true, { origin_model_name: 'public-model' }, 'public-model'],
    [
      true,
      { origin_model_name: 'public-model', upstream_model_name: '' },
      'public-model',
    ],
    [false, { upstream_model_name: 'private-model' }, '-'],
    [true, undefined, '-'],
    [false, undefined, '-'],
  ])(
    'given admin=%s and properties=%j, the Model column displays %s',
    (isAdmin, properties, expected) => {
      render(<TaskTable log={{ ...log, properties }} isAdmin={isAdmin} />)
      const headers = screen.getAllByRole('columnheader')
      const index = headers.findIndex(
        (header) => header.textContent === 'Model'
      )
      expect(index).toBeGreaterThanOrEqual(0)
      const cell = screen.getAllByRole('cell')[index]
      expect(cell).toHaveTextContent(expected)
      if (isAdmin && properties?.origin_model_name) {
        expect(cell).toHaveTextContent(properties.origin_model_name)
      }
      if (!isAdmin) {
        expect(screen.queryByText('private-model')).not.toBeInTheDocument()
      }
    }
  )

  test('given a task ID, its cell shows only the ID and still copies it', () => {
    userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.spyOn(navigator.clipboard, 'writeText').mockImplementation(writeText)
    render(<TaskTable log={log} isAdmin={false} />)
    const badge = screen.getByTitle('Click to copy: task-public-id')
    expect(badge.closest('td')).toHaveTextContent(/^task-public-id$/)
    fireEvent.click(badge)
    expect(writeText).toHaveBeenCalledWith('task-public-id')
  })

  test.each([log.finish_time, undefined, 0])(
    'given finish_time=%s, creation and finish have separate columns',
    (finish_time) => {
      render(<TaskTable log={{ ...log, finish_time }} isAdmin={false} />)
      const headers = screen.getAllByRole('columnheader')
      const cells = screen.getAllByRole('cell')
      const createdIndex = headers.findIndex(
        (header) => header.textContent === 'Created At'
      )
      const finishIndex = headers.findIndex(
        (header) => header.textContent === 'Finished At'
      )
      expect(createdIndex).toBeGreaterThanOrEqual(0)
      expect(finishIndex).toBeGreaterThanOrEqual(0)
      expect(cells[createdIndex]).toHaveTextContent(
        formatTimestampToDate(log.submit_time, 'seconds')
      )
      expect(cells[finishIndex]).toHaveTextContent(
        finish_time ? formatTimestampToDate(finish_time, 'seconds') : '-'
      )
      expect(
        screen.queryByRole('columnheader', { name: 'Submit Time' })
      ).not.toBeInTheDocument()
    }
  )

  test.each([false, true])(
    'given admin=%s, details hide internal fields only for regular users',
    async (isAdmin) => {
      render(
        <TaskDetailsDialog
          log={log}
          isAdmin={isAdmin}
          isRoot={false}
          open
          onOpenChange={() => undefined}
        />
      )
      const dialog = within(await screen.findByRole('dialog'))
      expect(dialog.getAllByText('public-model').length).toBeGreaterThan(0)
      expect(dialog.getByText('Request Model')).toBeVisible()
      expect(dialog.getByText('Action')).toBeVisible()
      if (isAdmin) {
        expect(dialog.getByText('Platform')).toBeVisible()
        expect(dialog.getByText('Actual Model')).toBeVisible()
        expect(dialog.getByText('Admin plugin')).toBeVisible()
      } else {
        expect(dialog.queryByText('Platform')).not.toBeInTheDocument()
        expect(dialog.queryByText('Actual Model')).not.toBeInTheDocument()
        expect(dialog.queryByText('private-platform')).not.toBeInTheDocument()
        expect(dialog.queryByText('private-model')).not.toBeInTheDocument()
        expect(dialog.queryByText('Admin plugin')).not.toBeInTheDocument()
      }
    }
  )

  test.each([false, true])(
    'given admin=%s and full video_info, details show the same video fields as the list',
    async (isAdmin) => {
      render(
        <TaskDetailsDialog
          log={{
            ...log,
            video_info: {
              resolution: '1080p',
              duration_seconds: 12.5,
              has_reference_video: true,
              consumed_tokens: 12345,
            },
          }}
          isAdmin={isAdmin}
          isRoot={false}
          open
          onOpenChange={() => undefined}
        />
      )
      const dialog = within(await screen.findByRole('dialog'))
      expect(
        dialog.getByRole('region', { name: 'Basic Information' })
      ).toBeVisible()
      expect(dialog.getByText('Resolution')).toBeVisible()
      expect(dialog.getByText('1080p')).toBeVisible()
      expect(dialog.getByText('Duration (seconds)')).toBeVisible()
      expect(dialog.getByText(formatNumber(12.5, undefined))).toBeVisible()
      expect(dialog.getByText('Has Reference Video')).toBeVisible()
      expect(dialog.getByText('Yes')).toBeVisible()
      expect(dialog.getByText('Consumed Tokens')).toBeVisible()
      expect(dialog.getByText(formatNumber(12345, undefined))).toBeVisible()
    }
  )

  test('given video_info with zero duration and no reference video, details keep 0 and No instead of dashes', async () => {
    render(
      <TaskDetailsDialog
        log={{
          ...log,
          video_info: { duration_seconds: 0, has_reference_video: false },
        }}
        isAdmin={false}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(
      dialog.getAllByText(formatNumber(0, undefined)).length
    ).toBeGreaterThan(0)
    expect(dialog.getByText('No')).toBeVisible()
  })

  test('given partial video_info, details show dashes for unknown video fields', async () => {
    render(
      <TaskDetailsDialog
        log={{ ...log, video_info: { resolution: '720p' } }}
        isAdmin={false}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('Resolution').parentElement).toHaveTextContent(
      '720p'
    )
    expect(
      dialog.getByText('Duration (seconds)').parentElement
    ).toHaveTextContent('-')
    expect(
      dialog.getByText('Has Reference Video').parentElement
    ).toHaveTextContent('-')
    expect(dialog.getByText('Consumed Tokens').parentElement).toHaveTextContent(
      '-'
    )
  })

  test('given no video_info, details omit the video section entirely', async () => {
    render(
      <TaskDetailsDialog
        log={log}
        isAdmin={false}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.queryByText('Video Information')).not.toBeInTheDocument()
    expect(dialog.getByText('Resolution').parentElement).toHaveTextContent('-')
    expect(dialog.getByText('Consumed Tokens').parentElement).toHaveTextContent(
      '-'
    )
  })

  test('given recorded events, the task dialog shows a two-column overview and ordered timeline', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: [
          {
            kind: 'request',
            timestamp: 1700000000,
            payload: '{"model":"public-model","prompt":"hello"}',
          },
          {
            kind: 'accepted',
            timestamp: 1700000001,
            payload: '{"id":"task-public-id","status":"queued"}',
          },
          {
            kind: 'result',
            timestamp: 1700000060,
            payload: '{"status":"SUCCESS","usage":{"total_tokens":12}}',
          },
        ],
      },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <TaskDetailsDialog
          log={{
            ...log,
            status: 'SUCCESS',
            video_info: {
              resolution: '480p',
              duration_seconds: 15,
              has_reference_video: false,
              consumed_tokens: 12,
            },
          }}
          isAdmin={false}
          isRoot={false}
          open
          onOpenChange={() => undefined}
        />
      </QueryClientProvider>
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByText('User Request')).toBeVisible()
    expect(dialog.getByText('Submission Accepted')).toBeVisible()
    expect(dialog.getByText('Task Result')).toBeVisible()
    expect(dialog.getByText('Full Chain Events')).toBeVisible()
    expect(dialog.getByText('Task Duration')).toBeVisible()
    expect(
      dialog.getByRole('region', { name: 'Basic Information' })
    ).toHaveClass('md:grid-cols-2')
    expect(dialog.getByText('Cost')).toBeVisible()
    expect(dialog.getByText('Model')).toBeVisible()
    expect(dialog.getAllByText('public-model').length).toBeGreaterThan(0)
    expect(dialog.getByText('hello')).toBeVisible()
    expect(dialog.queryByText('private-model')).not.toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/api/task/1/events')
  })

  test('given an older task with no events, the timeline says no events instead of inventing data', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: [] },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <TaskDetailsDialog
          log={log}
          isAdmin={false}
          isRoot={false}
          open
          onOpenChange={() => undefined}
        />
      </QueryClientProvider>
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByText('No events recorded')).toBeVisible()
    expect(dialog.queryByText('User Request')).not.toBeInTheDocument()
  })

  test('given a task finishes while the dialog remains open, it loads the result event', async () => {
    const get = vi.spyOn(api, 'get')
    get.mockResolvedValueOnce({
      data: {
        success: true,
        data: [
          {
            kind: 'accepted',
            timestamp: 1700000001,
            payload: '{"id":"task-public-id","status":"queued"}',
          },
        ],
      },
    })
    get.mockResolvedValueOnce({
      data: {
        success: true,
        data: [
          {
            kind: 'accepted',
            timestamp: 1700000001,
            payload: '{"id":"task-public-id","status":"queued"}',
          },
          {
            kind: 'result',
            timestamp: 1700000060,
            payload: '{"status":"SUCCESS"}',
          },
        ],
      },
    })
    const component = (status: string) => (
      <TaskDetailsDialog
        log={{ ...log, status }}
        isAdmin={false}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    )
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const view = renderUI(
      <QueryClientProvider client={client}>
        {component('IN_PROGRESS')}
      </QueryClientProvider>
    )
    expect(await screen.findByText('Submission Accepted')).toBeVisible()
    view.rerender(
      <QueryClientProvider client={client}>
        {component('SUCCESS')}
      </QueryClientProvider>
    )
    expect(await screen.findByText('Task Result')).toBeVisible()
    expect(get).toHaveBeenCalledTimes(2)
  })

  test('given the event request fails, the dialog shows an error rather than an empty timeline', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('network unavailable'))
    render(
      <TaskDetailsDialog
        log={log}
        isAdmin={false}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByRole('alert')).toHaveTextContent(
      'Failed to load logs'
    )
    expect(dialog.queryByText('No events recorded')).not.toBeInTheDocument()
  })

  test.each([false, true])(
    'given admin=%s, mobile summary shows model and both times with role-safe plugin visibility',
    (isAdmin) => {
      render(
        <TaskTable
          log={{ ...log, finish_time: undefined }}
          isAdmin={isAdmin}
          mobile
        />
      )
      expect(screen.getByText('Created At')).toBeVisible()
      expect(screen.getByText('Finished At')).toBeVisible()
      expect(screen.getByText('Model')).toBeVisible()
      expect(
        screen.getByText(
          isAdmin ? 'Actual Model: private-model' : 'public-model'
        )
      ).toBeVisible()
      expect(
        screen.getByTitle('Click to copy: task-public-id')
      ).toHaveTextContent(/^task-public-id$/)
      expect(screen.queryByText('private-platform')).not.toBeInTheDocument()
      if (isAdmin) expect(screen.getByText('Admin plugin')).toBeVisible()
      else expect(screen.queryByText('Plugin')).not.toBeInTheDocument()
    }
  )
})
