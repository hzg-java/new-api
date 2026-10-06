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
import { Shield01Icon, Wrench01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Label } from '@/components/ui/label'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatLogQuota,
  formatNumber,
  formatTimestampToDate,
  formatUseTime,
} from '@/lib/format'
import { cn } from '@/lib/utils'

import { getTaskEvents } from '../../api'
import { taskActionMapper, taskStatusMapper } from '../../lib/mappers'
import { resolveTaskDetailAccess } from '../../lib/task-details'
import type { TaskEvent, TaskLog } from '../../types'
import { PluginAuthorLink } from '../plugin-author-link'

function DetailRow(props: {
  label: React.ReactNode
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className='grid min-w-0 grid-cols-[6rem_minmax(0,1fr)] gap-2 text-sm sm:grid-cols-[8rem_minmax(0,1fr)]'>
      <span className='text-muted-foreground text-xs'>{props.label}</span>
      <span
        className={cn(
          'min-w-0 text-xs break-all sm:wrap-break-word',
          props.mono && 'font-mono'
        )}
      >
        {props.value}
      </span>
    </div>
  )
}

function DetailSection(props: {
  label: string
  icon?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className='min-w-0 space-y-1.5'>
      <Label className='flex items-center gap-1.5 text-xs font-semibold'>
        {props.icon}
        {props.label}
      </Label>
      <div className='bg-muted/30 min-w-0 space-y-1.5 rounded-md border p-2.5'>
        {props.children}
      </div>
    </section>
  )
}

function formatTaskTimestamp(value?: number): string {
  return value ? formatTimestampToDate(value, 'seconds') : '-'
}

