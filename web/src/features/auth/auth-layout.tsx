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
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { useSystemConfig } from '@/hooks/use-system-config'

type AuthLayoutProps = {
  children: React.ReactNode
  variant?: 'default' | 'sign-in' | 'sign-up'
  formLabel?: string
}

export function AuthLayout({
  children,
  variant = 'default',
  formLabel,
}: AuthLayoutProps) {
  const { t } = useTranslation()
  const { systemName, logo, loading } = useSystemConfig()

  if (variant === 'sign-in' || variant === 'sign-up') {
    return (
      <main className='relative flex min-h-svh items-center justify-center overflow-x-clip bg-[#f7f9ff] px-4 py-7 text-[#172339] sm:px-8 sm:py-12 dark:bg-[#101827]'>
        <div
          className='pointer-events-none absolute -top-44 -right-32 h-[32rem] w-[32rem] rounded-full bg-[#d7e5ff] opacity-70 blur-3xl dark:opacity-10'
          aria-hidden='true'
        />
        <div
          className='pointer-events-none absolute -bottom-48 -left-36 h-[34rem] w-[34rem] rounded-full bg-[#e9dfff] opacity-70 blur-3xl dark:opacity-10'
          aria-hidden='true'
        />
        <div className='relative grid w-full max-w-[1120px] overflow-hidden rounded-[24px] border border-white/80 bg-white shadow-[0_36px_90px_rgba(54,84,149,0.12)] md:min-h-[658px] md:grid-cols-[46%_54%] md:rounded-[28px] dark:border-white/10 dark:bg-[#182235]'>
          <section className='relative flex min-h-[168px] flex-col overflow-hidden bg-[linear-gradient(150deg,#172b74_0%,#1c388c_52%,#244cb0_100%)] px-6 pt-6 text-white md:min-h-[658px] md:px-12 md:pt-12 md:pb-9'>
            <div
              className='pointer-events-none absolute top-1/3 -right-32 h-80 w-80 rounded-full bg-[#547eff]/50 blur-3xl'
              aria-hidden='true'
            />
            <Link
              to='/'
              className='relative z-10 flex w-fit items-center gap-3 text-lg font-semibold tracking-tight transition-opacity hover:opacity-80 focus-visible:rounded-lg focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white md:text-xl'
            >
              {loading ? (
                <Skeleton className='h-9 w-9 rounded-xl' />
              ) : (
                <span className='grid h-9 w-9 place-items-center rounded-xl bg-white p-1 shadow-lg'>
                  <img
                    src={logo}
                    alt={t('Logo')}
                    className='h-full w-full rounded-lg object-contain'
                  />
                </span>
              )}
              {loading ? (
                <Skeleton className='h-6 w-24' />
              ) : (
                <span>{systemName}</span>
              )}
            </Link>
            <div className='relative z-10 mt-6 md:mt-14'>
              <h1 className='text-[23px] leading-tight font-bold tracking-tight md:text-4xl md:leading-[1.23]'>
                {t('One gateway, more AI possibilities.')}
              </h1>
              <p className='mt-3 hidden max-w-sm text-sm leading-7 text-[#d4e1ff] md:block'>
                {t('Make model access simpler and every request easier.')}
              </p>
              <div className='mt-6 hidden w-fit rounded-full border border-white/20 bg-white/10 px-3 py-2 text-xs text-[#deebff] md:block'>
                {t('Unified access · Flexible management · Secure control')}
              </div>
              <div className='mt-5 hidden flex-wrap gap-2 text-xs text-[#ecf3ff] md:flex'>
                {[
                  t('Unified API'),
                  t('Multiple models'),
                  t('Usage insights'),
                ].map((item) => (
                  <span
                    key={item}
                    className='rounded-lg border border-white/20 bg-white/10 px-3 py-2'
                  >
                    {item}
                  </span>
                ))}
              </div>
            </div>
            <div
              className='pointer-events-none relative mt-auto hidden min-h-56 place-items-center md:grid'
              aria-hidden='true'
            >
              <div className='absolute h-40 w-[88%] rotate-[-12deg] rounded-[50%] border border-[#acd5ff]/40 shadow-[0_0_0_24px_rgba(98,170,255,0.06),0_0_36px_rgba(99,171,255,0.35)]' />
              <div className='absolute h-32 w-[78%] rotate-[20deg] rounded-[50%] border border-[#9ccfff]/30' />
              <svg
                viewBox='0 0 200 200'
                fill='none'
                className='relative z-10 h-44 w-44 drop-shadow-[0_15px_38px_rgba(113,202,255,0.55)]'
              >
                <defs>
                  <linearGradient
                    id='auth-cube-face'
                    x1='38'
                    y1='34'
                    x2='169'
                    y2='165'
                    gradientUnits='userSpaceOnUse'
                  >
                    <stop stopColor='#D5F7FF' stopOpacity='.91' />
                    <stop offset='.48' stopColor='#5E9DFD' stopOpacity='.72' />
                    <stop offset='1' stopColor='#5172EE' stopOpacity='.58' />
                  </linearGradient>
                </defs>
                <path
                  d='M100 30 158 60v70l-58 34-58-34V60l58-30Z'
                  fill='url(#auth-cube-face)'
                  fillOpacity='.55'
                  stroke='#D8F6FF'
                  strokeWidth='2.5'
                />
                <path
                  d='M42 60 100 95l58-35M100 95v69'
                  stroke='#C7EEFF'
                  strokeOpacity='.85'
                  strokeWidth='2'
                />
                <path
                  d='M100 54 137 74v43l-37 23-37-23V74l37-20Z'
                  fill='#98D7FF'
                  fillOpacity='.24'
                  stroke='#CAFCFF'
                  strokeWidth='2'
                />
                <path
                  d='m63 74 37 23 37-23M100 97v43'
                  stroke='#BDE9FF'
                  strokeWidth='1.5'
                />
                <path
                  d='m88 85 12-7 12 7v14l-12 7-12-7V85Z'
                  fill='#D9FBFF'
                  fillOpacity='.85'
                />
              </svg>
            </div>
          </section>
          <section
            className='flex min-w-0 items-center justify-center bg-white px-6 py-9 sm:px-10 md:px-9 lg:px-16 dark:bg-[#182235]'
            aria-label={
              formLabel ?? t(variant === 'sign-up' ? 'Sign up' : 'Sign in')
            }
          >
            <div className='w-full max-w-[390px]'>{children}</div>
          </section>
        </div>
      </main>
    )
  }

  return (
    <div className='relative grid h-svh max-w-none'>
      <Link
        to='/'
        className='absolute top-4 left-4 z-10 flex items-center gap-2 transition-opacity hover:opacity-80 sm:top-8 sm:left-8'
      >
        <div className='relative h-8 w-8'>
          {loading ? (
            <Skeleton className='absolute inset-0 rounded-full' />
          ) : (
            <img
              src={logo}
              alt={t('Logo')}
              className='h-8 w-8 rounded-full object-cover'
            />
          )}
        </div>
        {loading ? (
          <Skeleton className='h-6 w-24' />
        ) : (
          <h1 className='text-xl font-medium'>{systemName}</h1>
        )}
      </Link>
      <div className='container flex items-center pt-16 sm:pt-0'>
        <div className='mx-auto flex w-full flex-col justify-center space-y-2 px-4 py-8 sm:w-[480px] sm:p-8'>
          {children}
        </div>
      </div>
    </div>
  )
}
