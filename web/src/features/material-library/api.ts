import { api } from '@/lib/api'

import type { MobileCloudConfigFormValues } from './lib/mobile-cloud-config-schema'
import type { TopenRouterConfigFormValues } from './lib/topenrouter-config-schema'
import type {
  AddMaterialInput,
  MaterialCapabilityGridResponse,
  MaterialFilters,
  MaterialGroupListResponse,
  MaterialGroupResponse,
  MaterialListResponse,
  MaterialResponse,
  MaterialIntegrationProvider,
  MaterialTestRequest,
  MaterialTestResponse,
  MobileCloudConfigResponse,
  TopenRouterConfigResponse,
} from './types'

export type GetMaterialsParams = MaterialFilters & {
  offset?: number
  limit?: number
  scope?: 'self' | 'all'
  user_id?: number
}

export async function getMaterials(
  params: GetMaterialsParams = {}
): Promise<MaterialListResponse> {
  const query = new URLSearchParams()
  if (params.keyword) query.set('keyword', params.keyword)
  if (params.library_type) query.set('library_type', params.library_type)
  if (params.status) query.set('status', params.status)
  if (params.group_id != null) query.set('group_id', String(params.group_id))
  if (params.scope === 'all') {
    if (params.source_url) query.set('source_url', params.source_url)
    if (params.channel_id != null) {
      query.set('channel_id', String(params.channel_id))
    }
    if (params.upstream_status) {
      query.set('upstream_status', params.upstream_status)
    }
  }
  if (params.scope === 'all' && params.user_id != null) {
    query.set('user_id', String(params.user_id))
  }
  if (params.offset != null) query.set('offset', String(params.offset))
  if (params.limit != null) query.set('limit', String(params.limit))
  const path =
    params.scope === 'all' ? '/api/material/all' : '/api/material/list'
  const res = await api.get(`${path}?${query.toString()}`, {
    disableDuplicate: true,
  })
  return res.data
}

function getMaterialOwnerPath(targetUserId?: number): string {
  return targetUserId === undefined
    ? '/api/material'
    : `/api/material/admin/users/${targetUserId}`
}

export async function addMaterial(
  input: AddMaterialInput,
  targetUserId?: number
): Promise<MaterialResponse> {
  const base = getMaterialOwnerPath(targetUserId)
  const path = targetUserId === undefined ? `${base}/` : `${base}/materials`
  const res = await api.post(path, input)
  return res.data
}

export async function deleteMaterial(
  id: number,
  targetUserId?: number
): Promise<MaterialResponse> {
  const base = getMaterialOwnerPath(targetUserId)
  const path = targetUserId === undefined ? base : `${base}/materials`
  const res = await api.delete(`${path}/${id}`)
  return res.data
}

export async function retryMaterial(
  id: number,
  targetUserId?: number
): Promise<MaterialResponse> {
  const base = getMaterialOwnerPath(targetUserId)
  const path = targetUserId === undefined ? base : `${base}/materials`
  const res = await api.post(`${path}/${id}/retry`)
  return res.data
}

export async function getMaterialGroups(
  targetUserId?: number
): Promise<MaterialGroupListResponse> {
  const res = await api.get(`${getMaterialOwnerPath(targetUserId)}/groups`, {
    disableDuplicate: true,
  })
  return res.data
}

export async function createMaterialGroup(
  input: { name: string },
  targetUserId?: number
): Promise<MaterialGroupResponse> {
  const res = await api.post(
    `${getMaterialOwnerPath(targetUserId)}/groups`,
    input
  )
  return res.data
}

export async function renameMaterialGroup(
  id: number,
  input: { name: string },
  targetUserId?: number
): Promise<MaterialGroupResponse> {
  const res = await api.put(
    `${getMaterialOwnerPath(targetUserId)}/groups/${id}`,
    input
  )
  return res.data
}

export async function deleteMaterialGroup(
  id: number,
  targetUserId?: number
): Promise<MaterialGroupResponse> {
  const res = await api.delete(
    `${getMaterialOwnerPath(targetUserId)}/groups/${id}`
  )
  return res.data
}

export async function retryMaterialGroup(
  id: number,
  targetUserId?: number
): Promise<MaterialGroupResponse> {
  const res = await api.post(
    `${getMaterialOwnerPath(targetUserId)}/groups/${id}/retry`
  )
  return res.data
}

export async function updateMaterialGroup(
  id: number,
  input: { group_id: number | null },
  targetUserId?: number
): Promise<MaterialResponse> {
  const base = getMaterialOwnerPath(targetUserId)
  const path = targetUserId === undefined ? base : `${base}/materials`
  const res = await api.put(`${path}/${id}/group`, input)
  return res.data
}

// ============================================================================
// Admin: channel capability matrix
// ============================================================================

export async function getMaterialCapabilities(
  signal?: AbortSignal
): Promise<MaterialCapabilityGridResponse> {
  const res = await api.get('/api/material/capability/all', {
    signal,
    disableDuplicate: true,
  })
  return res.data
}

export async function getMobileCloudConfig(): Promise<MobileCloudConfigResponse> {
  const res = await api.get('/api/material/mobile-cloud/config', {
    disableDuplicate: true,
  })
  return res.data
}

export async function saveMobileCloudConfig(
  input: MobileCloudConfigFormValues
): Promise<MobileCloudConfigResponse> {
  const res = await api.put('/api/material/mobile-cloud/config', input)
  return res.data
}

export async function getTopenRouterConfig(): Promise<TopenRouterConfigResponse> {
  const res = await api.get('/api/material/topenrouter/config', {
    disableDuplicate: true,
  })
  return res.data
}

export async function saveTopenRouterConfig(
  input: TopenRouterConfigFormValues
): Promise<TopenRouterConfigResponse> {
  const res = await api.put('/api/material/topenrouter/config', input)
  return res.data
}

export async function testMaterialIntegration(
  provider: MaterialIntegrationProvider,
  request: MaterialTestRequest
): Promise<MaterialTestResponse> {
  const route = provider === 'mobile_cloud' ? 'mobile-cloud' : 'topenrouter'
  const res = await api.post(`/api/material/${route}/test`, request, {
    skipAuthRefresh: true,
  })
  return res.data
}
