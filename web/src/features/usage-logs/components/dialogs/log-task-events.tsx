import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { CodeBlock } from '@/components/ai-elements/code-block'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getLogTaskEvents } from '../../api'
import type { LogOtherData } from '../../types'
import { DetailRow, DetailSection } from './log-detail-layout'
import { TaskTimeline } from './task-details-dialog'

export function FailedTaskSubmission(props: {
  submission?: NonNullable<LogOtherData['admin_info']>['task_submission']
}) {
  const { t } = useTranslation()
  return (
    <div className='min-w-0 space-y-3 border-t pt-4'>
      {(['request', 'response'] as const).map((kind) => {
        const label = kind === 'request' ? t('User Request') : t('Response')
        const value = props.submission?.[kind]
        const code = JSON.stringify(value, null, 2)
        return (
          <DetailSection key={kind} label={label}>
            {code === undefined ? (
              <span className='text-muted-foreground text-xs'>
                {t('Not recorded')}
              </span>
            ) : (
              <>
                {value !== null &&
                  typeof value === 'object' &&
                  'unavailable' in value &&
                  typeof value.unavailable === 'string' && (
                    <span className='text-muted-foreground text-xs'>
                      {t('Not recorded')}
                    </span>
                  )}
                <CodeBlock
                  code={code}
                  language='json'
                  title={label}
                  enableCollapse={false}
                  maxExpandedLines={14}
                  showToolbar
                  showLineNumbers
                >
                  <CopyButton
                    value={code}
                    className='size-8'
                    iconClassName='size-3.5'
                  />
                </CodeBlock>
              </>
            )}
          </DetailSection>
        )
      })}
    </div>
  )
}

export function LogTaskEvents(props: {
  taskId: string
  ownerId: number
  viewerId: number
}) {
  const { t } = useTranslation()
  const events = useQuery({
    queryKey: [
      'log-task-events',
      'admin',
      props.taskId,
      props.ownerId,
      props.viewerId,
    ],
    queryFn: async ({ signal }) => {
      const response = requireServerSuccess(
        await getLogTaskEvents(props.taskId, props.ownerId, signal)
      )
      return (response.data ?? []).filter(
        (event) => event.kind === 'request' || event.kind === 'accepted'
      )
    },
    placeholderData: undefined,
  })

  if (events.isPending) {
    return <LoadingState inline size='sm' message={t('Loading')} />
  }
  if (events.isError) {
    return (
      <ErrorState title={t('Failed to load logs')} className='min-h-0 py-4' />
    )
  }

  return (
    <div className='min-w-0 space-y-2'>
      <TaskTimeline events={events.data} />
      {!events.data.some((event) => event.kind === 'request') && (
        <DetailRow label={t('User Request')} value={t('Not recorded')} />
      )}
      {!events.data.some((event) => event.kind === 'accepted') && (
        <DetailRow label={t('Submission Accepted')} value={t('Not recorded')} />
      )}
    </div>
  )
}
