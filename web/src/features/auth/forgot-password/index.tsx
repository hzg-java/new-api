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

import { AuthLayout } from '../auth-layout'
import { ForgotPasswordForm } from './components/forgot-password-form'

export function ForgotPassword() {
  const { t } = useTranslation()
  return (
    <AuthLayout variant='sign-in' formLabel={t('Forgot password')}>
      <div className='w-full space-y-8'>
        <div className='space-y-3'>
          <h2 className='text-[34px] leading-tight font-bold tracking-tight text-[#14213b] dark:text-white'>
            {t('Forgot password')}
          </h2>
          <p className='text-sm leading-6 text-[#8390a5] dark:text-slate-300'>
            {t(
              'Enter your registered email and we will send you a link to reset your password.'
            )}
          </p>
          <p className='text-sm leading-6 text-[#8390a5] dark:text-slate-300'>
            {t("Don't have an account?")}{' '}
            <Link
              to='/sign-up'
              className='font-semibold text-[#486fe2] hover:underline dark:text-[#9ab7ff]'
            >
              {t('Sign up')}
            </Link>
            .
          </p>
        </div>

        <ForgotPasswordForm />
        <div className='border-t border-[#ecf0f5] pt-5 text-center dark:border-white/10'>
          <Link
            to='/sign-in'
            className='text-sm font-semibold text-[#486fe2] hover:underline dark:text-[#9ab7ff]'
          >
            {t('Back to login')}
          </Link>
        </div>
      </div>
    </AuthLayout>
  )
}
