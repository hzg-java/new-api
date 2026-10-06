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
import { Link } from '@tanstack/react-router'
import { ArrowUpRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'
import { Button } from '@/components/ui/button'

interface CTAProps {
  className?: string
  isAuthenticated?: boolean
}

export function CTA(props: CTAProps) {
  const { t } = useTranslation()

  if (props.isAuthenticated) return null

  return (
    <section className='bg-white px-5 py-16 sm:px-6 md:py-24 dark:bg-[#0d1224]'>
      <AnimateInView
        animation='scale-in'
        className='mx-auto max-w-6xl rounded-[28px] border border-[#e1e9fd] bg-[radial-gradient(circle_at_12%_20%,#e6ebff,transparent_40%),radial-gradient(circle_at_92%_75%,#e2efff,transparent_40%),#f3f6ff] px-6 py-16 text-center md:py-20 dark:border-blue-500/20 dark:bg-[radial-gradient(circle_at_12%_20%,#253155,transparent_40%),#151e36]'
      >
        <h2 className='text-3xl font-bold tracking-tight text-slate-900 md:text-4xl dark:text-white'>
          {t('Ready to simplify')} {t('your AI integration?')}
        </h2>
        <p className='mx-auto mt-5 max-w-xl text-sm leading-7 text-slate-600 md:text-base dark:text-slate-300'>
          {t(
            'Deploy your own gateway and start routing requests through your configured upstream services.'
          )}
        </p>
        <div className='mt-8 flex flex-wrap items-center justify-center gap-3'>
          <Button
            className='h-12 rounded-full bg-[#3468eb] px-7 text-sm font-semibold text-white shadow-[0_10px_22px_rgba(52,104,235,.23)] hover:bg-[#2557d5]'
            render={<Link to='/sign-up' />}
          >
            {t('Sign up')}
            <ArrowUpRight className='size-4' aria-hidden='true' />
          </Button>
        </div>
      </AnimateInView>
    </section>
  )
}
