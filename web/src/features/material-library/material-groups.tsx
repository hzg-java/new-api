import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { MaterialSelect } from './material-select'
import { useMaterialGroups } from './use-material-groups'

export function MaterialGroupSelect(props: {
  value: number | null | undefined
  onChange: (value: number | null | undefined) => void
  filter?: boolean
  targetUserId?: number
}) {
  const { t } = useTranslation()
  const query = useMaterialGroups(props.targetUserId)
  if (query.isError) {
    return (
      <ErrorState
        className='min-h-0 p-3'
        title={t('We could not load material groups.')}
        description={getServerErrorMessage(
          query.error,
          t('We could not load material groups.')
        )}
        onRetry={() => void query.refetch()}
      />
    )
  }
  if (query.isPending) {
    return (
      <LoadingState
        inline
        size='sm'
        message={t('Loading material groups...')}
      />
    )
  }
  const options = [
    ...(props.filter ? [{ value: 'all', label: t('All groups') }] : []),
    { value: '0', label: t('Ungrouped') },
    ...query.data.map((group) => ({
      value: String(group.id),
      label: group.name,
    })),
  ]
  let value = String(props.value ?? 0)
  if (props.filter && props.value === undefined) value = 'all'
  return (
    <MaterialSelect
      label={t('Material group')}
      value={value}
      options={options}
      onChange={(selected) => {
        if (selected === 'all') props.onChange(undefined)
        else if (selected === '0') props.onChange(props.filter ? 0 : null)
        else props.onChange(Number(selected))
      }}
    />
  )
}
