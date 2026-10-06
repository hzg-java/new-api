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
import { Link, useSearch } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { useStatus } from '@/hooks/use-status'

import { AuthLayout } from '../auth-layout'
import { TermsFooter } from '../components/terms-footer'
import { UserAuthForm } from './components/user-auth-form'

export function SignIn() {
  const { t } = useTranslation()
  const { redirect } = useSearch({ from: '/(auth)/sign-in' })
  const { status } = useStatus()

  return (
    <AuthLayout variant='sign-in'>
      <div className='w-full'>
        <p className='text-xs font-bold tracking-[0.16em] text-[#4d73dd] dark:text-[#9ab7ff]'>
          {t('WELCOME BACK')}
        </p>
        <h2 className='mt-3 text-[34px] leading-tight font-bold tracking-tight text-[#14213b] dark:text-white'>
          {t('Welcome back!')}
        </h2>
        <p className='mt-3 mb-7 text-sm leading-6 text-[#8390a5] dark:text-slate-300'>
          {t('Sign in to continue exploring AI models.')}
        </p>
        <UserAuthForm redirectTo={redirect} />
        {!status?.self_use_mode_enabled &&
          status?.register_enabled !== false && (
            <p className='mt-6 border-t border-[#ecf0f5] pt-5 text-center text-sm text-[#8491a5] dark:border-white/10 dark:text-slate-300'>
              {t("Don't have an account?")}{' '}
              <Link
                to='/sign-up'
                className='font-semibold text-[#486fe2] hover:underline dark:text-[#9ab7ff]'
              >
                {t('Sign up')}
              </Link>
            </p>
          )}
        <TermsFooter
          variant='sign-in'
          status={status}
          className='mt-5 text-center'
        />
      </div>
    </AuthLayout>
  )
}
