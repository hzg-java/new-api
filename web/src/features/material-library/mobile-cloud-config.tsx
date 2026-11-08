import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { PasswordInput } from '@/components/password-input'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
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
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { getMobileCloudConfig, saveMobileCloudConfig } from './api'
import {
  mobileCloudConfigSchema,
  type MobileCloudConfigFormValues,
} from './lib/mobile-cloud-config-schema'
import { MaterialIntegrationTest } from './material-integration-test'
import type { MobileCloudConfig } from './types'

const CONFIG_QUERY_KEY = ['material-library', 'mobile-cloud-config']

export function MobileCloudConfigSettings(props: {
  onTestingChange?: (testing: boolean) => void
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: CONFIG_QUERY_KEY,
    queryFn: async () =>
      requireServerSuccess(await getMobileCloudConfig()).data,
    retry: false,
    meta: { errorToast: false },
  })

  if (query.isPending) return <Skeleton className='h-72 w-full' />
  return (
    <>
      {query.isError && (
        <ErrorState
          title={t('We could not load the Mobile Cloud configuration.')}
          description={query.error.message}
          onRetry={() => void query.refetch()}
        />
      )}
      {query.data && (
        <MobileCloudConfigForm
          config={query.data}
          onTestingChange={props.onTestingChange}
        />
      )}
    </>
  )
}

function MobileCloudConfigForm(props: {
  config: MobileCloudConfig
  onTestingChange?: (testing: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [isTesting, setIsTesting] = useState(false)
  const [draftRevision, setDraftRevision] = useState(0)
  const testConfiguration = useMemo(
    () => ({ saved: props.config, draftRevision }),
    [props.config, draftRevision]
  )
  const form = useForm<MobileCloudConfigFormValues>({
    resolver: zodResolver(mobileCloudConfigSchema(props.config)),
    defaultValues: {
      enabled: props.config.enabled,
      channel_id: props.config.channel_id,
      endpoint: props.config.endpoint,
      pool_id: props.config.pool_id,
      access_key: '',
      secret_key: '',
    },
  })

  useEffect(() => {
    // Background refreshes may update saved metadata, never unrelated edits or credentials.
    for (const name of [
      'enabled',
      'endpoint',
      'pool_id',
      'channel_id',
    ] as const) {
      if (!form.getFieldState(name).isDirty) {
        form.resetField(name, { defaultValue: props.config[name] })
      }
    }
  }, [props.config, form])

  const mutation = useMutation({
    // Credentials stay in RHF only, not React Query's retained mutation variables.
    mutationFn: async () => {
      try {
        return requireServerSuccess(
          await saveMobileCloudConfig(
            mobileCloudConfigSchema(props.config).parse(form.getValues())
          )
        ).data
      } catch (error) {
        // Axios errors can retain the plaintext request body in config.data.
        throw new Error(
          getServerErrorMessage(error, t('Save configuration failed'))
        )
      }
    },
    retry: false,
    gcTime: 0,
    onSuccess: (config) => {
      // Clear secrets before any asynchronous refetch, including failed refetches.
      form.reset({
        enabled: config.enabled,
        channel_id: config.channel_id,
        endpoint: config.endpoint,
        pool_id: config.pool_id,
        access_key: '',
        secret_key: '',
      })
      // Credential-only saves can leave all server metadata unchanged.
      setDraftRevision((revision) => revision + 1)
      queryClient.setQueryData(CONFIG_QUERY_KEY, config)
      toast.success(t('Configuration saved'))
      void queryClient.invalidateQueries({ queryKey: CONFIG_QUERY_KEY })
    },
    onError: (error) =>
      handleServerError(error, t('Save configuration failed')),
    onSettled: (): void => mutation.reset(),
  })
  const isSaving = mutation.isPending || form.formState.isSubmitting
  const encryptionUnavailable =
    props.config.configured && !props.config.encryption_ready
  const disabled = isSaving || isTesting || !props.config.available

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
        {!props.config.available && (
          <Alert>
            <AlertDescription>
              {t(
                'Mobile Cloud material integration is unavailable. Check the backend service, then reload this page.'
              )}
            </AlertDescription>
          </Alert>
        )}
        {props.config.available && !props.config.encryption_ready && (
          <Alert>
            <AlertDescription>
              {encryptionUnavailable
                ? t(
                    'The saved credentials cannot be decrypted. Restore the backend encryption key.'
                  )
                : t(
                    'Credential encryption is set up automatically when you save. No manual key setup is needed.'
                  )}
            </AlertDescription>
          </Alert>
        )}
        {props.config.channel_id === 0 && (
          <Alert>
            <AlertDescription>
              {t('Select a channel to complete this configuration.')}
            </AlertDescription>
          </Alert>
        )}
        <Form {...form}>
          <form
            noValidate
            aria-busy={isSaving || isTesting}
            className='space-y-4'
            onChange={() => setDraftRevision((revision) => revision + 1)}
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
                      onCheckedChange={(checked) => {
                        field.onChange(checked)
                        setDraftRevision((revision) => revision + 1)
                      }}
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
                      {props.config.channel_name} #{props.config.channel_id}
                    </FormDescription>
                  )}
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='access_key'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Access key (AK)')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      disabled={disabled}
                      autoComplete='off'
                      spellCheck={false}
                      placeholder={props.config.access_key_masked || undefined}
                    />
                  </FormControl>
                  <FormDescription>
                    {props.config.access_key_masked
                      ? t(
                          'Leave blank to keep the saved access key. Enter a new key to replace it.'
                        )
                      : t('Enter the access key for initial setup.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='secret_key'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Secret key (SK)')}</FormLabel>
                  <FormControl>
                    <PasswordInput
                      {...field}
                      disabled={disabled}
                      autoComplete='new-password'
                      spellCheck={false}
                    />
                  </FormControl>
                  <FormDescription>
                    {props.config.secret_key_configured
                      ? t(
                          'Leave blank to keep the saved secret key. Enter a new key to replace it.'
                        )
                      : t('Enter the secret key for initial setup.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='pool_id'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Pool-Id')}</FormLabel>
                  <FormControl>
                    <Input {...field} disabled={disabled} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <Accordion>
              <AccordionItem value='advanced'>
                <AccordionTrigger>{t('Advanced settings')}</AccordionTrigger>
                <AccordionContent keepMounted>
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
                </AccordionContent>
              </AccordionItem>
            </Accordion>
            <Button type='submit' disabled={disabled}>
              {isSaving ? t('Saving...') : t('Save configuration')}
            </Button>
          </form>
        </Form>
        <MaterialIntegrationTest
          provider='mobile_cloud'
          ready={
            props.config.configured &&
            props.config.available &&
            props.config.encryption_ready &&
            props.config.channel_id > 0
          }
          disabled={isSaving}
          testing={isTesting}
          configuration={testConfiguration}
          onTestingChange={(testing) => {
            setIsTesting(testing)
            props.onTestingChange?.(testing)
          }}
        />
      </CardContent>
    </Card>
  )
}
