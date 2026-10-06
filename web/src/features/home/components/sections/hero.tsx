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
import ClaudeIcon from '@lobehub/icons/es/Claude/components/Mono.js'
import DeepSeekIcon from '@lobehub/icons/es/DeepSeek/components/Mono.js'
import GeminiIcon from '@lobehub/icons/es/Gemini/components/Color.js'
import MetaIcon from '@lobehub/icons/es/Meta/components/Mono.js'
import OpenAIIcon from '@lobehub/icons/es/OpenAI/components/Mono.js'
import QwenIcon from '@lobehub/icons/es/Qwen/components/Mono.js'
import { Link } from '@tanstack/react-router'
import { ArrowUpRight } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { HeroTerminalDemo } from '../hero-terminal-demo'

interface HeroProps {
  className?: string
  isAuthenticated?: boolean
}

export function Hero(props: HeroProps) {
  const { t } = useTranslation()
  const [activeEcosystem, setActiveEcosystem] = useState<number | null>(null)
  const ecosystems = [
    {
      name: 'OpenAI',
      color: 'bg-[#111827] hover:bg-[#111827] dark:hover:bg-[#111827]',
      icon: OpenAIIcon,
    },
    {
      name: 'Claude',
      color: 'bg-[#d97757] hover:bg-[#d97757] dark:hover:bg-[#d97757]',
      icon: ClaudeIcon,
    },
    {
      name: 'Gemini',
      color: 'bg-[#e8edff] hover:bg-[#e8edff] dark:hover:bg-[#e8edff]',
      icon: GeminiIcon,
    },
    {
      name: 'DeepSeek',
      color: 'bg-[#4d6bfe] hover:bg-[#4d6bfe] dark:hover:bg-[#4d6bfe]',
      icon: DeepSeekIcon,
    },
    {
      name: 'Qwen',
      color: 'bg-[#615ced] hover:bg-[#615ced] dark:hover:bg-[#615ced]',
      icon: QwenIcon,
    },
    {
      name: 'Llama',
      color: 'bg-[#1d65c1] hover:bg-[#1d65c1] dark:hover:bg-[#1d65c1]',
      icon: MetaIcon,
    },
  ]

  return (
    <section
      className='relative isolate overflow-hidden bg-[#fafaff] px-5 pt-32 pb-20 text-center sm:px-6 md:pt-40 md:pb-24 dark:bg-[#0d1224]'
      data-testid='landing-hero'
    >
      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 -z-10 bg-[radial-gradient(circle_at_12%_32%,#e9eaff_0,transparent_30%),radial-gradient(circle_at_86%_15%,#e1eeff_0,transparent_30%),linear-gradient(180deg,#f7f8ff_0%,#fafaff_80%)] dark:bg-[radial-gradient(circle_at_12%_32%,#252d55_0,transparent_30%),radial-gradient(circle_at_86%_15%,#193359_0,transparent_30%)]'
      />
      <div className='mx-auto max-w-6xl'>
        <h1 className='text-[clamp(2.5rem,6vw,4.75rem)] leading-[1.13] font-extrabold tracking-tight text-slate-900 dark:text-white'>
          <span className='text-[#3468eb] dark:text-blue-400'>
            {t('One-stop access to all-model APIs')}
          </span>
        </h1>
        <p className='mx-auto mt-6 max-w-2xl text-sm leading-7 text-slate-600 sm:text-base sm:leading-8 dark:text-slate-300'>
          {t(
            'Unified access to multiple models makes AI app development easier.'
          )}
        </p>
        <div className='mt-8 flex flex-wrap items-center justify-center gap-3'>
          <Button
            className='h-12 rounded-full bg-[#3468eb] px-7 text-sm font-semibold text-white shadow-[0_10px_22px_rgba(52,104,235,.23)] hover:bg-[#2557d5]'
            render={
              <Link to={props.isAuthenticated ? '/dashboard' : '/sign-up'} />
            }
          >
            {props.isAuthenticated ? t('Go to Dashboard') : t('Sign up')}
            <ArrowUpRight className='size-4' aria-hidden='true' />
          </Button>
        </div>
        <div className='mx-auto mt-14 max-w-[880px] text-left md:mt-16'>
          <HeroTerminalDemo />
        </div>
        <div className='mx-auto mt-12 max-w-3xl' id='models'>
          <p className='sr-only'>{t('Multiple model ecosystems')}</p>
          <div
            role='group'
            aria-label={t('Multiple model ecosystems')}
            className='mx-auto grid h-[126px] max-w-[600px] grid-cols-6 items-center justify-items-center sm:h-[168px]'
          >
            {ecosystems.map(({ name, color, icon: Icon }, index) => {
              const active = activeEcosystem === index
              return (
                <Tooltip key={name} open={active}>
                  <TooltipTrigger
                    render={
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        aria-label={name}
                        aria-pressed={active}
                        onClick={() => setActiveEcosystem(index)}
                        onMouseEnter={() => setActiveEcosystem(index)}
                        onMouseLeave={() => setActiveEcosystem(null)}
                        onFocus={() => setActiveEcosystem(index)}
                        onBlur={() => setActiveEcosystem(null)}
                        className={`relative flex size-[clamp(2.75rem,12vw,4.5rem)] items-center justify-center rounded-xl p-0 shadow-[0_10px_22px_rgba(54,72,139,.12)] transition-[transform,box-shadow] duration-200 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500 sm:size-[88px] ${color} ${active ? '-translate-y-3 scale-[1.35] shadow-[0_16px_35px_rgba(54,72,139,.22)] sm:-translate-y-4' : ''}`}
                        style={{
                          zIndex: active ? ecosystems.length + 1 : index + 1,
                        }}
                      />
                    }
                  >
                    <Icon
                      aria-hidden='true'
                      className={
                        name === 'Gemini'
                          ? 'size-[70%]'
                          : 'size-[70%] text-white'
                      }
                    />
                  </TooltipTrigger>
                  <TooltipContent className='text-xs font-semibold sm:text-sm'>
                    {name}
                  </TooltipContent>
                </Tooltip>
              )
            })}
          </div>
        </div>
      </div>
    </section>
  )
}
