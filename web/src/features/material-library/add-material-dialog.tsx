import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { handleServerError } from '@/lib/handle-server-error'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'

import { addMaterial } from './api'
import { MaterialGroupSelect } from './material-groups'
import { addMaterialSchema, type AddMaterialFormValues } from './types'

export function AddMaterialDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  targetUserId?: number
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const formId = useId()
  const form = useForm<AddMaterialFormValues>({
    resolver: zodResolver(addMaterialSchema),
    defaultValues: {
      name: '',
      file_type: 'Image',
      source_url: '',
      group_id: null,
    },
  })
  const addMutation = useMutation({
    mutationFn: async (values: AddMaterialFormValues) => {
      const res = await addMaterial(
        { ...values, library_type: 'AIGC' },
        props.targetUserId
      )
      if (!res.success || !res.data) {
        throw createServerError(res, t('Add material failed'))
      }
      return res.data
    },
    retry: false,
    onSuccess: async () => {
      toast.success(t('Synchronization queued'))
      props.onOpenChange(false)
      await Promise.all([
        queryClient.cancelQueries({
          queryKey: ['material-library', 'materials'],
        }),
        queryClient.cancelQueries({
          queryKey: ['material-library', 'all-materials'],
        }),
      ])
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ['material-library', 'materials'],
        }),
        queryClient.invalidateQueries({
          queryKey: ['material-library', 'all-materials'],
        }),
      ])
    },
    onError: (error) => handleServerError(error, t('Add material failed')),
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!addMutation.isPending) props.onOpenChange(open)
      }}
      title={t('Add material')}
      description={t(
        'Add an AIGC image using a publicly accessible HTTPS URL. Preparation starts automatically.'
      )}
      footer={
        <>
          <Button
            variant='outline'
            disabled={addMutation.isPending}
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={formId} disabled={addMutation.isPending}>
            {addMutation.isPending ? t('Submitting...') : t('Submit material')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id={formId}
          noValidate
          className='space-y-4'
          onSubmit={form.handleSubmit((values) => {
            if (!addMutation.isPending) addMutation.mutate(values)
          })}
        >
          <fieldset disabled={addMutation.isPending} className='space-y-4'>
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Material name')}</FormLabel>
                  <FormControl>
                    <Input {...field} autoComplete='off' />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <MaterialGroupSelect
              value={form.watch('group_id')}
              targetUserId={props.targetUserId}
              onChange={(value) =>
                form.setValue('group_id', value ?? null, {
                  shouldDirty: true,
                })
              }
            />
            <FormField
              control={form.control}
              name='source_url'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Material URL')}</FormLabel>
                  <FormControl>
                    <Input {...field} type='url' autoComplete='off' />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </fieldset>
          {addMutation.isError && (
            <Alert variant='destructive'>
              <AlertDescription className='break-words whitespace-normal'>
                {getServerErrorMessage(
                  addMutation.error,
                  t('Add material failed')
                )}
              </AlertDescription>
            </Alert>
          )}
        </form>
      </Form>
    </Dialog>
  )
}
