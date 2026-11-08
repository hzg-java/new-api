import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { CopyableMaterialValue } from './binding-details'
import { MATERIAL_TEST_OPERATION_LABEL } from './constants'
import type { MaterialTestResult } from './types'

function materialResponseIds(
  value: unknown,
  path = ''
): { path: string; label: string; value: string }[] {
  if (!value || typeof value !== 'object') return []
  return Object.entries(value).flatMap(([key, item]) => {
    const itemPath = path ? `${path}.${key}` : key
    if (
      /^(id|group_?id|asset_?id|upstream_?id)$/i.test(key) &&
      typeof item === 'string' &&
      item
    ) {
      return [{ path: itemPath, label: key, value: item }]
    }
    return materialResponseIds(item, itemPath)
  })
}

export function MaterialTestResultView(props: {
  result?: MaterialTestResult
  error: Error | null
  testing: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const result = props.result
  const failed = props.error !== null || result?.success === false
  // Only the backend's safe projection is displayed or copied, never the request or Axios response.
  const responseJson =
    result?.response !== undefined
      ? JSON.stringify(result.response, null, 2)
      : undefined
  const ids = materialResponseIds(result?.response)

  return (
    <>
      <div role='status' aria-live='polite' aria-busy={props.testing}>
        {props.testing && (
          <p className='text-muted-foreground text-sm'>
            {t('Testing material API...')}
          </p>
        )}
        {(result || props.error) && (
          <Alert
            role='presentation'
            variant={failed ? 'destructive' : 'default'}
          >
            <AlertTitle>
              {failed
                ? t('Material API test failed')
                : t('Material API test succeeded')}
            </AlertTitle>
            <AlertDescription className='min-w-0 space-y-1 break-words whitespace-normal'>
              {result && (
                <>
                  <p>
                    {t(MATERIAL_TEST_OPERATION_LABEL[result.operation])}（
                    {result.operation}）
                  </p>
                  <p>
                    {t('Elapsed: {{duration}} ms', {
                      duration: formatNumber(result.elapsed_ms, locale),
                    })}
                  </p>
                  {result.total !== undefined && (
                    <p>
                      {t('Total: {{total}}', {
                        total: formatNumber(result.total, locale),
                      })}
                    </p>
                  )}
                  {result.message && (
                    <p>{getServerErrorMessage({ message: result.message })}</p>
                  )}
                </>
              )}
              {props.error && (
                <p>
                  {getServerErrorMessage(
                    props.error,
                    t('Material API test failed')
                  )}
                </p>
              )}
            </AlertDescription>
          </Alert>
        )}
      </div>
      {responseJson !== undefined && (
        <div className='min-w-0 space-y-2'>
          {ids.length > 0 && (
            <div className='max-h-48 space-y-2 overflow-auto'>
              {ids.map((id) => (
                <CopyableMaterialValue
                  key={id.path}
                  label={id.label}
                  value={id.value}
                />
              ))}
            </div>
          )}
          <CopyButton
            value={responseJson}
            size='sm'
            variant='outline'
            aria-label={t('Copy response JSON')}
          >
            {t('Copy response JSON')}
          </CopyButton>
          <pre
            aria-label={t('Response JSON')}
            className='bg-muted max-h-80 overflow-auto rounded-md p-3 text-xs break-all whitespace-pre-wrap'
          >
            {responseJson}
          </pre>
        </div>
      )}
    </>
  )
}
