/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Markdown } from '@/components/ui/markdown'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { cn } from '@/lib/utils'

import { apiDocument } from './api-document'
import { CodeExample } from './code-example'

const [introduction, ...sections] = apiDocument.split(/^## /m)
const documentSections = sections.map((section, index) => {
  const newline = section.indexOf('\n')
  const body = section.slice(newline + 1)
  const endpoint = /```text\n(GET|POST) (\S+)\n```/.exec(body)
  return {
    id: `api-section-${index + 1}`,
    title: section.slice(0, newline),
    method: endpoint?.[1],
    url: endpoint?.[2],
    parts: body.split(/(```[^\n]*\n[\s\S]*?```)/g).filter(Boolean),
  }
})

export function DocsContent() {
  const { t } = useTranslation()
  const [activeId, setActiveId] = useState(() => {
    const id = window.location.hash.slice(1)
    return (
      documentSections.find((section) => section.id === id)?.id ??
      documentSections[0].id
    )
  })
  useEffect(() => {
    const onHashChange = () => {
      const id = window.location.hash.slice(1)
      setActiveId(
        documentSections.find((section) => section.id === id)?.id ??
          documentSections[0].id
      )
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])
  const activeSection =
    documentSections.find((section) => section.id === activeId) ??
    documentSections[0]
  const selectSection = (id: string) => {
    setActiveId(id)
    window.history.replaceState(null, '', `#${id}`)
  }

  return (
    <div className='mx-auto max-w-7xl pb-12'>
      <header className='mb-8 border-b pb-6'>
        <Markdown className='[&_blockquote]:rounded-lg [&_blockquote]:border-blue-500 [&_blockquote]:bg-blue-500/5 [&_blockquote]:px-4 [&_blockquote]:py-3 [&_h1]:text-3xl [&_h1]:tracking-tight'>
          {introduction}
        </Markdown>
      </header>
      <div className='grid items-start gap-8 lg:grid-cols-[240px_minmax(0,1fr)]'>
        <aside className='lg:sticky lg:top-24'>
          <NativeSelect
            aria-label={t('Docs')}
            value={activeId}
            onChange={(event) => selectSection(event.target.value)}
            className='mb-4 w-full lg:hidden'
          >
            {documentSections.map((section) => (
              <NativeSelectOption key={section.id} value={section.id}>
                {section.title}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <nav aria-label={t('Docs')} className='hidden space-y-2 lg:block'>
            <p className='text-muted-foreground mb-4 px-3 text-xs font-semibold tracking-widest'>
              {t('Docs')}
            </p>
            {documentSections.map((section) => (
              <Button
                key={section.id}
                variant='ghost'
                aria-current={activeId === section.id ? 'page' : undefined}
                onClick={() => selectSection(section.id)}
                className={cn(
                  'h-auto w-full justify-start gap-3 rounded-lg px-3 py-3 text-left text-sm',
                  activeId === section.id &&
                    'bg-primary/10 text-primary hover:bg-primary/15'
                )}
              >
                <span className='min-w-0 flex-1'>
                  {section.title.replace(/^\d+\. /, '')}
                </span>
                {section.method && (
                  <span
                    className={cn(
                      'text-[10px] font-bold',
                      section.method === 'GET'
                        ? 'text-blue-600 dark:text-blue-400'
                        : 'text-emerald-700 dark:text-emerald-400'
                    )}
                  >
                    {section.method}
                  </span>
                )}
              </Button>
            ))}
          </nav>
        </aside>
        <article
          lang='zh-CN'
          className='bg-card min-w-0 rounded-2xl border shadow-sm'
        >
          <div className='bg-muted/30 border-b px-5 py-6 sm:px-8'>
            <h2 className='text-2xl font-semibold tracking-tight'>
              {activeSection.title.replace(/^\d+\. /, '')}
            </h2>
            {activeSection.url && (
              <div className='bg-background mt-4 flex min-w-0 items-start gap-3 rounded-lg border p-3'>
                <span
                  className={cn(
                    'rounded px-2 py-1 font-mono text-xs font-bold',
                    activeSection.method === 'GET'
                      ? 'bg-blue-500/10 text-blue-700 dark:text-blue-400'
                      : 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
                  )}
                >
                  {activeSection.method}
                </span>
                <code className='min-w-0 flex-1 pt-1 font-mono text-sm break-all'>
                  {activeSection.url.replace('https://www.zongsiai.vip', '')}
                </code>
                <CopyButton value={activeSection.url} size='sm' />
              </div>
            )}
          </div>
          <div key={activeSection.id} className='space-y-5 px-5 py-6 sm:px-8'>
            {activeSection.parts.map((part) => {
              const code = /^```([^\n]*)\n([\s\S]*?)```$/.exec(part)
              if (!code) {
                return (
                  <Markdown
                    key={part}
                    className='[&_th]:border-border/60 [&_th]:bg-muted/60 [&_td]:border-border/60 [&_blockquote]:rounded-lg [&_blockquote]:border-amber-500 [&_blockquote]:bg-amber-500/5 [&_blockquote]:px-4 [&_blockquote]:py-3 [&_h3]:mt-8 [&_h3]:text-base [&_h3]:font-semibold [&_table]:overflow-x-auto [&_table]:rounded-lg [&_td]:py-3 [&_td]:align-top [&_td_code]:whitespace-nowrap [&_td:last-child]:min-w-64 [&_td:last-child]:whitespace-normal [&_td:not(:last-child)]:whitespace-nowrap [&_th]:whitespace-nowrap'
                  >
                    {part}
                  </Markdown>
                )
              }
              const value = code[2].replace(/\n$/, '')
              return <CodeExample key={part} code={value} language={code[1]} />
            })}
          </div>
        </article>
      </div>
    </div>
  )
}
