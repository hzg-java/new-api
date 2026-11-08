import {
  onlineManager,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { useState } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { MaterialIntegrationTest } from '../material-integration-test'
import type { MaterialIntegrationProvider } from '../types'

const clients: QueryClient[] = []
const configuration = {}

function Harness(props: { provider: MaterialIntegrationProvider }) {
  const [testing, setTesting] = useState(false)
  return (
    <MaterialIntegrationTest
      provider={props.provider}
      ready
      disabled={false}
      testing={testing}
      configuration={configuration}
      onTestingChange={setTesting}
    />
  )
}

function renderTest(provider: MaterialIntegrationProvider = 'mobile_cloud') {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: 3 } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <Harness provider={provider} />
    </QueryClientProvider>
  )
  return client
}

async function selectOperation(
  user: ReturnType<typeof userEvent.setup>,
  operation: string
) {
  await user.click(screen.getByRole('combobox', { name: 'API operation' }))
  await user.click(
    screen.getByRole('option', { name: new RegExp(`（${operation}）$`) })
  )
}

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        provider: 'mobile_cloud',
        channel_id: 42,
        channel_name: 'Saved channel',
        operation: 'create_group',
        success: true,
        elapsed_ms: 4,
        response: { groupId: 'group-new' },
      },
    },
  })
})

afterEach(() => {
  cleanup()
  onlineManager.setOnline(true)
  clients.forEach((client) => client.clear())
  clients.length = 0
  vi.restoreAllMocks()
  localStorage.clear()
  sessionStorage.clear()
})

it('shows Chinese operation descriptions and switches language without changing the request operation', async () => {
  i18next.addResourceBundle('zh', 'translation', zh.translation)
  try {
    await act(async () => {
      await i18next.changeLanguage('zh')
    })
    const user = userEvent.setup()
    renderTest()
    const selector = screen.getByRole('combobox', { name: '接口操作' })
    expect(selector).toHaveTextContent('查询素材分组（list_groups）')
    await user.click(selector)
    expect(
      screen.getAllByRole('option').map((option) => option.textContent)
    ).toEqual([
      '查询素材分组（list_groups）',
      '创建素材分组（create_group）',
      '获取素材分组详情（get_group）',
      '修改素材分组（update_group）',
      '删除素材分组（delete_group）',
      '查询素材列表（list_assets）',
      '上传素材（create_asset）',
      '获取素材详情（get_asset）',
      '修改素材名称（update_asset）',
      '删除素材（delete_asset）',
    ])
    await user.click(
      screen.getByRole('option', { name: '创建素材分组（create_group）' })
    )
    await user.type(screen.getByLabelText('groupName'), 'Campaign')
    await user.click(screen.getByRole('button', { name: '执行' }))
    expect(await screen.findByText('素材接口测试成功')).toBeVisible()
    expect(screen.getByRole('status')).toHaveTextContent(
      '创建素材分组（create_group）'
    )
    expect(api.post).toHaveBeenCalledExactlyOnceWith(
      '/api/material/mobile-cloud/test',
      {
        operation: 'create_group',
        parameters: { groupName: 'Campaign' },
      },
      { skipAuthRefresh: true }
    )
    await act(async () => {
      await i18next.changeLanguage('en')
    })
    expect(
      screen.getByRole('combobox', { name: 'API operation' })
    ).toHaveTextContent('Create material group（create_group）')
    expect(screen.getByRole('status')).toHaveTextContent(
      'Create material group（create_group）'
    )
    expect(api.post).toHaveBeenCalledTimes(1)
  } finally {
    await act(async () => {
      await i18next.changeLanguage('en')
    })
    i18next.removeResourceBundle('zh', 'translation')
  }
})

