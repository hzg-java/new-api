import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toaster } from 'sonner'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MaterialCapabilities } from '../capabilities'

const configPath = '/api/material/topenrouter/config'
const configKey = ['material-library', 'topenrouter-config']
const defaultEndpoint = 'https://tp-api.chinadatapay.com:8000'
const initialConfig = {
  available: true,
  enabled: true,
  configured: false,
  endpoint: defaultEndpoint,
  token_configured: false,
  channel_id: 0,
  channel_name: '',
}
const savedConfig = {
  ...initialConfig,
  configured: true,
  token_configured: true,
  channel_id: 42,
  channel_name: 'TopenRouter video',
}
const clients: QueryClient[] = []

async function openSettings(user: ReturnType<typeof userEvent.setup>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <MaterialCapabilities />
      <Toaster />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('tab', { name: 'TopenRouter' }))
  return client
}

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === configPath) {
      return { data: { success: true, data: initialConfig } }
    }
    if (url === '/api/material/mobile-cloud/config') {
      return {
        data: {
          success: true,
          data: {
            available: true,
            enabled: true,
            configured: false,
            encryption_ready: true,
            endpoint: 'https://mobile.example.test',
            access_key_masked: '',
            secret_key_configured: false,
            pool_id: '',
            channel_id: 0,
            channel_name: '',
          },
        },
      }
    }
    throw new Error(`Unexpected GET: ${url}`)
  })
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, data: savedConfig },
  })
})

afterEach(() => {
  cleanup()
  for (const client of clients) client.clear()
  clients.length = 0
  localStorage.clear()
  sessionStorage.clear()
  vi.restoreAllMocks()
})

