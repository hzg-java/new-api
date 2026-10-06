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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import type { SystemStatus } from '@/features/auth/types'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { api } from '@/lib/api'

import { SignUp } from '../index'

vi.mock('@/hooks/use-status', () => ({ useStatus: vi.fn() }))
vi.mock('@/hooks/use-system-config', () => ({ useSystemConfig: vi.fn() }))

const mockStatus = vi.mocked(useStatus)
const mockConfig = vi.mocked(useSystemConfig)

function renderSignUp(status: Partial<SystemStatus>) {
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
  const signUp = createRoute({
    getParentRoute: () => auth,
    path: '/sign-up',
    component: SignUp,
  })
  const signIn = createRoute({
    getParentRoute: () => root,
    path: '/sign-in',
    component: () => <div>Sign in destination</div>,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([signUp]), signIn]),
    history: createMemoryHistory({ initialEntries: ['/sign-up'] }),
  })
  return render(<RouterProvider router={router} />)
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  document.querySelector('#cf-turnstile')?.remove()
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

it('shows the shared brand layout without duplicate account or legal notices', async () => {
  renderSignUp({
    email_verification: false,
    user_agreement_enabled: true,
    privacy_policy_enabled: true,
  })
  expect(await screen.findByText('Demo Gateway')).toBeVisible()
  expect(screen.getByRole('region', { name: 'Sign up' })).toBeVisible()
  expect(screen.getByRole('main')).toHaveClass('overflow-x-clip')
  expect(
    screen.getAllByRole('heading', { name: 'Create an account' })
  ).toHaveLength(1)
  expect(screen.queryByText('Account information')).not.toBeInTheDocument()
  expect(screen.getAllByRole('link', { name: 'User Agreement' })).toHaveLength(
    1
  )
  expect(screen.getAllByRole('link', { name: 'Privacy Policy' })).toHaveLength(
    1
  )
  expect(screen.getByRole('link', { name: 'Sign in' })).toHaveAttribute(
    'href',
    '/sign-in'
  )
  expect(screen.getByRole('button', { name: 'Create account' })).toBeDisabled()
  await userEvent.setup().click(screen.getByRole('checkbox'))
  expect(screen.getByRole('button', { name: 'Create account' })).toBeEnabled()
})

it('omits email verification fields only when the server status disables them', async () => {
  renderSignUp({ email_verification: false })
  expect(
    await screen.findByRole('textbox', { name: 'Username' })
  ).toHaveAttribute('autocomplete', 'username')
  expect(
    screen.queryByRole('textbox', { name: /Email \(required/ })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('textbox', { name: 'Verification code' })
  ).not.toBeInTheDocument()
  expect(screen.getByLabelText('Password')).toHaveAttribute(
    'autocomplete',
    'new-password'
  )
  expect(screen.getByLabelText('Confirm password')).toHaveAttribute(
    'autocomplete',
    'new-password'
  )
})

it('keeps email verification and legal consent when enabled by the server', async () => {
  const post = vi.spyOn(api, 'post')
  renderSignUp({
    email_verification: true,
    user_agreement_enabled: true,
  })
  expect(
    await screen.findByRole('textbox', {
      name: 'Email (required for verification)',
    })
  ).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Verification code' })
  ).toHaveAttribute('autocomplete', 'one-time-code')
  expect(screen.getByRole('button', { name: 'Create account' })).toBeDisabled()
  await userEvent.setup().click(screen.getByRole('checkbox'))
  expect(screen.getByRole('button', { name: 'Send code' })).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})

it('validates password confirmation before sending a registration request', async () => {
  const post = vi.spyOn(api, 'post')
  renderSignUp({ email_verification: false })
  const user = userEvent.setup()
  await user.type(
    await screen.findByRole('textbox', { name: 'Username' }),
    'new-user'
  )
  await user.type(screen.getByLabelText('Password'), 'LongPassword123!')
  await user.type(
    screen.getByLabelText('Confirm password'),
    'DifferentPassword123!'
  )
  await user.click(screen.getByRole('button', { name: 'Create account' }))
  expect(await screen.findByText("Passwords don't match.")).toBeVisible()
  expect(post).not.toHaveBeenCalled()
})

it('submits valid fields through the existing registration endpoint when email verification is disabled', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'Registration failed' },
  })
  renderSignUp({ email_verification: false })
  const user = userEvent.setup()
  const region = await screen.findByRole('region', { name: 'Sign up' })
  await user.type(
    within(region).getByRole('textbox', { name: 'Username' }),
    'new-user'
  )
  await user.type(screen.getByLabelText('Password'), 'LongPassword123!')
  await user.type(screen.getByLabelText('Confirm password'), 'LongPassword123!')
  await user.click(screen.getByRole('button', { name: 'Create account' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/user/register',
      expect.objectContaining({
        username: 'new-user',
        password: 'LongPassword123!',
      }),
      expect.objectContaining({ params: { turnstile: '' } })
    )
  )
})
