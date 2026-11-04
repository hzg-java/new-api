import { QueryClient, QueryObserver } from '@tanstack/react-query'
import { waitFor } from '@testing-library/react'
import type { AxiosAdapter, AxiosResponse } from 'axios'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/http-client'

import {
  addMaterial,
  createMaterialGroup,
  deleteMaterial,
  deleteMaterialGroup,
  retryMaterialGroup,
  getMaterialGroups,
  getMaterials,
  getMobileCloudConfig,
  getTopenRouterConfig,
  renameMaterialGroup,
  retryMaterial,
  saveMobileCloudConfig,
  saveTopenRouterConfig,
  updateMaterialGroup,
} from '../api'
import { mobileCloudConfigSchema } from '../lib/mobile-cloud-config-schema'
import { topenRouterConfigSchema } from '../lib/topenrouter-config-schema'
import type { MobileCloudConfig } from '../types'

const originalAdapter = api.defaults.adapter

afterEach(() => {
  api.defaults.adapter = originalAdapter
})

it.each([
  { provider: 'mobile-cloud', enabled: true },
  { provider: 'mobile-cloud', enabled: false },
  { provider: 'topenrouter', enabled: true },
  { provider: 'topenrouter', enabled: false },
])(
  'round-trips $provider enabled=$enabled in the full configuration payload',
  async ({ provider, enabled }) => {
    const path = `/api/material/${provider}/config`
    const adapter = vi.fn<AxiosAdapter>(async (config) => ({
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
      data: { success: true, data: { enabled } },
    }))
    api.defaults.adapter = adapter
    const input = {
      enabled,
      channel_id: 42,
      endpoint: 'https://materials.example.test',
      pool_id: 'saved-pool',
      access_key: '',
      secret_key: '',
      token: '',
    }
    let payload
    if (provider === 'mobile-cloud') {
      payload = mobileCloudConfigSchema({
        access_key_masked: 'AK****saved',
        secret_key_configured: true,
      }).parse(input)
      expect((await getMobileCloudConfig()).data.enabled).toBe(enabled)
      expect((await saveMobileCloudConfig(payload)).data.enabled).toBe(enabled)
    } else {
      payload = topenRouterConfigSchema({ token_configured: true }).parse(input)
      expect((await getTopenRouterConfig()).data.enabled).toBe(enabled)
      expect((await saveTopenRouterConfig(payload)).data.enabled).toBe(enabled)
    }
    expect(payload.enabled).toBe(enabled)
    expect(
      adapter.mock.calls.map(([config]) => ({
        method: config.method,
        url: config.url,
        data: config.data,
        disableDuplicate: config.disableDuplicate,
      }))
    ).toEqual([
      { method: 'get', url: path, disableDuplicate: true },
      { method: 'put', url: path, data: JSON.stringify(payload) },
    ])
  }
)

it.each(['mobile-cloud', 'topenrouter'])(
  'requires an explicit boolean enabled in the %s form schema',
  (provider) => {
    const schema =
      provider === 'mobile-cloud'
        ? mobileCloudConfigSchema({
            access_key_masked: 'AK****saved',
            secret_key_configured: true,
          })
        : topenRouterConfigSchema({ token_configured: true })
    const input = {
      channel_id: 42,
      endpoint: 'https://materials.example.test',
      pool_id: 'saved-pool',
      access_key: '',
      secret_key: '',
      token: '',
    }
    for (const enabled of [undefined, null, 'false', 0]) {
      const result = schema.safeParse({ ...input, enabled })
      expect(result.success).toBe(false)
      expect(result.error?.issues.map((issue) => issue.path)).toEqual([
        ['enabled'],
      ])
    }
  }
)

