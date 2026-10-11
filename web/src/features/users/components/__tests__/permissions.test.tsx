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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { User } from '../../types'
import { UsersMutateDrawer } from '../users-mutate-drawer'
import { UsersProvider } from '../users-provider'

const target: User = {
  id: 2,
  username: 'managed-admin',
  display_name: 'Managed admin',
  role: 10,
  status: 1,
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}
const label = "View other accounts' audit logs"
const description =
  'View audit records from user and admin roles. Root records are always excluded.'

function renderPermissions(
  viewerRole: number,
  allowed?: boolean,
  creating = false,
  loadError = false
) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'operator', role: viewerRole })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/authz/catalog') {
      return {
        data: {
          success: true,
          data: {
            resources: [
              {
                resource: 'audit',
                label_key: 'Audit Logs',
                actions: [
                  {
                    action: 'read',
                    label_key: label,
                    description_key: description,
                  },
                ],
              },
            ],
            roles: [{ key: 'admin', grants: { audit: { read: false } } }],
          },
        },
      }
    }
    if (url === '/api/user/2/model-pricing') {
      if (loadError) throw new Error('Pricing unavailable')
      return {
        data: {
          success: true,
          data: { multipliers: { 'gpt-4': 1.5, 'gpt-5': 2 } },
        },
      }
    }
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    return {
      data: {
        success: true,
        data: {
          ...target,
          admin_permissions:
            allowed === undefined ? {} : { audit: { read: allowed } },
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer
          open
          onOpenChange={() => undefined}
          currentRow={creating ? undefined : target}
        />
      </UsersProvider>
    </QueryClientProvider>
  )
  return { ...view, client }
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it.each([undefined, true])(
  'root can save an audit grant or revocation from the existing editor (previous=%s)',
  async (allowed) => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    renderPermissions(100, allowed)
    await screen.findByDisplayValue('Managed admin')
    const checkbox = await screen.findByRole('checkbox', {
      name: new RegExp(label),
    })
    await waitFor(() =>
      expect(checkbox).toHaveAttribute('aria-checked', String(!!allowed))
    )
    expect(screen.getByText(description)).toBeVisible()
    await userEvent.click(checkbox)
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/user/',
        expect.objectContaining({
          id: 2,
          admin_permissions: { audit: { read: !allowed } },
        })
      )
    )
  }
)

it('root replaces user multipliers independently and blank removes an override', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const { client } = renderPermissions(100)
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const multipliers = await screen.findAllByRole('textbox', {
    name: 'Pricing multiplier',
  })
  await userEvent.clear(multipliers[0])
  await userEvent.clear(multipliers[1])
  await userEvent.type(multipliers[1], '0.75')
  await userEvent.click(
    screen.getByRole('button', { name: 'Save user model pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/2/model-pricing', {
      multipliers: { 'gpt-5': 0.75 },
    })
  )
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ['pricing'] })
  expect(screen.getByRole('dialog')).toBeVisible()
})

it.each(['0', '-1', 'Infinity', 'NaN'])(
  'rejects invalid user multiplier %s before sending',
  async (value) => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    renderPermissions(100)
    const inputs = await screen.findAllByRole('textbox', {
      name: 'Pricing multiplier',
    })
    await userEvent.clear(inputs[0])
    await userEvent.type(inputs[0], value)
    await userEvent.click(
      screen.getByRole('button', { name: 'Save user model pricing' })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Enter a finite, positive pricing multiplier'
    )
    expect(put).not.toHaveBeenCalled()
  }
)

it('adds model rows and removes all overrides with a replacement empty map', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  renderPermissions(100)
  await screen.findByDisplayValue('1.5')
  await userEvent.click(screen.getByRole('button', { name: 'Add model' }))
  const names = screen.getAllByRole('textbox', { name: 'Model name' })
  const values = screen.getAllByRole('textbox', { name: 'Pricing multiplier' })
  await userEvent.type(names[2], 'custom-model')
  await userEvent.type(values[2], '3')
  await userEvent.click(
    screen.getByRole('button', { name: 'Save user model pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenLastCalledWith('/api/user/2/model-pricing', {
      multipliers: { 'gpt-4': 1.5, 'gpt-5': 2, 'custom-model': 3 },
    })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save user model pricing' })
    ).toBeEnabled()
  )
  while (screen.queryAllByRole('button', { name: 'Remove' }).length > 0) {
    await userEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0])
  }
  await userEvent.click(
    screen.getByRole('button', { name: 'Save user model pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenLastCalledWith('/api/user/2/model-pricing', {
      multipliers: {},
    })
  )
})

it('blocks saving after user pricing fails to load and offers retry', async () => {
  renderPermissions(100, undefined, false, true)
  expect(await screen.findByText('Pricing unavailable')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Save user model pricing' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
})

it('keeps user pricing edits and displays backend save failures', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'Pricing update denied' },
  })
  renderPermissions(100)
  await screen.findByDisplayValue('1.5')
  await userEvent.click(
    screen.getByRole('button', { name: 'Save user model pricing' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Pricing update denied'
  )
  expect(screen.getByDisplayValue('1.5')).toBeVisible()
})

it.each([
  [10, false],
  [100, true],
])(
  'does not load or expose user pricing for role=%s creating=%s',
  async (role, creating) => {
    renderPermissions(role as number, undefined, creating as boolean)
    await screen.findByRole('textbox', { name: 'Display Name' })
    expect(
      screen.queryByText('User model pricing multipliers')
    ).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalledWith('/api/user/2/model-pricing')
  }
)

it('admin cannot edit the audit permission even when the catalog is available', async () => {
  renderPermissions(10)
  await screen.findByDisplayValue('Managed admin')
  expect(
    screen.queryByRole('checkbox', { name: new RegExp(label) })
  ).not.toBeInTheDocument()
})