describe('TopenRouter configuration', () => {
  it.each([true, false])(
    'displays the loaded enabled=%s state and its video-only scope',
    async (enabled) => {
      vi.mocked(api.get).mockResolvedValue({
        data: { success: true, data: { ...savedConfig, enabled } },
      })
      const user = userEvent.setup()
      await openSettings(user)
      const toggle = await screen.findByRole('switch', {
        name: 'Automatic material preparation',
      })
      expect(toggle).toHaveAttribute('aria-checked', String(enabled))
      expect(toggle).toHaveAccessibleDescription(
        'Only controls automatic material preparation for video requests. When disabled, original parameters pass through unchanged. Manual material library operations are unaffected.'
      )
      expect(screen.getByText(enabled ? 'Enabled' : 'Disabled')).toBeVisible()
      expect(screen.getByLabelText('Token')).toBeEnabled()
    }
  )

  it.each([true, false])(
    'saves enabled=%s after a keyboard toggle and keeps it after reload',
    async (enabled) => {
      vi.mocked(api.get).mockResolvedValue({
        data: { success: true, data: { ...savedConfig, enabled: !enabled } },
      })
      vi.mocked(api.put).mockResolvedValueOnce({
        data: { success: true, data: { ...savedConfig, enabled } },
      })
      const user = userEvent.setup()
      await openSettings(user)
      const toggle = await screen.findByRole('switch', {
        name: 'Automatic material preparation',
      })
      toggle.focus()
      await user.keyboard(' ')
      expect(toggle).toHaveAttribute('aria-checked', String(enabled))
      vi.mocked(api.get).mockResolvedValue({
        data: { success: true, data: { ...savedConfig, enabled } },
      })
      await user.click(
        screen.getByRole('button', { name: 'Save configuration' })
      )
      await waitFor(() =>
        expect(api.put).toHaveBeenCalledWith(configPath, {
          enabled,
          channel_id: savedConfig.channel_id,
          endpoint: defaultEndpoint,
          token: '',
        })
      )
      await waitFor(() =>
        expect(
          screen.getByRole('button', { name: 'Save configuration' })
        ).toBeEnabled()
      )
      expect(toggle).toHaveAttribute('aria-checked', String(enabled))
      cleanup()
      await openSettings(user)
      expect(
        await screen.findByRole('switch', {
          name: 'Automatic material preparation',
        })
      ).toHaveAttribute('aria-checked', String(enabled))
    }
  )

  it.each([true, false])(
    'refreshes untouched enabled=%s but never overwrites a dirty toggle',
    async (enabled) => {
      vi.mocked(api.get).mockResolvedValue({
        data: { success: true, data: { ...savedConfig, enabled: !enabled } },
      })
      const user = userEvent.setup()
      const client = await openSettings(user)
      const toggle = await screen.findByRole('switch', {
        name: 'Automatic material preparation',
      })
      act(() => client.setQueryData(configKey, { ...savedConfig, enabled }))
      await waitFor(() =>
        expect(toggle).toHaveAttribute('aria-checked', String(enabled))
      )
      await user.click(toggle)
      act(() =>
        client.setQueryData(configKey, {
          ...savedConfig,
          enabled,
          channel_id: 7,
        })
      )
      expect(toggle).toHaveAttribute('aria-checked', String(!enabled))
      expect(screen.getByText(!enabled ? 'Enabled' : 'Disabled')).toBeVisible()
    }
  )

  it('uses the default HTTPS endpoint and requires an existing channel while explaining AIGC-only support', async () => {
    const user = userEvent.setup()
    await openSettings(user)
    expect(await screen.findByLabelText('API endpoint')).toHaveValue(
      defaultEndpoint
    )
    expect(screen.getByLabelText('Token')).toHaveAttribute('type', 'password')
    const channel = screen.getByRole('spinbutton', { name: 'Channel ID' })
    expect(channel).toHaveValue(null)
    expect(channel).toHaveAttribute('min', '1')
    expect(channel).toHaveAttribute('step', '1')
    expect(
      screen.getByText(
        'TopenRouter supports AIGC images only; real-person verification is not required.'
      )
    ).toBeVisible()
    expect(
      screen.getByText('Select a channel to complete this configuration.')
    ).toBeVisible()
    expect(
      screen.queryByText(
        'Saving automatically creates a material-only channel that is not used for model routing.'
      )
    ).not.toBeInTheDocument()
    for (const label of [
      'Model',
      'Access key (AK)',
      'Secret key (SK)',
      'Pool-Id',
    ]) {
      expect(screen.queryByLabelText(label)).not.toBeInTheDocument()
    }
    expect(
      screen.queryByText(/connected|connection verified/i)
    ).not.toBeInTheDocument()
  })

  it.each(['', '0', '-1', '1.5'])(
    'rejects channel ID %j before saving otherwise valid credentials',
    async (channelId) => {
      const user = userEvent.setup()
      await openSettings(user)
      await user.type(
        await screen.findByLabelText('Token'),
        'new-material-token'
      )
      const channel = screen.getByRole('spinbutton', { name: 'Channel ID' })
      if (channelId) await user.type(channel, channelId)
      await user.click(
        screen.getByRole('button', { name: 'Save configuration' })
      )
      expect(channel).toHaveAttribute('aria-invalid', 'true')
      expect(channel).toHaveAccessibleDescription('Select a valid channel ID.')
      expect(api.put).not.toHaveBeenCalled()
    }
  )

  it('requires a token and channel on initial setup before saving normalized credentials and clearing the token', async () => {
    const user = userEvent.setup()
    await openSettings(user)
    await user.click(
      await screen.findByRole('button', { name: 'Save configuration' })
    )
    expect(screen.getByLabelText('Token')).toHaveAttribute(
      'aria-invalid',
      'true'
    )
    expect(screen.getByLabelText('Channel ID')).toHaveAttribute(
      'aria-invalid',
      'true'
    )
    expect(api.put).not.toHaveBeenCalled()
    await user.type(screen.getByLabelText('Channel ID'), '42')
    await user.clear(screen.getByLabelText('API endpoint'))
    await user.type(
      screen.getByLabelText('API endpoint'),
      '  https://materials.example.test  '
    )
    await user.type(screen.getByLabelText('Token'), '  new-material-token  ')
    const saved = {
      ...savedConfig,
      endpoint: 'https://materials.example.test',
    }
    vi.mocked(api.put).mockResolvedValueOnce({
      data: { success: true, data: saved },
    })
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: saved },
    })
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(configPath, {
        enabled: true,
        channel_id: 42,
        endpoint: 'https://materials.example.test',
        token: 'new-material-token',
      })
    )
    await waitFor(() => expect(screen.getByLabelText('Token')).toHaveValue(''))
    expect(screen.getByLabelText('Channel ID')).toHaveValue(42)
    expect(screen.getByLabelText('Channel ID')).toHaveAccessibleDescription(
      'TopenRouter video #42'
    )
    expect(screen.getByLabelText('API endpoint')).toHaveValue(saved.endpoint)
    expect(
      screen.queryByText(/connected|connection verified/i)
    ).not.toBeInTheDocument()
  })

  it('submits an empty token to keep the existing credential and displays the saved channel ID without a name', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: { ...savedConfig, channel_name: '' } },
    })
    const user = userEvent.setup()
    await openSettings(user)
    const token = await screen.findByLabelText('Token')
    expect(token).toHaveValue('')
    expect(token).toHaveAccessibleDescription(
      'Leave blank to keep the existing credential'
    )
    expect(screen.getByLabelText('Channel ID')).toHaveValue(42)
    expect(screen.getByLabelText('Channel ID')).toHaveAccessibleDescription(
      '#42'
    )
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(configPath, {
        enabled: true,
        channel_id: 42,
        endpoint: defaultEndpoint,
        token: '',
      })
    )
  })

  it.each([
    '',
    'not-a-url',
    'http://insecure.example.test',
    'https://user:password@example.test',
    'https://example.test/#fragment',
  ])(
    'rejects invalid or insecure endpoint %j without sending credentials',
    async (endpoint) => {
      const user = userEvent.setup()
      await openSettings(user)
      await user.type(
        await screen.findByLabelText('Token'),
        'new-material-token'
      )
      await user.type(screen.getByLabelText('Channel ID'), '42')
      const input = screen.getByLabelText('API endpoint')
      await user.clear(input)
      if (endpoint) await user.type(input, endpoint)
      await user.click(
        screen.getByRole('button', { name: 'Save configuration' })
      )
      expect(input).toHaveAttribute('aria-invalid', 'true')
      expect(api.put).not.toHaveBeenCalled()
    }
  )

  it.each(['business', 'network'] as const)(
    'preserves the draft and reports a %s save failure without success',
    async (failure) => {
      if (failure === 'business') {
        vi.mocked(api.put).mockResolvedValueOnce({
          data: { success: false, message: 'Configuration rejected' },
        })
      } else {
        vi.mocked(api.put).mockRejectedValueOnce(
          new Error('Network unavailable')
        )
      }
      const user = userEvent.setup()
      await openSettings(user)
      await user.type(
        await screen.findByLabelText('Token'),
        '  unsaved-token  '
      )
      await user.type(screen.getByLabelText('Channel ID'), '42')
      await user.click(
        screen.getByRole('switch', { name: 'Automatic material preparation' })
      )
      await user.click(
        screen.getByRole('button', { name: 'Save configuration' })
      )
      expect(
        await screen.findByText(
          failure === 'business'
            ? 'Configuration rejected'
            : 'Network unavailable'
        )
      ).toBeVisible()
      expect(screen.getByLabelText('Token')).toHaveValue('  unsaved-token  ')
      expect(screen.getByLabelText('Channel ID')).toHaveValue(42)
      expect(
        screen.getByRole('switch', { name: 'Automatic material preparation' })
      ).toHaveAttribute('aria-checked', 'false')
      expect(api.put).toHaveBeenCalledWith(
        configPath,
        expect.objectContaining({ enabled: false })
      )
      expect(
        screen.getByRole('button', { name: 'Save configuration' })
      ).toBeEnabled()
      expect(screen.queryByText('Configuration saved')).not.toBeInTheDocument()
    }
  )

  it('disables inputs and duplicate submits while saving without retaining the token in mutation variables or browser storage', async () => {
    let finishSave!: (response: {
      data: { success: boolean; data: typeof savedConfig }
    }) => void
    vi.mocked(api.put).mockReturnValueOnce(
      new Promise((resolve) => {
        finishSave = resolve
      })
    )
    const user = userEvent.setup()
    const client = await openSettings(user)
    await user.type(
      await screen.findByLabelText('Token'),
      'pending-private-token'
    )
    await user.type(screen.getByLabelText('Channel ID'), '42')
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText('Token')).toBeDisabled()
    expect(screen.getByLabelText('API endpoint')).toBeDisabled()
    expect(screen.getByLabelText('Channel ID')).toBeDisabled()
    const toggle = screen.getByRole('switch', {
      name: 'Automatic material preparation',
    })
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-checked', 'true')
    expect(
      screen.getByRole('button', { name: 'Toggle password visibility' })
    ).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
    const form = screen.getByLabelText('Token').closest('form')
    if (!form) throw new Error('Configuration must expose a submit form')
    expect(form).toHaveAttribute('aria-busy', 'true')
    fireEvent.submit(form)
    expect(api.put).toHaveBeenCalledTimes(1)
    expect(
      JSON.stringify(
        client
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state)
      )
    ).not.toContain('pending-private-token')
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
    await act(async () =>
      finishSave({ data: { success: true, data: savedConfig } })
    )
    await waitFor(() => expect(screen.getByLabelText('Token')).toHaveValue(''))
  })

  it('clears a replacement token before a delayed refetch and preserves new dirty edits when it completes', async () => {
    const user = userEvent.setup()
    const client = await openSettings(user)
    await screen.findByLabelText('Token')
    let finishRefetch!: (response: {
      data: { success: boolean; data: typeof savedConfig }
    }) => void
    vi.mocked(api.get).mockReturnValueOnce(
      new Promise((resolve) => {
        finishRefetch = resolve
      })
    )
    await user.type(screen.getByLabelText('Token'), 'replacement-private-token')
    await user.type(screen.getByLabelText('Channel ID'), '42')
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() => expect(screen.getByLabelText('Token')).toHaveValue(''))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save configuration' })
      ).toBeEnabled()
    )
    expect(screen.getByLabelText('Channel ID')).toHaveValue(42)
    expect(screen.getByLabelText('Channel ID')).toHaveAccessibleDescription(
      'TopenRouter video #42'
    )
    expect(
      JSON.stringify(
        client
          .getQueryCache()
          .getAll()
          .map((query) => query.state)
      )
    ).not.toContain('replacement-private-token')
    expect(
      JSON.stringify(
        client
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state)
      )
    ).not.toContain('replacement-private-token')
    await user.clear(screen.getByLabelText('API endpoint'))
    await user.type(
      screen.getByLabelText('API endpoint'),
      'https://draft.example.test'
    )
    await user.type(screen.getByLabelText('Token'), 'next-draft-token')
    await user.clear(screen.getByLabelText('Channel ID'))
    await user.type(screen.getByLabelText('Channel ID'), '84')
    await user.click(
      screen.getByRole('switch', { name: 'Automatic material preparation' })
    )
    await act(async () =>
      finishRefetch({
        data: {
          success: true,
          data: {
            ...savedConfig,
            channel_id: 7,
            channel_name: 'Updated video channel',
          },
        },
      })
    )
    await waitFor(() =>
      expect(screen.getByLabelText('Channel ID')).toHaveAccessibleDescription(
        'Updated video channel #7'
      )
    )
    expect(screen.getByLabelText('API endpoint')).toHaveValue(
      'https://draft.example.test'
    )
    expect(screen.getByLabelText('Token')).toHaveValue('next-draft-token')
    expect(screen.getByLabelText('Channel ID')).toHaveValue(84)
    expect(
      screen.getByRole('switch', { name: 'Automatic material preparation' })
    ).toHaveAttribute('aria-checked', 'false')
  })

  it('refreshes an untouched saved channel after success resets its dirty state', async () => {
    const user = userEvent.setup()
    const client = await openSettings(user)
    await user.type(await screen.findByLabelText('Channel ID'), '42')
    await user.type(screen.getByLabelText('Token'), 'new-material-token')
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: savedConfig },
    })
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() => expect(screen.getByLabelText('Token')).toHaveValue(''))
    expect(screen.getByLabelText('Channel ID')).toHaveValue(42)
    const refreshedConfig = {
      ...savedConfig,
      channel_id: 7,
      channel_name: 'Updated video channel',
    }
    vi.mocked(api.get).mockResolvedValueOnce({
      data: { success: true, data: refreshedConfig },
    })
    await act(async () => {
      await client.refetchQueries({ queryKey: configKey })
    })
    await waitFor(() =>
      expect(screen.getByLabelText('Channel ID')).toHaveValue(7)
    )
    expect(screen.getByLabelText('Channel ID')).toHaveAccessibleDescription(
      'Updated video channel #7'
    )
  })

  it('does not retain Axios request credentials in caches or console output after save rejection', async () => {
    const logs = [
      vi.spyOn(console, 'error'),
      vi.spyOn(console, 'warn'),
      vi.spyOn(console, 'log'),
    ]
    vi.mocked(api.put).mockRejectedValueOnce({
      isAxiosError: true,
      message: 'Request failed',
      config: { data: '{"token":"private-failed-token"}' },
      response: {
        status: 400,
        data: { success: false, message: 'Configuration rejected' },
      },
    })
    const user = userEvent.setup()
    const client = await openSettings(user)
    await user.type(
      await screen.findByLabelText('Token'),
      'private-failed-token'
    )
    await user.type(screen.getByLabelText('Channel ID'), '42')
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    expect(await screen.findByText('Configuration rejected')).toBeVisible()
    expect(screen.getByLabelText('Token')).toHaveValue('private-failed-token')
    const cachedState = JSON.stringify({
      queries: client
        .getQueryCache()
        .getAll()
        .map((query) => query.state),
      mutations: client
        .getMutationCache()
        .getAll()
        .map((mutation) => mutation.state),
      logs: logs.flatMap((log) => log.mock.calls),
    })
    expect(cachedState).not.toContain('private-failed-token')
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })

  it('disables configuration when the backend marks the integration unavailable', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: { ...initialConfig, available: false, enabled: false },
      },
    })
    const user = userEvent.setup()
    await openSettings(user)
    expect(await screen.findByLabelText('Token')).toBeDisabled()
    expect(screen.getByLabelText('API endpoint')).toBeDisabled()
    expect(screen.getByLabelText('Channel ID')).toBeDisabled()
    const toggle = screen.getByRole('switch', {
      name: 'Automatic material preparation',
    })
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    expect(
      screen.getByText(
        'TopenRouter material integration is unavailable. Check the backend service, then reload this page.'
      )
    ).toBeVisible()
    const save = screen.getByRole('button', { name: 'Save configuration' })
    expect(save).toBeDisabled()
    const form = screen.getByLabelText('Token').closest('form')
    if (!form) throw new Error('Configuration must expose a submit form')
    fireEvent.submit(form)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('hides the form until loading completes and retries a failed initial configuration request', async () => {
    let finishLoad!: (response: {
      data: { success: boolean; message: string }
    }) => void
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (url !== configPath) {
        return { data: { success: true, data: initialConfig } }
      }
      return new Promise((resolve) => {
        finishLoad = resolve
      })
    })
    const user = userEvent.setup()
    await openSettings(user)
    expect(screen.queryByLabelText('Token')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Save configuration' })
    ).not.toBeInTheDocument()
    await act(async () =>
      finishLoad({
        data: { success: false, message: 'Configuration unavailable' },
      })
    )
    expect(await screen.findByText('Configuration unavailable')).toBeVisible()
    expect(screen.queryByLabelText('Token')).not.toBeInTheDocument()
    vi.mocked(api.get).mockResolvedValueOnce({
      data: { success: true, data: initialConfig },
    })
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByLabelText('Token')).toBeEnabled()
  })

  it('preserves the unsaved form when a background configuration request fails', async () => {
    const user = userEvent.setup()
    const client = await openSettings(user)
    await user.type(
      await screen.findByLabelText('Token'),
      'unsaved-private-token'
    )
    await user.type(screen.getByLabelText('Channel ID'), '84')
    await user.clear(screen.getByLabelText('API endpoint'))
    await user.type(
      screen.getByLabelText('API endpoint'),
      'https://draft.example.test'
    )
    vi.mocked(api.get).mockRejectedValueOnce(new Error('Refresh unavailable'))
    await act(async () => {
      await client.refetchQueries({ queryKey: configKey })
    })
    expect(await screen.findByText('Refresh unavailable')).toBeVisible()
    expect(screen.getByLabelText('Token')).toHaveValue('unsaved-private-token')
    expect(screen.getByLabelText('Channel ID')).toHaveValue(84)
    expect(screen.getByLabelText('API endpoint')).toHaveValue(
      'https://draft.example.test'
    )
  })
})
