import dayjs from 'dayjs'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'

import { BINDING_STATE_LABEL } from './constants'
import type { GroupBinding, MaterialBinding } from './types'

export function CopyableMaterialValue(props: { label: string; value: string }) {
  const { t } = useTranslation()
  return (
    <div className='min-w-0 space-y-1 text-sm'>
      <span className='text-muted-foreground'>{props.label}</span>
      <div className='flex min-w-0 items-start gap-1'>
        <code className='min-w-0 break-all whitespace-normal'>
          {props.value || '—'}
        </code>
        {props.value && (
          <CopyButton
            value={props.value}
            aria-label={t('Copy {{label}}', { label: props.label })}
          />
        )}
      </div>
    </div>
  )
}

export function BindingDetails(props: {
  binding: MaterialBinding | GroupBinding
}) {
  const { t } = useTranslation()
  const binding = props.binding
  const isMaterial = 'asset_id' in binding
  return (
    <div className='min-w-0 space-y-2 rounded-md border p-3 text-sm'>
      <p className='break-all whitespace-normal'>
        {binding.channel_name} #{binding.channel_id} · {binding.provider}
      </p>
      <CopyableMaterialValue
        label={t('Upstream Group ID')}
        value={isMaterial ? binding.group_id : binding.upstream_id}
      />
      {isMaterial && (
        <CopyableMaterialValue
          label={t('Upstream Asset ID')}
          value={binding.asset_id}
        />
      )}
      {isMaterial && (
        <p>
          {t('Upstream status')}: <span>{binding.upstream_status || '—'}</span>
        </p>
      )}
      <p>
        {t('Sync status')}:{' '}
        <span>{t(BINDING_STATE_LABEL[binding.state] ?? binding.state)}</span>
      </p>
      {binding.fail_reason && (
        <p className='text-destructive break-all whitespace-normal'>
          {binding.fail_reason}
        </p>
      )}
      <p className='text-muted-foreground'>
        {t('Updated at')}:{' '}
        {dayjs.unix(binding.updated_time).format('YYYY-MM-DD HH:mm:ss')}
      </p>
    </div>
  )
}
