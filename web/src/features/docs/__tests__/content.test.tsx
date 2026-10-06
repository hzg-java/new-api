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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'

import { DocsContent } from '../content'

afterEach(() => {
  cleanup()
  window.history.replaceState(null, '', '/docs')
})

it('switches the active interface without stacking all six interfaces', async () => {
  const user = userEvent.setup()
  render(<DocsContent />)
  const nav = screen.getByRole('navigation', { name: 'Docs' })
  expect(within(nav).getAllByRole('button')).toHaveLength(6)
  expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent(
    '文本对话接口'
  )
  await user.click(within(nav).getByRole('button', { name: /视频生成接口/ }))
  expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent(
    '视频生成接口'
  )
  expect(screen.getByRole('article')).toHaveTextContent('本节尚未实测')
  expect(
    within(nav).getByRole('button', { name: /视频生成接口/ })
  ).toHaveAttribute('aria-current', 'page')
  expect(
    screen.queryByRole('heading', { name: /文本对话接口/ })
  ).not.toBeInTheDocument()
})

it('mobile interface selection preserves download caveats and synchronizes navigation', async () => {
  const user = userEvent.setup()
  render(<DocsContent />)
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Docs' }),
    'api-section-6'
  )
  expect(screen.getByRole('article')).toHaveTextContent(
    '没有 /content 代理下载端点'
  )
  expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent(
    '视频下载说明'
  )
})

it('restores a linked interface and falls back for unknown hashes', () => {
  window.history.replaceState(null, '', '/docs#api-section-3')
  const first = render(<DocsContent />)
  expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent(
    '图像编辑接口'
  )
  first.unmount()
  window.history.replaceState(null, '', '/docs#unknown')
  render(<DocsContent />)
  expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent(
    '文本对话接口'
  )
})

it('keeps table headers and short fields unwrapped while descriptions can wrap', () => {
  render(<DocsContent />)
  for (const table of screen.getAllByRole('table')) {
    expect(table.parentElement).toHaveClass('[&_th]:whitespace-nowrap')
    expect(table.parentElement).toHaveClass(
      '[&_td:not(:last-child)]:whitespace-nowrap'
    )
    expect(table.parentElement).toHaveClass('[&_td:last-child]:min-w-64')
    expect(table.parentElement).toHaveClass('[&_table]:overflow-x-auto')
  }
})

it('copies an executable example while preserving the API key placeholder', async () => {
  const user = userEvent.setup()
  render(<DocsContent />)
  await user.click(
    screen
      .getAllByRole('button', { name: 'Copy to clipboard' })
      .at(-1) as HTMLElement
  )
  const copied = await navigator.clipboard.readText()
  expect(copied).toContain('curl https://www.zongsiai.vip/v1/chat/completions')
  expect(copied).toContain('Bearer $OPENAI_API_KEY')
  expect(copied).not.toContain('```')
})
