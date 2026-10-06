import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { Footer } from '../footer'

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    to,
    children,
    ...props
  }: React.PropsWithChildren<{ to: string }>) => (
    <a href={to} {...props}>
      {children}
    </a>
  ),
}))

afterEach(() => {
  cleanup()
  useSystemConfigStore.getState().setConfig({ footerHtml: undefined })
})

it.each(['', '<p>Custom footer</p>'])(
  'omits upstream attribution and preserves legal links with footer HTML %s',
  (footerHtml) => {
    useSystemConfigStore
      .getState()
      .setConfig({ systemName: '启元', footerHtml })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: Infinity } },
    })
    client.setQueryData(STATUS_QUERY_KEY, {
      user_agreement_enabled: true,
      privacy_policy_enabled: true,
    })
    const { container } = render(
      <QueryClientProvider client={client}>
        <Footer />
      </QueryClientProvider>
    )
    expect(container).not.toHaveTextContent('New API')
    expect(
      container.querySelector(
        'a[href="https://github.com/QuantumNous/new-api"]'
      )
    ).toBeNull()
    expect(
      screen.getByRole('link', { name: 'User Agreement' })
    ).toHaveAttribute('href', '/user-agreement')
    expect(
      screen.getByRole('link', { name: 'Privacy Policy' })
    ).toHaveAttribute('href', '/privacy-policy')
    if (footerHtml) {
      expect(screen.getByText('Custom footer')).toBeVisible()
    } else {
      expect(container).toHaveTextContent(`© ${new Date().getFullYear()} 启元.`)
    }
    client.clear()
  }
)
