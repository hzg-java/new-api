import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { Profile } from '..'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

test.each([1, 10, 100])(
  'profile preferences visibility follows role %s',
  async (role) => {
    const profile = {
      id: 1,
      username: 'preview',
      display_name: 'Preview',
      role,
      quota: 0,
      used_quota: 0,
      request_count: 0,
      status: 1,
      group: 'default',
      setting: '{}',
    }
    useAuthStore
      .getState()
      .auth.setUser({ ...profile, permissions: { sidebar_settings: true } })
    vi.spyOn(api, 'get').mockImplementation(async (url) => ({
      data: {
        success: true,
        data: url === '/api/user/self' ? profile : { checkin_enabled: false },
      },
    }))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <Profile />
      </QueryClientProvider>
    )
    await screen.findByText('Preview')
    expect(screen.queryByText('User ID 1')).not.toBeInTheDocument()
    expect(
      screen.queryByText('default', { exact: true })
    ).not.toBeInTheDocument()
    expect(screen.queryByText('@preview')).not.toBeInTheDocument()
    if (role === 1) {
      expect(
        screen.queryByText('Sidebar Personal Settings')
      ).not.toBeInTheDocument()
      expect(
        screen.queryByText('Settings & Preferences')
      ).not.toBeInTheDocument()
      expect(screen.queryByText('Language Preferences')).not.toBeInTheDocument()
    } else {
      expect(screen.getByText('Sidebar Personal Settings')).toBeInTheDocument()
      expect(screen.getByText('Settings & Preferences')).toBeInTheDocument()
      expect(screen.getByText('Language Preferences')).toBeInTheDocument()
    }
  }
)
