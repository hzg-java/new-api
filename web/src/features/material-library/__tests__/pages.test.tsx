import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { Route as LegacyMaterialsRoute } from '@/routes/_authenticated/materials'
import { useAuthStore } from '@/stores/auth-store'

import { MaterialLibrary } from '../index'
import { MaterialRecords } from '../records'
import type { Material } from '../types'

const api = await import('../api')
const usersApi = await import('@/features/users/api')

vi.mock('@/features/users/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/users/api')>()),
  searchUsers: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  getMaterials: vi.fn(),
  addMaterial: vi.fn(),
  deleteMaterial: vi.fn(),
  retryMaterial: vi.fn(),
  getMaterialGroups: vi.fn(),
  createMaterialGroup: vi.fn(),
  renameMaterialGroup: vi.fn(),
  updateMaterialGroup: vi.fn(),
  getMaterialCapabilities: vi.fn(),
}))

const sampleMaterial: Material = {
  id: 1,
  material_id: 'mat_abc123',
  user_id: 7,
  username: 'alice',
  name: 'hero clip',
  group_id: null,
  group_name: '',
  library_type: 'AIGC',
  file_type: 'Video',
  source_url: 'https://8.8.8.8/a.png',
  status: 'pending',
  fail_reason: '',
  can_retry: false,
  created_time: 1_700_000_000,
  updated_time: 1_700_000_000,
}

function renderWithClient(
  ui: React.ReactElement,
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
) {
  return render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>{ui}</TooltipProvider>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.mocked(api.getMaterials).mockReset()
  vi.mocked(api.getMaterialGroups)
    .mockReset()
    .mockResolvedValue({ success: true, data: [] })
  vi.mocked(api.addMaterial).mockReset()
  vi.mocked(api.deleteMaterial).mockReset()
  vi.mocked(api.retryMaterial).mockReset()
  vi.mocked(api.getMaterialCapabilities).mockReset()
})

