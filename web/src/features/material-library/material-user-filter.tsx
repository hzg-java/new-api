import { useInfiniteQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
  useComboboxAnchor,
} from '@/components/ui/combobox'
import { searchUsers } from '@/features/users/api'
import type { User } from '@/features/users/types'
import { useDebounce } from '@/hooks/use-debounce'
import { createServerError } from '@/lib/server-error-message'

export type MaterialOwner = Pick<User, 'id' | 'username' | 'display_name'>

function ownerLabel(owner: MaterialOwner): string {
  return owner.username
}

export function MaterialUserFilter(props: {
  value: MaterialOwner | null
  onChange: (owner: MaterialOwner | null) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const keyword = useDebounce(search.trim(), 300)
  const anchor = useComboboxAnchor()
  const query = useInfiniteQuery({
    queryKey: ['material-library', 'user-search', keyword],
    enabled: open,
    initialPageParam: 1,
    queryFn: async ({ pageParam }) => {
      const response = await searchUsers({
        keyword,
        p: pageParam,
        page_size: 20,
      })
      if (!response.success || !response.data) {
        throw createServerError(response, t('We could not load users.'))
      }
      return response.data
    },
    getNextPageParam: (page, _pages, pageParam) =>
      pageParam * 20 < page.total ? pageParam + 1 : undefined,
    retry: false,
  })
  const searching = query.isPending || keyword !== search.trim()
  const items = searching
    ? []
    : (query.data?.pages.flatMap((page) =>
        page.items.filter((user) => !user.DeletedAt)
      ) ?? [])
  let inputValue = props.value ? ownerLabel(props.value) : ''
  if (open) inputValue = search

  return (
    <div className='flex min-w-0 items-center gap-2'>
      <Combobox<MaterialOwner>
        items={items}
        value={props.value}
        open={open}
        inputValue={inputValue}
        filter={null}
        itemToStringLabel={ownerLabel}
        isItemEqualToValue={(item, value) => item.id === value.id}
        onOpenChange={(nextOpen, details) => {
          setOpen(nextOpen)
          if (details.reason !== 'input-change') setSearch('')
        }}
        onInputValueChange={(value, details) => {
          if (details.reason === 'input-change') setSearch(value)
        }}
        onValueChange={(owner) => {
          if (owner) props.onChange(owner)
        }}
      >
        <div ref={anchor} className='min-w-0 flex-1'>
          <ComboboxInput
            showTrigger={false}
            className='w-full'
            aria-label={t('User')}
            placeholder={t('Username')}
            triggerAriaLabel={t('User')}
          />
        </div>
        <ComboboxContent anchor={anchor}>
          {searching && <LoadingState inline message={t('Loading...')} />}
          {query.isError && (
            <ErrorState
              className='min-h-0 p-3'
              title={t('We could not load users.')}
              onRetry={() => void query.refetch()}
            />
          )}
          {!searching && query.isSuccess && (
            <ComboboxEmpty>{t('No results found')}</ComboboxEmpty>
          )}
          <ComboboxList>
            {(owner: MaterialOwner) => (
              <ComboboxItem key={owner.id} value={owner}>
                <span className='min-w-0 break-all'>{ownerLabel(owner)}</span>
              </ComboboxItem>
            )}
          </ComboboxList>
          {!searching && query.hasNextPage && (
            <Button
              variant='ghost'
              className='w-full'
              disabled={query.isFetchingNextPage}
              onClick={() => void query.fetchNextPage()}
            >
              {query.isFetchingNextPage ? t('Loading...') : t('More')}
            </Button>
          )}
        </ComboboxContent>
      </Combobox>
      <Button
        variant='outline'
        disabled={!props.value}
        onClick={() => {
          props.onChange(null)
          setSearch('')
          setOpen(false)
        }}
      >
        {t('All users')}
      </Button>
    </div>
  )
}
