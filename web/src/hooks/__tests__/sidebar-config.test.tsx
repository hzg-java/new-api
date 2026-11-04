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
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterContextProvider,
} from '@tanstack/react-router'
import { cleanup, render, renderHook, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest'

import { checkIsActive } from '@/components/layout/lib/url-utils'
import { SearchProvider } from '@/context/search-provider'
import {
  parseSidebarModulesAdmin,
  serializeSidebarModulesAdmin,
} from '@/features/system-settings/maintenance/config'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarView } from '../use-sidebar-view'

const originalGetAnimations = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})

afterAll(() => {
  if (originalGetAnimations) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      originalGetAnimations
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})

beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  useAuthStore.getState().auth.reset()
})

function sidebarFor(
  admin?: object,
  user?: object,
  canConfigure = true,
  role = 10
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    SidebarModulesAdmin: admin ? JSON.stringify(admin) : '',
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    role,
    permissions: { sidebar_settings: canConfigure },
    sidebar_modules: user ? JSON.stringify(user) : '',
  })
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory({ initialEntries: ['/materials'] }),
  })
  function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        <RouterContextProvider router={router}>
          {props.children}
        </RouterContextProvider>
      </QueryClientProvider>
    )
  }
  const result = renderHook(() => useSidebarView().navGroups, {
    wrapper: Wrapper,
  })
  return { ...result, Wrapper }
}

describe('security sidebar visibility', () => {
  it('hides security navigation for ordinary users even when configuration enables it', () => {
    const { result } = sidebarFor(undefined, undefined, true, 1)
    expect(
      result.current
        .flatMap((group) => group.items)
        .some((item) => 'url' in item && item.url === '/security')
    ).toBe(false)
  })

  it('old configurations show Security & Access immediately after Profile and keep API Keys', () => {
    const { result } = sidebarFor(
      { personal: { enabled: true, personal: true, topup: true } },
      { personal: { enabled: true, personal: true } }
    )
    expect(
      result.current
        .find((group) => group.id === 'personal')
        ?.items.map((item) => item.title)
    ).toEqual(['Wallet', 'Profile', 'Security & Access'])
    expect(
      result.current
        .flatMap((group) => group.items)
        .some((item) => item.title === 'API Keys')
    ).toBe(true)
  })
  it.each([
    [{ personal: { enabled: true, security: false } }, undefined],
    [{ personal: { enabled: false } }, { personal: { security: true } }],
    [undefined, { personal: { enabled: true, security: false } }],
    [undefined, { personal: { enabled: false } }],
  ])(
    'admin or user disablement hides Security & Access (%j, %j)',
    (admin, user) => {
      const { result } = sidebarFor(admin, user)
      expect(
        result.current
          .flatMap((group) => group.items)
          .some((item) => item.title === 'Security & Access')
      ).toBe(false)
    }
  )
  it('users without sidebar configuration permission retain the admin view', () => {
    const { result } = sidebarFor(
      undefined,
      { personal: { security: false } },
      false
    )
    expect(
      result.current
        .flatMap((group) => group.items)
        .some((item) => item.title === 'Security & Access')
    ).toBe(true)
  })
})

describe('audit log sidebar entry', () => {
  it('admin settings default Audit Logs to visible and preserve its independent toggle when saved', () => {
    const config = parseSidebarModulesAdmin(
      '{"console":{"enabled":true,"log":true}}'
    )
    expect(config.console.audit).toBe(true)
    config.console.audit = false
    const saved = parseSidebarModulesAdmin(serializeSidebarModulesAdmin(config))
    const { result } = sidebarFor(saved)
    const titles = result.current
      .flatMap((group) => group.items)
      .map((item) => item.title)
    expect(titles).not.toContain('Audit Logs')
    expect(titles).toContain('Usage Logs')
  })

  it('legacy configurations show a separate Audit Logs link immediately after Usage Logs', () => {
    const { result } = sidebarFor(
      { console: { enabled: true, log: true } },
      { console: { enabled: true, log: true } }
    )
    const items =
      result.current.find((group) => group.id === 'general')?.items ?? []
    const usageIndex = items.findIndex((item) => item.title === 'Usage Logs')
    expect(items[usageIndex + 1]).toMatchObject({
      title: 'Audit Logs',
      url: '/usage-logs/audit',
    })
    const selected = items.filter((item) =>
      checkIsActive('/usage-logs/audit', item)
    )
    expect(selected.map((item) => item.title)).toEqual(['Audit Logs'])
  })

  it.each([
    [{ console: { enabled: true, audit: false } }, undefined],
    [{ console: { enabled: false } }, { console: { audit: true } }],
    [undefined, { console: { enabled: true, audit: false } }],
    [undefined, { console: { enabled: false } }],
  ])(
    'admin and personal visibility rules can hide Audit Logs (%j, %j)',
    (admin, user) => {
      const { result } = sidebarFor(admin, user)
      expect(
        result.current
          .flatMap((group) => group.items)
          .some((item) => item.title === 'Audit Logs')
      ).toBe(false)
    }
  )

  it('hiding Usage Logs does not hide the independently configured Audit Logs entry', () => {
    const { result } = sidebarFor({ console: { enabled: true, log: false } })
    const titles = result.current
      .flatMap((group) => group.items)
      .map((item) => item.title)
    expect(titles).not.toContain('Usage Logs')
    expect(titles).toContain('Audit Logs')
  })
})

