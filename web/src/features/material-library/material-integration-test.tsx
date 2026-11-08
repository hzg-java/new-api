import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import { Label } from '@/components/ui/label'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { testMaterialIntegration } from './api'
import { MATERIAL_TEST_OPERATION_LABEL } from './constants'
import {
  MATERIAL_TEST_OPERATIONS,
  materialTestDefaults,
  materialTestFields,
  materialTestParameters,
  materialTestSchema,
  type MaterialTestFormValues,
} from './lib/material-test-schema'
import { MaterialSelect } from './material-select'
import { MaterialTestResultView } from './material-test-result'
import type {
  MaterialIntegrationProvider,
  MaterialTestOperation,
} from './types'

const assetTypes = ['Image', 'Video', 'Audio'].map((value) => ({
  value,
  label: value,
}))

type MaterialIntegrationTestProps = {
  provider: MaterialIntegrationProvider
  ready: boolean
  disabled: boolean
  testing: boolean
  configuration: object
  onTestingChange: (testing: boolean) => void
}

export function MaterialIntegrationTest(props: MaterialIntegrationTestProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const operationOptions = useMemo(
    () =>
      MATERIAL_TEST_OPERATIONS.map((value) => ({
        value,
        label: `${t(MATERIAL_TEST_OPERATION_LABEL[value])}（${value}）`,
      })),
    [t]
  )
  const [operation, setOperation] =
    useState<MaterialTestOperation>('list_groups')
  const [confirming, setConfirming] = useState(false)
  const [clearDescription, setClearDescription] = useState(false)
  const fields = useMemo(
    () => materialTestFields(props.provider, operation),
    [props.provider, operation]
  )
  const form = useForm<MaterialTestFormValues>({
    resolver: zodResolver(materialTestSchema(fields)),
    defaultValues: materialTestDefaults(fields),
  })
  const inFlight = useRef(false)
  const configuration = props.configuration
  const latestConfiguration = useRef(configuration)
  const requestedConfiguration = useRef(configuration)
  latestConfiguration.current = configuration
  const deleting = operation === 'delete_group' || operation === 'delete_asset'
  const writing =
    !operation.startsWith('list_') && !operation.startsWith('get_')

  const mutation = useMutation({
    // Parameters remain only in the transient form, never mutation variables or storage.
    mutationFn: async () => {
      try {
        const response = requireServerSuccess(
          await testMaterialIntegration(props.provider, {
            operation,
            parameters: {
              ...materialTestParameters(fields, form.getValues()),
              ...(operation === 'update_group' && clearDescription
                ? { description: '' }
                : {}),
            },
            ...(deleting ? { confirmed: true } : {}),
          })
        )
        if (!response.data) throw new Error(t('Material API test failed'))
        return { result: response.data, configuration }
      } catch (error) {
        // Drop Axios config, request body and causes before retaining any error.
        throw new Error(
          getServerErrorMessage(error, t('Material API test failed'))
        )
      }
    },
    retry: false,
    // Do not queue an offline action for automatic execution on reconnection.
    networkMode: 'always',
    gcTime: 0,
    onError: (error) => {
      if (requestedConfiguration.current === latestConfiguration.current) {
        handleServerError(error, t('Material API test failed'))
      }
    },
    onSettled: () => {
      inFlight.current = false
      props.onTestingChange(false)
      if (requestedConfiguration.current !== latestConfiguration.current) {
        mutation.reset()
      }
    },
  })
  const reset = mutation.reset
  useEffect(() => {
    reset()
    setConfirming(false)
  }, [configuration, reset])
  useEffect(() => {
    form.reset(materialTestDefaults(fields))
  }, [fields, form])

  const disabled =
    !props.ready || props.disabled || props.testing || mutation.isPending

  function runTest(): void {
    if (disabled || inFlight.current) return
    inFlight.current = true
    requestedConfiguration.current = configuration
    props.onTestingChange(true)
    setConfirming(false)
    mutation.reset()
    mutation.mutate()
  }

  function clearResult(): void {
    mutation.reset()
    setConfirming(false)
  }

  const result =
    mutation.data?.configuration === configuration
      ? mutation.data.result
      : undefined
  const isError =
    mutation.isError && requestedConfiguration.current === configuration

  return (
    <section
      className='min-w-0 space-y-3 border-t pt-4'
      aria-label={t('Material API test')}
    >
      <h3 className='text-sm font-medium'>{t('Material API test')}</h3>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Tests use the saved configuration. Unsaved changes are not included.'
        )}
      </p>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Fill in parameters and execute one operation at a time. Parameters are not saved; requests are never retried automatically.'
        )}
      </p>
      {!props.ready && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Save a usable configuration with a valid channel before testing.'
          )}
        </p>
      )}
      <MaterialSelect
        label={t('API operation')}
        value={operation}
        options={operationOptions}
        disabled={disabled}
        onChange={(value) => {
          if (disabled || inFlight.current) return
          clearResult()
          setClearDescription(false)
          setOperation(value as MaterialTestOperation)
        }}
      />
      {writing && (
        <Alert>
          <AlertDescription>
            {t(
              'This operation changes real upstream data using the saved channel credentials.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {operation === 'create_group' && (
        <p className='text-muted-foreground text-sm'>
          {t('New groups use AIGC.')}
        </p>
      )}
      {operation === 'create_asset' && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Import from a publicly accessible HTTP or HTTPS URL. Local file upload is not supported.'
          )}
        </p>
      )}
      <Form {...form}>
        <form
          noValidate
          autoComplete='off'
          className='space-y-3'
          aria-label={t('Request parameters')}
          aria-busy={props.testing || mutation.isPending}
          onChange={clearResult}
          onSubmit={(event) => {
            event.preventDefault()
            if (disabled || inFlight.current) return
            void form.handleSubmit(() => {
              if (deleting) setConfirming(true)
              else runTest()
            })(event)
          }}
        >
          <div className='grid min-w-0 gap-3 sm:grid-cols-2'>
            {fields.map((item) => (
              <FormField
                key={item.name}
                control={form.control}
                name={item.name}
                render={({ field }) => (
                  <FormItem className='min-w-0'>
                    {item.name !== 'assetType' && (
                      <FormLabel>
                        {item.name}
                        {item.optional ? ` (${t('optional')})` : ''}
                      </FormLabel>
                    )}
                    {item.name === 'assetType' ? (
                      <MaterialSelect
                        label='assetType'
                        value={field.value ?? 'Image'}
                        options={assetTypes}
                        disabled={disabled}
                        onChange={(value) => {
                          field.onChange(value)
                          clearResult()
                        }}
                      />
                    ) : (
                      <FormControl>
                        <Input
                          {...field}
                          value={field.value ?? ''}
                          type={item.type ?? 'text'}
                          disabled={
                            disabled ||
                            (item.name === 'description' && clearDescription)
                          }
                          readOnly={item.name === 'groupType'}
                          min={item.type === 'number' ? 1 : undefined}
                          step={item.type === 'number' ? 1 : undefined}
                          max={item.max}
                        />
                      </FormControl>
                    )}
                    {item.name === 'groupIds' && (
                      <FormDescription>
                        {t('Comma-separated IDs; leave blank to omit.')}
                      </FormDescription>
                    )}
                    {item.name === 'statuses' && (
                      <FormDescription>
                        {t(
                          'Comma-separated statuses: ACTIVE, PROCESSING, FAILED.'
                        )}
                      </FormDescription>
                    )}
                    {item.type === 'number' && (
                      <FormDescription>
                        {item.max
                          ? t('Positive integer, at most {{max}}.', {
                              max: formatNumber(item.max, locale),
                            })
                          : t('Positive integer.')}
                      </FormDescription>
                    )}
                    <FormMessage />
                  </FormItem>
                )}
              />
            ))}
          </div>
          {operation === 'update_group' && (
            <Label className='flex items-center gap-2'>
              <Checkbox
                checked={clearDescription}
                disabled={disabled}
                onCheckedChange={(checked) => {
                  setClearDescription(checked)
                  clearResult()
                }}
              />
              {t('Clear description')}
            </Label>
          )}
          <Button type='submit' disabled={disabled}>
            {t('Execute')}
          </Button>
        </form>
      </Form>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('Confirm upstream deletion')}
        desc={
          <div className='space-y-2'>
            <p>
              {t(
                'This permanently deletes the upstream resource. Verify the ID before continuing.'
              )}
            </p>
            <code className='block break-all'>
              {form.getValues(
                deleting && operation === 'delete_group' ? 'groupId' : 'assetId'
              )}
            </code>
          </div>
        }
        destructive
        confirmText={t('Delete')}
        disabled={disabled}
        handleConfirm={runTest}
      />
      <MaterialTestResultView
        result={result}
        error={isError ? mutation.error : null}
        testing={props.testing}
      />
    </section>
  )
}
