import { useTranslation } from 'react-i18next'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { BillingHistoryCard } from '@/features/wallet/components/billing-history-card'

interface UserTopupRecordsDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: { id: number; username: string }
}

export function UserTopupRecordsDrawer(props: UserTopupRecordsDrawerProps) {
  const { t } = useTranslation()

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-4xl')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle className='pr-8 break-words'>
            {t('Top-up records')} · {props.user.username}
          </SheetTitle>
          <SheetDescription>
            {t('ID')}: {props.user.id}
          </SheetDescription>
        </SheetHeader>
        <div className={sideDrawerFormClassName()}>
          {props.open && (
            <BillingHistoryCard key={props.user.id} userId={props.user.id} />
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
