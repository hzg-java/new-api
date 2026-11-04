import { MusicNote01Icon, Video01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { AudioPreviewDialog } from '@/features/usage-logs/components/dialogs/audio-preview-dialog'
import { ImageDialog } from '@/features/usage-logs/components/dialogs/image-dialog'

import type { Material } from './types'

export function MaterialPreview(props: { material: Material }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [thumbnailFailed, setThumbnailFailed] = useState(false)
  const [videoFailed, setVideoFailed] = useState(false)
  const material = props.material
  // Historical media can use HTTP; creation has a stricter HTTPS-only contract.
  const source = material.source_url.trim()
  const url = URL.canParse(source) ? new URL(source) : null
  const safeSource =
    url &&
    (url.protocol === 'http:' || url.protocol === 'https:') &&
    !url.username &&
    !url.password

  if (!safeSource) {
    return (
      <span className='text-muted-foreground flex h-12 w-24 items-center justify-center text-center text-xs whitespace-normal'>
        {t('Preview unavailable')}
      </span>
    )
  }

  let thumbnail = (
    <>
      <HugeiconsIcon
        icon={material.file_type === 'Video' ? Video01Icon : MusicNote01Icon}
        aria-hidden='true'
      />
      {t('Preview')}
    </>
  )
  if (material.file_type === 'Image') {
    thumbnail = thumbnailFailed ? (
      <span className='text-muted-foreground text-xs'>
        {t('Preview unavailable')}
      </span>
    ) : (
      <img
        src={source}
        alt={material.name}
        width={64}
        height={48}
        loading='lazy'
        referrerPolicy='no-referrer'
        className='h-12 w-16 rounded-md object-cover'
        onError={() => setThumbnailFailed(true)}
      />
    )
  }

  return (
    <>
      <Button
        type='button'
        variant='ghost'
        aria-label={t('Preview {{name}}', { name: material.name })}
        aria-haspopup='dialog'
        className='h-12 w-24 p-0 whitespace-normal'
        onClick={() => {
          setVideoFailed(false)
          setOpen(true)
        }}
      >
        {thumbnail}
      </Button>
      {open && material.file_type === 'Image' && (
        <ImageDialog
          imageUrl={source}
          imageAlt={material.name}
          description={material.name}
          showUrl={false}
          open={open}
          onOpenChange={setOpen}
        />
      )}
      {open && material.file_type === 'Audio' && (
        <AudioPreviewDialog
          open={open}
          onOpenChange={setOpen}
          clips={[{ audio_url: source, title: material.name }]}
        />
      )}
      {open && material.file_type === 'Video' && (
        <Dialog
          open={open}
          onOpenChange={setOpen}
          title={t('Material preview')}
          description={material.name}
          descriptionClassName='break-all'
          contentClassName='sm:max-w-3xl'
        >
          {videoFailed ? (
            <ErrorState
              title={t('Media preview failed. Please try again.')}
              className='min-h-0 py-6'
              onRetry={() => setVideoFailed(false)}
            />
          ) : (
            <video
              src={source}
              aria-label={material.name}
              controls
              playsInline
              preload='none'
              className='max-h-[60vh] w-full rounded-md bg-black'
              onError={() => setVideoFailed(true)}
            />
          )}
        </Dialog>
      )}
    </>
  )
}
