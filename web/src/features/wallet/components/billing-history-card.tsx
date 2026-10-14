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
import { useQuery } from '@tanstack/react-query'
import { Receipt, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePagination,
  StaticDataTable,
  useDataTable,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { getUserLogs } from '@/features/usage-logs/api'
import { usageLogSchema } from '@/features/usage-logs/data/schema'
import { parseLogOther } from '@/features/usage-logs/lib/format'
import { formatLogQuota } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { formatTimestamp } from '../lib/billing'

export function BillingHistoryCard() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 10 })
  const page = pagination.pageIndex + 1
  const { pageSize } = pagination
  const history = useQuery({
    queryKey: ['wallet-topup-logs', userId, page, pageSize],
    queryFn: async () => {
      const data = requireServerSuccess(
        await getUserLogs({ p: page, page_size: pageSize, type: 1 })
      ).data
      return {
        total: data?.total ?? 0,
        items: usageLogSchema.array().parse(data?.items ?? []),
      }
    },
  })
  const records = history.data?.items ?? []
  const total = history.data?.total ?? 0
  const { table } = useDataTable({
    data: records,
    columns: [],
    pagination,
    onPaginationChange: (updater) => {
      setPagination((previous) => {
        const next = typeof updater === 'function' ? updater(previous) : updater
        return next.pageSize === previous.pageSize
          ? next
          : { ...next, pageIndex: 0 }
      })
    },
    manualPagination: true,
    totalCount: total,
  })

  return (
    <TitledCard
      title={t('Top-up records')}
      icon={<Receipt aria-hidden='true' className='h-4 w-4' />}
      iconTone='info'
      disableHoverEffect
      action={
        <Button
          variant='outline'
          size='sm'
          onClick={() => void history.refetch()}
          disabled={history.isFetching}
        >
          <RefreshCw aria-hidden='true' className='h-4 w-4' />
          {t('Refresh')}
        </Button>
      }
    >
      <div className='space-y-4' aria-busy={history.isFetching}>
        {history.isPending && <Skeleton className='h-32 w-full' />}
        {history.isError && (
          <p role='alert' className='text-destructive py-8 text-center text-sm'>
            {t('Failed to load billing history')}
          </p>
        )}
        {!history.isPending && !history.isError && records.length === 0 && (
          <div className='text-muted-foreground py-10 text-center text-sm'>
            {t('No billing records found')}
          </div>
        )}
        {!history.isPending && !history.isError && records.length > 0 && (
          <StaticDataTable
            data={records}
            getRowKey={(record) => record.id}
            tableProps={{ 'aria-label': t('Top-up records') }}
            columns={[
              {
                id: 'time',
                header: t('Time'),
                cell: (record) => formatTimestamp(record.created_at),
              },
              { id: 'type', header: t('Type'), cell: () => t('Top-up') },
              {
                id: 'description',
                header: t('Top-up description'),
                cell: () => t('Administrator manual top-up'),
              },
              {
                id: 'amount',
                header: t('Amount'),
                cell: (record) => {
                  const params = parseLogOther(record.other)?.op?.params
                  return typeof params?.from === 'number' &&
                    typeof params.to === 'number'
                    ? formatLogQuota(params.to - params.from)
                    : t('Not recorded')
                },
              },
              {
                id: 'balance',
                header: t('Account balance'),
                cell: (record) => {
                  const params = parseLogOther(record.other)?.op?.params
                  return typeof params?.to === 'number'
                    ? formatLogQuota(params.to)
                    : t('Not recorded')
                },
              },
            ]}
          />
        )}
        {!history.isPending && !history.isError && records.length > 0 && (
          <div className='border-t pt-4'>
            <DataTablePagination table={table} />
          </div>
        )}
      </div>
    </TitledCard>
  )
}
