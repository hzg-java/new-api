import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { processChartData } from '@/features/dashboard/lib/charts'
import type { QuotaDataItem } from '@/features/dashboard/types'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { ConsumptionDistributionChart } from '../consumption-distribution-chart'
import { ModelCharts } from '../model-charts'

vi.mock('@visactor/react-vchart', () => ({
  VChart: (props: { spec: { type: string } }) => (
    <div role='img' aria-label={props.spec.type} />
  ),
}))
vi.mock('@visactor/vchart', () => ({
  ThemeManager: { setCurrentTheme: () => undefined },
}))

const data: QuotaDataItem[] = [
  { model_name: 'alpha', created_at: 1794268800, quota: 500000, count: 4 },
  { model_name: 'beta', created_at: 1794268800, quota: 2000000, count: 1 },
  { model_name: 'alpha', created_at: 1794355200, quota: 500000, count: 6 },
]

beforeEach(() => {
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
})

afterEach(async () => {
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  await i18next.changeLanguage('en')
})

describe('model chart ranking data', () => {
  it('aggregates each model across time and sorts consumption separately from calls', () => {
    const result = processChartData(data)

    expect(result.quotaRanking.map((item) => [item.model, item.value])).toEqual(
      [
        ['beta', 2000000],
        ['alpha', 1000000],
      ]
    )
    expect(result.callRanking.map((item) => [item.model, item.value])).toEqual([
      ['alpha', 10],
      ['beta', 1],
    ])
    expect(result.quotaRanking[0].color).toBe(result.callRanking[1].color)
  })

  it('returns empty rankings when the selected range has no records', () => {
    const result = processChartData([])
    expect(result.quotaRanking).toEqual([])
    expect(result.callRanking).toEqual([])
  })
})

