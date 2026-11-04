import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { CellContext, ColumnDef } from '@tanstack/react-table'
import dayjs from 'dayjs'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import {
  DataTablePage,
  DataTableToolbar,
  TruncatedCell,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { handleServerError } from '@/lib/handle-server-error'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'

import { AddMaterialDialog } from './add-material-dialog'
import { deleteMaterial, getMaterials, retryMaterial } from './api'
import { ChangeMaterialGroupDialog } from './change-material-group-dialog'
import {
  MATERIAL_LIBRARY_LABEL,
  MATERIAL_STATUS_LABEL,
  materialStatusVariant,
} from './constants'
import { ManageGroupsDialog } from './manage-groups-dialog'
import { MaterialGroupSelect } from './material-groups'
import { MaterialPreview } from './material-preview'
import { MaterialSelect } from './material-select'
import { MaterialUserFilter, type MaterialOwner } from './material-user-filter'
import { MaterialRecordDrawer } from './record-drawer'
import type { Material, MaterialFilters, MaterialStatus } from './types'

const EMPTY_MATERIALS: Material[] = []
const MATERIALS_QUERY_KEY = ['material-library', 'materials']

function MaterialPreviewCell(props: CellContext<Material, unknown>) {
  const material = props.row.original
  return (
    <MaterialPreview
      key={`${material.id}:${material.source_url}:${material.file_type}`}
      material={material}
    />
  )
}

export function MaterialLibrary(props: { admin?: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [owner, setOwner] = useState<MaterialOwner | null>(null)
  const targetUserId = props.admin ? owner?.id : undefined
  const queryKey = props.admin
    ? ['material-library', 'all-materials']
    : MATERIALS_QUERY_KEY
  const canManageOwner = !props.admin || owner !== null
  const [addOpen, setAddOpen] = useState(false)
  const [filters, setFilters] = useState<MaterialFilters>({
    keyword: '',
    status: '',
  })
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [pendingDelete, setPendingDelete] = useState<Material | null>(null)
  const [pendingRetry, setPendingRetry] = useState<Material | null>(null)
  const [detailId, setDetailId] = useState<number | null>(null)
  const [manageGroupsOpen, setManageGroupsOpen] = useState(false)
  const [changingGroup, setChangingGroup] = useState<Material | null>(null)

  const materialsQuery = useQuery({
    queryKey: [...queryKey, targetUserId, filters, pagination],
    queryFn: async () => {
      const res = await getMaterials({
        ...(props.admin ? { scope: 'all', user_id: targetUserId } : {}),
        ...filters,
        group_id: filters.group_id,
        offset: pagination.pageIndex * pagination.pageSize,
        limit: pagination.pageSize,
      })
      if (!res.success || !Array.isArray(res.data)) {
        throw createServerError(res, t('We could not load materials.'))
      }
      return { materials: res.data, total: res.total ?? 0 }
    },
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.materials.some(
        (material) =>
          material.status === 'pending' || material.status === 'processing'
      )
        ? 5000
        : false,
  })

  const deleteMutation = useMutation({
    mutationFn: async (material: Material) => {
      const res = props.admin
        ? await deleteMaterial(material.id, material.user_id)
        : await deleteMaterial(material.id)
      if (!res.success || !res.data) {
        throw createServerError(res, t('Delete material failed'))
      }
      return res.data
    },
    onSuccess: async (saved) => {
      toast.success(t('Deletion queued'))
      const keys = [MATERIALS_QUERY_KEY, ['material-library', 'all-materials']]
      await Promise.all(
        keys.map((queryKey) => queryClient.cancelQueries({ queryKey }))
      )
      for (const queryKey of keys) {
        queryClient.setQueriesData<{ materials: Material[]; total: number }>(
          { queryKey },
          (page) =>
            page && {
              ...page,
              materials: page.materials.map((material) =>
                material.id === saved.id && material.user_id === saved.user_id
                  ? saved
                  : material
              ),
            }
        )
      }
      setPendingDelete(null)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: MATERIALS_QUERY_KEY }),
        queryClient.invalidateQueries({
          queryKey: ['material-library', 'all-materials'],
        }),
      ])
    },
    onError: (error) => handleServerError(error, t('Delete material failed')),
  })

  const retryMutation = useMutation({
    mutationFn: async (material: Material) => {
      const res = props.admin
        ? await retryMaterial(material.id, material.user_id)
        : await retryMaterial(material.id)
      if (!res.success || !res.data) {
        throw createServerError(res, t('Retry preparation failed'))
      }
      return res.data
    },
    retry: false,
    onSuccess: async (saved) => {
      setPendingRetry(null)
      const keys = [MATERIALS_QUERY_KEY, ['material-library', 'all-materials']]
      await Promise.all(
        keys.map((queryKey) => queryClient.cancelQueries({ queryKey }))
      )
      for (const queryKey of keys) {
        queryClient.setQueriesData<{ materials: Material[]; total: number }>(
          { queryKey },
          (page) =>
            page && {
              ...page,
              materials: page.materials.map((material) =>
                material.id === saved.id && material.user_id === saved.user_id
                  ? saved
                  : material
              ),
            }
        )
      }
      await Promise.all(
        keys.map((queryKey) => queryClient.invalidateQueries({ queryKey }))
      )
    },
    onError: (error) => handleServerError(error, t('Retry preparation failed')),
  })

  const materials = materialsQuery.data?.materials ?? EMPTY_MATERIALS
  const detail = materials.find((material) => material.id === detailId) ?? null
  const groupTarget = materials.find(
    (material) => material.id === changingGroup?.id
  )
  const canChangeGroup =
    groupTarget?.status === 'pending' &&
    !groupTarget.deleting &&
    !groupTarget.bindings?.length
  const deleteTarget = materials.find(
    (material) => material.id === pendingDelete?.id
  )
  const canDeleteTarget =
    deleteTarget !== undefined &&
    (!deleteTarget.deleting || deleteTarget.status === 'failed')
  const retryTarget = materials.find(
    (material) => material.id === pendingRetry?.id
  )
  const canRetryTarget =
    retryTarget?.status === 'failed' &&
    retryTarget.can_retry &&
    !retryTarget.deleting

  const columns: ColumnDef<Material, unknown>[] = [
    ...(props.admin
      ? [
          {
            id: 'user',
            header: t('User'),
            cell: (context: CellContext<Material, unknown>) => (
              <TruncatedCell className='max-w-48' tabIndex={0}>
                {context.row.original.username || '—'}
              </TruncatedCell>
            ),
          },
        ]
      : []),
    {
      id: 'preview',
      header: t('Preview'),
      size: 112,
      cell: MaterialPreviewCell,
    },
    {
      id: 'name',
      header: t('Material name'),
      cell: ({ row }) =>
        props.admin ? (
          <TruncatedCell className='max-w-48 font-medium' tabIndex={0}>
            {row.original.name}
          </TruncatedCell>
        ) : (
          <span className='font-medium'>{row.original.name}</span>
        ),
    },
    {
      id: 'group_name',
      header: t('Material group'),
      cell: ({ row }) =>
        props.admin ? (
          <TruncatedCell className='max-w-48' tabIndex={0}>
            {row.original.group_name || t('Ungrouped')}
          </TruncatedCell>
        ) : (
          <span className='block max-w-48 break-all whitespace-normal'>
            {row.original.group_name || t('Ungrouped')}
          </span>
        ),
    },
    {
      id: 'library_type',
      header: t('Library type'),
      cell: ({ row }) => t(MATERIAL_LIBRARY_LABEL[row.original.library_type]),
    },
    {
      id: 'file_type',
      header: t('File type'),
      cell: ({ row }) => t(row.original.file_type),
    },
    {
      id: 'material_id',
      header: t('Platform material ID'),
      cell: ({ row }) => (
        <div className='flex min-w-0 items-center gap-1'>
          <TruncatedCell
            className='text-muted-foreground max-w-48 font-mono text-xs'
            tabIndex={0}
          >
            {row.original.material_id}
          </TruncatedCell>
          <CopyButton value={row.original.material_id} />
        </div>
      ),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: ({ row }) => (
        <div
          className={
            props.admin
              ? 'flex min-w-0 items-center gap-2'
              : 'min-w-0 space-y-1'
          }
        >
          <Badge variant={materialStatusVariant(row.original.status)}>
            {row.original.deleting && row.original.status !== 'failed'
              ? t('Deleting')
              : t(MATERIAL_STATUS_LABEL[row.original.status])}
          </Badge>
          {row.original.status === 'failed' &&
            row.original.fail_reason &&
            (props.admin ? (
              <TruncatedCell
                className='text-destructive max-w-72 text-xs'
                tabIndex={0}
              >
                {row.original.fail_reason}
              </TruncatedCell>
            ) : (
              <p className='text-destructive max-w-72 text-xs break-words whitespace-normal'>
                {row.original.fail_reason}
              </p>
            ))}
        </div>
      ),
    },
    ...(props.admin
      ? [
          {
            id: 'bindings',
            header: t('Channel bindings'),
            cell: (context: CellContext<Material, unknown>) => (
              <TruncatedCell className='max-w-80 text-xs' tabIndex={0}>
                {context.row.original.bindings?.length
                  ? context.row.original.bindings
                      .map(
                        (binding) =>
                          `${binding.channel_name} #${binding.channel_id}`
                      )
                      .join(' · ')
                  : '—'}
              </TruncatedCell>
            ),
          },
        ]
      : []),
    {
      id: 'created_time',
      header: t('Created at'),
      cell: ({ row }) =>
        dayjs.unix(row.original.created_time).format('YYYY-MM-DD HH:mm'),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <div className='flex w-max max-w-full items-center gap-1 overflow-x-auto'>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => setDetailId(row.original.id)}
          >
            {t('Details')}
          </Button>
          {!row.original.deleting &&
            row.original.status === 'pending' &&
            !row.original.bindings?.length && (
              <Button
                variant='ghost'
                size='sm'
                onClick={() => setChangingGroup(row.original)}
              >
                {t('Change group')}
              </Button>
            )}
          {!row.original.deleting &&
            row.original.status === 'failed' &&
            row.original.can_retry && (
              <Button
                variant='ghost'
                size='sm'
                disabled={retryMutation.isPending}
                onClick={() => {
                  retryMutation.reset()
                  setPendingRetry(row.original)
                }}
              >
                {t('Retry preparation')}
              </Button>
            )}
          <Button
            variant='ghost'
            size='sm'
            disabled={
              deleteMutation.isPending ||
              (row.original.deleting && row.original.status !== 'failed')
            }
            onClick={() => {
              deleteMutation.reset()
              setPendingDelete(row.original)
            }}
          >
            {row.original.deleting && row.original.status === 'failed'
              ? t('Retry deletion')
              : t('Delete')}
          </Button>
        </div>
      ),
    },
  ]

  const { table } = useDataTable({
    data: materials,
    columns,
    totalCount: materialsQuery.data?.total ?? 0,
    manualPagination: true,
    columnFilters: [],
    pagination,
    onPaginationChange: setPagination,
    columnVisibilityStorageKey: false,
    columnSizingStorageKey: false,
    ensurePageInRange: (pageCount) => {
      if (
        materialsQuery.isSuccess &&
        !materialsQuery.isFetching &&
        pagination.pageIndex >= Math.max(1, pageCount)
      ) {
        setPagination((previous) => ({
          ...previous,
          pageIndex: Math.max(0, pageCount - 1),
        }))
      }
    },
  })

  const groupFilter = canManageOwner ? (
    <MaterialGroupSelect
      key={targetUserId ?? 'self'}
      targetUserId={targetUserId}
      filter
      value={filters.group_id}
      onChange={(value) => {
        setFilters((previous) => ({
          ...previous,
          group_id: value ?? undefined,
        }))
        setPagination((previous) => ({ ...previous, pageIndex: 0 }))
      }}
    />
  ) : null
  const hasAdvancedFilters = !!(
    filters.source_url ||
    filters.channel_id ||
    filters.upstream_status ||
    filters.group_id !== undefined
  )

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {props.admin ? t('All materials') : t('Material library')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            disabled={materialsQuery.isFetching}
            onClick={() => void materialsQuery.refetch()}
          >
            {t('Refresh')}
          </Button>
          <Button
            variant='outline'
            disabled={!canManageOwner}
            onClick={() => setManageGroupsOpen(true)}
          >
            {t('Manage groups')}
          </Button>
          <Button disabled={!canManageOwner} onClick={() => setAddOpen(true)}>
            {t('Add material')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex h-full min-h-0 flex-col'>
            {props.admin && !owner && (
              <p className='text-muted-foreground mb-3 shrink-0 text-sm'>
                {t('Select a user to add materials and manage groups.')}
              </p>
            )}
            {!props.admin && (
              <p className='text-muted-foreground mb-3 shrink-0 text-sm'>
                {t(
                  'Add an AIGC image using a publicly accessible HTTPS URL. Preparation starts automatically.'
                )}
              </p>
            )}
            <div className='mb-3 shrink-0'>
              <DataTableToolbar
                table={table}
                hideViewOptions
                className='items-end'
                hasAdditionalFilters={
                  !!(filters.keyword || filters.status || hasAdvancedFilters)
                }
                hasExpandedActiveFilters={hasAdvancedFilters}
                onReset={() => {
                  setFilters({ keyword: '', status: '' })
                  setPagination((previous) => ({ ...previous, pageIndex: 0 }))
                }}
                customSearch={
                  <>
                    {props.admin && (
                      <div className='w-full min-w-0 sm:w-72'>
                        <MaterialUserFilter
                          value={owner}
                          onChange={(nextOwner) => {
                            setOwner(nextOwner)
                            setFilters((previous) => ({
                              ...previous,
                              group_id: undefined,
                            }))
                            setPagination((previous) => ({
                              ...previous,
                              pageIndex: 0,
                            }))
                            setDetailId(null)
                            setPendingDelete(null)
                            setPendingRetry(null)
                            setChangingGroup(null)
                            setAddOpen(false)
                            setManageGroupsOpen(false)
                          }}
                        />
                      </div>
                    )}
                    <Input
                      className='w-full sm:w-60'
                      aria-label={t('Search materials')}
                      placeholder={t('Search materials')}
                      value={filters.keyword}
                      onChange={(event) => {
                        setFilters((previous) => ({
                          ...previous,
                          keyword: event.target.value,
                        }))
                        setPagination((previous) => ({
                          ...previous,
                          pageIndex: 0,
                        }))
                      }}
                    />
                  </>
                }
                additionalSearch={
                  <>
                    <div className='w-full min-w-0 sm:w-44'>
                      <MaterialSelect
                        label={t('Status')}
                        value={filters.status || 'all'}
                        onChange={(value) => {
                          setFilters((previous) => ({
                            ...previous,
                            status:
                              value === 'all' ? '' : (value as MaterialStatus),
                          }))
                          setPagination((previous) => ({
                            ...previous,
                            pageIndex: 0,
                          }))
                        }}
                        options={[
                          { value: 'all', label: t('All statuses') },
                          {
                            value: 'pending',
                            label: t('Pending preparation'),
                          },
                          { value: 'processing', label: t('Preparing') },
                          { value: 'ready', label: t('Ready') },
                          { value: 'failed', label: t('Failed') },
                        ]}
                      />
                    </div>
                    {!props.admin && (
                      <div className='w-full min-w-0 sm:w-60'>
                        {groupFilter}
                      </div>
                    )}
                  </>
                }
                expandable={
                  props.admin ? (
                    <div className='order-last grid w-full min-w-0 items-end gap-3 sm:grid-cols-2 lg:grid-cols-4'>
                      <Input
                        aria-label={t('Source URL')}
                        placeholder={t('Source URL')}
                        value={filters.source_url ?? ''}
                        onChange={(event) => {
                          setFilters((previous) => ({
                            ...previous,
                            source_url: event.target.value,
                          }))
                          setPagination((previous) => ({
                            ...previous,
                            pageIndex: 0,
                          }))
                        }}
                      />
                      <Input
                        type='number'
                        min={1}
                        step={1}
                        aria-label={t('Channel ID')}
                        placeholder={t('Channel ID')}
                        value={filters.channel_id ?? ''}
                        onChange={(event) => {
                          const id = Number(event.target.value)
                          setFilters((previous) => ({
                            ...previous,
                            channel_id:
                              Number.isSafeInteger(id) && id > 0
                                ? id
                                : undefined,
                          }))
                          setPagination((previous) => ({
                            ...previous,
                            pageIndex: 0,
                          }))
                        }}
                      />
                      <MaterialSelect
                        label={t('Upstream status')}
                        value={filters.upstream_status || 'all'}
                        onChange={(value) => {
                          setFilters((previous) => ({
                            ...previous,
                            upstream_status:
                              value === 'all'
                                ? ''
                                : (value as MaterialFilters['upstream_status']),
                          }))
                          setPagination((previous) => ({
                            ...previous,
                            pageIndex: 0,
                          }))
                        }}
                        options={[
                          { value: 'all', label: t('All statuses') },
                          ...['ACTIVE', 'PROCESSING', 'FAILED'].map(
                            (value) => ({
                              value,
                              label: value,
                            })
                          ),
                        ]}
                      />
                      {groupFilter}
                    </div>
                  ) : undefined
                }
              />
            </div>
            {materialsQuery.isError ? (
              <ErrorState
                title={t('We could not load materials.')}
                onRetry={() => void materialsQuery.refetch()}
              />
            ) : (
              <DataTablePage
                className='h-auto flex-1'
                table={table}
                columns={columns}
                isLoading={materialsQuery.isLoading}
                isFetching={materialsQuery.isFetching}
                emptyTitle={t('No materials yet')}
                emptyDescription={
                  props.admin
                    ? undefined
                    : t(
                        'Add an AIGC image using a publicly accessible HTTPS URL. Preparation starts automatically.'
                      )
                }
                toolbarProps={null}
              />
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {addOpen && (
        <AddMaterialDialog
          targetUserId={targetUserId}
          open={addOpen}
          onOpenChange={setAddOpen}
        />
      )}
      {manageGroupsOpen && (
        <ManageGroupsDialog
          targetUserId={targetUserId}
          open
          onOpenChange={setManageGroupsOpen}
        />
      )}
      {groupTarget && canChangeGroup && (
        <ChangeMaterialGroupDialog
          key={groupTarget.id}
          material={groupTarget}
          targetUserId={props.admin ? groupTarget.user_id : undefined}
          open
          onOpenChange={(open) => {
            if (!open) setChangingGroup(null)
          }}
        />
      )}
      <MaterialRecordDrawer
        admin={props.admin}
        material={detail}
        open={detail !== null}
        onOpenChange={(open) => {
          if (!open) setDetailId(null)
        }}
        retryPending={retryMutation.isPending}
        onRetry={(material) => {
          if (retryMutation.isPending) return
          retryMutation.reset()
          setPendingRetry(material)
        }}
      />
      <ConfirmDialog
        open={pendingRetry !== null}
        onOpenChange={(open) => {
          if (!open && !retryMutation.isPending) setPendingRetry(null)
        }}
        title={t('Retry preparation')}
        desc={
          <span className='block space-y-2'>
            <span className='block break-words'>{pendingRetry?.name}</span>
            <span className='block'>
              {t(
                'Retry checks an uncertain previous attempt before preparing the material again. Do not add a duplicate material.'
              )}
            </span>
          </span>
        }
        disabled={!canRetryTarget}
        isLoading={retryMutation.isPending}
        confirmText={
          retryMutation.isPending ? t('Retrying...') : t('Retry preparation')
        }
        handleConfirm={() => {
          if (retryTarget && canRetryTarget && !retryMutation.isPending) {
            retryMutation.mutate(retryTarget)
          }
        }}
      >
        {retryMutation.isError && (
          <Alert variant='destructive'>
            <AlertDescription className='break-words whitespace-normal'>
              {getServerErrorMessage(
                retryMutation.error,
                t('Retry preparation failed')
              )}
            </AlertDescription>
          </Alert>
        )}
      </ConfirmDialog>
      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open && !deleteMutation.isPending) setPendingDelete(null)
        }}
        title={t('Delete material')}
        desc={
          <span className='block space-y-2'>
            <span className='block break-all'>
              {pendingDelete?.name} · {pendingDelete?.material_id}
            </span>
            <span className='block'>
              {t(
                'Are you sure you want to delete this material? This cannot be undone.'
              )}
            </span>
          </span>
        }
        destructive
        disabled={!canDeleteTarget}
        isLoading={deleteMutation.isPending}
        confirmText={deleteMutation.isPending ? t('Deleting...') : t('Delete')}
        handleConfirm={() => {
          if (deleteTarget && canDeleteTarget && !deleteMutation.isPending) {
            deleteMutation.mutate(deleteTarget)
          }
        }}
      >
        {deleteMutation.isError && (
          <Alert variant='destructive'>
            <AlertDescription className='break-all whitespace-normal'>
              {getServerErrorMessage(
                deleteMutation.error,
                t('Delete material failed')
              )}
            </AlertDescription>
          </Alert>
        )}
      </ConfirmDialog>
    </>
  )
}
