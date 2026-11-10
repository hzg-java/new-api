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

import { apiDocument } from '../api-document'
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

it('documents optional CNY billing in synchronous and streaming chat examples', () => {
  render(<DocsContent />)
  const article = screen.getByRole('article')
  expect(article).toHaveTextContent('usage.billing')
  expect(article).toHaveTextContent('人民币元（CNY）')
  expect(screen.getByRole('banner')).toHaveTextContent('缺失不等于免费')
  expect(screen.getByRole('banner')).toHaveTextContent('usage.cost')
  const examples = [...article.querySelectorAll('pre')].map(
    (element) => element.textContent ?? ''
  )
  const json = examples.find((example) => example.startsWith('{'))
  expect(JSON.parse(json ?? '{}').usage.billing).toBe(0.01234)
  const stream = examples.find((example) => example.includes('data: [DONE]'))
  expect(stream).toContain('"choices":[]')
  expect(stream).toContain('"billing":0.01234')
  expect(stream?.trimEnd().endsWith('data: [DONE]')).toBe(true)
})

it.each([
  ['api-section-2', 0.25],
  ['api-section-3', 0.18],
] as const)(
  'shows numeric confirmed image charges in %s',
  (section, amount) => {
    window.history.replaceState(null, '', `/docs#${section}`)
    render(<DocsContent />)
    const article = screen.getByRole('article')
    expect(article).toHaveTextContent('usage.billing')
    const example = [...article.querySelectorAll('pre')].find((element) =>
      element.textContent?.startsWith('{')
    )
    expect(JSON.parse(example?.textContent ?? '{}').usage.billing).toBe(amount)
    const eventType =
      section === 'api-section-2' ? 'image_generation' : 'image_edit'
    const stream = [...article.querySelectorAll('pre')].find((element) =>
      element.textContent?.includes(`event: ${eventType}.usage`)
    )?.textContent
    expect(stream).toContain(`event: ${eventType}.completed`)
    expect(stream).toContain(`"usage":{"billing":${amount}}`)
    expect(article).toHaveTextContent('正常 EOF')
  }
)

it('keeps pending video examples unpriced and documents confirmed totals and refunds', async () => {
  const user = userEvent.setup()
  window.history.replaceState(null, '', '/docs#api-section-4')
  render(<DocsContent />)
  let article = screen.getByRole('article')
  expect(article).toHaveTextContent('异步提交时不返回最终费用')
  expect(article).toHaveTextContent('/doubao/api/v3/contents/generations/tasks')
  expect(article).toHaveTextContent('兼容映射尚未确认')
  const pending = [...article.querySelectorAll('pre')].find((element) =>
    element.textContent?.startsWith('{')
  )
  expect(JSON.parse(pending?.textContent ?? '{}').usage).toBeUndefined()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Docs' }),
    'api-section-5'
  )
  article = screen.getByRole('article')
  expect(article).toHaveTextContent('单个任务的最终总费用')
  expect(article).toHaveTextContent('状态为 succeeded 也不代表结算已确认')
  const examples = [...article.querySelectorAll('pre')]
    .map((element) => element.textContent ?? '')
    .filter((example) => example.startsWith('{'))
    .map((example) => JSON.parse(example))
  expect(examples[0].usage.billing).toBe(1.25)
  expect(
    examples.find((example) => example.status === 'failed')?.usage.billing
  ).toBe(0)
  expect(
    examples.find((example) => example.status === 'running')?.usage
  ).toBeUndefined()
  const unconfirmed = examples.find(
    (example) => example.status === 'succeeded' && !example.usage
  )
  expect(unconfirmed).toBeDefined()
  expect(unconfirmed).not.toHaveProperty('usage.billing')
})

it('keeps every documented JSON response parseable and never labels sample fees as measured', () => {
  for (const match of apiDocument.matchAll(/```json\n([\s\S]*?)```/g)) {
    expect(() => JSON.parse(match[1])).not.toThrow()
  }
  expect(apiDocument).toContain('金额仅为示例，不代表模型定价或实测账单')
  expect(apiDocument).toContain('批量扣费尚未确认落账')
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
