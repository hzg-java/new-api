import { Link2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { TitledCard } from '@/components/ui/titled-card'
import type { UserProfile } from '@/features/profile/types'
import { ROLE } from '@/lib/roles'

import { AccountActionCard } from './account-action-card'
import { AccountBindings } from './account-bindings'

export function LoginAuthenticationSection(props: {
  profile: UserProfile
  onUpdate: () => void
}) {
  const { t } = useTranslation()
  return (
    <section aria-labelledby='security-authentication' className='space-y-3'>
      <h3 id='security-authentication' className='text-sm font-semibold'>
        {t('Login & Authentication')}
      </h3>
      <AccountActionCard
        action='password'
        username={props.profile.username}
        hasPassword={props.profile.has_password}
        onUpdate={props.onUpdate}
      />
      {props.profile.role >= ROLE.ADMIN && (
        <TitledCard
          title={t('Account Bindings')}
          icon={<Link2 className='size-4' />}
          headerClassName='px-3 py-2.5 !pb-2.5 sm:px-4 sm:py-2.5 sm:!pb-2.5'
          contentClassName='p-3 sm:p-3'
          titleClassName='text-sm sm:text-sm'
          iconClassName='size-7 sm:size-7'
          disableHoverEffect
        >
          <AccountBindings profile={props.profile} onUpdate={props.onUpdate} />
        </TitledCard>
      )}
    </section>
  )
}