it('fails an offline execution immediately without queuing it for reconnection', async () => {
  const user = userEvent.setup()
  renderTest()
  onlineManager.setOnline(false)
  vi.mocked(api.post).mockRejectedValueOnce(new Error('Offline request failed'))
  await user.click(screen.getByRole('button', { name: 'Execute' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  expect(await screen.findByText('Material API test failed')).toBeVisible()
  onlineManager.setOnline(true)
  expect(api.post).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('button', { name: 'Execute' })).toBeEnabled()
})

it('offers ten manual operations without credentials, automatic calls or legacy query buttons', async () => {
  renderTest()
  expect(
    screen.queryByRole('button', { name: 'Test group query' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Test material query' })
  ).not.toBeInTheDocument()
  expect(screen.getByLabelText('pageNo')).toHaveValue(1)
  expect(screen.getByLabelText('pageSize')).toHaveValue(1)
  for (const label of [
    'API endpoint',
    'Access key (AK)',
    'Secret key (SK)',
    'Token',
  ]) {
    expect(screen.queryByLabelText(label)).not.toBeInTheDocument()
  }
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'API operation' }))
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual([
    'List material groups（list_groups）',
    'Create material group（create_group）',
    'Get material group details（get_group）',
    'Update material group（update_group）',
    'Delete material group（delete_group）',
    'List materials（list_assets）',
    'Upload material（create_asset）',
    'Get material details（get_asset）',
    'Rename material（update_asset）',
    'Delete material（delete_asset）',
  ])
  expect(api.post).not.toHaveBeenCalled()
})

it.each([
  ['create_group', { groupName: 'Campaign', description: 'Demo' }],
  ['get_group', { groupId: 'group-1' }],
  [
    'update_group',
    { groupId: 'group-1', groupName: 'Renamed', description: 'Demo' },
  ],
  ['get_asset', { assetId: 'asset-1' }],
  ['update_asset', { assetId: 'asset-1', assetName: 'Renamed' }],
] as const)(
  'executes %s once with manually entered camelCase parameters',
  async (operation, parameters) => {
    const user = userEvent.setup()
    renderTest()
    await selectOperation(user, operation)
    for (const [name, value] of Object.entries(parameters)) {
      await user.type(
        screen.getByRole('textbox', { name: new RegExp(`^${name}`) }),
        value
      )
    }
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledExactlyOnceWith(
        '/api/material/mobile-cloud/test',
        { operation, parameters },
        { skipAuthRefresh: true }
      )
    )
    expect(await screen.findByText('Material API test succeeded')).toBeVisible()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  }
)

it('shows safe response JSON and copies a returned ID without persisting or caching the request', async () => {
  const user = userEvent.setup()
  const writeText = vi
    .spyOn(navigator.clipboard, 'writeText')
    .mockResolvedValue()
  const client = renderTest()
  await selectOperation(user, 'create_group')
  await user.type(screen.getByLabelText('groupName'), 'private-draft-name')
  await user.click(screen.getByRole('button', { name: 'Execute' }))
  const json = await screen.findByLabelText('Response JSON')
  expect(json).toHaveTextContent('"groupId": "group-new"')
  await user.click(screen.getByRole('button', { name: 'Copy response JSON' }))
  expect(writeText).toHaveBeenCalledWith('{\n  "groupId": "group-new"\n}')
  await user.click(screen.getByRole('button', { name: 'Copy groupId' }))
  expect(writeText).toHaveBeenLastCalledWith('group-new')
  expect(
    JSON.stringify(
      client
        .getMutationCache()
        .getAll()
        .map((mutation) => mutation.state)
    )
  ).not.toContain('private-draft-name')
  expect(localStorage.length).toBe(0)
  expect(sessionStorage.length).toBe(0)
  await user.type(screen.getByLabelText('groupName'), '-edited')
  expect(screen.queryByLabelText('Response JSON')).not.toBeInTheDocument()
  expect(api.post).toHaveBeenCalledTimes(1)
  await selectOperation(user, 'get_group')
  await user.type(screen.getByLabelText('groupId'), 'group-new')
  expect(api.post).toHaveBeenCalledTimes(1)
})

