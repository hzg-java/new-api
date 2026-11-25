import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type {
  FlowQuotaDataItem,
  QuotaDataItem,
} from '@/features/dashboard/types'
import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { FlowCharts } from '../flow/flow-charts'
import { UserCharts } from '../users/user-charts'

// Canvas rendering is outside jsdom; keep the dashboard, queries and alerts real.
vi.mock('@visactor/react-vchart', () => ({
  VChart: (props: { spec: { type: string } }) => (
    <div role='img' aria-label={props.spec.type} />
  ),
}))
vi.mock('@visactor/vchart', () => ({
  ThemeManager: { setCurrentTheme: () => undefined },
}))

const warningKey =
  'Some historical usage could not be verified and is shown without correction.'
const userRow: QuotaDataItem = {
  user_id: 1,
  username: 'dashboard-user',
  created_at: 1794268800,
  quota: 500000,
  count: 2,
  token_used: 100,
}
const flowRow: FlowQuotaDataItem = {
  user_id: 1,
  username: 'dashboard-user',
  token_id: 1,
  token_name: 'App key',
  use_group: 'default',
  model_name: 'video-model',
  quota: 500000,
  count: 2,
  token_used: 100,
}
let client: QueryClient

beforeEach(() => {
  window.localStorage.clear()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
})

afterEach(async () => {
  cleanup()
  client.clear()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  window.localStorage.clear()
  i18next.removeResourceBundle('en', 'translation')
  i18next.removeResourceBundle('zh', 'translation')
  await i18next.changeLanguage('en')
})

describe('dashboard chart usage history warnings', () => {
  it.each([
    { panel: 'flow', endpoint: '/api/data/flow/self', row: flowRow },
    { panel: 'users', endpoint: '/api/data/users', row: userRow },
  ])(
    'shows and localizes the $panel warning only while a row is flagged',
    async (scenario) => {
      let rows = [scenario.row, { ...scenario.row, unreconciled: true }]
      vi.spyOn(api, 'get').mockImplementation(async (url) => {
        if (url !== scenario.endpoint) {
          throw new Error(`Unexpected request: ${url}`)
        }
        return { data: { success: true, data: rows } }
      })
      i18next.addResourceBundle('en', 'translation', en.translation)
      i18next.addResourceBundle('zh', 'translation', zh.translation)
      render(
        <QueryClientProvider client={client}>
          {scenario.panel === 'flow' ? (
            <FlowCharts />
          ) : (
            <UserCharts
              filters={{
                timeGranularity: 'day',
                selectedRange: 7,
                topUserLimit: 10,
              }}
              onFiltersChange={vi.fn()}
            />
          )}
        </QueryClientProvider>
      )

      expect(await screen.findByRole('alert')).toHaveTextContent(warningKey)
      await act(async () => {
        await i18next.changeLanguage('zh')
      })
      expect(screen.getByRole('alert')).toHaveTextContent(
        '部分历史用量无法核实，已按原始数据展示，未作修正。'
      )

      rows = [{ ...scenario.row, unreconciled: false }, scenario.row]
      await act(async () => {
        await client.invalidateQueries({ queryKey: ['dashboard'] })
      })
      await waitFor(() =>
        expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      )
    }
  )

  it.each([
    { panel: 'flow', endpoint: '/api/data/flow/self', row: flowRow },
    { panel: 'users', endpoint: '/api/data/users', row: userRow },
  ])(
    'hides the $panel warning for absent or false flags and empty results',
    async (scenario) => {
      let rows = [scenario.row, { ...scenario.row, unreconciled: false }]
      vi.spyOn(api, 'get').mockImplementation(async (url) => {
        if (url !== scenario.endpoint) {
          throw new Error(`Unexpected request: ${url}`)
        }
        return { data: { success: true, data: rows } }
      })
      render(
        <QueryClientProvider client={client}>
          {scenario.panel === 'flow' ? (
            <FlowCharts />
          ) : (
            <UserCharts
              filters={{
                timeGranularity: 'day',
                selectedRange: 7,
                topUserLimit: 10,
              }}
              onFiltersChange={vi.fn()}
            />
          )}
        </QueryClientProvider>
      )

      await screen.findAllByRole('img')
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()

      rows = []
      await act(async () => {
        await client.invalidateQueries({ queryKey: ['dashboard'] })
      })
      await waitFor(() =>
        expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      )
    }
  )
})