function TaskTimeline(props: { events: TaskEvent[] }) {
  const { t } = useTranslation()
  const labels = {
    request: t('User Request'),
    accepted: t('Submission Accepted'),
    result: t('Task Result'),
  }
  return (
    <section
      className='space-y-4 border-t pt-4'
      aria-label={t('Full Chain Events')}
    >
      <h3 className='text-sm font-semibold'>{t('Full Chain Events')}</h3>
      {props.events.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No events recorded')}
        </p>
      ) : (
        <ol className='space-y-0'>
          {props.events.map((event) => {
            let fields: Record<string, unknown> = {}
            try {
              fields = JSON.parse(event.payload) as Record<string, unknown>
            } catch {
              fields = {}
            }
            return (
              <li
                key={event.kind}
                className='border-border relative border-l pb-5 pl-5 last:border-l-transparent last:pb-0'
              >
                <span
                  className='bg-primary absolute top-1 -left-1 size-2 rounded-full'
                  aria-hidden='true'
                />
                <div className='mb-2 flex flex-wrap items-center justify-between gap-2'>
                  <h4 className='text-sm font-medium'>{labels[event.kind]}</h4>
                  <time
                    className='text-muted-foreground font-mono text-xs'
                    dateTime={new Date(event.timestamp * 1000).toISOString()}
                  >
                    {formatTaskTimestamp(event.timestamp)}
                  </time>
                </div>
                <div className='bg-muted/30 relative max-h-56 overflow-auto rounded-md border p-3'>
                  <CopyButton
                    value={event.payload}
                    className='absolute top-1 right-1 size-7'
                    iconClassName='size-3.5'
                  />
                  <div className='space-y-1 pr-7 font-mono text-xs break-all whitespace-pre-wrap'>
                    {Object.entries(fields).map(([key, value]) => (
                      <div key={key}>
                        <span className='text-muted-foreground'>{key}: </span>
                        <span>
                          {typeof value === 'object'
                            ? JSON.stringify(value, null, 2)
                            : String(value)}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              </li>
            )
          })}
        </ol>
      )}
    </section>
  )
}

interface TaskDetailsDialogProps {
  log: TaskLog
  isAdmin: boolean
  isRoot: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function TaskDetailsDialog(props: TaskDetailsDialogProps) {
  const { t, i18n } = useTranslation()
  const access = resolveTaskDetailAccess(props.log, props.isAdmin, props.isRoot)
  const plugin = access.plugin
  const runtime = access.runtime
  const properties = props.log.properties
  const videoInfo = props.log.video_info
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const events = useQuery({
    queryKey: ['task-events', props.log.id, props.log.status],
    enabled: props.open && props.log.id > 0,
    refetchInterval:
      props.open && !['SUCCESS', 'FAILURE'].includes(props.log.status)
        ? 5000
        : false,
    queryFn: async () => {
      const response = await getTaskEvents(props.log.id)
      if (!response.success) {
        throw new Error(response.message || t('Failed to load logs'))
      }
      return response.data || []
    },
  })
  const billingLabels = {
    per_call: t('Per-call'),
    per_token: t('Per-token'),
    tiered_expr: t('Dynamic Pricing'),
  }
  const duration =
    props.log.submit_time &&
    props.log.finish_time &&
    props.log.finish_time >= props.log.submit_time
      ? formatUseTime(props.log.finish_time - props.log.submit_time)
      : '-'
  let hasReferenceVideoLabel = '-'
  if (videoInfo?.has_reference_video != null) {
    hasReferenceVideoLabel = videoInfo.has_reference_video ? t('Yes') : t('No')
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        <span className='flex items-center gap-2'>
          {t('Task Details')}
          <StatusBadge
            label={t(
              taskStatusMapper.getLabel(
                props.log.status,
                props.log.status || 'Submitting'
              )
            )}
            variant={taskStatusMapper.getVariant(props.log.status)}
            size='sm'
            copyable={false}
          />
        </span>
      }
      description={t('View the complete details for this task')}
      contentClassName='min-w-0 overflow-hidden sm:max-w-4xl'
      contentHeight='min(78dvh, 850px)'
      bodyClassName='pr-2 sm:pr-4'
    >
      <div className='min-w-0 space-y-4 overflow-x-hidden'>
        <section
          aria-label={t('Basic Information')}
          className='bg-muted/20 grid min-w-0 gap-x-6 gap-y-3 rounded-lg border p-4 md:grid-cols-2'
        >
          <DetailRow label={t('Task ID')} value={props.log.task_id} mono />
          <DetailRow
            label={t('Model')}
            value={properties?.origin_model_name || '-'}
            mono
          />
          <DetailRow
            label={t('Task Status')}
            value={t(
              taskStatusMapper.getLabel(
                props.log.status,
                props.log.status || 'Submitting'
              )
            )}
          />
          <DetailRow
            label={t('Billing Mode')}
            value={
              props.log.billing_mode
                ? billingLabels[props.log.billing_mode]
                : t('Not recorded')
            }
          />
          <DetailRow
            label={t('Resolution')}
            value={videoInfo?.resolution || '-'}
            mono
          />
          <DetailRow
            label={t('Duration (seconds)')}
            value={formatNumber(videoInfo?.duration_seconds, locale)}
            mono
          />
          <DetailRow
            label={t('Has Reference Video')}
            value={hasReferenceVideoLabel}
          />
          <DetailRow
            label={t('Consumed Tokens')}
            value={formatNumber(videoInfo?.consumed_tokens, locale)}
            mono
          />
          <DetailRow
            label={t('Cost')}
            value={
              ['SUCCESS', 'FAILURE'].includes(props.log.status)
                ? formatLogQuota(props.log.quota)
                : '—'
            }
            mono
          />
          <DetailRow label={t('Task Duration')} value={duration} mono />
          <DetailRow
            label={t('Created At')}
            value={formatTaskTimestamp(props.log.submit_time)}
            mono
          />
          <DetailRow
            label={t('Finished At')}
            value={formatTaskTimestamp(props.log.finish_time)}
            mono
          />
        </section>
        {events.isSuccess ? <TaskTimeline events={events.data} /> : null}
        {events.isPending && props.open ? (
          <p className='text-muted-foreground text-xs'>{t('Loading')}</p>
        ) : null}
        {events.isError ? (
          <p role='alert' className='text-destructive text-xs'>
            {t('Failed to load logs')}
          </p>
        ) : null}
        <DetailSection label={t('Additional Information')}>
          {props.isAdmin ? (
            <DetailRow label={t('Platform')} value={props.log.platform} mono />
          ) : null}
          <DetailRow
            label={t('Action')}
            value={t(taskActionMapper.getLabel(props.log.action))}
          />
          <DetailRow
            label={t('Progress')}
            value={props.log.progress || '-'}
            mono
          />
          <DetailRow
            label={t('Start Time')}
            value={formatTaskTimestamp(props.log.start_time)}
            mono
          />
          {properties?.origin_model_name ? (
            <DetailRow
              label={t('Request Model')}
              value={properties.origin_model_name}
              mono
            />
          ) : null}
          {props.isAdmin && properties?.upstream_model_name ? (
            <DetailRow
              label={t('Actual Model')}
              value={properties.upstream_model_name}
              mono
            />
          ) : null}
          {props.log.fail_reason ? (
            <DetailRow label={t('Fail Reason')} value={props.log.fail_reason} />
          ) : null}
        </DetailSection>

        {props.isAdmin ? (
          <DetailSection
            label={t('Admin Only')}
            icon={
              <HugeiconsIcon
                icon={Shield01Icon}
                className='size-3.5 text-blue-500'
                strokeWidth={2}
              />
            }
          >
            <DetailRow
              label={t('User')}
              value={props.log.username || String(props.log.user_id)}
            />
            <DetailRow
              label={t('Channel')}
              value={`#${props.log.channel_id}`}
              mono
            />
            <DetailRow label={t('Group')} value={props.log.group || '-'} />
            <DetailRow
              label={t('Quota')}
              value={
                ['SUCCESS', 'FAILURE'].includes(props.log.status)
                  ? formatLogQuota(props.log.quota)
                  : '—'
              }
              mono
            />
            {props.log.admin_info?.request_id ? (
              <DetailRow
                label={t('Request ID')}
                value={props.log.admin_info.request_id}
                mono
              />
            ) : null}
            {props.log.admin_info?.request_path ? (
              <DetailRow
                label={t('Request Path')}
                value={props.log.admin_info.request_path}
                mono
              />
            ) : null}
            {plugin ? (
              <>
                <DetailRow
                  label={t('Task Plugin')}
                  value={plugin.name || plugin.key}
                />
                <DetailRow label={t('Plugin key')} value={plugin.key} mono />
                <DetailRow
                  label={t('Version')}
                  value={plugin.version || '-'}
                  mono
                />
                {plugin.author ? (
                  <DetailRow
                    label={t('Plugin author')}
                    value={<PluginAuthorLink author={plugin.author} showUrl />}
                  />
                ) : null}
              </>
            ) : null}
          </DetailSection>
        ) : null}

        {props.isRoot && props.log.root_info ? (
          <DetailSection
            label={t('Root Diagnostics')}
            icon={
              <HugeiconsIcon
                icon={Wrench01Icon}
                className='size-3.5 text-amber-500'
                strokeWidth={2}
              />
            }
          >
            {runtime ? (
              <>
                <DetailRow
                  label={t('API Version')}
                  value={String(runtime.api_version)}
                  mono
                />
                <DetailRow
                  label={t('Plugin Generation')}
                  value={String(runtime.generation)}
                  mono
                />
              </>
            ) : null}
            {access.upstreamTaskId ? (
              <DetailRow
                label={t('Upstream Task ID')}
                value={access.upstreamTaskId}
                mono
              />
            ) : null}
            {access.nodeName ? (
              <DetailRow label={t('Node Name')} value={access.nodeName} mono />
            ) : null}
          </DetailSection>
        ) : null}
      </div>
    </Dialog>
  )
}