it.each(['delete_group', 'delete_asset'] as const)(
  'requires explicit confirmation for %s and never reuses confirmation',
  async (operation) => {
    const user = userEvent.setup()
    renderTest()
    await selectOperation(user, operation)
    await user.type(
      screen.getByLabelText(
        operation === 'delete_group' ? 'groupId' : 'assetId'
      ),
      'upstream-id'
    )
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    expect(api.post).not.toHaveBeenCalled()
    let dialog = screen.getByRole('alertdialog')
    expect(dialog).toHaveTextContent('upstream-id')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(api.post).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledExactlyOnceWith(
        '/api/material/mobile-cloud/test',
        {
          operation,
          parameters: {
            [operation === 'delete_group' ? 'groupId' : 'assetId']:
              'upstream-id',
          },
          confirmed: true,
        },
        { skipAuthRefresh: true }
      )
    )
    await screen.findByText('Material API test succeeded')
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    expect(screen.getByRole('alertdialog')).toBeVisible()
    expect(api.post).toHaveBeenCalledTimes(1)
  }
)

it.each(['mobile_cloud', 'topenrouter'] as const)(
  'imports a URL for %s with provider-specific optional fields',
  async (provider) => {
    const user = userEvent.setup()
    renderTest(provider)
    await selectOperation(user, 'create_asset')
    expect(
      screen.getByText(
        'Import from a publicly accessible HTTP or HTTPS URL. Local file upload is not supported.'
      )
    ).toBeVisible()
    await user.type(
      screen.getByLabelText('assetUrl'),
      'https://assets.example.test/image.png'
    )
    if (provider === 'mobile_cloud') {
      await user.click(screen.getByRole('button', { name: 'Execute' }))
      expect(screen.getByLabelText('groupId')).toHaveAttribute(
        'aria-invalid',
        'true'
      )
      expect(screen.getByLabelText('assetName')).toHaveAttribute(
        'aria-invalid',
        'true'
      )
      expect(api.post).not.toHaveBeenCalled()
      await user.type(screen.getByLabelText('groupId'), 'group-1')
      await user.type(screen.getByLabelText('assetName'), 'Portrait')
    } else {
      expect(screen.getByLabelText('groupId (optional)')).toBeVisible()
      expect(screen.getByLabelText('assetName (optional)')).toBeVisible()
    }
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        `/api/material/${provider === 'mobile_cloud' ? 'mobile-cloud' : 'topenrouter'}/test`,
        {
          operation: 'create_asset',
          parameters: {
            assetUrl: 'https://assets.example.test/image.png',
            assetType: 'Image',
            ...(provider === 'mobile_cloud'
              ? { groupId: 'group-1', assetName: 'Portrait' }
              : {}),
          },
        },
        { skipAuthRefresh: true }
      )
    )
  }
)

it.each(['mobile_cloud', 'topenrouter'] as const)(
  'sends only supported list filters for %s and validates documented page limits',
  async (provider) => {
    const user = userEvent.setup()
    renderTest(provider)
    const size = screen.getByLabelText('pageSize')
    fireEvent.change(size, {
      target: { value: provider === 'mobile_cloud' ? '101' : '201' },
    })
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    expect(size).toHaveAttribute('aria-invalid', 'true')
    expect(api.post).not.toHaveBeenCalled()
    if (provider === 'topenrouter') {
      expect(
        screen.queryByLabelText('groupName (optional)')
      ).not.toBeInTheDocument()
    }
    await selectOperation(user, 'list_assets')
    await user.clear(screen.getByLabelText('pageSize'))
    await user.type(screen.getByLabelText('pageSize'), '200')
    await user.type(screen.getByLabelText('groupIds (optional)'), 'g-1, g-2')
    if (provider === 'mobile_cloud') {
      await user.type(screen.getByLabelText('assetName (optional)'), 'Portrait')
      await user.type(
        screen.getByLabelText('statuses (optional)'),
        'ACTIVE,FAILED'
      )
    } else {
      expect(
        screen.queryByLabelText('assetName (optional)')
      ).not.toBeInTheDocument()
      expect(
        screen.queryByLabelText('statuses (optional)')
      ).not.toBeInTheDocument()
      expect(screen.queryByLabelText('groupType')).not.toBeInTheDocument()
    }
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        expect.any(String),
        {
          operation: 'list_assets',
          parameters: {
            pageNo: 1,
            pageSize: 200,
            groupIds: ['g-1', 'g-2'],
            ...(provider === 'mobile_cloud'
              ? {
                  groupType: 'AIGC',
                  assetName: 'Portrait',
                  statuses: ['ACTIVE', 'FAILED'],
                }
              : {}),
          },
        },
        { skipAuthRefresh: true }
      )
    )
  }
)