afterEach(() => {
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

describe('retired materials route', () => {
  it.each([
    { role: 1, to: '/403' },
    { role: 10, to: '/material-records' },
  ])(
    'redirects role $role to $to without loading materials',
    ({ role, to }) => {
      useAuthStore.getState().auth.setUser({ id: 7, username: 'alice', role })
      const guard = LegacyMaterialsRoute.options.beforeLoad
      expect(guard).toBeTypeOf('function')
      try {
        ;(guard as () => void)()
        expect.fail('Expected route redirect')
      } catch (error) {
        expect((error as { options: { to: string } }).options.to).toBe(to)
      }
      expect(api.getMaterials).not.toHaveBeenCalled()
    }
  )
})

describe('reusable material component (not a user route)', () => {
  it('lists fixture records in the reusable non-admin component without admin fields', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    renderWithClient(<MaterialLibrary />)
    expect(await screen.findByText('hero clip')).toBeVisible()
    expect(screen.getByText('mat_abc123')).toBeVisible()
    expect(screen.getByText('Pending preparation')).toBeVisible()
    expect(screen.queryByText('Upstream material ID')).not.toBeInTheDocument()
  })

  it('submits an AIGC image and closes the dialog', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [],
      total: 0,
    })
    vi.mocked(api.addMaterial).mockResolvedValue({
      success: true,
      data: sampleMaterial,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialLibrary />)
    await user.click(screen.getByRole('button', { name: 'Add material' }))
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    await user.type(within(dialog).getByLabelText('Material name'), 'Portrait')
    await user.type(
      within(dialog).getByLabelText('Material URL'),
      'https://8.8.8.8/image.png'
    )
    await user.click(
      within(dialog).getByRole('button', { name: 'Submit material' })
    )
    await waitFor(() => expect(api.addMaterial).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.addMaterial).mock.calls[0][0]).toMatchObject({
      name: 'Portrait',
      library_type: 'AIGC',
      file_type: 'Image',
      source_url: 'https://8.8.8.8/image.png',
    })
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Add material' })).toBeNull()
    )
  })

  it('offers only AIGC image creation without source tabs or irrelevant selectors', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [],
      total: 0,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialLibrary />)
    expect(
      screen.queryByRole('combobox', { name: 'Library type' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add material' }))
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    expect(within(dialog).queryByRole('tab')).not.toBeInTheDocument()
    expect(
      within(dialog).queryByRole('combobox', { name: 'File type' })
    ).not.toBeInTheDocument()
    expect(
      within(dialog).queryByRole('combobox', { name: 'Library type' })
    ).not.toBeInTheDocument()
    expect(
      within(dialog).getByText(
        'Add an AIGC image using a publicly accessible HTTPS URL. Preparation starts automatically.'
      )
    ).toBeVisible()
    expect(api.addMaterial).not.toHaveBeenCalled()
  })

  it.each([
    'not-a-url',
    'http://8.8.8.8/a.png',
    'ftp://8.8.8.8/a.png',
    'https://user:secret@8.8.8.8/a.png',
    'https://@8.8.8.8/a.png',
    'https://:@8.8.8.8/a.png',
    'https://8.8.8.8/a.png#fragment',
    'https://8.8.8.8/a.png#',
  ])(
    'rejects invalid or credential-bearing material URL %s with a field error',
    async (url) => {
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [],
        total: 0,
      })
      const user = userEvent.setup()
      renderWithClient(<MaterialLibrary />)
      await user.click(screen.getByRole('button', { name: 'Add material' }))
      const dialog = screen.getByRole('dialog', { name: 'Add material' })
      await user.type(
        within(dialog).getByLabelText('Material name'),
        'Portrait'
      )
      const input = within(dialog).getByLabelText('Material URL')
      await user.type(input, url)
      await user.click(
        within(dialog).getByRole('button', { name: 'Submit material' })
      )
      expect(
        await within(dialog).findByText(
          'Enter an HTTPS URL without credentials or a fragment.'
        )
      ).toBeVisible()
      expect(input).toHaveAttribute('aria-invalid', 'true')
      expect(api.addMaterial).not.toHaveBeenCalled()
    }
  )

  it('submits trimmed values and retains them when saving fails', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [],
      total: 0,
    })
    vi.mocked(api.addMaterial).mockResolvedValue({
      success: false,
      message: 'URL rejected by security policy',
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialLibrary />)
    await user.click(screen.getByRole('button', { name: 'Add material' }))
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    await user.type(
      within(dialog).getByLabelText('Material name'),
      '  Portrait  '
    )
    await user.type(
      within(dialog).getByLabelText('Material URL'),
      'https://8.8.8.8/a.png'
    )
    await user.click(
      within(dialog).getByRole('button', { name: 'Submit material' })
    )
    await waitFor(() =>
      expect(api.addMaterial).toHaveBeenCalledWith(
        {
          name: 'Portrait',
          library_type: 'AIGC',
          file_type: 'Image',
          source_url: 'https://8.8.8.8/a.png',
          group_id: null,
        },
        undefined
      )
    )
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'URL rejected by security policy'
    )
    expect(within(dialog).getByLabelText('Material name')).toHaveValue(
      '  Portrait  '
    )
    expect(
      within(dialog).getByRole('button', { name: 'Submit material' })
    ).toBeEnabled()
  })

  it('keeps the list within its fixed height below the preparation instructions', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    renderWithClient(<MaterialLibrary />)
    const notice = await screen.findByText(
      'Add an AIGC image using a publicly accessible HTTPS URL. Preparation starts automatically.'
    )
    expect(notice.parentElement).toHaveClass(
      'flex',
      'h-full',
      'min-h-0',
      'flex-col'
    )
    expect(notice.nextElementSibling).toHaveClass('shrink-0')
    expect(notice.parentElement?.lastElementChild).toHaveClass(
      'flex-1',
      'min-h-0'
    )
  })

  it('blocks duplicate submission and dismissal while adding a material', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [],
      total: 0,
    })
    let finish!: (value: Awaited<ReturnType<typeof api.addMaterial>>) => void
    vi.mocked(api.addMaterial).mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      })
    )
    const user = userEvent.setup()
    renderWithClient(<MaterialLibrary />)
    await user.click(screen.getByRole('button', { name: 'Add material' }))
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    await user.type(within(dialog).getByLabelText('Material name'), 'Portrait')
    await user.type(
      within(dialog).getByLabelText('Material URL'),
      'https://8.8.8.8/a.png'
    )
    await user.click(
      within(dialog).getByRole('button', { name: 'Submit material' })
    )
    expect(
      await within(dialog).findByRole('button', { name: 'Submitting...' })
    ).toBeDisabled()
    expect(
      within(dialog).getByRole('button', { name: 'Cancel' })
    ).toBeDisabled()
    expect(within(dialog).getByLabelText('Material URL')).toBeDisabled()
    expect(within(dialog).getByLabelText('Material name')).toBeDisabled()
    await user.keyboard('{Escape}')
    expect(dialog).toBeVisible()
    expect(api.addMaterial).toHaveBeenCalledTimes(1)
    await act(async () => finish({ success: true, data: sampleMaterial }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })

  it('keeps admin-only fields out of the reusable non-admin drawer', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialLibrary />)
    await user.click(await screen.findByRole('button', { name: 'Details' }))
    const drawer = await screen.findByRole('dialog', {
      name: 'Material details',
    })
    expect(within(drawer).getByText('mat_abc123')).toBeVisible()
    expect(within(drawer).getByText('Updated at')).toBeVisible()
    expect(within(drawer).queryByText('Owner')).not.toBeInTheDocument()
    expect(
      within(drawer).queryByText('Upstream authentication mapping')
    ).not.toBeInTheDocument()
    expect(
      within(drawer).queryByText(sampleMaterial.source_url)
    ).not.toBeInTheDocument()
  })

  it('refreshes cached admin records after deleting an owned material', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    })
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    vi.mocked(api.deleteMaterial).mockResolvedValue({
      success: true,
      data: { ...sampleMaterial, deleting: true, status: 'processing' },
    })
    const user = userEvent.setup()
    const view = renderWithClient(<MaterialRecords />, client)
    await screen.findByText('hero clip')
    view.rerender(
      <QueryClientProvider client={client}>
        <MaterialLibrary />
      </QueryClientProvider>
    )
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [],
      total: 0,
    })
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    await screen.findByText('No materials yet')
    view.rerender(
      <QueryClientProvider client={client}>
        <MaterialRecords />
      </QueryClientProvider>
    )
    expect(await screen.findByText('No materials yet')).toBeVisible()
    expect(screen.queryByText('hero clip')).not.toBeInTheDocument()
    client.clear()
  })

  it('deletes an owned material after confirmation', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    vi.mocked(api.deleteMaterial).mockResolvedValue({
      success: true,
      data: { ...sampleMaterial, deleting: true, status: 'processing' },
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialLibrary />)
    await screen.findByText('hero clip')
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    const confirm = screen.getByRole('alertdialog')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.deleteMaterial).toHaveBeenCalledWith(1))
  })
})

