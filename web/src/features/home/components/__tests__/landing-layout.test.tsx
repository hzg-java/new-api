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
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { STATUS_QUERY_KEY } from '@/lib/status-query'

import { HeroTerminalDemo } from '../hero-terminal-demo'
import { Features } from '../sections/features'
import { Hero } from '../sections/hero'
import { HowItWorks } from '../sections/how-it-works'

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    to,
    children,
    ...props
  }: React.PropsWithChildren<{ to: string }>) => (
    <a href={to} {...props}>
      {children}
    </a>
  ),
}))

let queryClient: QueryClient

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
  queryClient.setQueryData(STATUS_QUERY_KEY, { docs_link: '' })
})
afterEach(() => {
  cleanup()
  queryClient.clear()
})

function renderHero() {
  return render(
    <QueryClientProvider client={queryClient}>
      <Hero />
    </QueryClientProvider>
  )
}

it('keeps signup actions without extra documentation or pricing actions', () => {
  const { container } = renderHero()
  const hero = container.querySelector('[data-testid="landing-hero"]')
  expect(hero).toHaveTextContent('One-stop access to all-model APIs')
  expect(hero).toHaveTextContent(
    'Unified access to multiple models makes AI app development easier.'
  )
  expect(hero).not.toHaveTextContent('Unified API Gateway for')
  expect(hero).not.toHaveTextContent('AI Application Infrastructure Foundation')
  expect(hero).not.toHaveTextContent('NewAPI')
  expect(hero).not.toHaveTextContent('API preview only · Not live data')
  expect(screen.queryByRole('button', { name: 'Docs' })).not.toBeInTheDocument()
  expect(hero).not.toHaveTextContent('View Pricing')
  expect(screen.getByRole('button', { name: /Sign up/ })).toHaveAttribute(
    'href',
    '/sign-up'
  )
  const ecosystems = container.querySelector('#models') as HTMLElement
  expect(ecosystems).toBeInTheDocument()
  expect(ecosystems.querySelectorAll('button')).toHaveLength(6)
  const modelCards = within(ecosystems)
  expect(modelCards.getByRole('group')).toHaveClass('grid', 'grid-cols-6')
  expect(
    [...ecosystems.querySelectorAll('button')].every(
      (card) => card.getAttribute('aria-pressed') === 'false'
    )
  ).toBe(true)
  expect(
    screen.queryByText('Claude', { selector: 'div' })
  ).not.toBeInTheDocument()
  expect(modelCards.getByRole('button', { name: 'OpenAI' })).toBeInTheDocument()
  expect(modelCards.getByRole('button', { name: 'Llama' })).toBeInTheDocument()
})

it.each(['https://docs.example.com/guide', '/docs'])(
  'does not add a hero Docs action for backend URL %s',
  (docsUrl) => {
    queryClient.setQueryData(STATUS_QUERY_KEY, { docs_link: docsUrl })
    renderHero()
    expect(
      screen.queryByRole('button', { name: 'Docs' })
    ).not.toBeInTheDocument()
  }
)

