import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'

import {
  createMaterialGroup,
  renameMaterialGroup,
  deleteMaterialGroup,
  retryMaterialGroup,
} from './api'
import { BindingDetails } from './binding-details'
import { MATERIAL_STATUS_LABEL } from './constants'
import {
  materialGroupSchema,
  type Material,
  type MaterialGroup,
  type MaterialGroupFormValues,
} from './types'
import {
  getMaterialGroupsQueryKey,
  invalidateMaterialLists,
  useMaterialGroups,
} from './use-material-groups'

export function ManageGroupsDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  targetUserId?: number
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const groupsQueryKey = getMaterialGroupsQueryKey(props.targetUserId)
  const query = useMaterialGroups(props.targetUserId)
  const formId = useId()
  const [editing, setEditing] = useState<MaterialGroup | null>(null)
  const form = useForm<MaterialGroupFormValues>({
    resolver: zodResolver(materialGroupSchema),
    defaultValues: { name: '' },
  })
  const [pendingDelete, setPendingDelete] = useState<MaterialGroup | null>(null)
  const mutation = useMutation({
    mutationFn: async (
      action:
        | { type: 'save'; values: MaterialGroupFormValues }
        | { type: 'delete'; group: MaterialGroup }
        | { type: 'retry'; group: MaterialGroup }
    ) => {
      let res
      if (action.type === 'delete') {
        res = await deleteMaterialGroup(action.group.id, props.targetUserId)
      } else if (action.type === 'retry') {
        res = await retryMaterialGroup(action.group.id, props.targetUserId)
      } else {
        res = editing
          ? await renameMaterialGroup(
              editing.id,
              action.values,
              props.targetUserId
            )
          : await createMaterialGroup(action.values, props.targetUserId)
      }
      if (!res.success || !res.data) {
        throw createServerError(res, t('Save material group failed'))
      }
      return res.data
    },
    onSuccess: async (saved) => {
      // Keep the saved name if subsequent reads fail or older reads finish late.
      await Promise.all([
        client.cancelQueries({ queryKey: groupsQueryKey, exact: true }),
        client.cancelQueries({ queryKey: ['material-library', 'materials'] }),
        client.cancelQueries({
          queryKey: ['material-library', 'all-materials'],
        }),
      ])
      client.setQueryData<MaterialGroup[]>(groupsQueryKey, (groups) => {
        if (!groups) return groups
        if (!groups.some((group) => group.id === saved.id)) {
          return [...groups, saved]
        }
        return groups.map((group) => (group.id === saved.id ? saved : group))
      })
      for (const scope of ['materials', 'all-materials']) {
        client.setQueriesData<{ materials: Material[]; total: number }>(
          { queryKey: ['material-library', scope] },
          (page) =>
            page && {
              ...page,
              materials: page.materials.map((material) =>
                material.group_id === saved.id &&
                material.user_id === saved.user_id
                  ? { ...material, group_name: saved.name }
                  : material
              ),
            }
        )
      }
      toast.success(
        saved.deleting ? t('Deletion queued') : t('Synchronization queued')
      )
      setPendingDelete(null)
      setEditing(null)
      form.reset({ name: '' })
      await Promise.all([
        client.invalidateQueries({ queryKey: groupsQueryKey, exact: true }),
        invalidateMaterialLists(client),
      ])
    },
    onError: (error, action) => {
      if (action.type === 'save') {
        form.setError('name', {
          message: getServerErrorMessage(
            error,
            t('Save material group failed')
          ),
        })
      }
    },
  })
  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!mutation.isPending) props.onOpenChange(open)
      }}
      title={t('Manage material groups')}
      description={t(
        'Group changes sync to upstream channels. Only empty groups can be deleted.'
      )}
      footer={
        <>
          <Button
            variant='outline'
            disabled={mutation.isPending}
            onClick={() => props.onOpenChange(false)}
          >
            {t('Close')}
          </Button>
          <Button type='submit' form={formId} disabled={mutation.isPending}>
            {mutation.isPending ? t('Saving...') : null}
            {!mutation.isPending &&
              (editing ? t('Save group') : t('Create group'))}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        {mutation.isError && mutation.variables?.type === 'retry' && (
          <p
            role='alert'
            className='text-destructive break-all whitespace-normal'
          >
            {getServerErrorMessage(
              mutation.error,
              t('Save material group failed')
            )}
          </p>
        )}
        <ConfirmDialog
          open={pendingDelete !== null}
          onOpenChange={(open) => {
            if (!open && !mutation.isPending) setPendingDelete(null)
          }}
          title={t('Delete material group')}
          desc={t(
            'Only empty groups can be deleted. Deletion is queued for upstream channels.'
          )}
          destructive
          confirmText={mutation.isPending ? t('Deleting...') : t('Delete')}
          isLoading={mutation.isPending}
          handleConfirm={() => {
            if (pendingDelete && !mutation.isPending) {
              mutation.mutate({ type: 'delete', group: pendingDelete })
            }
          }}
        >
          {mutation.isError && (
            <p
              role='alert'
              className='text-destructive break-all whitespace-normal'
            >
              {getServerErrorMessage(
                mutation.error,
                t('Save material group failed')
              )}
            </p>
          )}
        </ConfirmDialog>
        <Form {...form}>
          <form
            id={formId}
            noValidate
            onSubmit={form.handleSubmit((values) => {
              if (!mutation.isPending) mutation.mutate({ type: 'save', values })
            })}
          >
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Group name')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      autoComplete='off'
                      disabled={mutation.isPending}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            {editing && (
              <Button
                className='mt-2'
                variant='ghost'
                disabled={mutation.isPending}
                onClick={() => {
                  setEditing(null)
                  form.reset({ name: '' })
                }}
              >
                {t('Cancel rename')}
              </Button>
            )}
          </form>
        </Form>
        {query.isPending && (
          <LoadingState inline message={t('Loading material groups...')} />
        )}
        {query.isError && (
          <ErrorState
            className='min-h-0 p-3'
            title={t('We could not load material groups.')}
            description={getServerErrorMessage(
              query.error,
              t('We could not load material groups.')
            )}
            onRetry={() => void query.refetch()}
          />
        )}
        {query.isSuccess && query.data.length === 0 && (
          <EmptyState
            className='min-h-0 p-3'
            title={t('No material groups yet')}
          />
        )}
        {query.isSuccess && (
          <ul className='divide-y'>
            {query.data.map((group) => (
              <li key={group.id} className='min-w-0 space-y-2 py-3'>
                <p className='min-w-0 break-all whitespace-normal'>
                  {group.name}
                </p>
                {group.status && (
                  <p className='text-sm'>
                    {group.deleting && group.status !== 'failed'
                      ? t('Deleting')
                      : t(MATERIAL_STATUS_LABEL[group.status])}
                  </p>
                )}
                {group.fail_reason && (
                  <p className='text-destructive text-sm break-all whitespace-normal'>
                    {group.fail_reason}
                  </p>
                )}
                {group.bindings?.map((binding) => (
                  <BindingDetails key={binding.id} binding={binding} />
                ))}
                <div className='flex flex-wrap gap-1'>
                  <Button
                    variant='ghost'
                    size='sm'
                    className='shrink-0'
                    disabled={
                      mutation.isPending ||
                      group.deleting ||
                      group.status === 'pending' ||
                      group.status === 'processing'
                    }
                    aria-label={t('Rename group: {{name}}', {
                      name: group.name,
                    })}
                    onClick={() => {
                      setEditing(group)
                      form.reset({ name: group.name })
                    }}
                  >
                    {t('Rename')}
                  </Button>
                  {group.status === 'failed' && !group.deleting && (
                    <Button
                      variant='ghost'
                      size='sm'
                      disabled={mutation.isPending}
                      onClick={() => {
                        mutation.reset()
                        mutation.mutate({ type: 'retry', group })
                      }}
                    >
                      {t('Retry synchronization')}
                    </Button>
                  )}
                  <Button
                    variant='ghost'
                    size='sm'
                    disabled={
                      mutation.isPending ||
                      (group.deleting && group.status !== 'failed')
                    }
                    aria-label={t('Delete group: {{name}}', {
                      name: group.name,
                    })}
                    onClick={() => {
                      mutation.reset()
                      setPendingDelete(group)
                    }}
                  >
                    {group.deleting && group.status === 'failed'
                      ? t('Retry deletion')
                      : t('Delete')}
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Dialog>
  )
}
