import dayjs from 'dayjs'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSectionClassName,
} from '@/components/drawer-layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'

import { BindingDetails, CopyableMaterialValue } from './binding-details'
import {
  MATERIAL_LIBRARY_LABEL,
  MATERIAL_STATUS_LABEL,
  materialStatusVariant,
} from './constants'
import type { Material } from './types'

function DetailRow(props: { label: string; children: React.ReactNode }) {
  return (
    <div className='flex items-start justify-between gap-3 text-sm'>
      <span className='text-muted-foreground shrink-0'>{props.label}</span>
      <span className='min-w-0 text-right break-all'>{props.children}</span>
    </div>
  )
}

export function MaterialRecordDrawer(props: {
  material: Material | null
  admin?: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
  onRetry?: (material: Material) => void
  retryPending?: boolean
}) {
  const { t } = useTranslation()
  const material = props.material
  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-lg')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Material details')}</SheetTitle>
          <SheetDescription>
            {t('Platform material details and preparation status.')}
          </SheetDescription>
        </SheetHeader>
        {material && (
          <div className={sideDrawerFormClassName()}>
            <section className={sideDrawerSectionClassName()}>
              <h4 className='text-sm font-medium'>{t('Platform material')}</h4>
              <div className='space-y-2'>
                <DetailRow label={t('Material name')}>
                  {material.name}
                </DetailRow>
                <DetailRow label={t('Material group')}>
                  {material.group_name || t('Ungrouped')}
                </DetailRow>
                {props.admin && (
                  <DetailRow label={t('Owner')}>
                    {material.username || '—'}
                  </DetailRow>
                )}
                <DetailRow label={t('Library type')}>
                  {t(MATERIAL_LIBRARY_LABEL[material.library_type])}
                </DetailRow>
                <DetailRow label={t('File type')}>
                  {t(material.file_type)}
                </DetailRow>
                <DetailRow label={t('Platform material ID')}>
                  <span className='inline-flex max-w-full items-start gap-1'>
                    <code className='text-muted-foreground min-w-0 text-xs break-all'>
                      {material.material_id}
                    </code>
                    <CopyButton value={material.material_id} />
                  </span>
                </DetailRow>
                <DetailRow label={t('Status')}>
                  <Badge variant={materialStatusVariant(material.status)}>
                    {material.deleting && material.status !== 'failed'
                      ? t('Deleting')
                      : t(MATERIAL_STATUS_LABEL[material.status])}
                  </Badge>
                </DetailRow>
                <DetailRow label={t('Created at')}>
                  {dayjs.unix(material.created_time).format('YYYY-MM-DD HH:mm')}
                </DetailRow>
                <DetailRow label={t('Updated at')}>
                  {dayjs.unix(material.updated_time).format('YYYY-MM-DD HH:mm')}
                </DetailRow>
              </div>
              {props.admin && (
                <>
                  <CopyableMaterialValue
                    label={t('Source URL')}
                    value={material.source_url}
                  />
                  <h4 className='text-sm font-medium'>
                    {t('Channel bindings')}
                  </h4>
                  {material.bindings?.map((binding) => (
                    <BindingDetails key={binding.id} binding={binding} />
                  ))}
                  {!material.bindings?.length && (
                    <p className='text-muted-foreground text-sm'>
                      {t('No channel bindings yet')}
                    </p>
                  )}
                </>
              )}
              {!material.deleting &&
                (material.status === 'pending' ||
                  material.status === 'processing') && (
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'Add an AIGC image using a publicly accessible HTTPS URL. Preparation starts automatically.'
                    )}
                  </p>
                )}
              {material.status === 'failed' && material.fail_reason && (
                <Alert variant='destructive'>
                  <AlertTitle>{t('Failure reason')}</AlertTitle>
                  <AlertDescription className='break-words whitespace-normal'>
                    {material.fail_reason}
                  </AlertDescription>
                </Alert>
              )}
              {!material.deleting &&
                material.status === 'failed' &&
                material.can_retry &&
                props.onRetry && (
                  <Button
                    variant='outline'
                    disabled={props.retryPending}
                    onClick={() => props.onRetry?.(material)}
                  >
                    {t('Retry preparation')}
                  </Button>
                )}
            </section>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
