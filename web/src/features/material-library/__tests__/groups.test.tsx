import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AddMaterialDialog } from '../add-material-dialog'
import * as api from '../api'
import { BindingDetails } from '../binding-details'
import { ChangeMaterialGroupDialog } from '../change-material-group-dialog'
import { MaterialLibrary } from '../index'
import { ManageGroupsDialog } from '../manage-groups-dialog'
import { MaterialGroupSelect } from '../material-groups'
import { MaterialRecords } from '../records'
import type { Material } from '../types'

vi.mock('../api', () => ({
  getMaterials: vi.fn(),
  addMaterial: vi.fn(),
  deleteMaterial: vi.fn(),
  getMaterialGroups: vi.fn(),
  createMaterialGroup: vi.fn(),
  renameMaterialGroup: vi.fn(),
  retryMaterialGroup: vi.fn(),
  deleteMaterialGroup: vi.fn(),
  updateMaterialGroup: vi.fn(),
}))

const group = {
  id: 3,
  user_id: 7,
  name: 'Campaign',
  created_time: 1,
  updated_time: 1,
}
const material = {
  id: 1,
  material_id: 'mat_group',
  user_id: 7,
  name: 'Portrait',
  library_type: 'AIGC',
  file_type: 'Image',
  source_url: 'https://example.com/a.png',
  status: 'pending',
  fail_reason: '',
  can_retry: false,
  created_time: 1,
  updated_time: 1,
  group_id: 3,
  group_name: 'Campaign',
} satisfies Material

const clients: QueryClient[] = []
function renderPage(ui: React.ReactElement = <MaterialLibrary />) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  })
  clients.push(client)
  const view = render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
  return { ...view, client, user: userEvent.setup() }
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.getMaterials).mockResolvedValue({
    success: true,
    data: [material],
    total: 1,
  })
  vi.mocked(api.getMaterialGroups).mockResolvedValue({
    success: true,
    data: [group],
  })
})
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  vi.restoreAllMocks()
})

