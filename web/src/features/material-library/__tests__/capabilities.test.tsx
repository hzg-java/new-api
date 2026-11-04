import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MaterialCapabilities } from '../capabilities'

const clients: QueryClient[] = []

function renderIntegration() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <MaterialCapabilities />
    </QueryClientProvider>
  )
  return client
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/material/topenrouter/config') {
      return {
        data: {
          success: true,
          data: {
            available: true,
            enabled: false,
            configured: false,
            endpoint: 'https://tp-api.chinadatapay.com:8000',
            token_configured: false,
            channel_id: 0,
            channel_name: '',
          },
        },
      }
    }
    if (url !== '/api/material/mobile-cloud/config') {
      throw new Error(`Unexpected GET: ${url}`)
    }
    return {
      data: {
        success: true,
        data: {
          available: true,
          enabled: true,
          configured: false,
          encryption_ready: true,
          channel_id: 3,
          channel_name: 'Mobile Cloud channel',
          endpoint: 'https://materials.example.test',
          access_key_masked: '',
          secret_key_configured: false,
          pool_id: '',
        },
      },
    }
  })
})

afterEach(() => {
  cleanup()
  for (const client of clients) client.clear()
  clients.length = 0
  vi.restoreAllMocks()
})

describe('Material integrations page', () => {
  it('loads only Mobile Cloud by default under the shared integrations title', async () => {
    renderIntegration()
    expect(
      screen.getByRole('heading', { name: 'Material integrations' })
    ).toBeVisible()
    expect(screen.getByRole('tab', { name: 'Mobile Cloud' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    expect(screen.getByRole('tab', { name: 'TopenRouter' })).toHaveAttribute(
      'aria-selected',
      'false'
    )
    expect(await screen.findByLabelText('Access key (AK)')).toBeVisible()
    expect(
      screen.getByRole('switch', { name: 'Automatic material preparation' })
    ).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByLabelText('Token')).not.toBeInTheDocument()
    expect(api.get).toHaveBeenCalledTimes(1)
  })

  it('unmounts inactive providers and only refetches the active provider after switching tabs', async () => {
    const user = userEvent.setup()
    const client = renderIntegration()
    await screen.findByLabelText('Access key (AK)')
    await user.click(screen.getByRole('tab', { name: 'TopenRouter' }))
    expect(await screen.findByLabelText('Token')).toBeVisible()
    expect(
      screen.getByRole('switch', { name: 'Automatic material preparation' })
    ).toHaveAttribute('aria-checked', 'false')
    expect(screen.queryByLabelText('Access key (AK)')).not.toBeInTheDocument()
    expect(api.get).toHaveBeenCalledTimes(2)
    vi.mocked(api.get).mockClear()
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['material-library'] })
    })
    expect(api.get).toHaveBeenCalledTimes(1)
    expect(api.get).toHaveBeenCalledWith('/api/material/topenrouter/config', {
      disableDuplicate: true,
    })
    await user.click(screen.getByRole('tab', { name: 'Mobile Cloud' }))
    expect(await screen.findByLabelText('Access key (AK)')).toBeVisible()
    expect(
      screen.getByRole('switch', { name: 'Automatic material preparation' })
    ).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByLabelText('Token')).not.toBeInTheDocument()
    vi.mocked(api.get).mockClear()
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['material-library'] })
    })
    expect(api.get).toHaveBeenCalledTimes(1)
    expect(api.get).toHaveBeenCalledWith('/api/material/mobile-cloud/config', {
      disableDuplicate: true,
    })
  })

  it('supports keyboard tab selection with matching focus and accessible panel state', async () => {
    const user = userEvent.setup()
    renderIntegration()
    await screen.findByLabelText('Access key (AK)')
    const mobile = screen.getByRole('tab', { name: 'Mobile Cloud' })
    const topenrouter = screen.getByRole('tab', { name: 'TopenRouter' })
    mobile.focus()
    await user.keyboard('{ArrowRight}')
    expect(topenrouter).toHaveFocus()
    expect(topenrouter).toHaveAttribute('aria-selected', 'false')
    expect(screen.queryByLabelText('Token')).not.toBeInTheDocument()
    await user.keyboard('{Enter}')
    expect(topenrouter).toHaveAttribute('aria-selected', 'true')
    expect(
      await screen.findByRole('tabpanel', { name: 'TopenRouter' })
    ).toBeVisible()
    expect(await screen.findByLabelText('Token')).toBeVisible()
    await user.keyboard('{ArrowLeft}')
    expect(mobile).toHaveFocus()
    await user.keyboard(' ')
    expect(mobile).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByLabelText('Access key (AK)')).toBeVisible()
  })

  it('keeps credential configuration without manual capability controls or requests', async () => {
    renderIntegration()
    expect(await screen.findByLabelText('Access key (AK)')).toBeVisible()
    expect(screen.getByLabelText('Secret key (SK)')).toBeVisible()
    expect(screen.getByLabelText('Pool-Id')).toBeVisible()
    expect(screen.getByLabelText('Channel ID')).toHaveValue(3)
    expect(
      screen.getByRole('button', { name: 'Save configuration' })
    ).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: 'Mobile Cloud capabilities' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /^Edit capability:/ })
    ).not.toBeInTheDocument()
    expect(api.get).toHaveBeenCalledTimes(1)
    expect(api.get).toHaveBeenCalledWith('/api/material/mobile-cloud/config', {
      disableDuplicate: true,
    })
  })

  it('retains configuration error recovery without loading the capability matrix', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: { success: false, message: 'Configuration unavailable' },
    })
    const user = userEvent.setup()
    renderIntegration()
    expect(await screen.findByText('Configuration unavailable')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByLabelText('Access key (AK)')).toBeVisible()
    expect(api.get).toHaveBeenCalledTimes(2)
    expect(
      vi
        .mocked(api.get)
        .mock.calls.every(
          ([url]) => url === '/api/material/mobile-cloud/config'
        )
    ).toBe(true)
  })
})
