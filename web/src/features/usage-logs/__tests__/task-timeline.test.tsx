import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test } from 'vitest'

import {
  TaskDetailsDialog,
  TaskTimeline,
} from '../components/dialogs/task-details-dialog'
import type { TaskEvent, TaskLog } from '../types'

afterEach(cleanup)

describe('task event content', () => {
  test('omits the result node while retaining request and accepted nodes', () => {
    render(
      <TaskTimeline
        events={[
          { kind: 'request', timestamp: 1700000000, payload: '{}' },
          { kind: 'accepted', timestamp: 1700000001, payload: '{}' },
          {
            kind: 'result',
            timestamp: 1700000002,
            payload: '{"output":"done"}',
          },
        ]}
      />
    )
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
    expect(
      screen.queryByRole('textbox', { name: 'Task Result' })
    ).not.toBeInTheDocument()
  })

  test('shows the empty state when only a result event exists', () => {
    render(
      <TaskTimeline
        events={[{ kind: 'result', timestamp: 1700000000, payload: '{}' }]}
      />
    )
    expect(screen.getByText('No events recorded')).toBeInTheDocument()
  })

  test('reserves space for timeline markers inside horizontally clipped details', () => {
    render(
      <TaskTimeline
        events={[{ kind: 'request', timestamp: 1700000000, payload: '{}' }]}
      />
    )
    expect(screen.getByRole('list')).toHaveClass('pl-1')
  })

  test('keeps task result data visible when no terminal chain event was recorded', () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(['task-events', 1, 'SUCCESS'], [])
    const log = {
      id: 1,
      task_id: 'task-result',
      status: 'SUCCESS',
      data: { output: 'retained-result' },
    } as TaskLog
    render(
      <QueryClientProvider client={client}>
        <TaskDetailsDialog
          log={log}
          isAdmin
          open
          isRoot={false}
          onOpenChange={() => {}}
        />
      </QueryClientProvider>
    )
    expect(
      screen.getByRole('textbox', { name: 'Task Result' })
    ).toHaveTextContent('retained-result')
  })

  test.each([
    ['request', 'User Request'],
    ['accepted', 'Submission Accepted'],
  ] as const)(
    'formats %s JSON with indentation while preserving numeric lexemes and original copying',
    async (kind, label) => {
      const user = userEvent.setup()
      const payload =
        '{"id":9007199254740993,"options":{"scale":1.2300,"enabled":false},"items":[0,null],"prompt":"a, {b}: \\"quoted\\""}'
      render(
        <TaskTimeline events={[{ kind, timestamp: 1700000000, payload }]} />
      )
      const viewer = screen.getByRole('textbox', { name: label })
      const lines = Array.from(
        viewer.querySelectorAll('.cm-line'),
        (line) => line.textContent
      )
      expect(lines).toEqual([
        '{',
        '  "id": 9007199254740993,',
        '  "options": {',
        '    "scale": 1.2300,',
        '    "enabled": false',
        '  },',
        '  "items": [',
        '    0,',
        '    null',
        '  ],',
        '  "prompt": "a, {b}: \\"quoted\\""',
        '}',
      ])
      await user.click(
        screen.getByRole('button', { name: 'Copy to clipboard' })
      )
      expect(await navigator.clipboard.readText()).toBe(payload)
    }
  )

  test.each([
    '{invalid',
    'event: completed\ndata: {}\n',
    'null',
    '{"items":[],"options":{}}',
  ])(
    'preserves non-JSON requests and scalar or empty values: %s',
    (payload) => {
      render(
        <TaskTimeline
          events={[{ kind: 'request', timestamp: 1700000000, payload }]}
        />
      )
      const viewer = screen.getByRole('textbox', { name: 'User Request' })
      expect(
        viewer.querySelector('.cm-content')?.textContent?.replaceAll(/\s/g, '')
      ).toBe(payload.replaceAll(/\s/g, ''))
    }
  )

  test.each([
    '{"prompt":"hello","metadata":{"seed":0,"enabled":false},"id":9007199254740993}',
    '[{"url":"/video/result"},null,false,0]',
    'null',
    'event: response.completed\ndata: {"output":"done"}\n\n',
  ])(
    'displays and copies the complete payload without altering values: %s',
    async (payload) => {
      const user = userEvent.setup()
      render(
        <TaskTimeline
          events={[{ kind: 'accepted', timestamp: 1700000000, payload }]}
        />
      )
      const content = screen.getByRole('textbox', {
        name: 'Submission Accepted',
      })
      expect(content).toHaveAttribute('aria-readonly', 'true')
      expect(
        content.querySelector('.cm-content')?.textContent?.replaceAll(/\s/g, '')
      ).toBe(payload.replaceAll(/\s/g, ''))
      await user.click(
        screen.getByRole('button', { name: 'Copy to clipboard' })
      )
      expect(await navigator.clipboard.readText()).toBe(payload)
    }
  )

  test('marks historical summaries as not recorded instead of presenting them as complete', () => {
    const event: TaskEvent = {
      kind: 'request',
      timestamp: 1700000000,
      payload: '{"model":"video"}',
      detail_complete: false,
      detail_unavailable: 'size_limit_exceeded',
    }
    render(<TaskTimeline events={[event]} />)
    expect(screen.getByText('Not recorded')).toBeInTheDocument()
    expect(
      screen.getByRole('textbox', { name: 'User Request' }).textContent
    ).toContain('video')
  })

  test('shows an empty state when no events are available', () => {
    render(<TaskTimeline events={[]} />)
    expect(screen.getByText('No events recorded')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Copy to clipboard' })
    ).not.toBeInTheDocument()
  })

  test('copies a long payload including fields beyond the visible editor viewport', async () => {
    const user = userEvent.setup()
    const payload = JSON.stringify({
      prompt: 'a'.repeat(12000),
      final: 'last-field',
    })
    render(
      <TaskTimeline
        events={[{ kind: 'request', timestamp: 1700000000, payload }]}
      />
    )
    const viewer = screen.getByRole('textbox', { name: 'User Request' })
    expect(viewer).toHaveAttribute('aria-readonly', 'true')
    expect(viewer.closest('.code-block-scroll')).toHaveClass('overflow-auto')
    expect(viewer.closest('.code-block-scroll')).toHaveStyle({
      maxHeight: '23rem',
    })
    await user.click(screen.getByRole('button', { name: 'Copy to clipboard' }))
    expect(await navigator.clipboard.readText()).toBe(payload)
  })
})
