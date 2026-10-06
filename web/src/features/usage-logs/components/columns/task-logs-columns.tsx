/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { ViewIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import type { ColumnDef } from '@tanstack/react-table'
/* eslint-disable react-refresh/only-export-components */
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { toIntlLocale } from '@/i18n/languages'
import { getUserAvatarFallback, getUserAvatarStyle } from '@/lib/avatar'
import {
  formatLogQuota,
  formatNumber,
  formatTimestampToDate,
} from '@/lib/format'
import { cn } from '@/lib/utils'

import { taskStatusMapper } from '../../lib/mappers'
import type { TaskLog } from '../../types'
import { TaskDetailsDialog } from '../dialogs/task-details-dialog'
import { PluginAuthorLink } from '../plugin-author-link'
import { TaskArtifactsCell } from '../task-artifacts'
import { useUsageLogsContext } from '../usage-logs-provider'
import {
  createDurationColumn,
  createChannelColumn,
  createProgressColumn,
} from './column-helpers'

function TaskDetailsCell(props: {
  log: TaskLog
  isAdmin: boolean
  isRoot: boolean
}) {
  const { t } = useTranslation()
  const [dialogOpen, setDialogOpen] = useState(false)

  return (
    <>
      <div className='flex max-w-[220px] flex-col items-start gap-1'>
        <button
          type='button'
          className='text-foreground inline-flex items-center gap-1 text-xs font-medium hover:underline'
          onClick={() => setDialogOpen(true)}
        >
          <HugeiconsIcon
            icon={ViewIcon}
            className='size-3'
            strokeWidth={2}
            aria-hidden='true'
          />
          {t('View details')}
        </button>
        {props.log.fail_reason ? (
          <span className='max-w-full truncate text-xs text-red-600 dark:text-red-400'>
            {props.log.fail_reason}
          </span>
        ) : null}
      </div>
      <TaskDetailsDialog
        log={props.log}
        isAdmin={props.isAdmin}
        isRoot={props.isRoot}
        open={dialogOpen}
        onOpenChange={setDialogOpen}
      />
    </>
  )
}

export function useTaskLogsColumns(
  isAdmin: boolean,
  isRoot: boolean
): ColumnDef<TaskLog>[] {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return useMemo(() => {
    const columns: ColumnDef<TaskLog>[] = [
      {
        accessorKey: 'submit_time',
        header: t('Created At'),
        cell: ({ row }) => (
          <span className='truncate font-mono text-xs tabular-nums'>
            {formatTimestampToDate(row.original.submit_time, 'seconds')}
          </span>
        ),
        size: 180,
      },
      {
        accessorKey: 'finish_time',
        header: t('Finished At'),
        cell: ({ row }) => (
          <span className='truncate font-mono text-xs tabular-nums'>
            {row.original.finish_time
              ? formatTimestampToDate(row.original.finish_time, 'seconds')
              : '-'}
          </span>
        ),
        size: 180,
      },
    ]

    if (isAdmin) {
      columns.push(
        createChannelColumn<TaskLog>({ headerLabel: t('Channel') }),
        {
          id: 'user',
          header: t('User'),
          accessorFn: (row) => row.username || row.user_id,
          cell: function UserCell({ row }) {
            const {
              sensitiveVisible,
              setSelectedUserId,
              setUserInfoDialogOpen,
            } = useUsageLogsContext()
            const log = row.original
            const displayName = log.username || String(log.user_id || '?')

            return (
              <button
                type='button'
                className='flex items-center gap-1.5 text-left'
                onClick={(e) => {
                  e.stopPropagation()
                  setSelectedUserId(log.user_id)
                  setUserInfoDialogOpen(true)
                }}
              >
                <Avatar className='ring-border/60 size-6 ring-1 max-sm:hidden'>
                  <AvatarFallback
                    className={cn(
                      'text-[11px] font-semibold',
                      !sensitiveVisible && 'bg-muted text-muted-foreground'
                    )}
                    style={
                      sensitiveVisible
                        ? getUserAvatarStyle(displayName)
                        : undefined
                    }
                  >
                    {sensitiveVisible
                      ? getUserAvatarFallback(displayName)
                      : '•'}
                  </AvatarFallback>
                </Avatar>
                <span className='text-muted-foreground truncate text-sm hover:underline'>
                  {sensitiveVisible ? displayName : '••••'}
                </span>
              </button>
            )
          },
        },
        {
          id: 'plugin',
          header: t('Plugin'),
          accessorFn: (row) => row.admin_info?.task_plugin?.key ?? '',
          cell: ({ row }) => {
            const plugin = row.original.admin_info?.task_plugin
            if (!plugin) {
              return <span className='text-muted-foreground/60 text-xs'>-</span>
            }
            return (
              <div className='flex max-w-[170px] flex-col gap-0.5'>
                <span className='truncate text-xs font-medium'>
                  {plugin.name || plugin.key}
                </span>
                <span className='text-muted-foreground truncate font-mono text-[11px]'>
                  {plugin.key}
                  {plugin.version ? ` @ ${plugin.version}` : ''}
                </span>
                {plugin.author ? (
                  <PluginAuthorLink
                    author={plugin.author}
                    showUrl
                    className='text-muted-foreground text-[11px]'
                  />
                ) : null}
              </div>
            )
          },
        }
      )
    }

    columns.push(
      {
        accessorKey: 'task_id',
        header: t('Task ID'),
        cell: ({ row }) => {
          const taskId = row.getValue('task_id') as string
          if (!taskId) {
            return <span className='text-muted-foreground/60 text-xs'>-</span>
          }
          return (
            <div className='flex max-w-[170px] flex-col gap-0.5'>
              <StatusBadge
                label={taskId}
                copyText={taskId}
                variant='neutral'
                size='sm'
                className='border-border/60 bg-muted/30 !text-foreground max-w-full truncate rounded-md border px-1.5 py-0.5 font-mono'
              />
            </div>
          )
        },
        meta: { mobileTitle: true },
      },
      {
        id: 'model',
        header: t('Model'),
        accessorFn: (log) => {
          const properties = log.properties
          if (isAdmin) {
            return (
              properties?.upstream_model_name ||
              properties?.origin_model_name ||
              '-'
            )
          }
          return properties?.origin_model_name || '-'
        },
        cell: ({ row }) => {
          const properties = row.original.properties
          if (!isAdmin) {
            return (
              <span className='block max-w-[200px] truncate text-xs'>
                {properties?.origin_model_name || '-'}
              </span>
            )
          }
          return (
            <div className='flex max-w-[240px] flex-col gap-0.5 text-xs'>
              <span className='truncate'>
                {t('Request Model')}: {properties?.origin_model_name || '-'}
              </span>
              <span className='text-muted-foreground truncate'>
                {t('Actual Model')}: {properties?.upstream_model_name || '-'}
              </span>
            </div>
          )
        },
      },
      {
        id: 'cost',
        header: t('Cost'),
        accessorFn: (log) => log.quota,
        cell: ({ row }) => (
          <span className='font-mono text-xs tabular-nums'>
            {['SUCCESS', 'FAILURE'].includes(row.original.status)
              ? formatLogQuota(row.original.quota)
              : '—'}
          </span>
        ),
      },
      createDurationColumn<TaskLog>({
        submitTimeKey: 'submit_time',
        finishTimeKey: 'finish_time',
        unit: 'seconds',
        headerLabel: t('Duration'),
        warningThresholdSec: 300,
      }),
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => {
          const status = row.getValue('status') as string
          return (
            <StatusBadge
              label={t(
                taskStatusMapper.getLabel(status, status || 'Submitting')
              )}
              variant={taskStatusMapper.getVariant(status)}
              size='sm'
              copyable={false}
              className='-ml-1.5'
            />
          )
        },
      },
      createProgressColumn<TaskLog>({ headerLabel: t('Progress') }),
      {
        id: 'resolution',
        header: t('Resolution'),
        accessorFn: (log) => log.video_info?.resolution,
        cell: ({ row }) => (
          <span className='text-xs'>
            {row.original.video_info?.resolution || '-'}
          </span>
        ),
      },
      {
        id: 'duration_seconds',
        header: t('Duration (seconds)'),
        accessorFn: (log) => log.video_info?.duration_seconds,
        cell: ({ row }) => (
          <span className='font-mono text-xs tabular-nums'>
            {formatNumber(row.original.video_info?.duration_seconds, locale)}
          </span>
        ),
      },
      {
        id: 'has_reference_video',
        header: t('Has Reference Video'),
        accessorFn: (log) => log.video_info?.has_reference_video,
        cell: ({ row }) => {
          const hasReferenceVideo = row.original.video_info?.has_reference_video
          if (hasReferenceVideo == null) {
            return <span className='text-xs'>-</span>
          }
          return (
            <span className='text-xs'>
              {hasReferenceVideo ? t('Yes') : t('No')}
            </span>
          )
        },
      },
      {
        id: 'consumed_tokens',
        header: t('Consumed Tokens'),
        accessorFn: (log) => log.video_info?.consumed_tokens,
        cell: ({ row }) => (
          <span className='font-mono text-xs tabular-nums'>
            {formatNumber(row.original.video_info?.consumed_tokens, locale)}
          </span>
        ),
      },
      {
        id: 'artifacts',
        header: t('Artifacts'),
        cell: ({ row }) => (
          <TaskArtifactsCell key={row.original.task_id} log={row.original} />
        ),
        size: 120,
        maxSize: 140,
      },
      {
        accessorKey: 'fail_reason',
        header: t('Details'),
        cell: ({ row }) => (
          <TaskDetailsCell
            key={row.original.task_id}
            log={row.original}
            isAdmin={isAdmin}
            isRoot={isRoot}
          />
        ),
        size: 220,
        maxSize: 240,
      }
    )

    const columnOrder = [
      'task_id',
      'user',
      'model',
      'cost',
      'duration',
      'status',
      'progress',
      'resolution',
      'duration_seconds',
      'has_reference_video',
      'consumed_tokens',
      'channel_id',
      'plugin',
      'submit_time',
      'finish_time',
      'artifacts',
      'fail_reason',
    ]
    return columns.sort((left, right) => {
      const leftId = left.id ?? ('accessorKey' in left ? left.accessorKey : '')
      const rightId =
        right.id ?? ('accessorKey' in right ? right.accessorKey : '')
      return (
        columnOrder.indexOf(String(leftId)) -
        columnOrder.indexOf(String(rightId))
      )
    })
  }, [t, locale, isAdmin, isRoot])
}
