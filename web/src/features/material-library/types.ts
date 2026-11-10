import { z } from 'zod'

export const addMaterialSchema = z.object({
  name: z.string().trim().min(1, 'Material name is required.'),
  file_type: z.literal('Image'),
  group_id: z.number().int().positive().nullable(),
  source_url: z
    .string()
    .trim()
    .refine((value) => {
      if (!URL.canParse(value)) return false
      const url = new URL(value)
      return (
        url.protocol === 'https:' &&
        /^https:\/\//i.test(value) &&
        !url.username &&
        !url.password &&
        !/^https:\/\/[^/?#]*@/i.test(value) &&
        !value.includes('#')
      )
    }, 'Enter an HTTPS URL without credentials or a fragment.'),
})

export type AddMaterialFormValues = z.infer<typeof addMaterialSchema>

export type MaterialLibraryType = 'AIGC' | 'LivenessFace'

export type MaterialFileType = 'Image' | 'Video' | 'Audio'

export type MaterialStatus = 'pending' | 'processing' | 'ready' | 'failed'

export type BindingState =
  | 'pending'
  | 'creating'
  | 'processing'
  | 'ready'
  | 'failed'
  | 'uncertain'
  | 'updating'
  | 'rejected'
  | 'retired'
  | 'deleting'
  | 'deleted'

// Admin-safe mappings supplied by the server; never include credentials or raw errors.
export type MaterialBinding = {
  id: number
  channel_id: number
  channel_name: string
  provider: string
  group_id: string
  asset_id: string
  state: BindingState
  upstream_status: string
  fail_reason: string
  can_retry: boolean
  updated_time: number
}

export type GroupBinding = {
  id: number
  channel_id: number
  channel_name: string
  provider: string
  upstream_id: string
  name: string
  state: BindingState
  fail_reason: string
  updated_time: number
}

export type Material = {
  id: number
  material_id: string
  user_id: number
  username?: string
  name: string
  group_id: number | null
  group_name: string
  library_type: MaterialLibraryType
  file_type: MaterialFileType
  source_url: string
  status: MaterialStatus
  deleting?: boolean
  bindings?: MaterialBinding[]
  // Safe preparation reason supplied by the backend, not raw upstream errors.
  fail_reason: string
  can_retry: boolean
  created_time: number
  updated_time: number
}

export type MaterialListResponse = {
  success: boolean
  message?: string
  data?: Material[]
  total?: number
}

export type MaterialResponse = {
  success: boolean
  message?: string
  data?: Material
}

export type AddMaterialInput = AddMaterialFormValues & {
  library_type: 'AIGC'
}

export type MaterialGroup = {
  id: number
  user_id: number
  name: string
  status?: MaterialStatus
  fail_reason?: string
  deleting?: boolean
  bindings?: GroupBinding[]
  created_time: number
  updated_time: number
}

export type MaterialGroupListResponse = {
  success: boolean
  message?: string
  data?: MaterialGroup[]
}

export type MaterialGroupResponse = {
  success: boolean
  message?: string
  data?: MaterialGroup
}

export const materialGroupSchema = z.object({
  name: z
    .string()
    .trim()
    .refine((name) => {
      const length = [...name].length
      return length >= 1 && length <= 64
    }, 'Group name must contain 1 to 64 characters.'),
})

export type MaterialGroupFormValues = z.infer<typeof materialGroupSchema>

export type MaterialFilters = {
  keyword?: string
  library_type?: MaterialLibraryType | ''
  status?: MaterialStatus | ''
  group_id?: number
  source_url?: string
  channel_id?: number
  upstream_status?: 'ACTIVE' | 'PROCESSING' | 'FAILED' | ''
}

// Admin access boundaries. These are distinct integration surfaces and must not
// be assumed to share resource spaces or upstream material IDs.
export type AccessBoundary =
  | 'millu_gateway'
  | 'volcano_direct'
  | 'mobile_cloud_direct'

export type CapabilityKey =
  | 'virtual_group_ops'
  | 'virtual_material_ops'
  | 'real_person_session_create'
  | 'real_person_status'
  | 'real_person_material_register'
  | 'video_reference_format'

export type CapabilityStatus = 'verified' | 'unsupported' | 'pending'

// One cell of the admin capability matrix.
export type MaterialCapabilityCell = {
  id: number
  access_boundary: AccessBoundary
  capability: CapabilityKey
  status: CapabilityStatus
  verification_note: string
  doc_basis: string
  verified_time: number
  updated_time: number
}

export type MaterialCapabilityGridResponse = {
  success: boolean
  message?: string
  data?: MaterialCapabilityCell[]
  boundaries?: AccessBoundary[]
  capabilities?: CapabilityKey[]
}

export type MobileCloudConfig = {
  available: boolean
  enabled: boolean
  configured: boolean
  encryption_ready: boolean
  endpoint: string
  access_key_masked: string
  secret_key_configured: boolean
  pool_id: string
  channel_id: number
  channel_name: string
}

export type MobileCloudConfigResponse = {
  success: boolean
  message?: string
  data: MobileCloudConfig
}

export type TopenRouterConfig = {
  available: boolean
  enabled: boolean
  configured: boolean
  endpoint: string
  token_configured: boolean
  channel_id: number
  channel_name: string
}

export type TopenRouterConfigResponse = {
  success: boolean
  message?: string
  data: TopenRouterConfig
}

export type MaterialIntegrationProvider = 'mobile_cloud' | 'topenrouter'

export type MaterialTestOperation =
  | 'list_groups'
  | 'create_group'
  | 'get_group'
  | 'update_group'
  | 'delete_group'
  | 'list_assets'
  | 'create_asset'
  | 'get_asset'
  | 'update_asset'
  | 'delete_asset'

export type MaterialTestParameters = {
  pageNo?: number
  pageSize?: number
  groupType?: 'AIGC'
  groupName?: string
  description?: string
  groupId?: string
  groupIds?: string[]
  assetName?: string
  assetId?: string
  assetUrl?: string
  assetType?: MaterialFileType
  statuses?: ('ACTIVE' | 'PROCESSING' | 'FAILED')[]
}

export type MaterialTestRequest = {
  operation: MaterialTestOperation
  parameters: MaterialTestParameters
  confirmed?: boolean
}

export type MaterialTestResult = {
  diagnostic_id: string
  provider: MaterialIntegrationProvider
  channel_id: number
  channel_name: string
  operation: MaterialTestOperation
  success: boolean
  elapsed_ms: number
  total?: number
  message?: string
  response?: unknown
}

export type MaterialTestResponse = {
  success: boolean
  message?: string
  data?: MaterialTestResult
}