it.each(['mobile_cloud', 'topenrouter'] as const)(
  'imports a valid HTTP URL for %s without narrowing the upstream protocol',
  async (provider) => {
    const user = userEvent.setup()
    renderTest(provider)
    await selectOperation(user, 'create_asset')
    await user.type(
      screen.getByLabelText('assetUrl'),
      'http://assets.example.test/image.png'
    )
    if (provider === 'mobile_cloud') {
      await user.type(screen.getByLabelText('groupId'), 'group-1')
      await user.type(screen.getByLabelText('assetName'), 'Portrait')
    }
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    await screen.findByText('Material API test succeeded')
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(vi.mocked(api.post).mock.calls[0][1]).toMatchObject({
      operation: 'create_asset',
      parameters: { assetUrl: 'http://assets.example.test/image.png' },
    })
  }
)

it('clears an upstream group description only when explicitly selected', async () => {
  const user = userEvent.setup()
  renderTest()
  await selectOperation(user, 'update_group')
  await user.type(screen.getByLabelText('groupId'), 'group-1')
  await user.type(screen.getByLabelText('groupName (optional)'), 'Renamed')
  await user.click(screen.getByRole('button', { name: 'Execute' }))
  await screen.findByText('Material API test succeeded')
  expect(vi.mocked(api.post).mock.calls[0][1]).toEqual({
    operation: 'update_group',
    parameters: { groupId: 'group-1', groupName: 'Renamed' },
  })
  await user.clear(screen.getByLabelText('groupName (optional)'))
  await user.click(screen.getByRole('checkbox', { name: 'Clear description' }))
  await user.click(screen.getByRole('button', { name: 'Execute' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(2))
  expect(vi.mocked(api.post).mock.calls[1][1]).toEqual({
    operation: 'update_group',
    parameters: { groupId: 'group-1', description: '' },
  })
})

it.each(['Video', 'Audio'])(
  'sends the manually selected %s URL import type without a chained action',
  async (assetType) => {
    const user = userEvent.setup()
    renderTest('topenrouter')
    await selectOperation(user, 'create_asset')
    await user.type(
      screen.getByLabelText('assetUrl'),
      'https://assets.example.test/media'
    )
    await user.click(screen.getByRole('combobox', { name: 'assetType' }))
    await user.click(screen.getByRole('option', { name: assetType }))
    expect(api.post).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    await screen.findByText('Material API test succeeded')
    expect(api.post).toHaveBeenCalledExactlyOnceWith(
      '/api/material/topenrouter/test',
      {
        operation: 'create_asset',
        parameters: {
          assetUrl: 'https://assets.example.test/media',
          assetType,
        },
      },
      { skipAuthRefresh: true }
    )
  }
)

it('renders multiple response IDs and untrusted names as bounded, copyable text', async () => {
  const name = `<img src=x onerror=alert(1)>${'Campaign'.repeat(40)}`
  vi.mocked(api.post).mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        provider: 'mobile_cloud',
        operation: 'list_groups',
        success: true,
        elapsed_ms: 1,
        total: 2,
        response: {
          list: [
            { groupId: 'g-1', name },
            { groupId: 'g-2', name: 'Second group' },
          ],
        },
      },
    },
  })
  const user = userEvent.setup()
  const writeText = vi
    .spyOn(navigator.clipboard, 'writeText')
    .mockResolvedValue()
  renderTest()
  await user.click(screen.getByRole('button', { name: 'Execute' }))
  const json = await screen.findByLabelText('Response JSON')
  expect(json).toHaveTextContent(name)
  expect(json.querySelector('img')).toBeNull()
  expect(json).toHaveClass('max-h-80', 'overflow-auto', 'break-all')
  const copies = screen.getAllByRole('button', { name: 'Copy groupId' })
  await user.click(copies[1])
  expect(writeText).toHaveBeenCalledWith('g-2')
  expect(api.post).toHaveBeenCalledTimes(1)
})

