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
import { Activity, BarChart3, ShieldCheck, UsersRound } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'

interface FeaturesProps {
  className?: string
}

export function Features(_props: FeaturesProps) {
  const { t } = useTranslation()
  const features = [
    {
      title: t('Secure & Reliable'),
      desc: t('Access controls and API keys help safeguard every request.'),
      icon: <ShieldCheck className='size-5' strokeWidth={1.8} />,
      tone: 'bg-[#edf2ff] text-[#4c6fe5] dark:bg-blue-500/15 dark:text-blue-300',
    },
    {
      title: t('Stable Routing'),
      desc: t('Channel routing and monitoring support steady model access.'),
      icon: <Activity className='size-5' strokeWidth={1.8} />,
      tone: 'bg-[#eef8f2] text-[#56a671] dark:bg-emerald-500/15 dark:text-emerald-300',
    },
    {
      title: t('Usage Insights'),
      desc: t('Review API usage, quotas and costs in one place.'),
      icon: <BarChart3 className='size-5' strokeWidth={1.8} />,
      tone: 'bg-[#fff8e7] text-[#d89d28] dark:bg-amber-500/15 dark:text-amber-300',
    },
    {
      title: t('Developer Friendly'),
      desc: t(
        'Clear API guidance makes integration and troubleshooting easier.'
      ),
      icon: <UsersRound className='size-5' strokeWidth={1.8} />,
      tone: 'bg-[#edf2ff] text-[#4c6fe5] dark:bg-blue-500/15 dark:text-blue-300',
    },
  ]

  return (
    <section
      id='features'
      className='relative z-10 scroll-mt-20 bg-[#fafbff] px-5 py-20 sm:px-6 md:py-24 dark:bg-[#0d1224]'
    >
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='mb-10 text-center md:mb-12'>
          <h2 className='text-3xl font-bold tracking-tight text-slate-900 md:text-4xl dark:text-white'>
            {t('Our Advantages')}
          </h2>
          <div
            aria-hidden='true'
            className='mx-auto mt-3 h-1 w-16 rounded-full bg-gradient-to-r from-[#5576ed] to-[#8cd8bc]'
          />
          <p className='mt-4 text-sm leading-6 text-slate-600 md:text-base dark:text-slate-300'>
            {t('A unified API gateway for secure, efficient model access.')}
          </p>
        </AnimateInView>
        <div className='grid gap-5 md:grid-cols-2 md:gap-6'>
          {features.map((feature, index) => (
            <AnimateInView
              key={feature.title}
              delay={index * 90}
              animation='fade-up'
              className='flex items-start gap-4 rounded-xl border border-[#e9edf8] bg-white p-6 shadow-[0_4px_12px_rgba(39,55,106,.07)] transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-[#cfdcff] hover:shadow-[0_12px_28px_rgba(39,55,106,.11)] sm:gap-5 sm:p-7 dark:border-slate-700 dark:bg-slate-800'
            >
              <div
                className={`flex size-12 shrink-0 items-center justify-center rounded-xl ${feature.tone}`}
                aria-hidden='true'
              >
                {feature.icon}
              </div>
              <div className='min-w-0 pt-0.5'>
                <h3 className='text-base font-bold text-slate-900 dark:text-white'>
                  {feature.title}
                </h3>
                <p className='mt-2 text-sm leading-6 text-slate-600 dark:text-slate-300'>
                  {feature.desc}
                </p>
              </div>
            </AnimateInView>
          ))}
        </div>
      </div>
    </section>
  )
}
