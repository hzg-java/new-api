/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import type { SystemStatus } from '@/features/auth/types'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { api } from '@/lib/api'

import { ForgotPassword } from '../../forgot-password'
import { SignIn } from '../index'

vi.mock('@/hooks/use-status', () => ({ useStatus: vi.fn() }))
vi.mock('@/hooks/use-system-config', () => ({ useSystemConfig: vi.fn() }))

const mockStatus = vi.mocked(useStatus)
const mockConfig = vi.mocked(useSystemConfig)

function renderSignIn(status: Partial<SystemStatus>, recovery = false) {
  mockStatus.mockReturnValue({
    status: status as SystemStatus,
    loading: false,
    error: null,
  })
  mockConfig.mockReturnValue({
    systemName: 'Demo Gateway',
    logo: '/logo.png',
    loading: false,
  } as ReturnType<typeof useSystemConfig>)

  const root = createRootRoute({ component: Outlet })
  const auth = createRoute({
    getParentRoute: () => root,
    id: '(auth)',
    component: Outlet,
  })
  const routes = [
    createRoute({
      getParentRoute: () => auth,
      path: '/sign-in',
      component: SignIn,
    }),
    createRoute({
      getParentRoute: () => root,
      path: '/sign-up',
      component: () => <div>Registration destination</div>,
    }),
    createRoute({
      getParentRoute: () => root,
      path: '/forgot-password',
      component: recovery
        ? ForgotPassword
        : () => <div>Password recovery destination</div>,
    }),
  ]
  const router = createRouter({
    routeTree: root.addChildren([
      auth.addChildren([routes[0]]),
      ...routes.slice(1),
    ]),
    history: createMemoryHistory({
      initialEntries: [recovery ? '/forgot-password' : '/sign-in'],
    }),
  })
  return render(<RouterProvider router={router} />)
}

it('recovery uses the shared brand layout and rejects invalid email without sending a request', async () => {
  const user = userEvent.setup()
  const request = vi.spyOn(api, 'get')
  renderSignIn({ turnstile_check: false }, true)
  expect(
    await screen.findByRole('heading', { name: 'Forgot password' })
  ).toBeVisible()
  expect(screen.getByRole('region', { name: 'Forgot password' })).toBeVisible()
  expect(screen.getByRole('textbox', { name: 'Email' })).toHaveAttribute(
    'autocomplete',
    'email'
  )
  expect(screen.getByRole('link', { name: 'Back to login' })).toHaveAttribute(
    'href',
    '/sign-in'
  )
  await user.click(screen.getByRole('button', { name: /Send reset email/ }))
  expect(
    await screen.findByText('Please enter a valid email address')
  ).toBeVisible()
  expect(request).not.toHaveBeenCalled()
})

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  document.querySelector('#cf-turnstile')?.remove()
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

it('renders the split brand and accessible password fields when password sign-in is enabled', async () => {
  renderSignIn({ password_login_enabled: true, register_enabled: true })
  expect(await screen.findByText('Demo Gateway')).toBeVisible()
  expect(screen.getByRole('link', { name: /Demo Gateway/ })).toHaveAttribute(
    'href',
    '/'
  )
  expect(screen.getByRole('region', { name: 'Sign in' })).toBeVisible()
  expect(screen.getByRole('main')).toHaveClass('overflow-x-clip')
  expect(screen.getByRole('main')).not.toHaveClass('overflow-hidden')
  expect(
    screen.getByRole('textbox', { name: 'Username or Email' })
  ).toHaveAttribute('autocomplete', 'username')
  expect(screen.getByLabelText('Password')).toHaveAttribute(
    'autocomplete',
    'current-password'
  )
  expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled()
  expect(
    screen.getByRole('link', { name: 'Forgot password?' })
  ).toHaveAttribute('href', '/forgot-password')
  expect(screen.getByRole('link', { name: 'Sign up' })).toHaveAttribute(
    'href',
    '/sign-up'
  )
})

it('hides password fields when password login is disabled by status', async () => {
  renderSignIn({ password_login_enabled: false, register_enabled: false })
  expect(await screen.findByText('Demo Gateway')).toBeVisible()
  expect(
    screen.queryByRole('textbox', { name: 'Username or Email' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Sign in' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('link', { name: 'Sign up' })
  ).not.toBeInTheDocument()
})

it('keeps legal consent required before allowing password submission', async () => {
  renderSignIn({ password_login_enabled: true, user_agreement_enabled: true })
  const button = await screen.findByRole('button', { name: 'Sign in' })
  expect(button).toBeDisabled()
  await userEvent.setup().click(screen.getByRole('checkbox'))
  await waitFor(() => expect(button).toBeEnabled())
})

it('submits valid credentials through the existing password login endpoint', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: false, message: 'Login failed' } })
  renderSignIn({ password_login_enabled: true })
  const user = userEvent.setup()
  await user.type(
    await screen.findByRole('textbox', { name: 'Username or Email' }),
    'test-user'
  )
  await user.type(screen.getByLabelText('Password'), 'test-password')
  await user.click(screen.getByRole('button', { name: 'Sign in' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/user/login?turnstile=',
      {
        username: 'test-user',
        password: 'test-password',
      },
      { skipAuthRefresh: true }
    )
  )
})

it('does not send a login request when password fields are empty', async () => {
  const post = vi.spyOn(api, 'post')
  renderSignIn({ password_login_enabled: true })
  await userEvent
    .setup()
    .click(await screen.findByRole('button', { name: 'Sign in' }))
  expect(
    await screen.findByText('Please enter your username or email')
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()
})

it('does not send a password login request before Turnstile verification', async () => {
  const post = vi.spyOn(api, 'post')
  renderSignIn({
    password_login_enabled: true,
    turnstile_check: true,
    turnstile_site_key: 'public-test-site-key',
  })
  expect(await screen.findByRole('button', { name: 'Sign in' })).toBeVisible()
  expect(document.querySelector('#cf-turnstile')).toHaveAttribute(
    'src',
    expect.stringContaining('challenges.cloudflare.com')
  )
  const user = userEvent.setup()
  await user.type(
    screen.getByRole('textbox', { name: 'Username or Email' }),
    'test-user'
  )
  await user.type(screen.getByLabelText('Password'), 'test-password')
  await user.click(screen.getByRole('button', { name: 'Sign in' }))
  expect(post).not.toHaveBeenCalled()
})

it('keeps the configured OAuth provider available when password login is disabled', async () => {
  renderSignIn({
    password_login_enabled: false,
    github_oauth: true,
    register_enabled: false,
  })
  expect(await screen.findByRole('button', { name: /GitHub/ })).toBeEnabled()
  expect(
    screen.queryByRole('textbox', { name: 'Username or Email' })
  ).not.toBeInTheDocument()
})