it.each([
  { scope: 'self', targetUserId: undefined },
  { scope: 'admin', targetUserId: 7 },
])(
  'uses the $scope owner endpoints for material and group operations',
  async ({ targetUserId }) => {
    const adapter = vi.fn<AxiosAdapter>(async (config) => ({
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
      data: { success: true },
    }))
    api.defaults.adapter = adapter
    const input = {
      name: 'Portrait',
      library_type: 'AIGC',
      file_type: 'Image',
      source_url: 'https://example.com/a.png',
      group_id: 3,
    } as const

    await addMaterial(input, targetUserId)
    await deleteMaterial(1, targetUserId)
    await retryMaterial(1, targetUserId)
    await getMaterialGroups(targetUserId)
    await createMaterialGroup({ name: 'Campaign' }, targetUserId)
    await renameMaterialGroup(3, { name: 'Renamed' }, targetUserId)
    await updateMaterialGroup(1, { group_id: null }, targetUserId)

    const base =
      targetUserId === undefined
        ? '/api/material'
        : '/api/material/admin/users/7'
    const materials = targetUserId === undefined ? base : `${base}/materials`
    expect(
      adapter.mock.calls.map(([config]) => ({
        method: config.method,
        url: config.url,
        data: config.data,
        disableDuplicate: config.disableDuplicate,
      }))
    ).toEqual([
      {
        method: 'post',
        url: targetUserId === undefined ? `${base}/` : materials,
        data: JSON.stringify(input),
      },
      { method: 'delete', url: `${materials}/1` },
      { method: 'post', url: `${materials}/1/retry` },
      { method: 'get', url: `${base}/groups`, disableDuplicate: true },
      {
        method: 'post',
        url: `${base}/groups`,
        data: JSON.stringify({ name: 'Campaign' }),
      },
      {
        method: 'put',
        url: `${base}/groups/3`,
        data: JSON.stringify({ name: 'Renamed' }),
      },
      {
        method: 'put',
        url: `${materials}/1/group`,
        data: JSON.stringify({ group_id: null }),
      },
    ])
  }
)

it.each([
  { scope: undefined, user_id: 7, path: '/api/material/list?group_id=0' },
  { scope: 'self', user_id: 7, path: '/api/material/list?group_id=0' },
  { scope: 'all', user_id: undefined, path: '/api/material/all?group_id=0' },
  { scope: 'all', user_id: 7, path: '/api/material/all?group_id=0&user_id=7' },
] as const)(
  'serializes user_id=$user_id only for scope=$scope',
  async ({ scope, user_id, path }) => {
    const adapter = vi.fn<AxiosAdapter>(async (config) => ({
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
      data: { success: true, data: [], total: 0 },
    }))
    api.defaults.adapter = adapter

    await getMaterials({ scope, user_id, group_id: 0 })

    expect(adapter).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({
        method: 'get',
        url: path,
        disableDuplicate: true,
      })
    )
  }
)

it('sends combined admin binding filters and preserves queued group mutation responses', async () => {
  const queued = {
    id: 3,
    user_id: 7,
    name: 'Campaign',
    deleting: true,
    status: 'processing',
    bindings: [],
  }
  const adapter = vi.fn<AxiosAdapter>(async (config) => ({
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
    data: { success: true, data: queued },
  }))
  api.defaults.adapter = adapter
  await getMaterials({
    scope: 'all',
    keyword: 'real-asset',
    source_url: 'original image.png',
    channel_id: 42,
    upstream_status: 'ACTIVE',
    group_id: 3,
  })
  const requestUrl = adapter.mock.calls[0][0].url
  expect(requestUrl).toBeDefined()
  const url = new URL(requestUrl ?? '', 'https://example.test')
  expect(Object.fromEntries(url.searchParams)).toEqual({
    keyword: 'real-asset',
    source_url: 'original image.png',
    channel_id: '42',
    upstream_status: 'ACTIVE',
    group_id: '3',
  })
  expect((await deleteMaterialGroup(3, 7)).data).toEqual(queued)
  expect((await retryMaterialGroup(3, 7)).data).toEqual(queued)
  expect(
    adapter.mock.calls.slice(1).map(([config]) => [config.method, config.url])
  ).toEqual([
    ['delete', '/api/material/admin/users/7/groups/3'],
    ['post', '/api/material/admin/users/7/groups/3/retry'],
  ])
})