it.each([
  [
    'list_groups',
    'groupName',
    'Campaign',
    {
      pageNo: 1,
      pageSize: 1,
      groupType: 'AIGC',
      groupName: 'Campaign',
      groupIds: ['g-1', 'g-2'],
    },
  ],
  [
    'list_assets',
    'assetName',
    'Portrait',
    {
      pageNo: 1,
      pageSize: 1,
      groupType: 'AIGC',
      assetName: 'Portrait',
      groupIds: ['g-1', 'g-2'],
    },
  ],
] as const)(
  'sends the supported Mobile Cloud %s name and group filters',
  async (operation, name, value, parameters) => {
    const user = userEvent.setup()
    renderTest()
    await selectOperation(user, operation)
    await user.type(screen.getByLabelText(`${name} (optional)`), value)
    await user.type(screen.getByLabelText('groupIds (optional)'), 'g-1, g-2')
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/api/material/mobile-cloud/test',
        { operation, parameters },
        { skipAuthRefresh: true }
      )
    )
  }
)

it.each([
  ['list_groups', 'pageNo', '0'],
  ['list_groups', 'pageSize', '1.5'],
  ['list_assets', 'statuses (optional)', 'UNKNOWN'],
  ['create_asset', 'assetUrl', 'file:///local/image.png'],
  [
    'create_asset',
    'assetUrl',
    'http://user:secret@assets.example.test/image.png',
  ],
  ['create_asset', 'assetUrl', 'http://@assets.example.test/image.png'],
  ['create_asset', 'assetUrl', 'http://assets.example.test/image.png#fragment'],
] as const)(
  'rejects invalid %s %s input without executing',
  async (operation, name, value) => {
    const user = userEvent.setup()
    renderTest()
    await selectOperation(user, operation)
    fireEvent.change(screen.getByLabelText(name), { target: { value } })
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    expect(screen.getByLabelText(name)).toHaveAttribute('aria-invalid', 'true')
    expect(api.post).not.toHaveBeenCalled()
  }
)

it.each([false, [], { deleted: true }] as const)(
  'displays safe empty or deletion response %j without losing it',
  async (response) => {
    vi.mocked(api.post).mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          provider: 'mobile_cloud',
          operation: 'list_groups',
          success: true,
          elapsed_ms: 0,
          total: 0,
          response,
        },
      },
    })
    const user = userEvent.setup()
    renderTest()
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    expect(await screen.findByLabelText('Response JSON')).toHaveTextContent(
      JSON.stringify(response, null, 2).replaceAll(/\s+/g, ' ')
    )
    expect(screen.getByText('Total: 0')).toBeVisible()
  }
)

it('clears results when switching operations and reports HTTP 400 without retry or retained request details', async () => {
  const user = userEvent.setup()
  const client = renderTest()
  await user.click(screen.getByRole('button', { name: 'Execute' }))
  await screen.findByText('Material API test succeeded')
  await selectOperation(user, 'get_asset')
  expect(
    screen.queryByText('Material API test succeeded')
  ).not.toBeInTheDocument()
  await user.type(screen.getByLabelText('assetId'), 'asset-invalid')
  vi.mocked(api.post).mockRejectedValueOnce({
    isAxiosError: true,
    config: { data: 'private-request-url' },
    response: {
      status: 400,
      data: { success: false, message: 'Invalid asset ID' },
    },
  })
  await user.click(screen.getByRole('button', { name: 'Execute' }))
  expect(await screen.findByText('Material API test failed')).toBeVisible()
  expect(screen.getByText('Invalid asset ID')).toBeVisible()
  expect(api.post).toHaveBeenCalledTimes(2)
  expect(
    JSON.stringify(
      client
        .getMutationCache()
        .getAll()
        .map((mutation) => mutation.state)
    )
  ).not.toContain('private-request-url')
})