describe.each([
  ['reusable component', MaterialLibrary],
  ['admin', MaterialRecords],
] as const)('%s material list errors', (_scope, Page) => {
  it('shows an error instead of an empty list and retries successfully', async () => {
    vi.mocked(api.getMaterials)
      .mockResolvedValueOnce({
        success: false,
        message: 'Materials unavailable',
      })
      .mockResolvedValue({ success: true, data: [sampleMaterial], total: 1 })
    const user = userEvent.setup()
    renderWithClient(<Page />)
    expect(
      await screen.findByText('We could not load materials.')
    ).toBeVisible()
    expect(screen.queryByText('No materials yet')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('hero clip')).toBeVisible()
  })
})

describe('admin all-materials view', () => {
  it.each([{ materials: [] }, { materials: [sampleMaterial] }])(
    'omits preparation instructions from the admin list with $materials.length records',
    async ({ materials }) => {
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: materials,
        total: materials.length,
      })
      renderWithClient(<MaterialRecords />)
      await screen.findByText(
        materials.length ? 'hero clip' : 'No materials yet'
      )
      expect(
        screen.queryAllByText(
          'Add an AIGC image using a publicly accessible HTTPS URL. Preparation starts automatically.'
        )
      ).toHaveLength(0)
    }
  )

  it.each([
    ['username', 'owner-with-a-long-name-'.repeat(8)],
    ['name', 'A material name with spaces and a long description '.repeat(6)],
    ['group_name', 'A group with a long name '.repeat(8)],
    ['material_id', 'mat_very_long_platform_identifier_'.repeat(8)],
  ] as const)(
    'ellipsizes long %s values and reveals the complete value on hover',
    async (field, value) => {
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [{ ...sampleMaterial, [field]: value }],
        total: 1,
      })
      const user = userEvent.setup()
      renderWithClient(<MaterialRecords />)
      const label = await screen.findByText(value.trim())
      const trigger = label.closest('[data-slot="tooltip-trigger"]')
      expect(trigger).toHaveClass('truncate')
      expect(trigger?.className).toMatch(/max-w-/)
      await user.hover(trigger as HTMLElement)
      await waitFor(() => {
        const fullText = screen
          .getAllByText(value.trim())
          .find((node) => node !== label)
        expect(fullText).toBeVisible()
      })
    }
  )

  it('keeps multiple channel bindings in one ellipsized line with complete hover text', async () => {
    const channelName = 'Mobile Cloud channel with a long name '
      .repeat(8)
      .trim()
    const bindings: NonNullable<Material['bindings']> = [
      {
        id: 9,
        channel_id: 42,
        channel_name: channelName,
        provider: 'mobile_cloud',
        group_id: 'group-42',
        asset_id: 'asset-42',
        state: 'ready',
        upstream_status: 'ACTIVE',
        fail_reason: '',
        can_retry: false,
        updated_time: 1_700_000_000,
      },
      {
        id: 10,
        channel_id: 43,
        channel_name: 'Second channel',
        provider: 'topenrouter',
        group_id: 'group-43',
        asset_id: 'asset-43',
        state: 'ready',
        upstream_status: 'ACTIVE',
        fail_reason: '',
        can_retry: false,
        updated_time: 1_700_000_000,
      },
    ]
    const text = `${channelName} #42 · Second channel #43`
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [{ ...sampleMaterial, bindings }],
      total: 1,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    const label = await screen.findByText(text)
    const trigger = label.closest('[data-slot="tooltip-trigger"]')
    expect(trigger).toHaveClass('truncate')
    await user.hover(trigger as HTMLElement)
    await waitFor(() => {
      const fullText = screen.getAllByText(text).find((node) => node !== label)
      expect(fullText).toBeVisible()
    })
  })

  it('keeps a long failure reason beside the status and reveals it on hover', async () => {
    const reason =
      'Preparation could not finish because the source is unavailable. '.repeat(
        8
      )
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [{ ...sampleMaterial, status: 'failed', fail_reason: reason }],
      total: 1,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    const label = await screen.findByText(reason.trim())
    const trigger = label.closest('[data-slot="tooltip-trigger"]')
    expect(trigger).toHaveClass('truncate')
    expect(trigger?.parentElement).toHaveClass('flex', 'items-center')
    expect(trigger?.parentElement).not.toHaveClass('flex-wrap')
    await user.hover(trigger as HTMLElement)
    await waitFor(() => {
      const fullText = screen
        .getAllByText(reason.trim())
        .find((node) => node !== label)
      expect(fullText).toBeVisible()
    })
  })

  it.each(['pending', 'ready', 'failed'] as const)(
    'keeps all row actions on one line for a %s material',
    async (status) => {
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [{ ...sampleMaterial, status, can_retry: true }],
        total: 1,
      })
      renderWithClient(<MaterialRecords />)
      const details = await screen.findByRole('button', { name: 'Details' })
      const actions = details.parentElement
      if (!actions) throw new Error('Row actions are missing')
      expect(actions).toHaveClass(
        'flex',
        'w-max',
        'max-w-full',
        'overflow-x-auto'
      )
      expect(actions).not.toHaveClass('flex-wrap')
      expect(
        within(actions).getByRole('button', { name: 'Delete' })
      ).toBeVisible()
      if (status === 'pending') {
        expect(
          within(actions).getByRole('button', { name: 'Change group' })
        ).toBeVisible()
      }
      if (status === 'failed') {
        expect(
          within(actions).getByRole('button', { name: 'Retry preparation' })
        ).toBeVisible()
      }
    }
  )

  it('keeps the owner and expanded filters available to recover from a filtered request failure', async () => {
    vi.mocked(api.getMaterials).mockImplementation(async (params) =>
      params?.source_url
        ? { success: false, message: 'Filtered materials unavailable' }
        : { success: true, data: [sampleMaterial], total: 1 }
    )
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await screen.findByText('hero clip')
    await user.click(screen.getByRole('button', { name: 'Expand' }))
    await user.type(
      screen.getByRole('textbox', { name: 'Source URL' }),
      'bad.png'
    )
    expect(
      await screen.findByText('We could not load materials.')
    ).toBeVisible()
    expect(screen.getByRole('combobox', { name: 'User' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Collapse' })).toHaveAttribute(
      'aria-expanded',
      'true'
    )
    expect(screen.getByRole('textbox', { name: 'Source URL' })).toHaveValue(
      'bad.png'
    )
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(await screen.findByText('hero clip')).toBeVisible()
    expect(
      screen.queryByText('We could not load materials.')
    ).not.toBeInTheDocument()
  })

  it('keeps primary filters visible and preserves advanced conditions while collapsed', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [],
      total: 0,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    expect(screen.getByRole('combobox', { name: 'User' })).toBeVisible()
    expect(
      screen.getByRole('textbox', { name: 'Search materials' })
    ).toBeVisible()
    expect(screen.getByRole('combobox', { name: 'Status' })).toBeVisible()
    expect(
      screen.queryByRole('textbox', { name: 'Source URL' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('spinbutton', { name: 'Channel ID' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('combobox', { name: 'Upstream status' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Expand' })).toHaveAttribute(
      'aria-expanded',
      'false'
    )
    await user.click(screen.getByRole('button', { name: 'Expand' }))
    expect(screen.getByRole('button', { name: 'Collapse' })).toHaveAttribute(
      'aria-expanded',
      'true'
    )
    await user.type(
      screen.getByRole('textbox', { name: 'Source URL' }),
      'original.png'
    )
    await user.type(
      screen.getByRole('spinbutton', { name: 'Channel ID' }),
      '42'
    )
    await user.click(screen.getByRole('combobox', { name: 'Upstream status' }))
    await user.click(await screen.findByRole('option', { name: 'ACTIVE' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({
          scope: 'all',
          source_url: 'original.png',
          channel_id: 42,
          upstream_status: 'ACTIVE',
          offset: 0,
        })
      )
    )
    await user.click(screen.getByRole('button', { name: 'Collapse' }))
    expect(
      screen.queryByRole('textbox', { name: 'Source URL' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Expand' })).toHaveAttribute(
      'aria-expanded',
      'false'
    )
    await user.type(
      screen.getByRole('textbox', { name: 'Search materials' }),
      'hero'
    )
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({
          keyword: 'hero',
          source_url: 'original.png',
          channel_id: 42,
          upstream_status: 'ACTIVE',
        })
      )
    )
    await user.click(screen.getByRole('button', { name: 'Expand' }))
    expect(screen.getByRole('textbox', { name: 'Source URL' })).toHaveValue(
      'original.png'
    )
    expect(screen.getByRole('spinbutton', { name: 'Channel ID' })).toHaveValue(
      42
    )
    expect(
      screen.getByRole('combobox', { name: 'Upstream status' })
    ).toHaveTextContent('ACTIVE')
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(
      screen.getByRole('textbox', { name: 'Search materials' })
    ).toHaveValue('')
    expect(screen.getByRole('textbox', { name: 'Source URL' })).toHaveValue('')
    expect(screen.getByRole('spinbutton', { name: 'Channel ID' })).toHaveValue(
      null
    )
    expect(
      screen.getByRole('combobox', { name: 'Upstream status' })
    ).toHaveTextContent('All statuses')
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ keyword: '', status: '', offset: 0 })
      )
    )
  })

  it('shows only channel names and IDs in the list while preserving complete copyable binding details', async () => {
    const binding = {
      id: 9,
      channel_id: 42,
      channel_name: 'Full Mobile Cloud channel',
      provider: 'mobile_cloud',
      group_id: `real-group-${'x'.repeat(100)}`,
      asset_id: 'real-asset-42',
      state: 'uncertain',
      upstream_status: 'PROCESSING',
      fail_reason: 'Safe reconciliation reason',
      can_retry: true,
      updated_time: 1700000010,
    }
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [{ ...sampleMaterial, bindings: [binding] } as Material],
      total: 1,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    expect(
      await screen.findByText('Full Mobile Cloud channel #42')
    ).toBeVisible()
    expect(screen.queryByText('real-asset-42')).not.toBeInTheDocument()
    expect(screen.queryByText(binding.group_id)).not.toBeInTheDocument()
    expect(screen.queryByText('PROCESSING')).not.toBeInTheDocument()
    expect(screen.queryByText('Uncertain')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Change group' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Details' }))
    const drawer = within(
      screen.getByRole('dialog', { name: 'Material details' })
    )
    expect(drawer.getByText(sampleMaterial.source_url)).toBeVisible()
    expect(drawer.getByText(binding.group_id)).toHaveClass('break-all')
    expect(drawer.getByText('real-asset-42')).toBeVisible()
    expect(drawer.getByText(/Full Mobile Cloud channel/)).toBeVisible()
    expect(drawer.getByText('PROCESSING')).toBeVisible()
    expect(drawer.getByText('Uncertain')).toBeVisible()
    expect(drawer.getByText(binding.fail_reason)).toBeVisible()
    await user.click(drawer.getByRole('button', { name: 'Copy Source URL' }))
    expect(await navigator.clipboard.readText()).toBe(sampleMaterial.source_url)
  })

  it('withdraws a pending group assignment when a refresh starts deletion', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />, client)
    await user.click(
      await screen.findByRole('button', { name: 'Change group' })
    )
    expect(
      await screen.findByRole('dialog', { name: 'Change material group' })
    ).toBeVisible()
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [{ ...sampleMaterial, deleting: true, status: 'processing' }],
      total: 1,
    })
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ['material-library', 'all-materials'],
      })
    })
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: 'Change material group' })
      ).not.toBeInTheDocument()
    )
    expect(api.updateMaterialGroup).not.toHaveBeenCalled()
    client.clear()
  })

  it('keeps queued deletion visible and disables duplicate deletion, preparation and group moves', async () => {
    const deleting = {
      ...sampleMaterial,
      deleting: true,
      status: 'processing' as const,
    }
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    vi.mocked(api.deleteMaterial).mockResolvedValue({
      success: true,
      data: deleting,
    } as never)
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [deleting],
      total: 1,
    })
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    expect(await screen.findByText('Deleting')).toBeVisible()
    expect(screen.getByText('hero clip')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
    expect(
      screen.queryByRole('button', { name: 'Change group' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Retry preparation' })
    ).not.toBeInTheDocument()
    expect(api.deleteMaterial).toHaveBeenCalledWith(1, 7)
  })

  it('retains a queued deletion in cache when its immediate refresh fails', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    vi.mocked(api.deleteMaterial).mockResolvedValue({
      success: true,
      data: { ...sampleMaterial, deleting: true, status: 'processing' },
    })
    const user = userEvent.setup()
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    renderWithClient(<MaterialRecords />, client)
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    vi.mocked(api.getMaterials).mockRejectedValue(
      new Error('Refresh unavailable')
    )
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(screen.queryByText('No materials yet')).not.toBeInTheDocument()
    expect(
      client.getQueriesData({
        queryKey: ['material-library', 'all-materials'],
      })[0][1]
    ).toMatchObject({
      materials: [{ id: 1, deleting: true, status: 'processing' }],
      total: 1,
    })
    client.clear()
  })

  it('disables an open deletion confirmation when a refresh shows deletion already queued', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />, client)
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [{ ...sampleMaterial, deleting: true, status: 'processing' }],
      total: 1,
    })
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ['material-library', 'all-materials'],
      })
    })
    const confirm = within(screen.getByRole('alertdialog'))
    await waitFor(() =>
      expect(confirm.getByRole('button', { name: 'Delete' })).toBeDisabled()
    )
    await user.click(confirm.getByRole('button', { name: 'Delete' }))
    expect(api.deleteMaterial).not.toHaveBeenCalled()
    client.clear()
  })

  it('retries failed deletion through DELETE rather than preparation', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [
        {
          ...sampleMaterial,
          deleting: true,
          status: 'failed',
          can_retry: true,
        } as Material,
      ],
      total: 1,
    })
    vi.mocked(api.deleteMaterial).mockResolvedValue({
      success: true,
      data: { ...sampleMaterial, deleting: true, status: 'processing' },
    } as never)
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await user.click(
      await screen.findByRole('button', { name: 'Retry deletion' })
    )
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    await waitFor(() => expect(api.deleteMaterial).toHaveBeenCalledWith(1, 7))
    expect(api.retryMaterial).not.toHaveBeenCalled()
  })

  const users = [
    { id: 7, username: 'alice', display_name: 'Alice' },
    { id: 8, username: 'bob', display_name: 'Bob' },
  ]

  beforeEach(() => {
    vi.mocked(usersApi.searchUsers).mockResolvedValue({
      success: true,
      data: { items: users, total: 2, page: 1, page_size: 20 },
    } as Awaited<ReturnType<typeof usersApi.searchUsers>>)
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
  })

  it('previews admin media without autoplay and retains the player during refresh', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    renderWithClient(<MaterialRecords />, client)
    const trigger = await screen.findByRole('button', {
      name: 'Preview hero clip',
    })
    await user.click(trigger)
    const dialog = screen.getByRole('dialog')
    const player = dialog.querySelector('video')
    expect(player).toHaveAttribute('controls')
    expect(player).not.toHaveAttribute('autoplay')
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ['material-library', 'all-materials'],
      })
    })
    expect(screen.getByRole('dialog')).toBe(dialog)
    expect(dialog.querySelector('video')).toBe(player)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(document.querySelector('video')).toBeNull())
    client.clear()
  })

  it('selects users by username or ID and resets group scope when the owner changes', async () => {
    vi.mocked(api.getMaterialGroups).mockImplementation(async (owner) => ({
      success: true,
      data: [
        {
          id: owner === 8 ? 4 : 3,
          user_id: owner ?? 7,
          name: owner === 8 ? 'Bob group' : 'Alice group',
          created_time: 1,
          updated_time: 1,
        },
      ],
    }))
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    expect(screen.getByRole('button', { name: 'Add material' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Manage groups' })).toBeDisabled()
    expect(api.getMaterialGroups).not.toHaveBeenCalled()
    const picker = screen.getByRole('combobox', { name: 'User' })
    expect(picker).toHaveAttribute('placeholder', 'Username')
    await user.type(picker, 'alice')
    await waitFor(() =>
      expect(usersApi.searchUsers).toHaveBeenCalledWith(
        expect.objectContaining({ keyword: 'alice' })
      )
    )
    await user.click(await screen.findByRole('option', { name: 'alice' }))
    expect(picker).toHaveValue('alice')
    expect(screen.queryByText('#7')).not.toBeInTheDocument()
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ scope: 'all', user_id: 7, offset: 0 })
      )
    )
    expect(
      screen.queryByRole('combobox', { name: 'Material group' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Expand' }))
    await user.click(
      await screen.findByRole('combobox', { name: 'Material group' })
    )
    await user.click(await screen.findByRole('option', { name: 'Alice group' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ user_id: 7, group_id: 3 })
      )
    )
    await user.clear(picker)
    await user.type(picker, '8')
    await waitFor(() =>
      expect(usersApi.searchUsers).toHaveBeenCalledWith(
        expect.objectContaining({ keyword: '8' })
      )
    )
    await user.click(await screen.findByRole('option', { name: 'bob' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ user_id: 8, group_id: undefined, offset: 0 })
      )
    )
    expect(api.getMaterialGroups).toHaveBeenCalledWith(8)
    expect(screen.getByRole('button', { name: 'Add material' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'All users' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ user_id: undefined, group_id: undefined })
      )
    )
    expect(
      screen.queryByRole('combobox', { name: 'Material group' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add material' })).toBeDisabled()
  })

  it('loads later user-search pages so substring matches cannot hide an exact owner ID', async () => {
    const firstPage = Array.from({ length: 20 }, (_, index) => ({
      id: 100 + index,
      username: `team7-${index}`,
      display_name: '',
      ...(index === 0 ? { DeletedAt: '2026-01-01' } : {}),
    }))
    vi.mocked(usersApi.searchUsers).mockImplementation(
      async ({ p }) =>
        ({
          success: true,
          data: {
            items: p === 2 ? [users[0]] : firstPage,
            total: 21,
            page: p ?? 1,
            page_size: 20,
          },
        }) as Awaited<ReturnType<typeof usersApi.searchUsers>>
    )
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await user.type(screen.getByRole('combobox', { name: 'User' }), '7')
    await waitFor(() =>
      expect(usersApi.searchUsers).toHaveBeenCalledWith(
        expect.objectContaining({ keyword: '7', p: 1 })
      )
    )
    expect(
      screen.queryByRole('option', { name: 'team7-0' })
    ).not.toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: 'More' }))
    await user.click(await screen.findByRole('option', { name: 'alice' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ user_id: 7 })
      )
    )
    expect(usersApi.searchUsers).toHaveBeenCalledWith(
      expect.objectContaining({ keyword: '7', p: 2 })
    )
  })

  it('adds material for the selected user through the shared dialog', async () => {
    vi.mocked(api.addMaterial).mockResolvedValue({
      success: true,
      data: sampleMaterial,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await user.type(screen.getByRole('combobox', { name: 'User' }), 'alice')
    await user.click(await screen.findByRole('option', { name: 'alice' }))
    await user.click(screen.getByRole('button', { name: 'Add material' }))
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    await user.type(
      within(dialog).getByLabelText('Material name'),
      'Admin portrait'
    )
    await user.type(
      within(dialog).getByLabelText('Material URL'),
      'https://8.8.8.8/a.png'
    )
    await user.click(
      within(dialog).getByRole('button', { name: 'Submit material' })
    )
    await waitFor(() =>
      expect(api.addMaterial).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'Admin portrait', group_id: null }),
        7
      )
    )
  })

  it('targets the row owner when moving and deleting without a user filter', async () => {
    vi.mocked(api.updateMaterialGroup).mockResolvedValue({
      success: true,
      data: sampleMaterial,
    })
    vi.mocked(api.deleteMaterial).mockResolvedValue({
      success: true,
      data: { ...sampleMaterial, deleting: true, status: 'processing' },
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await user.click(
      await screen.findByRole('button', { name: 'Change group' })
    )
    const dialog = screen.getByRole('dialog', { name: 'Change material group' })
    await within(dialog).findByRole('combobox', { name: 'Material group' })
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(api.updateMaterialGroup).toHaveBeenCalledWith(
        1,
        { group_id: null },
        7
      )
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    await waitFor(() => expect(api.deleteMaterial).toHaveBeenCalledWith(1, 7))
  })

  it('keeps user-search failures retryable without switching the list owner', async () => {
    vi.mocked(usersApi.searchUsers).mockRejectedValueOnce(new Error('Offline'))
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await user.click(screen.getByRole('combobox', { name: 'User' }))
    expect(await screen.findByText('We could not load users.')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('option', { name: 'alice' })).toBeVisible()
    expect(api.getMaterials).toHaveBeenLastCalledWith(
      expect.objectContaining({ user_id: undefined })
    )
  })

  it('filters admin records by preparation status without an irrelevant library selector', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [],
      total: 0,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    expect(
      screen.queryByRole('combobox', { name: 'Library type' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('combobox', { name: 'Status' }))
    await user.click(
      await screen.findByRole('option', { name: 'Pending preparation' })
    )
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ scope: 'all', status: 'pending', offset: 0 })
      )
    )
    await user.click(screen.getByRole('combobox', { name: 'Status' }))
    await user.click(await screen.findByRole('option', { name: 'Preparing' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({
          scope: 'all',
          status: 'processing',
          offset: 0,
        })
      )
    )
  })

  it('lists materials across users from the admin endpoint', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    renderWithClient(<MaterialRecords />)
    expect(await screen.findByText('mat_abc123')).toBeVisible()
    expect(screen.getByText('alice')).toBeVisible()
    expect(screen.queryByText('#7')).not.toBeInTheDocument()
    expect(vi.mocked(api.getMaterials).mock.calls[0][0]).toMatchObject({
      scope: 'all',
    })
  })

  it('does not fall back to an owner ID when the username is unavailable', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [{ ...sampleMaterial, username: undefined }],
      total: 1,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await screen.findByText('mat_abc123')
    expect(screen.queryByText('#7')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Details' }))
    const drawer = await screen.findByRole('dialog', {
      name: 'Material details',
    })
    expect(within(drawer).getByText('Owner')).toBeVisible()
    expect(within(drawer).getByText('—')).toBeVisible()
    expect(within(drawer).queryByText('#7')).not.toBeInTheDocument()
  })

  it('opens the admin drawer with platform info and preparation instructions', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [sampleMaterial],
      total: 1,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await screen.findByText('mat_abc123')
    await user.click(screen.getByRole('button', { name: 'Details' }))
    const drawer = await screen.findByRole('dialog', {
      name: 'Material details',
    })
    expect(within(drawer).getByText('mat_abc123')).toBeVisible()
    expect(within(drawer).getByText('alice')).toBeVisible()
    expect(within(drawer).queryByText('#7')).not.toBeInTheDocument()
    expect(within(drawer).getByText('Pending preparation')).toBeVisible()
    expect(
      within(drawer).getByText(
        'Add an AIGC image using a publicly accessible HTTPS URL. Preparation starts automatically.'
      )
    ).toBeVisible()
    expect(
      within(drawer).queryByText('Upstream authentication mapping')
    ).not.toBeInTheDocument()
  })
})

