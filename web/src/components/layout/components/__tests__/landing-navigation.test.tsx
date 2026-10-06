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
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useTopNavLinks } from '@/hooks/use-top-nav-links'

import { PublicHeader } from '../public-header'

const navLinks = [
  { title: 'Home', href: '/' },
  { title: 'Console', href: '/dashboard' },
  { title: 'Model Square', href: '/pricing' },
  { title: 'Rankings', href: '/rankings' },
  { title: 'Docs', href: 'https://docs.newapi.ai/', external: true },
  { title: 'About', href: '/about' },
]

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
  useNavigate: () => vi.fn(),
  useRouterState: () => ({ location: { pathname: '/' } }),
}))
vi.mock('@/hooks/use-top-nav-links', () => ({
  useTopNavLinks: vi.fn(),
}))
vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: () => ({ systemName: 'New API', logoLoaded: true }),
}))
vi.mock('@/hooks/use-notifications', () => ({
  useNotifications: () => ({ popoverOpen: false, unreadCount: 0 }),
}))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (selector?: (state: { auth: { user: null } }) => unknown) => {
    const state = { auth: { user: null } }
    return selector ? selector(state) : state
  },
}))
vi.mock('@/components/profile-dropdown', () => ({
  ProfileDropdown: () => null,
}))
vi.mock('@/features/system-update/system-update-action', () => ({
  SystemUpdateAction: () => null,
}))
vi.mock('@/components/layout/components/header-logo', () => ({
  HeaderLogo: () => null,
}))
vi.mock('@/components/dialog', () => ({ Dialog: () => null }))
vi.mock('@/context/theme-provider', () => ({
  useTheme: () => ({ theme: 'light', setTheme: vi.fn() }),
}))
vi.mock('@/components/notification-popover', () => ({
  NotificationPopover: () => null,
}))

beforeEach(() => {
  vi.mocked(useTopNavLinks).mockReturnValue(navLinks)
})
afterEach(() => cleanup())

it('preserves configured external Docs and other links in landing desktop order', () => {
  render(<PublicHeader landingNavigation />)
  const desktopNav = screen.getAllByRole('navigation')[0]
  const links = within(desktopNav).getAllByRole('link')
  expect(links.map((link) => link.textContent)).toEqual([
    'New API',
    'Home',
    'Model Square',
    'Console',
    'Rankings',
    'Docs',
    'About',
  ])
  const signIn = within(desktopNav).getByRole('button', { name: 'Sign in' })
  expect(signIn).toBeVisible()
  expect(signIn).toHaveAttribute('href', '/sign-in')
  expect(
    within(desktopNav).getByRole('link', { name: 'Model Square' })
  ).toHaveAttribute('href', '/pricing')
  expect(
    within(desktopNav).getByRole('link', { name: 'Docs' })
  ).toHaveAttribute('href', 'https://docs.newapi.ai/')
  expect(
    within(desktopNav).getByRole('link', { name: 'Docs' })
  ).toHaveAttribute('target', '_blank')
  expect(
    within(desktopNav).getByRole('button', { name: 'Change language' })
  ).toBeVisible()
  expect(
    within(desktopNav).getAllByRole('button', { name: 'Toggle theme' })[0]
  ).toBeVisible()
})

it('preserves the configured external Docs link in the landing mobile menu', async () => {
  const user = userEvent.setup()
  render(<PublicHeader landingNavigation />)
  await user.click(
    screen.getByRole('button', { name: 'Toggle navigation menu' })
  )
  const mobileNav = screen.getAllByRole('navigation')[1]
  expect(
    within(mobileNav).getByRole('link', { name: 'Model Square' })
  ).toHaveAttribute('href', '/pricing')
  const docsLink = within(mobileNav).getByRole('link', { name: 'Docs' })
  expect(docsLink).toHaveAttribute('href', 'https://docs.newapi.ai/')
  expect(docsLink).toHaveAttribute('target', '_blank')
  expect(docsLink).toHaveAttribute('rel', 'noopener noreferrer')
})

it('does not inject Docs when configuration excludes it', () => {
  vi.mocked(useTopNavLinks).mockReturnValue(
    navLinks.filter((link) => link.title !== 'Docs')
  )
  render(<PublicHeader landingNavigation />)
  expect(screen.queryByRole('link', { name: 'Docs' })).not.toBeInTheDocument()
})

it('hides landing language and theme controls when their props are false', () => {
  render(
    <PublicHeader
      landingNavigation
      showLanguageSwitcher={false}
      showThemeSwitch={false}
    />
  )
  expect(
    screen.queryByRole('button', { name: 'Change language' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Toggle theme' })
  ).not.toBeInTheDocument()
})
