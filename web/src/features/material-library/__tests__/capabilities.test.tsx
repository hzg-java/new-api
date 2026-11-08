import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MaterialCapabilities } from '../capabilities'

const mobileCloudConfig = {
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
}
const topenRouterConfig = {
  available: true,
  enabled: false,
  configured: false,
  endpoint: 'https://tp-api.chinadatapay.com:8000',
  token_configured: false,
  channel_id: 0,
  channel_name: '',
}
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
          data: topenRouterConfig,
        },
      }
    }
    if (url !== '/api/material/mobile-cloud/config') {
      throw new Error(`Unexpected GET: ${url}`)
    }
    return {
      data: {
        success: true,
        data: mobileCloudConfig,
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
  it.each([
    {
      provider: 'mobile_cloud',
      tab: 'Mobile Cloud',
      otherTab: 'TopenRouter',
      outcome: 'success',
    },
    {
      provider: 'mobile_cloud',
      tab: 'Mobile Cloud',
      otherTab: 'TopenRouter',
      outcome: 'failure',
    },
    {
      provider: 'topenrouter',
      tab: 'TopenRouter',
      otherTab: 'Mobile Cloud',
      outcome: 'success',
    },
    {
      provider: 'topenrouter',
      tab: 'TopenRouter',
      otherTab: 'Mobile Cloud',
      outcome: 'failure',
    },
  ] as const)(
    'blocks click and keyboard provider switching during a pending $tab test and restores it after $outcome',
    async ({ provider, tab, otherTab, outcome }) => {
      vi.mocked(api.get).mockImplementation(async (url) => {
        if (url === '/api/material/mobile-cloud/config') {
          return {
            data: {
              success: true,
              data: {
                ...mobileCloudConfig,
                configured: true,
                access_key_masked: 'AK****saved',
                secret_key_configured: true,
                pool_id: 'test-pool',
              },
            },
          }
        }
        if (url === '/api/material/topenrouter/config') {
          return {
            data: {
              success: true,
              data: {
                ...topenRouterConfig,
                configured: true,
                token_configured: true,
                channel_id: 7,
                channel_name: 'TopenRouter channel',
              },
            },
          }
        }
        throw new Error(`Unexpected GET: ${url}`)
      })
      const result = {
        provider,
        channel_id: provider === 'mobile_cloud' ? 3 : 7,
        channel_name:
          provider === 'mobile_cloud'
            ? 'Mobile Cloud channel'
            : 'TopenRouter channel',
        operation: 'list_groups',
        success: true,
        elapsed_ms: 1,
      }
      let finishTest!: (response: {
        data: { success: boolean; data: typeof result }
      }) => void
      let failTest!: (error: Error) => void
      const post = vi.spyOn(api, 'post').mockReturnValueOnce(
        new Promise((resolve, reject) => {
          finishTest = resolve
          failTest = reject
        })
      )
      const put = vi
        .spyOn(api, 'put')
        .mockRejectedValue(new Error('Unexpected save'))
      const user = userEvent.setup()
      renderIntegration()
      if (provider === 'topenrouter') {
        await user.click(screen.getByRole('tab', { name: tab }))
      }
      const execute = await screen.findByRole('button', { name: 'Execute' })
      const active = screen.getByRole('tab', { name: tab })
      const inactive = screen.getByRole('tab', { name: otherTab })
      await user.click(execute)
      expect(screen.getByText('Testing material API...')).toBeVisible()

      await user.click(inactive)
      expect(active).toHaveAttribute('aria-selected', 'true')
      expect(inactive).toHaveAttribute('aria-selected', 'false')
      inactive.focus()
      await user.keyboard('{Enter} ')
      expect(active).toHaveAttribute('aria-selected', 'true')
      active.focus()
      await user.keyboard('{ArrowRight}{Enter} ')
      expect(active).toHaveAttribute('aria-selected', 'true')
      for (const trigger of [active, inactive]) {
        expect(trigger).toHaveAttribute('aria-disabled', 'true')
      }
      expect(execute).toBeDisabled()
      const save = screen.getByRole('button', { name: 'Save configuration' })
      expect(save).toBeDisabled()
      await user.click(execute)
      await user.click(save)
      expect(post).toHaveBeenCalledTimes(1)
      expect(put).not.toHaveBeenCalled()

      await act(async () => {
        if (outcome === 'success') {
          finishTest({ data: { success: true, data: result } })
        } else {
          failTest(new Error('Test unavailable'))
        }
      })
      expect(
        await screen.findByText(
          outcome === 'success'
            ? 'Material API test succeeded'
            : 'Material API test failed'
        )
      ).toBeVisible()
      for (const trigger of [active, inactive]) {
        expect(trigger).not.toHaveAttribute('aria-disabled', 'true')
      }
      expect(execute).toBeEnabled()
      expect(save).toBeEnabled()
      await user.click(inactive)
      expect(
        await screen.findByRole('tabpanel', { name: otherTab })
      ).toBeVisible()
      expect(
        screen.queryByRole('tabpanel', { name: tab })
      ).not.toBeInTheDocument()
      inactive.focus()
      await user.keyboard('{ArrowRight}{Enter}')
      expect(await screen.findByRole('tabpanel', { name: tab })).toBeVisible()
      expect(
        screen.queryByRole('tabpanel', { name: otherTab })
      ).not.toBeInTheDocument()
      expect(post).toHaveBeenCalledTimes(1)
    }
  )

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
