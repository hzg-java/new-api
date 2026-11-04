import type {
  AccessBoundary,
  BindingState,
  CapabilityKey,
  CapabilityStatus,
  MaterialLibraryType,
  MaterialStatus,
} from './types'

// Label values are i18n keys; render them through t().
export const MATERIAL_LIBRARY_LABEL: Record<MaterialLibraryType, string> = {
  AIGC: 'AIGC',
  LivenessFace: 'LivenessFace',
}

export const MATERIAL_STATUS_LABEL: Record<MaterialStatus, string> = {
  pending: 'Pending preparation',
  processing: 'Preparing',
  ready: 'Ready',
  failed: 'Failed',
}

export const BINDING_STATE_LABEL: Record<BindingState, string> = {
  pending: 'Pending preparation',
  creating: 'Creating',
  processing: 'Preparing',
  ready: 'Ready',
  failed: 'Failed',
  uncertain: 'Uncertain',
  updating: 'Updating...',
  rejected: 'Failed',
  retired: 'Previous',
  deleting: 'Deleting',
  deleted: 'Deleted',
}

export function materialStatusVariant(
  status: MaterialStatus
): 'default' | 'secondary' | 'destructive' | 'warning' | 'outline' {
  switch (status) {
    case 'ready':
      return 'default'
    case 'processing':
      return 'warning'
    case 'failed':
      return 'destructive'
    default:
      return 'secondary'
  }
}

export const ACCESS_BOUNDARY_LABEL: Record<AccessBoundary, string> = {
  millu_gateway: 'Millu gateway',
  volcano_direct: 'Volcano direct',
  mobile_cloud_direct: 'Mobile Cloud direct',
}

export const CAPABILITY_LABEL: Record<CapabilityKey, string> = {
  virtual_group_ops: 'Virtual material group operations',
  virtual_material_ops: 'Virtual material upload / query / rename / delete',
  real_person_session_create: 'Real-person authentication session creation',
  real_person_status: 'Real-person authentication status query / callback',
  real_person_material_register: 'Real-person material registration / upload',
  video_reference_format: 'Video generation material reference format',
}

export const CAPABILITY_STATUS_LABEL: Record<CapabilityStatus, string> = {
  verified: 'Verified',
  unsupported: 'Unsupported',
  pending: 'To be verified',
}

export function capabilityStatusVariant(
  status: CapabilityStatus
): 'default' | 'secondary' | 'destructive' | 'warning' | 'outline' {
  switch (status) {
    case 'verified':
      return 'default'
    case 'unsupported':
      return 'destructive'
    default:
      return 'warning'
  }
}