describe('model chart side rankings', () => {
  it('shows descending consumption amounts beside the existing chart and keeps chart switching', async () => {
    const user = userEvent.setup()
    render(<ConsumptionDistributionChart data={data} />)
    const ranking = screen.getByRole('region', {
      name: 'Model Consumption Ranking',
    })
    const rows = within(ranking).getAllByRole('listitem')

    expect(
      rows.map((row) =>
        within(row).getByRole('progressbar').getAttribute('aria-label')
      )
    ).toEqual(['beta', 'alpha'])
    expect(within(rows[0]).getByText('$4')).toBeVisible()
    expect(within(rows[1]).getByText('$2')).toBeVisible()
    expect(within(ranking).getByRole('list')).toHaveClass('overflow-y-auto')
    expect(ranking).toHaveClass('lg:border-t-0', 'lg:border-l')
    expect(ranking.parentElement).toHaveClass(
      'grid-cols-1',
      'lg:grid-cols-[minmax(0,1fr)_20rem]'
    )
    expect(await screen.findByRole('img', { name: 'bar' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Area Chart' }))
    expect(await screen.findByRole('img', { name: 'area' })).toBeVisible()
    expect(within(ranking).getAllByRole('listitem')).toHaveLength(2)
  })

  it('shows descending call counts and refreshes the ranking when filtered data changes', async () => {
    const view = render(<ModelCharts data={data} />)
    const ranking = screen.getByRole('region', { name: 'Model Call Ranking' })
    const rows = within(ranking).getAllByRole('listitem')
    expect(
      rows.map((row) =>
        within(row).getByRole('progressbar').getAttribute('aria-label')
      )
    ).toEqual(['alpha', 'beta'])
    expect(within(rows[0]).getByText('10')).toBeVisible()
    await screen.findByRole('img', { name: 'area' })

    view.rerender(<ModelCharts data={[data[1]]} />)
    expect(within(ranking).getAllByRole('listitem')).toHaveLength(1)
    expect(within(ranking).getByText('beta')).toBeVisible()
    expect(within(ranking).queryByText('alpha')).not.toBeInTheDocument()
  })

  it('hides stale ranking values during loading and shows an empty state for an empty range', async () => {
    const view = render(<ModelCharts data={data} loading />)
    const ranking = screen.getByRole('region', { name: 'Model Call Ranking' })
    expect(ranking).toHaveAttribute('aria-busy', 'true')
    expect(within(ranking).getByText('Loading...')).toBeVisible()
    expect(within(ranking).queryByRole('list')).not.toBeInTheDocument()
    await screen.findByRole('img')

    view.rerender(<ModelCharts data={[]} />)
    expect(ranking).toHaveAttribute('aria-busy', 'false')
    expect(within(ranking).getByText('No data available')).toBeVisible()
  })

  it('keeps zero-use models visible without an invalid progress value', async () => {
    render(
      <ModelCharts
        data={[
          {
            model_name: 'free-model',
            created_at: 1794268800,
            count: 0,
            quota: 0,
          },
        ]}
      />
    )
    const ranking = screen.getByRole('region', { name: 'Model Call Ranking' })
    expect(within(ranking).getByText('free-model')).toBeVisible()
    expect(within(ranking).getByRole('progressbar')).toHaveAttribute(
      'aria-valuenow',
      '0'
    )
    await screen.findByRole('img')
  })

  it.each([
    ['Call Count Distribution', 'pie'],
    ['Call Count Ranking', 'bar'],
  ])(
    'keeps the call ranking when switching to %s',
    async (buttonName, chartType) => {
      const user = userEvent.setup()
      render(<ModelCharts data={data} />)
      await screen.findByRole('img', { name: 'area' })
      await user.click(screen.getByRole('button', { name: buttonName }))
      expect(await screen.findByRole('img', { name: chartType })).toBeVisible()
      expect(
        within(
          screen.getByRole('region', { name: 'Model Call Ranking' })
        ).getAllByRole('listitem')
      ).toHaveLength(2)
    }
  )

  it('keeps every ranked model scrollable and exposes full names without widening the panel', async () => {
    const longName =
      'provider/model-with-an-extremely-long-name-without-spaces-and-version-20261110'
    const manyModels = Array.from({ length: 24 }, (_, index) => ({
      model_name: index === 0 ? longName : `model-${index}`,
      created_at: 1794268800,
      count: 24 - index,
      quota: 24 - index,
    }))
    render(<ModelCharts data={manyModels} />)
    const ranking = screen.getByRole('region', { name: 'Model Call Ranking' })
    expect(within(ranking).getAllByRole('listitem')).toHaveLength(24)
    expect(within(ranking).getByTitle(longName)).toHaveClass('truncate')
    expect(within(ranking).getByRole('list')).toHaveClass(
      'min-h-0',
      'overflow-y-auto'
    )
    expect(
      within(ranking).getByRole('progressbar', { name: longName })
    ).toHaveAttribute('aria-valuenow', '100')
    await screen.findByRole('img')
  })

  it('uses the configured currency and updates number formatting when the interface language changes', async () => {
    const config = useSystemConfigStore.getState().config
    useSystemConfigStore.setState({
      config: {
        ...config,
        currency: {
          ...config.currency,
          quotaDisplayType: 'CNY',
          usdExchangeRate: 7,
        },
      },
    })
    render(
      <>
        <ConsumptionDistributionChart data={data} />
        <ModelCharts data={[{ ...data[0], count: 1234 }]} />
      </>
    )
    expect(
      within(
        screen.getByRole('region', { name: 'Model Consumption Ranking' })
      ).getByText('¥28')
    ).toBeVisible()
    const ranking = screen.getByRole('region', { name: 'Model Call Ranking' })
    expect(within(ranking).getByText('1,234')).toBeVisible()

    await act(async () => {
      await i18next.changeLanguage('fr')
    })
    expect(
      within(ranking).getByText(new Intl.NumberFormat('fr').format(1234), {
        normalizer: (value) => value,
      })
    ).toBeVisible()
    await screen.findAllByRole('img')
  })
})
