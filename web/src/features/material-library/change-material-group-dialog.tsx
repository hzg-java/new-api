import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'

import { updateMaterialGroup } from './api'
import { MaterialGroupSelect } from './material-groups'
import type { Material } from './types'
import {
  invalidateMaterialLists,
  useMaterialGroups,
} from './use-material-groups'

export function ChangeMaterialGroupDialog(props: {
  material: Material
  open: boolean
  onOpenChange: (open: boolean) => void
  targetUserId?: number
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const formId = useId()
  const [groupId, setGroupId] = useState(props.material.group_id)
  const groups = useMaterialGroups(props.targetUserId)
  const mutation = useMutation({
    mutationFn: async () => {
      const res = await updateMaterialGroup(
        props.material.id,
        { group_id: groupId },
        props.targetUserId
      )
      if (!res.success || !res.data) {
        throw createServerError(res, t('Change material group failed'))
      }
      return res.data
    },
    onSuccess: async () => {
      toast.success(t('Material group updated'))
      props.onOpenChange(false)
      await invalidateMaterialLists(client)
    },
    onError: (error) =>
      handleServerError(error, t('Change material group failed')),
  })
  const canSave = groups.isSuccess && !mutation.isPending
  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!mutation.isPending) props.onOpenChange(open)
      }}
      title={t('Change material group')}
      description={props.material.name}
      footer={
        <>
          <Button
            variant='outline'
            disabled={mutation.isPending}
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={formId} disabled={!canSave}>
            {mutation.isPending ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <form
        id={formId}
        className='space-y-4'
        onSubmit={(event) => {
          event.preventDefault()
          if (canSave) mutation.mutate()
        }}
      >
        <p className='text-muted-foreground text-sm break-all'>
          {t('Current group')}: {props.material.group_name || t('Ungrouped')}
        </p>
        <fieldset disabled={mutation.isPending} className='min-w-0'>
          <MaterialGroupSelect
            value={groupId}
            targetUserId={props.targetUserId}
            onChange={(value) => setGroupId(value ?? null)}
          />
        </fieldset>
        {mutation.isError && (
          <p role='alert' className='text-destructive text-sm break-words'>
            {getServerErrorMessage(
              mutation.error,
              t('Change material group failed')
            )}
          </p>
        )}
      </form>
    </Dialog>
  )
}
