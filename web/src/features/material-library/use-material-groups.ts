import { useQuery, type QueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { createServerError } from '@/lib/server-error-message'

import { getMaterialGroups } from './api'

export const MATERIAL_GROUPS_QUERY_KEY = ['material-library', 'groups']

export function getMaterialGroupsQueryKey(
  targetUserId?: number
): (string | number)[] {
  return targetUserId === undefined
    ? MATERIAL_GROUPS_QUERY_KEY
    : [...MATERIAL_GROUPS_QUERY_KEY, 'admin', targetUserId]
}

export function useMaterialGroups(targetUserId?: number) {
  const { t } = useTranslation()
  return useQuery({
    queryKey: getMaterialGroupsQueryKey(targetUserId),
    queryFn: async () => {
      const res = await getMaterialGroups(targetUserId)
      if (!res.success || !Array.isArray(res.data)) {
        throw createServerError(res, t('We could not load material groups.'))
      }
      return res.data
    },
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.some(
        (group) => group.status === 'pending' || group.status === 'processing'
      )
        ? 5000
        : false,
  })
}

export async function invalidateMaterialLists(client: QueryClient) {
  await Promise.all([
    client.invalidateQueries({ queryKey: ['material-library', 'materials'] }),
    client.invalidateQueries({
      queryKey: ['material-library', 'all-materials'],
    }),
  ])
}
