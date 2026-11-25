import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DEFAULT_DASHBOARD_CHART_PREFERENCES } from '@/features/dashboard/constants'
import { buildDefaultDashboardFilters } from '@/features/dashboard/lib'
import { api } from '@/lib/api'

import { LogStatCards } from '../log-stat-cards'
import { ModelsFilter } from '../models-filter-dialog'

const now = new Date(2026, 10, 20, 10, 25, 30)
const midnight = new Date(2026, 10, 20)
const getAnimationsDescriptor = Object.getOwnPropertyDescriptor(
  Element.prototype,
  'getAnimations'
)

beforeEach(() => {
  localStorage.clear()
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(now)
  if (!Element.prototype.getAnimations) {
    Object.defineProperty(Element.prototype, 'getAnimations', {
      configurable: true,
      value: () => [],
    })
  }
})

afterEach(() => {
  vi.useRealTimers()
  localStorage.clear()
  if (getAnimationsDescriptor) {
    Object.defineProperty(
      Element.prototype,
      'getAnimations',
      getAnimationsDescriptor
    )
  } else {
    Reflect.deleteProperty(Element.prototype, 'getAnimations')
  }
})

describe('dashboard today range', () => {
  it('defaults to local midnight through the current time rather than the last 24 hours', () => {
    const filters = buildDefaultDashboardFilters()
    expect(filters.start_timestamp).toEqual(midnight)
    expect(filters.end_timestamp).toEqual(now)
  })

  it.each([new Date(2026, 0, 1, 0, 0, 0), new Date(2026, 10, 20, 0, 5, 0)])(
    'keeps the start within the current day at %s',
    (currentTime) => {
      vi.setSystemTime(currentTime)
      const start = new Date(currentTime)
      start.setHours(0, 0, 0, 0)
      const filters = buildDefaultDashboardFilters()
      expect(filters.start_timestamp).toEqual(start)
      expect(filters.end_timestamp).toEqual(currentTime)
    }
  )

  it('requests today from the statistics endpoint when no explicit range is supplied', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: [] },
    })
    render(<LogStatCards />)

    await screen.findByText('Total Tokens')
    expect(get).toHaveBeenCalledWith('/api/data/self', {
      params: {
        start_timestamp: midnight.getTime() / 1000,
        end_timestamp: now.getTime() / 1000,
        default_time: 'hour',
      },
    })
  })

  it('applies Today from midnight, preserves longer presets, and resets back to today', async () => {
    const user = userEvent.setup()
    const onFilterChange = vi.fn()
    const onReset = vi.fn()
    render(
      <ModelsFilter
        preferences={DEFAULT_DASHBOARD_CHART_PREFERENCES}
        currentFilters={buildDefaultDashboardFilters()}
        onFilterChange={onFilterChange}
        onReset={onReset}
      />
    )
    await user.click(screen.getByRole('button', { name: 'Filter' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('button', { name: '1 Day' })).toBeNull()
    await user.click(within(dialog).getByRole('button', { name: 'Today' }))
    await user.click(
      within(dialog).getByRole('button', { name: 'Apply Filters' })
    )
    expect(onFilterChange).toHaveBeenLastCalledWith({
      start_timestamp: midnight,
      end_timestamp: now,
      time_granularity: 'hour',
    })

    await user.click(screen.getByRole('button', { name: 'Filter' }))
    await user.click(screen.getByRole('button', { name: '7 Days' }))
    await user.click(screen.getByRole('button', { name: 'Apply Filters' }))
    expect(onFilterChange).toHaveBeenLastCalledWith({
      start_timestamp: new Date(now.getTime() - 7 * 86400000),
      end_timestamp: now,
      time_granularity: 'day',
    })

    await user.click(screen.getByRole('button', { name: 'Filter' }))
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(onReset).toHaveBeenCalledOnce()
  })
})
