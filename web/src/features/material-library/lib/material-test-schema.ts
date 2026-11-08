import { z } from 'zod'

import type {
  MaterialIntegrationProvider,
  MaterialTestOperation,
  MaterialTestParameters,
} from '../types'

export const MATERIAL_TEST_OPERATIONS: MaterialTestOperation[] = [
  'list_groups',
  'create_group',
  'get_group',
  'update_group',
  'delete_group',
  'list_assets',
  'create_asset',
  'get_asset',
  'update_asset',
  'delete_asset',
]

export type MaterialTestField = {
  name: keyof MaterialTestParameters
  optional?: boolean
  type?: 'number' | 'url'
  max?: number
}

const materialTestFormValuesSchema = z.record(z.string(), z.string())
export type MaterialTestFormValues = z.infer<
  typeof materialTestFormValuesSchema
>

export function materialTestFields(
  provider: MaterialIntegrationProvider,
  operation: MaterialTestOperation
): MaterialTestField[] {
  const mobile = provider === 'mobile_cloud'
  const pagination: MaterialTestField[] = [
    { name: 'pageNo', type: 'number' },
    { name: 'pageSize', type: 'number', max: mobile ? undefined : 200 },
  ]
  switch (operation) {
    case 'list_groups':
      if (!mobile) return pagination
      return [
        pagination[0],
        { name: 'pageSize', type: 'number', max: 100 },
        { name: 'groupType' },
        { name: 'groupName', optional: true },
        { name: 'groupIds', optional: true },
      ]
    case 'create_group':
      return [{ name: 'groupName' }, { name: 'description', optional: true }]
    case 'get_group':
    case 'delete_group':
      return [{ name: 'groupId' }]
    case 'update_group':
      return [
        { name: 'groupId' },
        { name: 'groupName', optional: true },
        { name: 'description', optional: true },
      ]
    case 'list_assets':
      if (!mobile) return [...pagination, { name: 'groupIds', optional: true }]
      return [
        ...pagination,
        { name: 'groupType' },
        { name: 'assetName', optional: true },
        { name: 'groupIds', optional: true },
        { name: 'statuses', optional: true },
      ]
    case 'create_asset':
      return [
        { name: 'groupId', optional: !mobile },
        { name: 'assetName', optional: !mobile },
        { name: 'assetUrl', type: 'url' },
        { name: 'assetType' },
      ]
    case 'get_asset':
    case 'delete_asset':
      return [{ name: 'assetId' }]
    case 'update_asset':
      return [{ name: 'assetId' }, { name: 'assetName' }]
  }
}

function parameterSchema(field: MaterialTestField) {
  if (field.type === 'number') {
    return z
      .string()
      .trim()
      .transform(Number)
      .pipe(
        z
          .number()
          .int()
          .min(1)
          .max(field.max ?? Number.MAX_SAFE_INTEGER)
      )
  }
  if (field.name === 'assetUrl') {
    return z
      .string()
      .trim()
      .refine((value) => {
        if (!URL.canParse(value)) return false
        const url = new URL(value)
        return (
          (url.protocol === 'http:' || url.protocol === 'https:') &&
          /^https?:\/\//i.test(value) &&
          !url.username &&
          !url.password &&
          !/^https?:\/\/[^/?#]*@/i.test(value) &&
          !value.includes('#')
        )
      })
  }
  if (field.name === 'groupType') return z.literal('AIGC')
  if (field.name === 'assetType') return z.enum(['Image', 'Video', 'Audio'])
  if (field.name === 'groupIds' || field.name === 'statuses') {
    const item =
      field.name === 'statuses'
        ? z.enum(['ACTIVE', 'PROCESSING', 'FAILED'])
        : z.string().min(1)
    return z
      .string()
      .transform((value) => value.split(',').map((part) => part.trim()))
      .pipe(z.array(item).min(1))
  }
  return field.optional ? z.string().trim() : z.string().trim().min(1)
}

export function materialTestSchema(fields: MaterialTestField[]) {
  return materialTestFormValuesSchema.superRefine((values, context) => {
    for (const field of fields) {
      const value = values[field.name] ?? ''
      if (field.optional && !value.trim()) continue
      if (!parameterSchema(field).safeParse(value).success) {
        context.addIssue({
          code: 'custom',
          path: [field.name],
          message: 'Enter a valid value.',
        })
      }
    }
  })
}

export function materialTestDefaults(
  fields: MaterialTestField[]
): MaterialTestFormValues {
  return Object.fromEntries(
    fields.map((field) => {
      if (field.type === 'number') return [field.name, '1']
      if (field.name === 'groupType') return [field.name, 'AIGC']
      if (field.name === 'assetType') return [field.name, 'Image']
      return [field.name, '']
    })
  )
}

export function materialTestParameters(
  fields: MaterialTestField[],
  values: MaterialTestFormValues
): MaterialTestParameters {
  const entries = fields.flatMap((field) => {
    const value = values[field.name] ?? ''
    if (field.optional && !value.trim()) return []
    return [[field.name, parameterSchema(field).parse(value)]]
  })
  return Object.fromEntries(entries) as MaterialTestParameters
}
