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
import { SignUpForm } from './components/sign-up-form'

export function SignUp() {
  const { t } = useTranslation()

  return (
    <AuthLayout variant='sign-up'>
      <div className='w-full'>
        <h2 className='text-[34px] leading-tight font-bold tracking-tight text-[#14213b] dark:text-white'>
          {t('Create an account')}
        </h2>
        <p className='mt-3 mb-7 text-sm leading-6 text-[#8390a5] dark:text-slate-300'>
          {t('Fill in your account details to start using AI models.')}
        </p>
        <SignUpForm />
        <p className='mt-6 border-t border-[#ecf0f5] pt-5 text-center text-sm text-[#8491a5] dark:border-white/10 dark:text-slate-300'>
          {t('Already have an account?')}{' '}
          <Link
            to='/sign-in'
            className='font-semibold text-[#486fe2] hover:underline dark:text-[#9ab7ff]'
          >
            {t('Sign in')}
          </Link>
        </p>
      </div>
    </AuthLayout>
  )
}
