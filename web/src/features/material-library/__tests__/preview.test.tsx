import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest'

import { getMaterialGroups, getMaterials } from '../api'
import { MaterialLibrary } from '../index'
import type { Material, MaterialListResponse } from '../types'

vi.mock('../api', () => ({
  getMaterials: vi.fn(),
  addMaterial: vi.fn(),
  deleteMaterial: vi.fn(),
  getMaterialGroups: vi.fn(),
  createMaterialGroup: vi.fn(),
  renameMaterialGroup: vi.fn(),
  updateMaterialGroup: vi.fn(),
}))

const material: Material = {
  id: 1,
  material_id: 'mat_preview',
  user_id: 7,
  name: 'Portrait',
  group_id: null,
  group_name: '',
  library_type: 'AIGC',
  file_type: 'Image',
  source_url: 'https://example.com/portrait.png?signature=private',
  status: 'pending',
  fail_reason: '',
  can_retry: false,
  created_time: 1_700_000_000,
  updated_time: 1_700_000_000,
}

function renderLibrary(overrides: Partial<Material> = {}) {
  vi.mocked(getMaterials).mockResolvedValue({
    success: true,
    data: [{ ...material, ...overrides }],
    total: 1,
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <MaterialLibrary />
    </QueryClientProvider>
  )
  return { ...view, user: userEvent.setup() }
}

const originalGetAnimations = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})

afterAll(() => {
  if (originalGetAnimations) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      originalGetAnimations
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getMaterialGroups).mockResolvedValue({ success: true, data: [] })
})
afterEach(() => vi.restoreAllMocks())

