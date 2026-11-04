import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { PasswordInput } from '@/components/password-input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { getTopenRouterConfig, saveTopenRouterConfig } from './api'
import {
  TOPENROUTER_DEFAULT_ENDPOINT,
  topenRouterConfigSchema,
  type TopenRouterConfigFormValues,
} from './lib/topenrouter-config-schema'
import type { TopenRouterConfig } from './types'

const CONFIG_QUERY_KEY = ['material-library', 'topenrouter-config']

export function TopenRouterConfigSettings() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: CONFIG_QUERY_KEY,
    queryFn: async () =>
      requireServerSuccess(await getTopenRouterConfig()).data,
    retry: false,
    meta: { errorToast: false },
  })

  if (query.isPending) return <Skeleton className='h-72 w-full' />
  return (
    <>
      {query.isError && (
        <ErrorState
          title={t('Oops! Something went wrong')}
          description={query.error.message}
          onRetry={() => void query.refetch()}
        />
      )}
      {query.data && <TopenRouterConfigForm config={query.data} />}
    </>
  )
}

function TopenRouterConfigForm(props: { config: TopenRouterConfig }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const form = useForm<TopenRouterConfigFormValues>({
    resolver: zodResolver(topenRouterConfigSchema(props.config)),
    defaultValues: {
      enabled: props.config.enabled,
      channel_id: props.config.channel_id,
      endpoint: props.config.endpoint || TOPENROUTER_DEFAULT_ENDPOINT,
      token: '',
    },
  })

  useEffect(() => {
    // Refetches refresh saved metadata, never unsaved edits or tokens.
    if (!form.getFieldState('enabled').isDirty) {
      form.resetField('enabled', { defaultValue: props.config.enabled })
    }
    if (!form.getFieldState('channel_id').isDirty) {
      form.resetField('channel_id', { defaultValue: props.config.channel_id })
    }
    if (!form.getFieldState('endpoint').isDirty) {
      form.resetField('endpoint', {
        defaultValue: props.config.endpoint || TOPENROUTER_DEFAULT_ENDPOINT,
      })
    }
  }, [
    props.config.enabled,
    props.config.channel_id,
    props.config.endpoint,
    form,
  ])

  const mutation = useMutation({
    // Read the token from RHF only; React Query must not retain it as variables.
    mutationFn: async () => {
      try {
        return requireServerSuccess(
          await saveTopenRouterConfig(
            topenRouterConfigSchema(props.config).parse(form.getValues())
          )
        ).data
      } catch (error) {
        // Drop Axios config.data and error causes before reaching caches or UI.
        throw new Error(
          getServerErrorMessage(error, t('Save configuration failed'))
        )
      }
    },
    retry: false,
    gcTime: 0,
    onSuccess: (config) => {
      form.reset({
        enabled: config.enabled,
        channel_id: config.channel_id,
        endpoint: config.endpoint || TOPENROUTER_DEFAULT_ENDPOINT,
        token: '',
      })
      queryClient.setQueryData(CONFIG_QUERY_KEY, config)
      toast.success(t('Configuration saved'))
      void queryClient.invalidateQueries({ queryKey: CONFIG_QUERY_KEY })
    },
    onError: (error) =>
      handleServerError(error, t('Save configuration failed')),
    onSettled: (): void => mutation.reset(),
  })
  const isSaving = mutation.isPending || form.formState.isSubmitting
  const disabled = isSaving || !props.config.available

  return (
    <Card>
      <CardHeader>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <CardTitle>{t('Integration configuration')}</CardTitle>
          <Badge variant={props.config.configured ? 'secondary' : 'outline'}>
            {props.config.configured
              ? t('Configuration saved')
              : t('Not configured')}
          </Badge>
        </div>
        <CardDescription>
          {t(
            'Saving stores configuration only; it does not test connectivity or verify capabilities.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'TopenRouter supports AIGC images only; real-person verification is not required.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t('Select a channel to complete this configuration.')}
        </p>
        {!props.config.available && (
          <Alert>
            <AlertDescription>
              {t(
                'TopenRouter material integration is unavailable. Enable it on the backend, then reload this page.'
              )}
            </AlertDescription>
          </Alert>
        )}
        <Form {...form}>
          <form
            noValidate
            aria-busy={isSaving}
            className='space-y-4'
            onSubmit={(event) => {
              if (disabled) {
                event.preventDefault()
                return
              }
              void form.handleSubmit(async () => {
                await mutation.mutateAsync().catch(() => undefined)
              })(event)
            }}
          >
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between gap-4'>
                  <div className='space-y-2'>
                    <div className='flex flex-wrap items-center gap-2'>
                      <FormLabel>
                        {t('Automatic material preparation')}
                      </FormLabel>
                      <Badge variant={field.value ? 'secondary' : 'outline'}>
                        {field.value ? t('Enabled') : t('Disabled')}
                      </Badge>
                    </div>
                    <FormDescription>
                      {t(
                        'Only controls automatic material preparation for video requests. When disabled, original parameters pass through unchanged. Manual material library operations are unaffected.'
                      )}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      ref={field.ref}
                      name={field.name}
                      checked={field.value}
                      onCheckedChange={field.onChange}
                      onBlur={field.onBlur}
                      disabled={disabled}
                    />
                  </FormControl>
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='channel_id'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Channel ID')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='number'
                      min={1}
                      step={1}
                      value={field.value || ''}
                      disabled={disabled}
                      onChange={(event) =>
                        field.onChange(Number(event.target.value))
                      }
                    />
                  </FormControl>
                  {props.config.channel_id > 0 && (
                    <FormDescription className='break-all whitespace-normal'>
                      {props.config.channel_name} #
                      {formatNumber(props.config.channel_id, locale)}
                    </FormDescription>
                  )}
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='endpoint'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('API endpoint')}</FormLabel>
                  <FormControl>
                    <Input {...field} type='url' disabled={disabled} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='token'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Token')}</FormLabel>
                  <FormControl>
                    <PasswordInput
                      {...field}
                      disabled={disabled}
                      autoComplete='new-password'
                      spellCheck={false}
                    />
                  </FormControl>
                  {props.config.token_configured && (
                    <FormDescription>
                      {t('Leave blank to keep the existing credential')}
                    </FormDescription>
                  )}
                  <FormMessage />
                </FormItem>
              )}
            />
            <Button type='submit' disabled={disabled}>
              {isSaving ? t('Saving...') : t('Save configuration')}
            </Button>
          </form>
        </Form>
      </CardContent>
    </Card>
  )
}
