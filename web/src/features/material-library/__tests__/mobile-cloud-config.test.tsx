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

const configPath = '/api/material/mobile-cloud/config'
const configKey = ['material-library', 'mobile-cloud-config']
const initialConfig = {
  available: true,
  enabled: true,
  configured: false,
  encryption_ready: true,
  endpoint: 'https://materials.example.test',
  access_key_masked: '',
  secret_key_configured: false,
  pool_id: '',
  channel_id: 0,
  channel_name: '',
}
const savedConfig = {
  ...initialConfig,
  configured: true,
  access_key_masked: 'AK****saved',
  secret_key_configured: true,
  pool_id: 'test-pool',
  channel_id: 42,
  channel_name: 'Full Mobile Cloud channel',
}
const clients: QueryClient[] = []

function renderSettings() {
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
  return client
}

async function enterCredentials(user: ReturnType<typeof userEvent.setup>) {
  await user.type(
    await screen.findByLabelText('Access key (AK)'),
    'test-access-key'
  )
  await user.type(screen.getByLabelText('Secret key (SK)'), 'test-secret-key')
  await user.type(screen.getByLabelText('Pool-Id'), 'test-pool')
  await user.type(screen.getByLabelText('Channel ID'), '42')
}

beforeEach(() => {
  localStorage.clear()
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === configPath) {
      return { data: { success: true, data: initialConfig } }
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
  vi.restoreAllMocks()
})