describe('owner-targeted material grouping', () => {
  it.each([
    ['updating', 'Updating...'],
    ['rejected', 'Failed'],
    ['retired', 'Previous'],
  ] as const)(
    'labels backend %s bindings without exposing raw state codes',
    (state, label) => {
      renderPage(
        <BindingDetails
          binding={{
            id: 1,
            channel_id: 42,
            channel_name: 'Full channel name',
            provider: 'mobile_cloud',
            group_id: 'remote-group',
            asset_id: 'remote-asset',
            state,
            upstream_status: '',
            fail_reason: '',
            can_retry: false,
            updated_time: 1700000000,
          }}
        />
      )
      expect(screen.getByText(label)).toBeVisible()
      expect(screen.queryByText(state, { exact: true })).not.toBeInTheDocument()
    }
  )

  it('shows real upstream group mappings and queues explicit sync retries', async () => {
    const failedGroup = {
      ...group,
      status: 'failed',
      fail_reason: 'Safe sync failure',
      deleting: false,
      bindings: [
        {
          id: 1,
          channel_id: 42,
          channel_name: 'Full channel name',
          provider: 'mobile_cloud',
          upstream_id: 'real-upstream-group',
          name: 'Campaign',
          state: 'failed',
          fail_reason: 'Safe binding failure',
          updated_time: 1700000000,
        },
      ],
    }
    vi.mocked(api.getMaterialGroups).mockResolvedValue({
      success: true,
      data: [failedGroup],
    } as never)
    const retry = vi.spyOn(api, 'retryMaterialGroup').mockResolvedValue({
      success: true,
      data: { ...failedGroup, status: 'pending' },
    } as never)
    const { user } = renderPage(
      <ManageGroupsDialog open onOpenChange={vi.fn()} targetUserId={7} />
    )
    expect(await screen.findByText('real-upstream-group')).toBeVisible()
    expect(screen.getByText(/Full channel name/)).toBeVisible()
    expect(screen.getByText('Safe binding failure')).toBeVisible()
    await user.click(
      screen.getByRole('button', { name: 'Retry synchronization' })
    )
    await waitFor(() => expect(retry).toHaveBeenCalledWith(3, 7))
  })

  it('confirms group deletion and preserves a nonempty group with the server reason', async () => {
    const remove = vi.spyOn(api, 'deleteMaterialGroup').mockResolvedValue({
      success: false,
      message: 'Only empty groups can be deleted',
    })
    const { user } = renderPage(
      <ManageGroupsDialog open onOpenChange={vi.fn()} targetUserId={7} />
    )
    await user.click(
      await screen.findByRole('button', { name: 'Delete group: Campaign' })
    )
    expect(remove).not.toHaveBeenCalled()
    const confirm = within(
      screen.getByRole('alertdialog', { name: 'Delete material group' })
    )
    await user.click(confirm.getByRole('button', { name: 'Delete' }))
    expect(await confirm.findByRole('alert')).toHaveTextContent(
      'Only empty groups can be deleted'
    )
    expect(confirm.getByRole('button', { name: 'Delete' })).toBeEnabled()
    expect(remove).toHaveBeenCalledWith(3, 7)
  })

  it.each(['pending', 'processing'] as const)(
    'polls %s groups until synchronization settles and stops polling after unmount',
    async (status) => {
      vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
      vi.mocked(api.getMaterialGroups).mockResolvedValue({
        success: true,
        data: [{ ...group, status }],
      })
      const { unmount } = renderPage(
        <ManageGroupsDialog open onOpenChange={vi.fn()} targetUserId={7} />
      )
      try {
        await screen.findByText(
          status === 'pending' ? 'Pending preparation' : 'Preparing'
        )
        vi.mocked(api.getMaterialGroups).mockResolvedValue({
          success: true,
          data: [{ ...group, status: 'ready' }],
        } as never)
        await act(async () => {
          await vi.advanceTimersByTimeAsync(5000)
        })
        expect(await screen.findByText('Ready')).toBeVisible()
        expect(api.getMaterialGroups).toHaveBeenCalledTimes(2)
        unmount()
        await act(async () => {
          await vi.advanceTimersByTimeAsync(10000)
        })
        expect(api.getMaterialGroups).toHaveBeenCalledTimes(2)
      } finally {
        vi.useRealTimers()
      }
    }
  )

  it('queues group deletion, blocks duplicate actions and retries a failed deletion via DELETE', async () => {
    const deleting = { ...group, deleting: true, status: 'processing' as const }
    vi.mocked(api.deleteMaterialGroup).mockResolvedValue({
      success: true,
      data: deleting,
    })
    const { user, client } = renderPage(
      <ManageGroupsDialog open onOpenChange={vi.fn()} targetUserId={7} />
    )
    await user.click(
      await screen.findByRole('button', { name: 'Delete group: Campaign' })
    )
    vi.mocked(api.getMaterialGroups).mockResolvedValue({
      success: true,
      data: [deleting],
    })
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    expect(await screen.findByText('Deleting')).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Delete group: Campaign' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Rename group: Campaign' })
    ).toBeDisabled()
    act(() =>
      client.setQueryData(
        ['material-library', 'groups', 'admin', 7],
        [
          {
            ...deleting,
            status: 'failed',
            fail_reason: 'Safe deletion failure',
          },
        ]
      )
    )
    expect(await screen.findByText('Safe deletion failure')).toBeVisible()
    const remove = screen.getByRole('button', {
      name: 'Delete group: Campaign',
    })
    expect(remove).toHaveTextContent('Retry deletion')
    expect(remove).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: 'Retry synchronization' })
    ).not.toBeInTheDocument()
    await user.click(remove)
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    await waitFor(() =>
      expect(api.deleteMaterialGroup).toHaveBeenCalledTimes(2)
    )
    expect(api.retryMaterialGroup).not.toHaveBeenCalled()
  })

  it('loads only the selected owner directory when a reusable selector changes owner', async () => {
    const selfGroup = { ...group, name: 'My group' }
    const otherGroup = { ...group, user_id: 8, name: 'Other owner group' }
    vi.mocked(api.getMaterialGroups).mockImplementation(
      async (targetUserId) => ({
        success: true,
        data: [targetUserId === 7 ? group : otherGroup],
      })
    )
    const onChange = vi.fn()
    const { client, rerender, user } = renderPage(
      <MaterialGroupSelect value={null} onChange={onChange} targetUserId={7} />
    )
    client.setQueryData(['material-library', 'groups'], [selfGroup])
    await user.click(
      await screen.findByRole('combobox', { name: 'Material group' })
    )
    expect(
      await screen.findByRole('option', { name: 'Campaign' })
    ).toBeVisible()
    expect(
      screen.queryByRole('option', { name: 'My group' })
    ).not.toBeInTheDocument()
    await user.keyboard('{Escape}')

    rerender(
      <QueryClientProvider client={client}>
        <MaterialGroupSelect
          value={null}
          onChange={onChange}
          targetUserId={8}
        />
      </QueryClientProvider>
    )
    await user.click(
      await screen.findByRole('combobox', { name: 'Material group' })
    )
    await user.click(
      await screen.findByRole('option', { name: 'Other owner group' })
    )

    expect(onChange).toHaveBeenCalledExactlyOnceWith(3)
    expect(api.getMaterialGroups).toHaveBeenNthCalledWith(1, 7)
    expect(api.getMaterialGroups).toHaveBeenNthCalledWith(2, 8)
    expect(client.getQueryData(['material-library', 'groups'])).toEqual([
      selfGroup,
    ])
    expect(
      client.getQueryData(['material-library', 'groups', 'admin', 7])
    ).toEqual([group])
    expect(
      client.getQueryData(['material-library', 'groups', 'admin', 8])
    ).toEqual([otherGroup])
  })

  it('propagates the target owner to the add dialog directory and submission', async () => {
    vi.mocked(api.addMaterial).mockResolvedValue({
      success: true,
      data: material,
    })
    const onOpenChange = vi.fn()
    const { user } = renderPage(
      <AddMaterialDialog open onOpenChange={onOpenChange} targetUserId={7} />
    )
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    await user.type(within(dialog).getByLabelText('Material name'), 'Portrait')
    await user.type(
      within(dialog).getByLabelText('Material URL'),
      'https://example.com/a.png'
    )
    await user.click(
      await within(dialog).findByRole('combobox', { name: 'Material group' })
    )
    await user.click(await screen.findByRole('option', { name: 'Campaign' }))
    await user.click(
      within(dialog).getByRole('button', { name: 'Submit material' })
    )

    await waitFor(() =>
      expect(api.addMaterial).toHaveBeenCalledExactlyOnceWith(
        {
          name: 'Portrait',
          library_type: 'AIGC',
          file_type: 'Image',
          source_url: 'https://example.com/a.png',
          group_id: 3,
        },
        7
      )
    )
    expect(api.getMaterialGroups).toHaveBeenCalledExactlyOnceWith(7)
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('propagates the target owner to group assignment and preserves the failed selection for retry', async () => {
    vi.mocked(api.updateMaterialGroup)
      .mockResolvedValueOnce({ success: false, message: 'Move rejected' })
      .mockResolvedValueOnce({
        success: true,
        data: { ...material, group_id: null, group_name: '' },
      })
    const onOpenChange = vi.fn()
    const { user } = renderPage(
      <ChangeMaterialGroupDialog
        material={material}
        open
        onOpenChange={onOpenChange}
        targetUserId={7}
      />
    )
    const dialog = screen.getByRole('dialog', { name: 'Change material group' })
    await user.click(
      await within(dialog).findByRole('combobox', { name: 'Material group' })
    )
    await user.click(await screen.findByRole('option', { name: 'Ungrouped' }))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'Move rejected'
    )
    expect(
      within(dialog).getByRole('combobox', { name: 'Material group' })
    ).toHaveTextContent('Ungrouped')
    expect(onOpenChange).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(api.updateMaterialGroup).toHaveBeenNthCalledWith(
      1,
      1,
      { group_id: null },
      7
    )
    expect(api.updateMaterialGroup).toHaveBeenNthCalledWith(
      2,
      1,
      { group_id: null },
      7
    )
    expect(api.getMaterialGroups).toHaveBeenCalledExactlyOnceWith(7)
  })

  it('creates groups in the target directory without changing or invalidating self and other-owner directories', async () => {
    vi.mocked(api.getMaterialGroups).mockResolvedValue({
      success: true,
      data: [],
    })
    vi.mocked(api.createMaterialGroup).mockResolvedValue({
      success: true,
      data: group,
    })
    const { client, user } = renderPage(
      <ManageGroupsDialog open onOpenChange={vi.fn()} targetUserId={7} />
    )
    const selfKey = ['material-library', 'groups']
    const otherKey = ['material-library', 'groups', 'admin', 8]
    const selfGroup = { ...group, name: 'My group' }
    const otherGroup = { ...group, user_id: 8, name: 'Other owner group' }
    client.setQueryData(selfKey, [selfGroup])
    client.setQueryData(otherKey, [otherGroup])
    const dialog = screen.getByRole('dialog', {
      name: 'Manage material groups',
    })
    await within(dialog).findByText('No material groups yet')
    await user.type(within(dialog).getByLabelText('Group name'), ' Campaign ')
    vi.mocked(api.getMaterialGroups).mockRejectedValue(new Error('Offline'))
    await user.click(
      within(dialog).getByRole('button', { name: 'Create group' })
    )

    await within(dialog).findByText('We could not load material groups.')
    expect(api.createMaterialGroup).toHaveBeenCalledExactlyOnceWith(
      { name: 'Campaign' },
      7
    )
    expect(
      client.getQueryData(['material-library', 'groups', 'admin', 7])
    ).toEqual([group])
    expect(client.getQueryData(selfKey)).toEqual([selfGroup])
    expect(client.getQueryData(otherKey)).toEqual([otherGroup])
    expect(client.getQueryState(selfKey)?.isInvalidated).toBe(false)
    expect(client.getQueryState(otherKey)?.isInvalidated).toBe(false)
  })

  it.each([undefined, 7])(
    'renames owner=%s without cancelling or invalidating other directories and patches both material caches by owner and group',
    async (targetUserId) => {
      const saved = { ...group, name: 'Renamed' }
      const selfGroup = { ...group, name: 'My group' }
      const otherGroup = { ...group, user_id: 8, name: 'Other owner group' }
      vi.mocked(api.renameMaterialGroup).mockResolvedValue({
        success: true,
        data: saved,
      })
      const { client, user } = renderPage(
        <ManageGroupsDialog
          open
          onOpenChange={vi.fn()}
          targetUserId={targetUserId}
        />
      )
      const ownerKey =
        targetUserId === undefined
          ? ['material-library', 'groups']
          : ['material-library', 'groups', 'admin', 7]
      const untouchedKeys = [
        ['material-library', 'groups', 'admin', 8],
        targetUserId === undefined
          ? ['material-library', 'groups', 'admin', 7]
          : ['material-library', 'groups'],
      ]
      client.setQueryData(untouchedKeys[0], [otherGroup])
      client.setQueryData(untouchedKeys[1], [selfGroup])
      let finishOtherRead!: (groups: (typeof group)[]) => void
      const otherRead = client.fetchQuery({
        queryKey: untouchedKeys[0],
        staleTime: 0,
        queryFn: () =>
          new Promise<(typeof group)[]>((resolve) => {
            finishOtherRead = resolve
          }),
      })
      expect(client.getQueryState(untouchedKeys[0])?.fetchStatus).toBe(
        'fetching'
      )
      const pages = ['materials', 'all-materials'].map((scope) => [
        'material-library',
        scope,
        'cached',
      ])
      const otherOwnerMaterial = {
        ...material,
        id: 2,
        user_id: 8,
        group_name: 'Other owner group',
      }
      const otherGroupMaterial = {
        ...material,
        id: 3,
        group_id: 4,
        group_name: 'Another group',
      }
      for (const key of pages) {
        client.setQueryData(key, {
          materials: [material, otherOwnerMaterial, otherGroupMaterial],
          total: 3,
        })
      }
      const dialog = screen.getByRole('dialog', {
        name: 'Manage material groups',
      })
      await user.click(
        await within(dialog).findByRole('button', {
          name: 'Rename group: Campaign',
        })
      )
      await user.clear(within(dialog).getByLabelText('Group name'))
      await user.type(within(dialog).getByLabelText('Group name'), 'Renamed')
      vi.mocked(api.getMaterialGroups).mockRejectedValue(new Error('Offline'))
      await user.click(
        within(dialog).getByRole('button', { name: 'Save group' })
      )

      await within(dialog).findByText('We could not load material groups.')
      expect(api.renameMaterialGroup).toHaveBeenCalledExactlyOnceWith(
        3,
        { name: 'Renamed' },
        targetUserId
      )
      expect(client.getQueryData(ownerKey)).toEqual([saved])
      expect(client.getQueryData(untouchedKeys[0])).toEqual([otherGroup])
      expect(client.getQueryData(untouchedKeys[1])).toEqual([selfGroup])
      for (const key of untouchedKeys) {
        expect(client.getQueryState(key)?.isInvalidated).toBe(false)
      }
      expect(client.getQueryState(untouchedKeys[0])?.fetchStatus).toBe(
        'fetching'
      )
      for (const key of pages) {
        expect(client.getQueryData(key)).toEqual({
          materials: [
            { ...material, group_name: 'Renamed' },
            otherOwnerMaterial,
            otherGroupMaterial,
          ],
          total: 3,
        })
        expect(client.getQueryState(key)?.isInvalidated).toBe(true)
      }
      await act(async () => {
        finishOtherRead([otherGroup])
        await otherRead
      })
    }
  )
})