describe('material sidebar entries', () => {
  it('keeps administrator material entries out of the ordinary user command menu', async () => {
    const { Wrapper } = sidebarFor(undefined, undefined, true, ROLE.USER)
    render(<SearchProvider>{null}</SearchProvider>, { wrapper: Wrapper })
    const user = userEvent.setup()
    await user.keyboard('{Control>}k{/Control}')

    expect(await screen.findByRole('option', { name: 'Profile' })).toBeVisible()
    expect(
      screen.queryByRole('option', { name: 'Material library' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: 'All materials' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: 'Material routes' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('option', {
        name: 'Material integrations',
      })
    ).not.toBeInTheDocument()
  })

  it('keeps disabled material sections out of the administrator command menu', async () => {
    const { Wrapper } = sidebarFor({
      console: { enabled: false },
      admin: { enabled: false },
    })
    render(<SearchProvider>{null}</SearchProvider>, { wrapper: Wrapper })
    const user = userEvent.setup()
    await user.keyboard('{Control>}k{/Control}')

    expect(await screen.findByRole('option', { name: 'Profile' })).toBeVisible()
    expect(
      screen.queryByRole('option', { name: 'Material library' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: 'All materials' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: 'Material routes' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('option', {
        name: 'Material integrations',
      })
    ).not.toBeInTheDocument()
  })

  it('keeps material routes out of the administrator command menu even when legacy settings enable it', async () => {
    const { Wrapper } = sidebarFor(
      { admin: { enabled: true, material_routes: true } },
      { admin: { enabled: true, material_routes: true } }
    )
    render(<SearchProvider>{null}</SearchProvider>, { wrapper: Wrapper })
    const user = userEvent.setup()
    await user.keyboard('{Control>}k{/Control}')

    expect(
      await screen.findByRole('option', { name: 'All materials' })
    ).toBeVisible()
    expect(
      screen.queryByRole('option', { name: 'Material routes' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: 'Material library' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('option', { name: 'Material integrations' })
    ).toBeVisible()
  })

  it.each([ROLE.USER, ROLE.ADMIN])(
    'never restores the retired console entry for role %s even with explicit enablement',
    (role) => {
      const { result } = sidebarFor(
        { console: { enabled: true, materials: true } },
        { console: { enabled: true, materials: true } },
        true,
        role
      )
      const urls = result.current.flatMap((group) =>
        group.items.map((item) => item.url)
      )
      expect(urls).not.toContain('/materials')
      if (role >= ROLE.ADMIN) {
        expect(urls.filter((url) => url === '/material-records')).toHaveLength(
          1
        )
        return
      }
      expect(urls).not.toContain('/material-records')
      expect(urls).not.toContain('/material-routes')
      expect(urls).not.toContain('/material-capabilities')
    }
  )

  it.each([ROLE.ADMIN, ROLE.SUPER_ADMIN])(
    'keeps material routes out of the sidebar for role %s with explicit legacy enablement',
    (role) => {
      const { result } = sidebarFor(
        { admin: { enabled: true, channel: true, material_routes: true } },
        { admin: { enabled: true, channel: true, material_routes: true } },
        true,
        role
      )
      const items =
        result.current.find((group) => group.id === 'admin')?.items ?? []
      expect(items).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            title: 'All materials',
            url: '/material-records',
          }),
          expect.objectContaining({
            title: 'Material integrations',
            url: '/material-capabilities',
          }),
        ])
      )
      expect(items.map((item) => item.url)).not.toContain('/material-routes')
      expect(
        items.filter((item) => checkIsActive('/material-records', item))
      ).toEqual([expect.objectContaining({ url: '/material-records' })])
    }
  )

  it.each([
    [{ console: { enabled: false }, admin: { enabled: false } }, undefined],
    [undefined, { console: { enabled: false }, admin: { enabled: false } }],
  ])(
    'hides material links when their sections are disabled (%j, %j)',
    (admin, user) => {
      const { result } = sidebarFor(admin, user)
      const urls = result.current.flatMap((group) =>
        group.items.map((item) => item.url)
      )
      expect(urls).not.toContain('/materials')
      expect(urls).not.toContain('/material-records')
      expect(urls).not.toContain('/material-routes')
      expect(urls).not.toContain('/material-capabilities')
    }
  )
})
