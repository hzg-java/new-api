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
import { BarChart3, KeyRound, PlugZap, UserRoundPlus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'

export function HowItWorks() {
  const { t } = useTranslation()
  const steps = [
    {
      title: t('Create account'),
      desc: t('Sign up and open the dashboard to get started.'),
      icon: <UserRoundPlus className='size-6' strokeWidth={1.7} />,
    },
    {
      title: t('Create API Key'),
      desc: t('Create a token and manage access with existing permissions.'),
      icon: <KeyRound className='size-6' strokeWidth={1.7} />,
    },
    {
      title: t('Connect'),
      desc: t(
        'Connect through OpenAI, Claude, Gemini, and other compatible API routes'
      ),
      icon: <PlugZap className='size-6' strokeWidth={1.7} />,
    },
    {
      title: t('Monitor'),
      desc: t('Review usage, quota and costs in the dashboard.'),
      icon: <BarChart3 className='size-6' strokeWidth={1.7} />,
    },
  ]

  return (
    <section
      id='steps'
      className='relative z-10 scroll-mt-20 bg-[#f4f6ff] px-5 py-20 sm:px-6 md:py-28 dark:bg-[#121b32]'
    >
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='mb-11 text-center md:mb-14'>
          <h2 className='text-3xl font-bold tracking-tight text-slate-900 md:text-4xl dark:text-white'>
            {t('Quick Access')}
          </h2>
          <p className='mt-4 text-sm text-slate-600 dark:text-slate-300'>
            {t('A clear path from account creation to API usage.')}
          </p>
        </AnimateInView>
        <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
          {steps.map((step, index) => (
            <AnimateInView
              key={step.title}
              delay={index * 90}
              animation='fade-up'
              className='rounded-2xl border border-[#e7ebf5] bg-white p-6 shadow-[0_12px_30px_rgba(38,61,118,.04)] dark:border-slate-700 dark:bg-slate-800'
            >
              <span className='text-[11px] font-bold tracking-[.12em] text-[#3468eb] dark:text-blue-400'>
                STEP {String(index + 1).padStart(2, '0')}
              </span>
              <div className='mt-7 flex size-12 items-center justify-center rounded-xl bg-[#edf2ff] text-[#3468eb] dark:bg-blue-500/15 dark:text-blue-300'>
                {step.icon}
              </div>
              <h3 className='mt-6 text-lg font-bold text-slate-900 dark:text-white'>
                {step.title}
              </h3>
              <p className='mt-2 text-sm leading-6 text-slate-600 dark:text-slate-300'>
                {step.desc}
              </p>
            </AnimateInView>
          ))}
        </div>
      </div>
    </section>
  )
}