describe('material list preview', () => {
  it('shows a lazy fixed-size thumbnail and opens the full image with the keyboard', async () => {
    const { user } = renderLibrary()
    const trigger = await screen.findByRole('button', {
      name: 'Preview Portrait',
    })
    const thumbnail = within(trigger).getByRole('img', { name: 'Portrait' })
    expect(thumbnail).toHaveAttribute('loading', 'lazy')
    expect(thumbnail).toHaveAttribute('width', '64')
    expect(thumbnail).toHaveAttribute('height', '48')
    expect(thumbnail).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(thumbnail).toHaveAttribute('src', material.source_url)
    const headers = screen.getAllByRole('columnheader')
    expect(headers[0]).toHaveTextContent('Preview')
    expect(headers[1]).toHaveTextContent('Material name')
    trigger.focus()
    await user.keyboard('{Enter}')
    const dialog = await screen.findByRole('dialog', { name: 'Image Preview' })
    const image = within(dialog).getByRole('img', { name: 'Portrait' })
    fireEvent.load(image)
    expect(image).toBeVisible()
    expect(dialog).not.toHaveTextContent('signature=private')
    expect(dialog).not.toHaveTextContent('View the generated image')
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('keeps an open player mounted when a pending list refresh returns unchanged materials', async () => {
    const videoMaterial = { ...material, file_type: 'Video' as const }
    const { user } = renderLibrary(videoMaterial)
    await screen.findByRole('button', { name: 'Preview Portrait' })
    let resolveRefresh: ((response: MaterialListResponse) => void) | undefined
    const refresh = new Promise<MaterialListResponse>((resolve) => {
      resolveRefresh = resolve
    })
    vi.mocked(getMaterials).mockReturnValueOnce(refresh)
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await user.click(screen.getByRole('button', { name: 'Preview Portrait' }))
    const dialog = await screen.findByRole('dialog')
    const player = dialog.querySelector('video')
    expect(player).not.toBeNull()
    await act(async () => {
      if (!resolveRefresh) throw new Error('Expected the pending list refresh')
      resolveRefresh({ success: true, data: [videoMaterial], total: 1 })
      await refresh
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Refresh', hidden: true })
      ).toBeEnabled()
    )
    expect(screen.getByRole('dialog')).toBe(dialog)
    expect(dialog.querySelector('video')).toBe(player)
  })

  it('keeps the preview accessible in the mobile card with a long material name', async () => {
    const originalMatchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...originalMatchMedia(query),
      matches:
        query === '(max-width: 640px)' || originalMatchMedia(query).matches,
    }))
    const name = 'Portrait with a long material name for a narrow mobile screen'
    const { user } = renderLibrary({ name })
    const trigger = await screen.findByRole('button', {
      name: `Preview ${name}`,
    })
    expect(screen.queryAllByRole('columnheader')).toHaveLength(0)
    expect(within(trigger).getByRole('img', { name })).toHaveAttribute(
      'width',
      '64'
    )
    await user.click(trigger)
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('img', { name })).toBeInTheDocument()
    expect(dialog).toHaveAccessibleDescription(name)
  })

  it('keeps a failed thumbnail in a bounded placeholder and permits opening the preview', async () => {
    const { user } = renderLibrary()
    const trigger = await screen.findByRole('button', {
      name: 'Preview Portrait',
    })
    fireEvent.error(within(trigger).getByRole('img'))
    expect(within(trigger).queryByRole('img')).not.toBeInTheDocument()
    expect(within(trigger).getByText('Preview unavailable')).toBeVisible()
    await user.click(trigger)
    const dialog = await screen.findByRole('dialog')
    fireEvent.error(within(dialog).getByRole('img'))
    expect(within(dialog).getByText('Failed to load image')).toBeVisible()
  })

  it.each(['Image', 'Video', 'Audio'] as const)(
    'preserves %s previews for existing HTTP history without relaxing new-material validation',
    async (file_type) => {
      const source_url = 'http://example.com/legacy-material'
      const { user } = renderLibrary({ file_type, source_url })
      await user.click(
        await screen.findByRole('button', { name: 'Preview Portrait' })
      )
      const dialog = await screen.findByRole('dialog')
      const media = dialog.querySelector('img,video,audio')
      expect(media).toHaveAttribute('src', source_url)
    }
  )

  it.each(['Video', 'Audio'] as const)(
    'loads %s only after opening and removes its player on close without autoplay',
    async (file_type) => {
      const { container, user } = renderLibrary({ file_type })
      const trigger = await screen.findByRole('button', {
        name: 'Preview Portrait',
      })
      const tag = file_type.toLowerCase()
      expect(container.querySelector('video, audio')).toBeNull()
      expect(within(trigger).queryByRole('img')).not.toBeInTheDocument()
      await user.click(trigger)
      const dialog = await screen.findByRole('dialog')
      const player = dialog.querySelector(tag)
      expect(player).toHaveAttribute('src', material.source_url)
      expect(player).toHaveAttribute('controls')
      expect(player).toHaveAttribute('preload', 'none')
      expect(player).not.toHaveAttribute('autoplay')
      await user.keyboard('{Escape}')
      await waitFor(() =>
        expect(document.querySelector('video, audio')).toBeNull()
      )
      await user.click(trigger)
      const reopened = await screen.findByRole('dialog')
      const reopenedPlayer = reopened.querySelector(tag)
      expect(reopenedPlayer).not.toBeNull()
      if (!reopenedPlayer) throw new Error('Expected the reopened media player')
      fireEvent.error(reopenedPlayer)
      expect(
        within(reopened).getByText(/playback failed|Media preview failed/)
      ).toBeVisible()
    }
  )

  it.each([
    '',
    'javascript:alert(1)',
    'data:image/svg+xml,invalid',
    'https://user:secret@example.com/image.png',
  ])(
    'does not create media elements or a preview button for an unsafe URL %s',
    async (source_url) => {
      const { container } = renderLibrary({ source_url })
      expect(await screen.findByText('Portrait')).toBeVisible()
      expect(
        screen.queryByRole('button', { name: 'Preview Portrait' })
      ).not.toBeInTheDocument()
      expect(screen.getByText('Preview unavailable')).toBeVisible()
      expect(container.querySelector('img,video,audio')).toBeNull()
    }
  )
})
