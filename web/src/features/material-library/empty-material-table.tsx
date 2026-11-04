import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import type { ReactNode } from 'react'

import { DataTablePage } from '@/components/data-table'

const emptyRows: Record<string, string>[] = []

// These isolated pages have no data source. Do not invent records or identifiers.
export function EmptyMaterialTable(props: {
  headers: string[]
  title: string
  description: string
  toolbar: ReactNode
}) {
  const columns = props.headers.map((header, index) => ({
    id: String(index),
    header,
  }))
  const table = useReactTable({
    data: emptyRows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <DataTablePage
      table={table}
      columns={columns}
      emptyTitle={props.title}
      emptyDescription={props.description}
      toolbar={props.toolbar}
      showPagination={false}
    />
  )
}