describe('material preparation', () => {
  const failed: Material = {
    ...sampleMaterial,
    status: 'failed',
    can_retry: true,
    fail_reason:
      'Preparation outcome is uncertain; retry to reconcile the existing material.',
  }

  it.each([false, true])(
    'wraps safe failure reasons in the row and drawer on mobile=%s',
    async (mobile) => {
      const original = window.matchMedia
      vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
        ...original(query),
        matches:
          query === '(max-width: 640px)' ? mobile : original(query).matches,
      }))
      const reason = `Preparation failed: <script>not executable</script> ${'unbroken-reason-'.repeat(40)}`
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [
          {
            ...failed,
            fail_reason: reason,
            upstream_material_id: 'private-upstream-id',
            credentials: 'private-credential',
          } as Material,
        ],
        total: 1,
      })
      const user = userEvent.setup()
      renderWithClient(<MaterialLibrary />)
      const label = await screen.findByText(reason.trim())
      expect(label).toHaveClass('whitespace-normal', 'break-words')
      await user.click(screen.getByRole('button', { name: 'Details' }))
      const drawer = screen.getByRole('dialog', { name: 'Material details' })
      expect(within(drawer).getByRole('alert')).toHaveTextContent(reason.trim())
      expect(
        within(drawer).getByRole('button', { name: 'Retry preparation' })
      ).toBeEnabled()
      expect(drawer).not.toHaveTextContent(sampleMaterial.source_url)
      expect(document.body).not.toHaveTextContent('private-upstream-id')
      expect(document.body).not.toHaveTextContent('private-credential')
      expect(document.querySelector('script')).toBeNull()
    }
  )

  it.each([
    { status: 'failed', can_retry: false },
    { status: 'pending', can_retry: true },
    { status: 'processing', can_retry: true },
    { status: 'ready', can_retry: true },
  ] as const)(
    'does not offer retry for $status when can_retry=$can_retry',
    async (overrides) => {
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [{ ...failed, ...overrides }],
        total: 1,
      })
      const user = userEvent.setup()
      renderWithClient(<MaterialLibrary />)
      await screen.findByText('hero clip')
      expect(
        screen.queryByRole('button', { name: 'Retry preparation' })
      ).not.toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Details' }))
      expect(
        within(screen.getByRole('dialog')).queryByRole('button', {
          name: 'Retry preparation',
        })
      ).not.toBeInTheDocument()
    }
  )

  it.each([false, true])(
    'reconciles uncertain creation only through retry for admin=%s and refreshes both material caches',
    async (admin) => {
      const client = new QueryClient({
        defaultOptions: {
          queries: { retry: false, staleTime: Infinity },
          mutations: { retry: false },
        },
      })
      const inactiveKey = [
        'material-library',
        admin ? 'materials' : 'all-materials',
        'cached',
      ]
      client.setQueryData(inactiveKey, { materials: [failed], total: 1 })
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [failed],
        total: 1,
      })
      let finish!: (
        value: Awaited<ReturnType<typeof api.retryMaterial>>
      ) => void
      vi.mocked(api.retryMaterial).mockReturnValue(
        new Promise((resolve) => {
          finish = resolve
        })
      )
      const user = userEvent.setup()
      renderWithClient(<MaterialLibrary admin={admin} />, client)
      await user.click(
        await screen.findByRole('button', { name: 'Retry preparation' })
      )
      const confirm = screen.getByRole('alertdialog', {
        name: 'Retry preparation',
      })
      expect(confirm).toHaveTextContent(
        'Retry checks an uncertain previous attempt before preparing the material again. Do not add a duplicate material.'
      )
      await user.click(
        within(confirm).getByRole('button', { name: 'Retry preparation' })
      )
      expect(
        await within(confirm).findByRole('button', { name: 'Retrying...' })
      ).toBeDisabled()
      await user.click(
        within(confirm).getByRole('button', { name: 'Retrying...' })
      )
      expect(
        within(confirm).getByRole('button', { name: 'Cancel' })
      ).toBeDisabled()
      await user.keyboard('{Escape}')
      expect(confirm).toBeVisible()
      expect(api.retryMaterial).toHaveBeenCalledTimes(1)
      expect(vi.mocked(api.retryMaterial).mock.calls[0]).toEqual(
        admin ? [1, 7] : [1]
      )
      const prepared = {
        ...failed,
        status: 'ready' as const,
        can_retry: false,
        fail_reason: '',
      }
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [prepared],
        total: 1,
      })
      await act(async () => finish({ success: true, data: prepared }))
      await waitFor(() =>
        expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      )
      expect(await screen.findByText('Ready')).toBeVisible()
      expect(screen.queryByText(failed.fail_reason)).not.toBeInTheDocument()
      expect(client.getQueryState(inactiveKey)?.isInvalidated).toBe(true)
      expect(api.addMaterial).not.toHaveBeenCalled()
      client.clear()
    }
  )

  it('targets the row owner even when the admin selector names a different owner', async () => {
    vi.mocked(usersApi.searchUsers).mockResolvedValue({
      success: true,
      data: {
        items: [{ id: 8, username: 'bob', display_name: 'Bob' }],
        total: 1,
        page: 1,
        page_size: 20,
      },
    } as Awaited<ReturnType<typeof usersApi.searchUsers>>)
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [failed],
      total: 1,
    })
    vi.mocked(api.retryMaterial).mockResolvedValue({
      success: true,
      data: failed,
    })
    const user = userEvent.setup()
    renderWithClient(<MaterialRecords />)
    await user.type(screen.getByRole('combobox', { name: 'User' }), 'bob')
    await user.click(await screen.findByRole('option', { name: 'bob' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ user_id: 8 })
      )
    )
    await user.click(
      await screen.findByRole('button', { name: 'Retry preparation' })
    )
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Retry preparation',
      })
    )
    await waitFor(() =>
      expect(api.retryMaterial).toHaveBeenCalledExactlyOnceWith(1, 7)
    )
  })

  it('keeps a failed retry confirmation open and permits another explicit retry', async () => {
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [failed],
      total: 1,
    })
    vi.mocked(api.retryMaterial)
      .mockResolvedValueOnce({
        success: false,
        message: 'Preparation is unavailable',
      })
      .mockResolvedValue({ success: true, data: failed })
    const user = userEvent.setup()
    renderWithClient(<MaterialLibrary />)
    await user.click(await screen.findByRole('button', { name: 'Details' }))
    await user.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: 'Retry preparation',
      })
    )
    const confirm = screen.getByRole('alertdialog')
    await user.click(
      within(confirm).getByRole('button', { name: 'Retry preparation' })
    )
    expect(await within(confirm).findByRole('alert')).toHaveTextContent(
      'Preparation is unavailable'
    )
    expect(
      within(confirm).getByRole('button', { name: 'Retry preparation' })
    ).toBeEnabled()
    await user.click(
      within(confirm).getByRole('button', { name: 'Retry preparation' })
    )
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(api.retryMaterial).toHaveBeenCalledTimes(2)
    expect(api.addMaterial).not.toHaveBeenCalled()
  })

  it.each(['pending', 'processing'] as const)(
    'polls a visible %s page until ready and stops after unmount',
    async (status) => {
      vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      })
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [{ ...sampleMaterial, status }],
        total: 1,
      })
      const view = renderWithClient(<MaterialLibrary />, client)
      await screen.findByText(
        status === 'pending' ? 'Pending preparation' : 'Preparing'
      )
      try {
        vi.mocked(api.getMaterials).mockResolvedValue({
          success: true,
          data: [{ ...sampleMaterial, status: 'ready' }],
          total: 1,
        })
        await act(async () => {
          await vi.advanceTimersByTimeAsync(5000)
        })
        expect(api.getMaterials).toHaveBeenCalledTimes(2)
        expect(await screen.findByText('Ready')).toBeVisible()
        await act(async () => {
          await vi.advanceTimersByTimeAsync(15000)
        })
        expect(api.getMaterials).toHaveBeenCalledTimes(2)
        vi.mocked(api.getMaterials).mockResolvedValue({
          success: true,
          data: [{ ...sampleMaterial, status }],
          total: 1,
        })
        await act(async () => {
          await client.invalidateQueries({
            queryKey: ['material-library', 'materials'],
          })
        })
        view.unmount()
        const count = vi.mocked(api.getMaterials).mock.calls.length
        await act(async () => {
          await vi.advanceTimersByTimeAsync(10000)
        })
        expect(api.getMaterials).toHaveBeenCalledTimes(count)
      } finally {
        vi.useRealTimers()
        client.clear()
      }
    }
  )

  it.each(['ready', 'failed', 'empty'] as const)(
    'does not poll a settled or empty %s page',
    async (status) => {
      vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: status === 'empty' ? [] : [{ ...sampleMaterial, status }],
        total: status === 'empty' ? 0 : 1,
      })
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      })
      renderWithClient(<MaterialLibrary />, client)
      await screen.findByText(
        status === 'empty' ? 'No materials yet' : 'hero clip'
      )
      try {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(15000)
        })
        expect(api.getMaterials).toHaveBeenCalledTimes(1)
      } finally {
        vi.useRealTimers()
        client.clear()
      }
    }
  )
})
