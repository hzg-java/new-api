import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { Progress } from '@/components/ui/progress'
import type { ModelChartRankingItem } from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber } from '@/lib/format'

interface ModelRankingProps {
  items: ModelChartRankingItem[]
  metric: 'quota' | 'calls'
  loading?: boolean
}

export function ModelRanking(props: ModelRankingProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const title =
    props.metric === 'quota'
      ? t('Model Consumption Ranking')
      : t('Model Call Ranking')
  const maxValue = props.items[0]?.value ?? 0

  return (
    <section
      aria-label={title}
      aria-busy={Boolean(props.loading)}
      className='flex h-[300px] min-w-0 flex-col gap-4 border-t p-4 sm:h-96 sm:p-5 lg:border-t-0 lg:border-l'
    >
      <h3 className='text-sm font-semibold'>{title}</h3>
      {props.loading && <LoadingState className='min-h-0 flex-1' size='sm' />}
      {!props.loading && props.items.length === 0 && (
        <EmptyState title={t('No data available')} className='min-h-0 py-8' />
      )}
      {!props.loading && props.items.length > 0 && (
        <ol className='min-h-0 flex-1 space-y-4 overflow-y-auto overscroll-contain pr-2'>
          {props.items.map((item, index) => {
            const value =
              props.metric === 'quota'
                ? formatQuotaWithCurrency(item.value, {
                    digitsLarge: 4,
                    digitsSmall: 4,
                    abbreviate: false,
                    locale,
                  })
                : formatNumber(item.value, locale)

            return (
              <li key={item.model} className='flex min-w-0 gap-3'>
                <span
                  aria-hidden='true'
                  className='text-muted-foreground w-5 shrink-0 text-right text-xs leading-5 tabular-nums'
                >
                  {formatNumber(index + 1, locale)}
                </span>
                <div className='min-w-0 flex-1 space-y-2'>
                  <div className='flex min-w-0 items-center justify-between gap-3 text-xs leading-5'>
                    <span className='truncate font-medium' title={item.model}>
                      {item.model}
                    </span>
                    <span className='shrink-0 font-medium tabular-nums'>
                      {value}
                    </span>
                  </div>
                  <Progress
                    value={
                      maxValue > 0
                        ? Math.max(0, (item.value / maxValue) * 100)
                        : 0
                    }
                    aria-label={item.model}
                    className='[&_[data-slot=progress-indicator]]:bg-current'
                    style={{ color: item.color }}
                  />
                </div>
              </li>
            )
          })}
        </ol>
      )}
    </section>
  )
}