it('scales ecosystem icons with their cards and preserves brand backgrounds on hover', () => {
  const { container } = renderHero()
  const ecosystems = container.querySelector('#models') as HTMLElement
  for (const card of ecosystems.querySelectorAll('button')) {
    expect(card.querySelector('svg')).toHaveClass('size-[70%]')
    expect(card.className).toMatch(/hover:bg-\[#[a-f0-9]+\]/)
    expect(card.className).toMatch(/dark:hover:bg-\[#[a-f0-9]+\]/)
  }
})

it('raises the hovered model and updates its tooltip without clicking', async () => {
  const user = userEvent.setup()
  const { container } = renderHero()
  const ecosystems = container.querySelector('#models') as HTMLElement
  const modelCards = within(ecosystems)
  const claude = modelCards.getByRole('button', { name: 'Claude' })
  await user.hover(claude)
  expect(claude).toHaveAttribute('aria-pressed', 'true')
  expect(claude).toHaveClass('-translate-y-3', 'scale-[1.35]')
  expect(claude).toHaveStyle({ zIndex: '7' })
  expect(await screen.findByText('Claude', { selector: 'div' })).toBeVisible()
  expect(modelCards.getByRole('button', { name: 'Gemini' })).toHaveAttribute(
    'aria-pressed',
    'false'
  )
  const qwen = modelCards.getByRole('button', { name: 'Qwen' })
  await user.hover(qwen)
  expect(qwen).toHaveAttribute('aria-pressed', 'true')
  expect(await screen.findByText('Qwen', { selector: 'div' })).toBeVisible()
  expect(claude).toHaveAttribute('aria-pressed', 'false')
  await waitFor(() =>
    expect(
      screen.queryByText('Claude', { selector: 'div' })
    ).not.toBeInTheDocument()
  )
  await user.unhover(qwen)
  await waitFor(() =>
    expect(
      screen.queryByText('Qwen', { selector: 'div' })
    ).not.toBeInTheDocument()
  )
  expect(
    [...ecosystems.querySelectorAll('button')].every(
      (card) => card.getAttribute('aria-pressed') === 'false'
    )
  ).toBe(true)
})

it('also updates the highlighted model by click or keyboard focus', async () => {
  const user = userEvent.setup()
  const { container } = renderHero()
  const ecosystems = container.querySelector('#models') as HTMLElement
  const modelCards = within(ecosystems)
  await user.click(modelCards.getByRole('button', { name: 'DeepSeek' }))
  expect(modelCards.getByRole('button', { name: 'DeepSeek' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await user.unhover(modelCards.getByRole('button', { name: 'DeepSeek' }))
  await user.tab({ shift: true })
  await user.tab({ shift: true })
  const claude = modelCards.getByRole('button', { name: 'Claude' })
  expect(claude).toHaveFocus()
  expect(claude).toHaveAttribute('aria-pressed', 'true')
  expect(await screen.findByText('Claude', { selector: 'div' })).toBeVisible()
  await user.tab()
  expect(claude).toHaveAttribute('aria-pressed', 'false')
  const gemini = modelCards.getByRole('button', { name: 'Gemini' })
  expect(gemini).toHaveFocus()
  expect(gemini).toHaveAttribute('aria-pressed', 'true')
  expect(await screen.findByText('Gemini', { selector: 'div' })).toBeVisible()
})

it('keeps streamlined sections without the bottom signup banner', () => {
  const { container } = render(
    <>
      <HowItWorks />
      <Features />
    </>
  )
  expect(screen.getByRole('heading', { name: 'Quick Access' })).toBeVisible()
  expect(container).not.toHaveTextContent('How It Works')
  expect(container).not.toHaveTextContent('Core Features')
  expect(container).not.toHaveTextContent('Ready to simplify')
  expect(container).not.toHaveTextContent('View Pricing')
  expect(
    screen.queryByRole('button', { name: /Sign up/ })
  ).not.toBeInTheDocument()
})

it('shows four access stages in order, from account creation to usage review', () => {
  const { container } = render(<HowItWorks />)
  const steps = [...container.querySelectorAll('h3')].map(
    (heading) => heading.textContent
  )
  expect(steps).toEqual([
    'Create account',
    'Create API Key',
    'Connect',
    'Monitor',
  ])
  expect(container.querySelector('#steps .grid')).toHaveClass(
    'sm:grid-cols-2',
    'lg:grid-cols-4'
  )
})

it('shows four two-column advantage cards with section heading', () => {
  const { container } = render(<Features />)
  expect(container.querySelector('#features')).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Our Advantages' })).toBeVisible()
  expect(container.querySelectorAll('#features h3')).toHaveLength(4)
  expect(container.querySelector('#features .grid')).toHaveClass(
    'md:grid-cols-2'
  )
  expect(screen.getByRole('heading', { name: 'Stable Routing' })).toBeVisible()
  expect(screen.getByRole('heading', { name: 'Usage Insights' })).toBeVisible()
})

it('hides API preview scrollbars while allowing horizontal scrolling on narrow screens', () => {
  render(<HeroTerminalDemo />)
  const strip = screen.getByRole('button', { name: 'Chat' }).parentElement
  expect(strip).toHaveClass(
    'overflow-x-auto',
    'overflow-y-hidden',
    '[scrollbar-width:none]',
    '[&::-webkit-scrollbar]:hidden'
  )
})

it('switches the sample API endpoint and curl line when another protocol is selected', async () => {
  const user = userEvent.setup()
  render(<HeroTerminalDemo />)
  expect(screen.getByRole('button', { name: 'Chat' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.getAllByText('/v1/chat/completions').length).toBeGreaterThan(0)
  expect(screen.getByText('curl')).toBeVisible()
  expect(
    screen.getByText(/OPENAI_BASE_URL\/v1\/chat\/completions/)
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Claude' }))
  expect(screen.getByRole('button', { name: 'Claude' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.getAllByText('/v1/messages').length).toBeGreaterThan(0)
  expect(screen.getByText(/OPENAI_BASE_URL\/v1\/messages/)).toBeVisible()
  expect(screen.queryByText('/v1/chat/completions')).not.toBeInTheDocument()
})
