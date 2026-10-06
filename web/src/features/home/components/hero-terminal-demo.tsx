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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

type AccentTone = 'emerald' | 'amber' | 'blue' | 'violet'

interface ApiDemoConfig {
  id: string
  label: string
  endpoint: string
  accent: AccentTone
}

const ACCENT_CLASSES: Record<
  AccentTone,
  {
    activeText: string
    activeBorder: string
    badge: string
  }
> = {
  emerald: {
    activeText: 'text-emerald-600 dark:text-emerald-400',
    activeBorder: 'border-emerald-500 dark:border-emerald-400',
    badge:
      'bg-emerald-500/10 text-emerald-600 dark:bg-emerald-400/10 dark:text-emerald-400',
  },
  amber: {
    activeText: 'text-amber-600 dark:text-amber-400',
    activeBorder: 'border-amber-500 dark:border-amber-400',
    badge:
      'bg-amber-500/10 text-amber-600 dark:bg-amber-400/10 dark:text-amber-400',
  },
  blue: {
    activeText: 'text-blue-600 dark:text-blue-400',
    activeBorder: 'border-blue-500 dark:border-blue-400',
    badge:
      'bg-blue-500/10 text-blue-600 dark:bg-blue-400/10 dark:text-blue-400',
  },
  violet: {
    activeText: 'text-violet-600 dark:text-violet-400',
    activeBorder: 'border-violet-500 dark:border-violet-400',
    badge:
      'bg-violet-500/10 text-violet-600 dark:bg-violet-400/10 dark:text-violet-400',
  },
}

const API_DEMOS: ApiDemoConfig[] = [
  {
    id: 'gpt-chat',
    label: 'Chat',
    endpoint: '/v1/chat/completions',
    accent: 'emerald',
  },
  {
    id: 'responses',
    label: 'Responses',
    endpoint: '/v1/responses',
    accent: 'amber',
  },
  {
    id: 'claude',
    label: 'Claude',
    endpoint: '/v1/messages',
    accent: 'blue',
  },
  {
    id: 'gemini',
    label: 'Gemini',
    endpoint: '/v1beta/models/{model}:generateContent',
    accent: 'violet',
  },
]

interface HeroTerminalDemoProps {
  className?: string
}

export function HeroTerminalDemo(props: HeroTerminalDemoProps) {
  const { t } = useTranslation()
  const [activeIndex, setActiveIndex] = useState(0)

  const demo = API_DEMOS[activeIndex]
  const accent = ACCENT_CLASSES[demo.accent]

  return (
    <div className={cn('mx-auto w-full max-w-[880px]', props.className)}>
      <div
        className={cn(
          'overflow-hidden rounded-[22px] border backdrop-blur-sm',
          'border-[#e2e7f4] bg-white/95 shadow-[0_22px_65px_rgba(38,61,118,0.12)]',
          'dark:border-white/[0.06] dark:bg-[#0b0f17]/95 dark:shadow-[0_20px_60px_-25px_rgba(0,0,0,0.7)]'
        )}
      >
        {/* Tab strip */}
        <div
          className={cn(
            'flex items-center gap-1 overflow-x-auto overflow-y-hidden border-b px-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden sm:gap-1.5 sm:px-3',
            'border-border/50 dark:border-white/[0.05]'
          )}
        >
          <span
            aria-hidden='true'
            className='hidden items-center gap-1.5 px-2 sm:flex'
          >
            <span className='size-2 rounded-full bg-[#ffb6b6]' />
            <span className='size-2 rounded-full bg-[#ffd9a1]' />
            <span className='size-2 rounded-full bg-[#b4e6c1]' />
          </span>
          {API_DEMOS.map((item, index) => {
            const tone = ACCENT_CLASSES[item.accent]
            const isActive = index === activeIndex
            return (
              <button
                key={item.id}
                type='button'
                aria-pressed={isActive}
                onClick={() => setActiveIndex(index)}
                className={cn(
                  'relative -mb-px flex items-center gap-1.5 border-b-2 px-2.5 py-2.5 text-[11px] font-medium tracking-wide transition-colors sm:px-3 sm:text-xs',
                  isActive
                    ? `${tone.activeBorder} ${tone.activeText}`
                    : 'text-foreground/40 hover:text-foreground/70 border-transparent'
                )}
              >
                {item.label}
              </button>
            )
          })}
          <div className='ml-auto flex items-center gap-2 pr-2 sm:pr-3'>
            <span className='inline-block size-1.5 rounded-full bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.45)]' />
            <span className='text-foreground/40 font-mono text-[10px] tracking-wider uppercase'>
              {t('API preview')}
            </span>
          </div>
        </div>

        {/* Endpoint + one-line curl */}
        <div className='px-5 py-4'>
          <div className='flex items-center gap-2.5'>
            <span
              className={cn(
                'rounded-md px-1.5 py-0.5 font-mono text-[10px] font-semibold tracking-wider',
                accent.badge
              )}
            >
              POST
            </span>
            <code className='text-foreground/75 truncate font-mono text-[12.5px]'>
              {demo.endpoint}
            </code>
          </div>
          <div className='text-foreground/55 mt-2 font-mono text-[12.5px] break-words whitespace-pre-wrap'>
            <span className='text-emerald-600 dark:text-emerald-400'>curl</span>
            {` -X POST "$OPENAI_BASE_URL${demo.endpoint}" -H "Authorization: Bearer $OPENAI_API_KEY"`}
          </div>
        </div>
      </div>
    </div>
  )
}