describe('Mobile Cloud configuration', () => {
  it('tests saved credentials without saving drafts and blocks duplicate tests or saves while pending', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: { ...savedConfig, enabled: false } },
    })
    const result = {
      provider: 'mobile_cloud',
      channel_id: 42,
      channel_name: savedConfig.channel_name,
      operation: 'list_groups',
      success: true,
      elapsed_ms: 12,
      total: 0,
      message: 'Read-only query completed',
    }
    let finishTest!: (value: {
      data: { success: boolean; data: typeof result }
    }) => void
    const post = vi.spyOn(api, 'post').mockReturnValueOnce(
      new Promise((resolve) => {
        finishTest = resolve
      })
    )
    const user = userEvent.setup()
    renderSettings()
    await user.type(await screen.findByLabelText('Access key (AK)'), 'draft-ak')
    await user.type(screen.getByLabelText('Secret key (SK)'), 'draft-sk')
    const groups = screen.getByRole('button', { name: 'Execute' })
    expect(
      screen.getByText(
        'Tests use the saved configuration. Unsaved changes are not included.'
      )
    ).toBeVisible()
    groups.focus()
    await user.keyboard('{Enter}')
    expect(groups).toBeDisabled()
    expect(
      screen.getByRole('combobox', { name: 'API operation' })
    ).toBeDisabled()
    expect(screen.getByLabelText('pageNo')).toBeDisabled()
    expect(screen.getByLabelText('pageSize')).toBeDisabled()
    const save = screen.getByRole('button', { name: 'Save configuration' })
    expect(save).toBeDisabled()
    expect(screen.getByLabelText('Access key (AK)')).toBeDisabled()
    const form = save.closest('form')
    if (!form) throw new Error('Configuration must expose a submit form')
    fireEvent.submit(form)
    fireEvent.click(groups)
    expect(post).toHaveBeenCalledExactlyOnceWith(
      '/api/material/mobile-cloud/test',
      {
        operation: 'list_groups',
        parameters: { pageNo: 1, pageSize: 1, groupType: 'AIGC' },
      },
      { skipAuthRefresh: true }
    )
    expect(api.put).not.toHaveBeenCalled()
    await act(async () => finishTest({ data: { success: true, data: result } }))
    expect(await screen.findByText('Material API test succeeded')).toBeVisible()
    expect(screen.getByText('Elapsed: 12 ms')).toBeVisible()
    expect(screen.getByText('Total: 0')).toBeVisible()
    expect(screen.getByText('Read-only query completed')).toBeVisible()
    expect(screen.getByLabelText('Access key (AK)')).toHaveValue('draft-ak')
    expect(screen.getByLabelText('Secret key (SK)')).toHaveValue('draft-sk')
    await user.type(screen.getByLabelText('Pool-Id'), '-edited')
    expect(
      screen.queryByText('Material API test succeeded')
    ).not.toBeInTheDocument()
  })

  it.each([
    { configured: false },
    { available: false },
    { encryption_ready: false },
    { channel_id: 0 },
  ])(
    'disables manual execution for unusable saved configuration %j',
    async (state) => {
      vi.mocked(api.get).mockResolvedValue({
        data: { success: true, data: { ...savedConfig, ...state } },
      })
      const post = vi
        .spyOn(api, 'post')
        .mockRejectedValue(new Error('No network permitted'))
      renderSettings()
      expect(
        await screen.findByRole('button', { name: 'Execute' })
      ).toBeDisabled()
      expect(
        screen.getByRole('combobox', { name: 'API operation' })
      ).toBeDisabled()
      expect(post).not.toHaveBeenCalled()
    }
  )

  it('clears a saved-credential test after replacing only the SK even when saved metadata is identical', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: { ...savedConfig } },
    })
    vi.mocked(api.put).mockResolvedValueOnce({
      data: { success: true, data: { ...savedConfig } },
    })
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          provider: 'mobile_cloud',
          channel_id: savedConfig.channel_id,
          channel_name: savedConfig.channel_name,
          operation: 'list_groups',
          success: true,
          elapsed_ms: 4,
        },
      },
    })
    const user = userEvent.setup()
    const client = renderSettings()
    await user.type(
      await screen.findByLabelText('Secret key (SK)'),
      'replacement-sk'
    )
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    expect(await screen.findByText('Material API test succeeded')).toBeVisible()
    expect(screen.getByLabelText('Secret key (SK)')).toHaveValue(
      'replacement-sk'
    )
    const savedMetadata = client.getQueryData(configKey)

    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() =>
      expect(screen.getByLabelText('Secret key (SK)')).toHaveValue('')
    )
    expect(api.put).toHaveBeenCalledExactlyOnceWith(configPath, {
      enabled: savedConfig.enabled,
      channel_id: savedConfig.channel_id,
      endpoint: savedConfig.endpoint,
      pool_id: savedConfig.pool_id,
      access_key: '',
      secret_key: 'replacement-sk',
    })
    expect(client.getQueryData(configKey)).toBe(savedMetadata)
    await waitFor(() =>
      expect(
        screen.queryByText('Material API test succeeded')
      ).not.toBeInTheDocument()
    )
    expect(screen.queryByText('Elapsed: 4 ms')).not.toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('disables tests during saving and clears a prior test when saved metadata changes', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: savedConfig },
    })
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          provider: 'mobile_cloud',
          channel_id: 42,
          channel_name: savedConfig.channel_name,
          operation: 'list_groups',
          success: true,
          elapsed_ms: 4,
        },
      },
    })
    let finishSave!: (value: {
      data: { success: boolean; data: typeof savedConfig }
    }) => void
    vi.mocked(api.put).mockReturnValueOnce(
      new Promise((resolve) => {
        finishSave = resolve
      })
    )
    const user = userEvent.setup()
    const client = renderSettings()
    await user.click(await screen.findByRole('button', { name: 'Execute' }))
    expect(await screen.findByText('Material API test succeeded')).toBeVisible()
    expect(screen.queryByText(/^Total:/)).not.toBeInTheDocument()
    act(() => client.setQueryData(configKey, { ...savedConfig, channel_id: 7 }))
    await waitFor(() =>
      expect(
        screen.queryByText('Material API test succeeded')
      ).not.toBeInTheDocument()
    )
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(screen.getByRole('button', { name: 'Execute' })).toBeDisabled()
    await act(async () =>
      finishSave({ data: { success: true, data: savedConfig } })
    )
  })

  it('keeps pending tests locked and discards a result after saved configuration changes', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: savedConfig },
    })
    const result = {
      provider: 'mobile_cloud',
      channel_id: 42,
      channel_name: savedConfig.channel_name,
      operation: 'list_groups',
      success: true,
      elapsed_ms: 5,
    }
    let finishTest!: (value: {
      data: { success: boolean; data: typeof result }
    }) => void
    vi.spyOn(api, 'post').mockReturnValueOnce(
      new Promise((resolve) => {
        finishTest = resolve
      })
    )
    const user = userEvent.setup()
    const client = renderSettings()
    await user.click(await screen.findByRole('button', { name: 'Execute' }))
    act(() => client.setQueryData(configKey, { ...savedConfig, channel_id: 7 }))
    await waitFor(() =>
      expect(screen.getByLabelText('Channel ID')).toHaveValue(7)
    )
    expect(screen.getByRole('button', { name: 'Execute' })).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Save configuration' })
    ).toBeDisabled()
    await act(async () => finishTest({ data: { success: true, data: result } }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Execute' })).toBeEnabled()
    )
    expect(
      screen.queryByText('Material API test succeeded')
    ).not.toBeInTheDocument()
  })

  it.each([true, false])(
    'displays the loaded enabled=%s state and its video-only scope',
    async (enabled) => {
      vi.mocked(api.get).mockResolvedValue({
        data: { success: true, data: { ...savedConfig, enabled } },
      })
      renderSettings()
      const toggle = await screen.findByRole('switch', {
        name: 'Automatic material preparation',
      })
      expect(toggle).toHaveAttribute('aria-checked', String(enabled))
      expect(toggle).toHaveAccessibleDescription(
        'Only controls automatic material preparation for video requests. When disabled, original parameters pass through unchanged. Manual material library operations are unaffected.'
      )
      expect(screen.getByText(enabled ? 'Enabled' : 'Disabled')).toBeVisible()
      expect(screen.getByLabelText('Pool-Id')).toBeEnabled()
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
      renderSettings()
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
          endpoint: savedConfig.endpoint,
          pool_id: savedConfig.pool_id,
          access_key: '',
          secret_key: '',
        })
      )
      await waitFor(() =>
        expect(
          screen.getByRole('button', { name: 'Save configuration' })
        ).toBeEnabled()
      )
      expect(toggle).toHaveAttribute('aria-checked', String(enabled))
      cleanup()
      renderSettings()
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
      const client = renderSettings()
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
          channel_name: 'Refreshed channel',
        })
      )
      expect(toggle).toHaveAttribute('aria-checked', String(!enabled))
      expect(screen.getByText(!enabled ? 'Enabled' : 'Disabled')).toBeVisible()
    }
  )

  it('requires a positive channel ID and preserves a dirty channel across refresh and failed save', async () => {
    const user = userEvent.setup()
    const client = renderSettings()
    await enterCredentials(user)
    const channel = screen.getByRole('spinbutton', { name: 'Channel ID' })
    await user.clear(channel)
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    expect(channel).toHaveAttribute('aria-invalid', 'true')
    expect(api.put).not.toHaveBeenCalled()
    await user.clear(channel)
    await user.type(channel, '42')
    act(() =>
      client.setQueryData(configKey, {
        ...initialConfig,
        channel_id: 7,
        channel_name: 'Saved channel',
      })
    )
    expect(channel).toHaveValue(42)
    vi.mocked(api.put).mockRejectedValueOnce(new Error('Save rejected'))
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(
        configPath,
        expect.objectContaining({ channel_id: 42 })
      )
    )
    expect(await screen.findByText('Save rejected')).toBeVisible()
    expect(channel).toHaveValue(42)
  })

  it('allows correcting a legacy unbound configuration without blocking on encryption readiness', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: {
          ...savedConfig,
          channel_id: 0,
          channel_name: '',
          encryption_ready: false,
        },
      },
    })
    const user = userEvent.setup()
    renderSettings()
    expect(
      await screen.findByText(
        'Select a channel to complete this configuration.'
      )
    ).toBeVisible()
    const channel = screen.getByRole('spinbutton', { name: 'Channel ID' })
    expect(channel).toBeEnabled()
    await user.clear(channel)
    await user.type(channel, '42')
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(
        configPath,
        expect.objectContaining({ channel_id: 42 })
      )
    )
  })

  it('requires initial AK, SK and Pool-Id before sending a save', async () => {
    const user = userEvent.setup()
    renderSettings()
    await user.click(
      await screen.findByRole('button', { name: 'Save configuration' })
    )
    for (const label of ['Access key (AK)', 'Secret key (SK)', 'Pool-Id']) {
      await waitFor(() =>
        expect(screen.getByLabelText(label)).toHaveAttribute(
          'aria-invalid',
          'true'
        )
      )
    }
    expect(api.put).not.toHaveBeenCalled()
    expect(screen.getByText('Not configured')).toBeVisible()
    await enterCredentials(user)
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(configPath, {
        enabled: true,
        endpoint: initialConfig.endpoint,
        access_key: 'test-access-key',
        secret_key: 'test-secret-key',
        pool_id: 'test-pool',
        channel_id: 42,
      })
    )
    await waitFor(() =>
      expect(screen.getByLabelText('Access key (AK)')).toHaveValue('')
    )
    expect(screen.getByLabelText('Secret key (SK)')).toHaveValue('')
  })

  it('uses the saved AK only as a placeholder and preserves both credentials when submitted blank', async () => {
    vi.mocked(api.get).mockImplementation(async (url) => ({
      data: { success: true, data: url === configPath ? savedConfig : [] },
    }))
    const user = userEvent.setup()
    const client = renderSettings()
    const accessKey = await screen.findByLabelText('Access key (AK)')
    expect(accessKey).toHaveValue('')
    expect(accessKey).toHaveAttribute(
      'placeholder',
      savedConfig.access_key_masked
    )
    expect(accessKey).toHaveAccessibleDescription(
      'Leave blank to keep the saved access key. Enter a new key to replace it.'
    )
    const secretKey = screen.getByLabelText('Secret key (SK)')
    expect(secretKey).toHaveValue('')
    expect(secretKey).toHaveAttribute('type', 'password')
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(configPath, {
        enabled: true,
        endpoint: savedConfig.endpoint,
        access_key: '',
        secret_key: '',
        pool_id: savedConfig.pool_id,
        channel_id: 42,
      })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save configuration' })
      ).toBeEnabled()
    )
    expect(screen.getAllByText('Configuration saved').length).toBeGreaterThan(0)
    expect(
      screen.queryByText(/connected|connection verified/i)
    ).not.toBeInTheDocument()
    expect(
      JSON.stringify(
        client
          .getQueryCache()
          .getAll()
          .map((query) => query.state.data)
      )
    ).not.toContain('test-secret-key')
  })

  it('clears replacement credentials immediately on success without caching secrets or overwriting a new draft on refetch', async () => {
    let finishRefetch!: (response: {
      data: { success: boolean; data: typeof savedConfig }
    }) => void
    let configRequests = 0
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (url !== configPath) return { data: { success: true, data: [] } }
      configRequests++
      if (configRequests === 1) {
        return { data: { success: true, data: savedConfig } }
      }
      return new Promise((resolve) => {
        finishRefetch = resolve
      })
    })
    const user = userEvent.setup()
    const client = renderSettings()
    await user.type(
      await screen.findByLabelText('Access key (AK)'),
      'replacement-ak'
    )
    await user.type(screen.getByLabelText('Secret key (SK)'), 'replacement-sk')
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() =>
      expect(screen.getByLabelText('Access key (AK)')).toHaveValue('')
    )
    expect(screen.getByLabelText('Secret key (SK)')).toHaveValue('')
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save configuration' })
      ).toBeEnabled()
    )
    const retainedMutations = JSON.stringify(
      client
        .getMutationCache()
        .getAll()
        .map((mutation) => mutation.state)
    )
    expect(retainedMutations).not.toContain('replacement-ak')
    expect(retainedMutations).not.toContain('replacement-sk')
    await user.clear(screen.getByLabelText('Pool-Id'))
    await user.type(screen.getByLabelText('Pool-Id'), 'unsaved-pool')
    await user.type(screen.getByLabelText('Access key (AK)'), 'next-ak')
    await user.click(
      screen.getByRole('switch', { name: 'Automatic material preparation' })
    )
    await act(async () =>
      finishRefetch({ data: { success: true, data: savedConfig } })
    )
    expect(
      screen.getByRole('switch', { name: 'Automatic material preparation' })
    ).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByLabelText('Pool-Id')).toHaveValue('unsaved-pool')
    expect(screen.getByLabelText('Access key (AK)')).toHaveValue('next-ak')
    expect(localStorage.length).toBe(0)
  })

  it.each(['business', 'network'] as const)(
    'keeps the draft and shows no success after a %s save failure',
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
      renderSettings()
      await enterCredentials(user)
      await user.click(
        screen.getByRole('switch', { name: 'Automatic material preparation' })
      )
      await user.click(
        screen.getByRole('button', { name: 'Save configuration' })
      )
      await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
      await waitFor(() =>
        expect(
          screen.getByRole('button', { name: 'Save configuration' })
        ).toBeEnabled()
      )
      expect(screen.getByLabelText('Access key (AK)')).toHaveValue(
        'test-access-key'
      )
      expect(screen.getByLabelText('Secret key (SK)')).toHaveValue(
        'test-secret-key'
      )
      expect(screen.getByLabelText('Pool-Id')).toHaveValue('test-pool')
      expect(
        screen.getByRole('switch', { name: 'Automatic material preparation' })
      ).toHaveAttribute('aria-checked', 'false')
      expect(api.put).toHaveBeenCalledWith(
        configPath,
        expect.objectContaining({ enabled: false })
      )
      expect(screen.queryByText('Configuration saved')).not.toBeInTheDocument()
      expect(screen.getByText('Not configured')).toBeVisible()
    }
  )

  it('preserves the exact draft on rejection while sending schema-normalized input', async () => {
    vi.mocked(api.put).mockResolvedValueOnce({
      data: { success: false, message: 'Configuration rejected' },
    })
    const user = userEvent.setup()
    renderSettings()
    await user.type(
      await screen.findByLabelText('Access key (AK)'),
      '  draft-ak  '
    )
    await user.type(screen.getByLabelText('Secret key (SK)'), '  draft-sk  ')
    await user.type(screen.getByLabelText('Pool-Id'), '  draft-pool  ')
    await user.type(screen.getByLabelText('Channel ID'), '42')
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    expect(await screen.findByText('Configuration rejected')).toBeVisible()
    expect(api.put).toHaveBeenCalledWith(configPath, {
      enabled: true,
      endpoint: initialConfig.endpoint,
      access_key: 'draft-ak',
      secret_key: 'draft-sk',
      pool_id: 'draft-pool',
      channel_id: 42,
    })
    expect(screen.getByLabelText('Access key (AK)')).toHaveValue('  draft-ak  ')
    expect(screen.getByLabelText('Secret key (SK)')).toHaveValue('  draft-sk  ')
    expect(screen.getByLabelText('Pool-Id')).toHaveValue('  draft-pool  ')
  })

  it('disables all fields and duplicate saves until the pending request completes', async () => {
    let finishSave!: (response: {
      data: { success: boolean; data: typeof savedConfig }
    }) => void
    vi.mocked(api.put).mockReturnValueOnce(
      new Promise((resolve) => {
        finishSave = resolve
      })
    )
    const user = userEvent.setup()
    renderSettings()
    await enterCredentials(user)
    await user.click(screen.getByRole('button', { name: 'Advanced settings' }))
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    for (const label of [
      'Access key (AK)',
      'Secret key (SK)',
      'Pool-Id',
      'API endpoint',
    ]) {
      expect(screen.getByLabelText(label)).toBeDisabled()
    }
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
    const toggle = screen.getByRole('switch', {
      name: 'Automatic material preparation',
    })
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-checked', 'true')
    expect(
      screen.getByRole('button', { name: 'Toggle password visibility' })
    ).toBeDisabled()
    const form = screen.getByLabelText('Pool-Id').closest('form')
    if (!form) throw new Error('Configuration must expose a submit form')
    expect(form).toHaveAttribute('aria-busy', 'true')
    fireEvent.submit(form)
    expect(api.put).toHaveBeenCalledTimes(1)
    await act(async () =>
      finishSave({ data: { success: true, data: savedConfig } })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save configuration' })
      ).toBeEnabled()
    )
  })

  it.each([
    {
      available: false,
      enabled: false,
      encryption_ready: true,
      instruction:
        'Mobile Cloud material integration is unavailable. Check the backend service, then reload this page.',
    },
  ])(
    'disables saving and explains setup when available=$available and encryption_ready=$encryption_ready',
    async (state) => {
      vi.mocked(api.get).mockImplementation(async (url) => ({
        data: {
          success: true,
          data: url === configPath ? { ...initialConfig, ...state } : [],
        },
      }))
      const user = userEvent.setup()
      renderSettings()
      const save = await screen.findByRole('button', {
        name: 'Save configuration',
      })
      expect(save).toBeDisabled()
      expect(screen.getByText(state.instruction)).toBeVisible()
      expect(screen.getByLabelText('Access key (AK)')).toBeDisabled()
      const toggle = screen.getByRole('switch', {
        name: 'Automatic material preparation',
      })
      expect(toggle).toHaveAttribute('aria-disabled', 'true')
      expect(toggle).toHaveAttribute('aria-checked', 'false')
      await user.click(toggle)
      expect(toggle).toHaveAttribute('aria-checked', 'false')
      await user.click(save)
      expect(api.put).not.toHaveBeenCalled()
    }
  )

  it('allows the first save without a manually configured encryption key', async () => {
    vi.mocked(api.get).mockImplementation(async (url) => ({
      data: {
        success: true,
        data:
          url === configPath
            ? { ...initialConfig, encryption_ready: false }
            : [],
      },
    }))
    const user = userEvent.setup()
    renderSettings()
    expect(await screen.findByLabelText('Access key (AK)')).toBeEnabled()
    await enterCredentials(user)
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(screen.queryByText(/restart it/)).not.toBeInTheDocument()
  })

  it('warns about unavailable encryption without blocking channel configuration', async () => {
    vi.mocked(api.get).mockImplementation(async (url) => ({
      data: {
        success: true,
        data:
          url === configPath ? { ...savedConfig, encryption_ready: false } : [],
      },
    }))
    renderSettings()
    expect(await screen.findByLabelText('Access key (AK)')).toBeEnabled()
    expect(
      screen.getByRole('button', { name: 'Save configuration' })
    ).toBeEnabled()
    expect(
      screen.getByText(
        'The saved credentials cannot be decrypted. Restore the backend encryption key.'
      )
    ).toBeVisible()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('keeps advanced settings collapsed and supports Enter and Space keyboard toggles without a connection test', async () => {
    const user = userEvent.setup()
    renderSettings()
    const toggle = await screen.findByRole('button', {
      name: 'Advanced settings',
    })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(
      screen.queryByRole('textbox', { name: 'API endpoint' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: /test.*connection|connection.*test/i,
      })
    ).not.toBeInTheDocument()
    toggle.focus()
    await user.keyboard('{Enter}')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('textbox', { name: 'API endpoint' })).toHaveValue(
      initialConfig.endpoint
    )
    await user.keyboard(' ')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(
      screen.queryByRole('textbox', { name: 'API endpoint' })
    ).not.toBeInTheDocument()
  })

  it('does not expose an enabled form before the initial configuration request completes', async () => {
    let finishLoad!: (response: {
      data: { success: boolean; data: typeof initialConfig }
    }) => void
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (url !== configPath) return { data: { success: true, data: [] } }
      return new Promise((resolve) => {
        finishLoad = resolve
      })
    })
    renderSettings()
    expect(screen.queryByLabelText('Access key (AK)')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Save configuration' })
    ).not.toBeInTheDocument()
    await act(async () =>
      finishLoad({ data: { success: true, data: initialConfig } })
    )
    expect(await screen.findByLabelText('Access key (AK)')).toBeEnabled()
  })

  it.each(['business', 'network'] as const)(
    'shows ErrorState instead of the form for an initial %s GET failure and retries only on request',
    async (failure) => {
      vi.mocked(api.get).mockImplementation(async (url) => {
        if (url !== configPath) return { data: { success: true, data: [] } }
        if (failure === 'network') throw new Error('Configuration unavailable')
        return {
          data: { success: false, message: 'Configuration unavailable' },
        }
      })
      const user = userEvent.setup()
      renderSettings()
      expect(
        await screen.findByText(
          'We could not load the Mobile Cloud configuration.'
        )
      ).toBeVisible()
      expect(screen.queryByLabelText('Access key (AK)')).not.toBeInTheDocument()
      expect(
        vi.mocked(api.get).mock.calls.filter(([url]) => url === configPath)
      ).toHaveLength(1)
      await user.click(screen.getByRole('button', { name: 'Retry' }))
      await waitFor(() =>
        expect(
          vi.mocked(api.get).mock.calls.filter(([url]) => url === configPath)
        ).toHaveLength(2)
      )
    }
  )

  it('keeps an unsaved draft visible when a background configuration refetch fails', async () => {
    const user = userEvent.setup()
    const client = renderSettings()
    await user.type(await screen.findByLabelText('Pool-Id'), 'draft-pool')
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (url !== configPath) return { data: { success: true, data: [] } }
      throw new Error('Refresh unavailable')
    })
    await act(async () => {
      await client.refetchQueries({ queryKey: configKey })
    })
    expect(await screen.findByText('Refresh unavailable')).toBeVisible()
    expect(screen.getByLabelText('Pool-Id')).toHaveValue('draft-pool')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  })

  it('preserves an unrelated dirty endpoint and pool when server configuration props are refreshed', async () => {
    const user = userEvent.setup()
    const client = renderSettings()
    await user.type(await screen.findByLabelText('Pool-Id'), 'draft-pool')
    await user.click(screen.getByRole('button', { name: 'Advanced settings' }))
    await user.clear(screen.getByLabelText('API endpoint'))
    await user.type(
      screen.getByLabelText('API endpoint'),
      'https://draft.example.test'
    )
    act(() => {
      client.setQueryData(configKey, savedConfig)
    })
    expect(screen.getByLabelText('Pool-Id')).toHaveValue('draft-pool')
    expect(screen.getByLabelText('API endpoint')).toHaveValue(
      'https://draft.example.test'
    )
  })
})