describe('reusable grouping components (not a user route)', () => {
  it('filters all, ungrouped and a specific group with the API contract', async () => {
    const { user } = renderPage()
    expect(await screen.findByText('Campaign')).toBeVisible()
    const select = await screen.findByRole('combobox', {
      name: 'Material group',
    })
    await user.click(select)
    await user.click(await screen.findByRole('option', { name: 'Ungrouped' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ group_id: 0, offset: 0 })
      )
    )
    await user.click(select)
    await user.click(await screen.findByRole('option', { name: 'Campaign' }))
    await waitFor(() =>
      expect(api.getMaterials).toHaveBeenLastCalledWith(
        expect.objectContaining({ group_id: 3, offset: 0 })
      )
    )
    await user.click(select)
    await user.click(await screen.findByRole('option', { name: 'All groups' }))
    await waitFor(() => expect(select).toHaveTextContent('All groups'))
    expect(api.getMaterials).toHaveBeenCalledWith(
      expect.objectContaining({ group_id: undefined })
    )
  })

  it('submits a selected group when adding a material', async () => {
    vi.mocked(api.addMaterial).mockResolvedValue({
      success: true,
      data: material,
    })
    const { user } = renderPage()
    await user.click(screen.getByRole('button', { name: 'Add material' }))
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    await user.type(
      within(dialog).getByLabelText('Material name'),
      'New portrait'
    )
    await user.type(
      within(dialog).getByLabelText('Material URL'),
      'https://example.com/b.png'
    )
    await user.click(
      await within(dialog).findByRole('combobox', { name: 'Material group' })
    )
    await user.click(await screen.findByRole('option', { name: 'Campaign' }))
    await user.click(
      within(dialog).getByRole('button', { name: 'Submit material' })
    )
    await waitFor(() =>
      expect(api.addMaterial).toHaveBeenCalledWith(
        expect.objectContaining({ group_id: 3 }),
        undefined
      )
    )
  })

  it('refreshes both lists and totals after adding while initial reads are pending, ignoring their late snapshots', async () => {
    let finishInitialRead!: (
      value: Awaited<ReturnType<typeof api.getMaterials>>
    ) => void
    const initialRead = new Promise<
      Awaited<ReturnType<typeof api.getMaterials>>
    >((resolve) => {
      finishInitialRead = resolve
    })
    const added = {
      ...material,
      id: 2,
      name: 'New portrait',
      material_id: 'mat_new',
    }
    vi.mocked(api.getMaterials)
      .mockReturnValueOnce(initialRead)
      .mockReturnValueOnce(initialRead)
      .mockResolvedValue({ success: true, data: [added, material], total: 2 })
    vi.mocked(api.addMaterial).mockResolvedValue({ success: true, data: added })
    const { user } = renderPage(
      <>
        <section aria-label='Reusable material component'>
          <MaterialLibrary />
        </section>
        <section aria-label='All material records'>
          <MaterialRecords />
        </section>
      </>
    )
    const library = within(
      screen.getByRole('region', { name: 'Reusable material component' })
    )
    const records = within(
      screen.getByRole('region', { name: 'All material records' })
    )
    await waitFor(() => expect(api.getMaterials).toHaveBeenCalledTimes(2))
    await user.click(library.getByRole('button', { name: 'Add material' }))
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    await user.type(within(dialog).getByLabelText('Material name'), added.name)
    await user.type(
      within(dialog).getByLabelText('Material URL'),
      added.source_url
    )
    await user.click(
      within(dialog).getByRole('button', { name: 'Submit material' })
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )

    for (const page of [library, records]) {
      expect(await page.findByText('New portrait')).toBeVisible()
      expect(page.getByText('2')).toBeVisible()
    }
    await act(async () => {
      finishInitialRead({ success: true, data: [material], total: 1 })
      await initialRead
    })
    for (const page of [library, records]) {
      expect(page.getByText('New portrait')).toBeVisible()
      expect(page.getByText('2')).toBeVisible()
    }
  })

  it('shows a retryable directory failure without pretending it is empty and still adds ungrouped', async () => {
    vi.mocked(api.getMaterialGroups).mockRejectedValue(new Error('Offline'))
    vi.mocked(api.addMaterial).mockResolvedValue({
      success: true,
      data: { ...material, group_id: null, group_name: '' },
    })
    const { user } = renderPage()
    await screen.findByText('We could not load material groups.')
    expect(
      screen.queryByRole('combobox', { name: 'Material group' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add material' }))
    const dialog = screen.getByRole('dialog', { name: 'Add material' })
    expect(
      await within(dialog).findByText('We could not load material groups.')
    ).toBeVisible()
    await user.type(within(dialog).getByLabelText('Material name'), 'Legacy')
    await user.type(
      within(dialog).getByLabelText('Material URL'),
      'https://example.com/b.png'
    )
    await user.click(
      within(dialog).getByRole('button', { name: 'Submit material' })
    )
    await waitFor(() =>
      expect(api.addMaterial).toHaveBeenCalledWith(
        expect.objectContaining({ group_id: null }),
        undefined
      )
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    vi.mocked(api.getMaterialGroups).mockResolvedValue({
      success: true,
      data: [group],
    })
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByRole('combobox', { name: 'Material group' })
    ).toBeVisible()
  })

  it('creates trimmed groups, validates unicode length and preserves a server field error', async () => {
    vi.mocked(api.getMaterialGroups).mockResolvedValue({
      success: true,
      data: [],
    })
    vi.mocked(api.createMaterialGroup)
      .mockResolvedValueOnce({ success: false, message: 'Name already exists' })
      .mockResolvedValue({ success: true, data: group })
    const { user } = renderPage()
    await user.click(screen.getByRole('button', { name: 'Manage groups' }))
    const dialog = screen.getByRole('dialog', {
      name: 'Manage material groups',
    })
    expect(
      await within(dialog).findByText('No material groups yet')
    ).toBeVisible()
    const input = within(dialog).getByLabelText('Group name')
    await user.type(input, '   ')
    await user.click(
      within(dialog).getByRole('button', { name: 'Create group' })
    )
    expect(
      await within(dialog).findByText(
        'Group name must contain 1 to 64 characters.'
      )
    ).toBeVisible()
    expect(api.createMaterialGroup).not.toHaveBeenCalled()
    await user.clear(input)
    await user.type(input, '界'.repeat(65))
    await user.click(
      within(dialog).getByRole('button', { name: 'Create group' })
    )
    expect(api.createMaterialGroup).not.toHaveBeenCalled()
    await user.clear(input)
    await user.type(input, '  Campaign  ')
    await user.click(
      within(dialog).getByRole('button', { name: 'Create group' })
    )
    expect(await within(dialog).findByText('Name already exists')).toBeVisible()
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveValue('  Campaign  ')
    vi.mocked(api.getMaterialGroups).mockResolvedValue({
      success: true,
      data: [group],
    })
    await user.click(
      within(dialog).getByRole('button', { name: 'Create group' })
    )
    await waitFor(() =>
      expect(api.createMaterialGroup).toHaveBeenLastCalledWith(
        { name: 'Campaign' },
        undefined
      )
    )
    expect(await within(dialog).findByText('Campaign')).toBeVisible()
  })

  it('does not clear the existing group while the directory is loading or failed', async () => {
    let finish!: (
      value: Awaited<ReturnType<typeof api.getMaterialGroups>>
    ) => void
    vi.mocked(api.getMaterialGroups).mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      })
    )
    const { user } = renderPage()
    await user.click(
      await screen.findByRole('button', { name: 'Change group' })
    )
    const dialog = screen.getByRole('dialog', { name: 'Change material group' })
    expect(within(dialog).getByText(/Current group: Campaign/)).toBeVisible()
    expect(within(dialog).getByText('Loading material groups...')).toBeVisible()
    expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled()
    await act(async () => finish({ success: false, message: 'Offline' }))
    expect(
      await within(dialog).findByText('We could not load material groups.')
    ).toBeVisible()
    expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(api.updateMaterialGroup).not.toHaveBeenCalled()
    vi.mocked(api.getMaterialGroups).mockResolvedValue({
      success: true,
      data: [group],
    })
    await user.click(within(dialog).getByRole('button', { name: 'Retry' }))
    expect(
      await within(dialog).findByRole('combobox', { name: 'Material group' })
    ).toHaveTextContent('Campaign')
  })

  it('moves a material to ungrouped and refreshes inactive admin caches', async () => {
    const { user, client } = renderPage()
    client.setQueryData(['material-library', 'all-materials', 'cached'], {
      materials: [material],
      total: 1,
    })
    vi.mocked(api.updateMaterialGroup).mockResolvedValue({
      success: true,
      data: { ...material, group_id: null, group_name: '' },
    })
    await user.click(
      await screen.findByRole('button', { name: 'Change group' })
    )
    const dialog = screen.getByRole('dialog', { name: 'Change material group' })
    await user.click(
      await within(dialog).findByRole('combobox', { name: 'Material group' })
    )
    await user.click(await screen.findByRole('option', { name: 'Ungrouped' }))
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [{ ...material, group_id: null, group_name: '' }],
      total: 1,
    })
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(api.updateMaterialGroup).toHaveBeenCalledWith(
        1,
        { group_id: null },
        undefined
      )
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(await screen.findByText('Ungrouped')).toBeVisible()
    expect(
      client.getQueryState(['material-library', 'all-materials', 'cached'])
        ?.isInvalidated
    ).toBe(true)
  })

  it('keeps a failed group move open with the selected value for retry', async () => {
    vi.mocked(api.updateMaterialGroup).mockResolvedValue({
      success: false,
      message: 'Move rejected',
    })
    const { user } = renderPage()
    await user.click(
      await screen.findByRole('button', { name: 'Change group' })
    )
    const dialog = screen.getByRole('dialog', { name: 'Change material group' })
    await user.click(
      await within(dialog).findByRole('combobox', { name: 'Material group' })
    )
    await user.click(await screen.findByRole('option', { name: 'Ungrouped' }))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'Move rejected'
    )
    expect(
      within(dialog).getByRole('combobox', { name: 'Material group' })
    ).toHaveTextContent('Ungrouped')
    expect(within(dialog).getByRole('button', { name: 'Save' })).toBeEnabled()
  })

  it('renames a group and updates both mounted lists and their already-open admin detail', async () => {
    vi.mocked(api.renameMaterialGroup).mockResolvedValue({
      success: true,
      data: { ...group, name: 'Renamed' },
    })
    const { user, container } = renderPage(
      <>
        <section aria-label='Reusable material component'>
          <MaterialLibrary />
        </section>
        <MaterialRecords />
      </>
    )
    await screen.findAllByText('Campaign')
    await user.click(screen.getAllByRole('button', { name: 'Details' })[1])
    const drawer = screen.getByRole('dialog', { name: 'Material details' })
    expect(within(drawer).getByText('Campaign')).toBeVisible()
    // The two views can share a QueryClient even when one has an open drawer.
    const selfLibrary = within(container).getByRole('region', {
      name: 'Reusable material component',
      hidden: true,
    })
    act(() => {
      ;(
        within(selfLibrary).getByRole('button', {
          name: 'Manage groups',
          hidden: true,
        }) as HTMLButtonElement
      ).click()
    })
    const manager = await screen.findByRole('dialog', {
      name: 'Manage material groups',
    })
    await user.click(
      await within(manager).findByRole('button', {
        name: 'Rename group: Campaign',
      })
    )
    const input = within(manager).getByLabelText('Group name')
    await user.clear(input)
    await user.type(input, 'Renamed')
    vi.mocked(api.getMaterialGroups).mockResolvedValue({
      success: true,
      data: [{ ...group, name: 'Renamed' }],
    })
    vi.mocked(api.getMaterials).mockResolvedValue({
      success: true,
      data: [{ ...material, group_name: 'Renamed' }],
      total: 1,
    })
    await user.click(
      within(manager).getByRole('button', { name: 'Save group' })
    )
    await waitFor(() =>
      expect(api.renameMaterialGroup).toHaveBeenCalledWith(
        3,
        { name: 'Renamed' },
        undefined
      )
    )
    await waitFor(() =>
      expect(within(drawer).queryByText('Campaign')).not.toBeInTheDocument()
    )
    expect(within(drawer).getByText('Renamed')).toBeInTheDocument()
    expect(api.getMaterials).toHaveBeenCalledWith(
      expect.objectContaining({ scope: 'all' })
    )
    expect(container.querySelectorAll('td')).not.toHaveLength(0)
    expect(within(container).queryByText('Campaign')).not.toBeInTheDocument()
  })

  it('retains the renamed group in shared caches and an open user detail when reloads fail', async () => {
    vi.mocked(api.renameMaterialGroup).mockResolvedValue({
      success: true,
      data: { ...group, name: 'Renamed' },
    })
    const { user, client, container } = renderPage()
    const adminKey = ['material-library', 'all-materials', 'inactive']
    client.setQueryData(adminKey, { materials: [material], total: 1 })
    await user.click(await screen.findByRole('button', { name: 'Details' }))
    const drawer = screen.getByRole('dialog', { name: 'Material details' })
    act(() => {
      ;(
        within(container).getByRole('button', {
          name: 'Manage groups',
          hidden: true,
        }) as HTMLButtonElement
      ).click()
    })
    const manager = await screen.findByRole('dialog', {
      name: 'Manage material groups',
    })
    await user.click(
      await within(manager).findByRole('button', {
        name: 'Rename group: Campaign',
      })
    )
    await user.clear(within(manager).getByLabelText('Group name'))
    await user.type(within(manager).getByLabelText('Group name'), 'Renamed')
    vi.mocked(api.getMaterialGroups).mockRejectedValue(new Error('Offline'))
    vi.mocked(api.getMaterials).mockRejectedValue(new Error('Offline'))
    await user.click(
      within(manager).getByRole('button', { name: 'Save group' })
    )
    await waitFor(() =>
      expect(within(drawer).getByText('Renamed')).toBeInTheDocument()
    )
    expect(client.getQueryData(['material-library', 'groups'])).toEqual([
      { ...group, name: 'Renamed' },
    ])
    expect(client.getQueryData(adminKey)).toEqual({
      materials: [{ ...material, group_name: 'Renamed' }],
      total: 1,
    })
    expect(client.getQueryState(adminKey)?.isInvalidated).toBe(true)
  })

  it('validates group name length by unicode characters rather than UTF-16 units', async () => {
    const name = '\u{20000}'.repeat(64)
    vi.mocked(api.createMaterialGroup).mockResolvedValue({
      success: true,
      data: { ...group, name },
    })
    const { user } = renderPage()
    await user.click(screen.getByRole('button', { name: 'Manage groups' }))
    const manager = screen.getByRole('dialog', {
      name: 'Manage material groups',
    })
    await user.type(within(manager).getByLabelText('Group name'), name)
    await user.click(
      within(manager).getByRole('button', { name: 'Create group' })
    )
    await waitFor(() =>
      expect(api.createMaterialGroup).toHaveBeenCalledWith({ name }, undefined)
    )
  })

  it('blocks duplicate saves and dismissal while renaming a group', async () => {
    let finish!: (
      value: Awaited<ReturnType<typeof api.renameMaterialGroup>>
    ) => void
    vi.mocked(api.renameMaterialGroup).mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      })
    )
    const { user } = renderPage()
    await user.click(screen.getByRole('button', { name: 'Manage groups' }))
    const manager = screen.getByRole('dialog', {
      name: 'Manage material groups',
    })
    await user.click(
      await within(manager).findByRole('button', {
        name: 'Rename group: Campaign',
      })
    )
    await user.click(
      within(manager).getByRole('button', { name: 'Save group' })
    )
    expect(
      await within(manager).findByRole('button', { name: 'Saving...' })
    ).toBeDisabled()
    expect(within(manager).getByLabelText('Group name')).toBeDisabled()
    expect(
      within(manager).getByRole('button', { name: 'Cancel rename' })
    ).toBeDisabled()
    await user.keyboard('{Escape}')
    expect(manager).toBeVisible()
    expect(api.renameMaterialGroup).toHaveBeenCalledTimes(1)
    await act(async () => finish({ success: true, data: group }))
    expect(
      await within(manager).findByRole('button', { name: 'Create group' })
    ).toBeEnabled()
  })

  it('blocks duplicate moves and dismissal while saving a material group', async () => {
    let finish!: (
      value: Awaited<ReturnType<typeof api.updateMaterialGroup>>
    ) => void
    vi.mocked(api.updateMaterialGroup).mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      })
    )
    const { user } = renderPage()
    await user.click(
      await screen.findByRole('button', { name: 'Change group' })
    )
    const dialog = screen.getByRole('dialog', { name: 'Change material group' })
    await within(dialog).findByRole('combobox', { name: 'Material group' })
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(
      await within(dialog).findByRole('button', { name: 'Saving...' })
    ).toBeDisabled()
    expect(
      within(dialog).getByRole('button', { name: 'Cancel' })
    ).toBeDisabled()
    expect(
      within(dialog).getByRole('combobox', { name: 'Material group' })
    ).toBeDisabled()
    await user.keyboard('{Escape}')
    expect(dialog).toBeVisible()
    expect(api.updateMaterialGroup).toHaveBeenCalledTimes(1)
    await act(async () => finish({ success: true, data: material }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })

  it('shows admin group names without requesting the current user group directory', async () => {
    const { user } = renderPage(<MaterialRecords />)
    expect(await screen.findByText('Campaign')).toBeVisible()
    expect(api.getMaterialGroups).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Details' }))
    expect(
      within(
        screen.getByRole('dialog', { name: 'Material details' })
      ).getByText('Campaign')
    ).toBeVisible()
  })

  it.each([false, true])(
    'wraps long group names and exposes actions on mobile=%s',
    async (mobile) => {
      const original = window.matchMedia
      vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
        ...original(query),
        matches:
          query === '(max-width: 640px)' ? mobile : original(query).matches,
      }))
      const name = 'LongGroup'.repeat(7)
      vi.mocked(api.getMaterials).mockResolvedValue({
        success: true,
        data: [{ ...material, group_name: name }],
        total: 1,
      })
      const { user } = renderPage()
      const label = await screen.findByText(name)
      expect(label).toHaveClass('whitespace-normal', 'break-all', 'max-w-48')
      expect(screen.getByRole('button', { name: 'Change group' })).toBeVisible()
      await user.click(screen.getByRole('button', { name: 'Details' }))
      expect(
        within(
          screen.getByRole('dialog', { name: 'Material details' })
        ).getByText(name)
      ).toBeVisible()
    }
  )
})