it.each(['groups', 'self', 'all', 'admin-groups', 'admin-list'] as const)(
  'does not reuse a pre-rename %s response for a post-rename refresh',
  async (scope) => {
    const group = { id: 3, user_id: 7, name: 'Campaign' }
    const isGroups = scope === 'groups' || scope === 'admin-groups'
    const targetUserId = scope.startsWith('admin-') ? 7 : undefined
    const payload = (name: string) => ({
      success: true,
      data: isGroups ? [{ ...group, name }] : [{ group_name: name }],
      total: 1,
    })
    let releaseOld: ((response: AxiosResponse) => void) | undefined
    const oldResponse = new Promise<AxiosResponse>((resolve) => {
      releaseOld = resolve
    })
    let oldRequest: AxiosResponse | undefined
    const adapter = vi.fn<AxiosAdapter>(async (config) => {
      const response = { status: 200, statusText: 'OK', headers: {}, config }
      if (config.method === 'put') {
        return {
          ...response,
          data: { success: true, data: { ...group, name: 'Renamed' } },
        }
      }
      if (!oldRequest) {
        oldRequest = { ...response, data: payload('Campaign') }
        return oldResponse
      }
      return { ...response, data: payload('Renamed') }
    })
    api.defaults.adapter = adapter
    const read = () => {
      if (isGroups) return getMaterialGroups(targetUserId)
      if (scope === 'self') return getMaterials({ scope: 'self' })
      return getMaterials({ scope: 'all', user_id: targetUserId })
    }
    const pending = read()
    await waitFor(() => expect(oldRequest).toBeDefined())
    await renameMaterialGroup(3, { name: 'Renamed' }, targetUserId)
    const refreshed = read()
    if (!releaseOld || !oldRequest) {
      throw new Error('Old request was not captured')
    }
    releaseOld(oldRequest)
    await pending
    expect((await refreshed).data).toEqual(payload('Renamed').data)
  }
)

it('keeps saved Mobile Cloud configuration when an older background GET finishes after saving', async () => {
  const queryKey = ['material-library', 'mobile-cloud-config']
  const previous: MobileCloudConfig = {
    available: true,
    enabled: true,
    configured: true,
    encryption_ready: true,
    endpoint: 'https://old.example.test',
    access_key_masked: '********old',
    secret_key_configured: true,
    pool_id: 'old-pool',
    channel_id: 42,
    channel_name: 'Full channel name',
  }
  const saved: MobileCloudConfig = {
    ...previous,
    endpoint: 'https://new.example.test',
    access_key_masked: '********new',
    pool_id: 'new-pool',
  }
  let releaseOld!: (response: AxiosResponse) => void
  const oldResponse = new Promise<AxiosResponse>((resolve) => {
    releaseOld = resolve
  })
  let oldRequest: AxiosResponse | undefined
  let reads = 0
  api.defaults.adapter = async (config) => {
    const response = { status: 200, statusText: 'OK', headers: {}, config }
    if (config.method === 'put') {
      return { ...response, data: { success: true, data: saved } }
    }
    reads++
    if (reads === 1) {
      oldRequest = { ...response, data: { success: true, data: previous } }
      return oldResponse
    }
    return { ...response, data: { success: true, data: saved } }
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(queryKey, previous)
  const observer = new QueryObserver(client, {
    queryKey,
    queryFn: async () => (await getMobileCloudConfig()).data,
  })
  const unsubscribe = observer.subscribe(() => undefined)
  try {
    await waitFor(() => expect(oldRequest).toBeDefined())
    const result = await saveMobileCloudConfig({
      enabled: saved.enabled,
      endpoint: saved.endpoint,
      access_key: '',
      secret_key: '',
      pool_id: saved.pool_id,
      channel_id: saved.channel_id,
    })
    client.setQueryData(queryKey, result.data)
    const refresh = client.invalidateQueries({ queryKey })
    if (!oldRequest) throw new Error('Background request was not captured')
    releaseOld(oldRequest)
    await refresh
    expect(client.getQueryData(queryKey)).toEqual(saved)
    expect(reads).toBe(2)
  } finally {
    unsubscribe()
    client.clear()
  }
})
