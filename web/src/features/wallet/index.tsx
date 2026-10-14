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
import { MessageCircle } from 'lucide-react'
import { useState, useEffect, useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { getSelf } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'

import { AffiliateRewardsCard } from './components/affiliate-rewards-card'
import { BillingHistoryCard } from './components/billing-history-card'
import { TransferDialog } from './components/dialogs/transfer-dialog'
import { SubscriptionPlansCard } from './components/subscription-plans-card'
import { WalletStatsCard } from './components/wallet-stats-card'
import { useAffiliate } from './hooks/use-affiliate'
import { useTopupInfo } from './hooks/use-topup-info'
import type { UserWalletData } from './types'

interface WalletProps {
  initialShowHistory?: boolean
}

export function Wallet(props: WalletProps) {
  const { t } = useTranslation()
  const [user, setUser] = useState<UserWalletData | null>(null)
  const [userLoading, setUserLoading] = useState(true)
  const [transferDialogOpen, setTransferDialogOpen] = useState(false)
  const historyRef = useRef<HTMLDivElement>(null)
  const { topupInfo } = useTopupInfo()
  const {
    affiliateLink,
    loading: affiliateLoading,
    transferQuota,
    transferring,
  } = useAffiliate()

  const fetchUser = useCallback(async () => {
    try {
      setUserLoading(true)
      const response = await getSelf()
      if (response.success && response.data) {
        setUser(response.data as UserWalletData)
      }
    } catch (error) {
      handleServerError(error)
    } finally {
      setUserLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchUser()
  }, [fetchUser])

  useEffect(() => {
    if (props.initialShowHistory) {
      historyRef.current?.scrollIntoView({ block: 'start' })
    }
  }, [props.initialShowHistory])

  const handleTransfer = async (amount: number) => {
    const success = await transferQuota(amount)
    if (success) await fetchUser()
    return success
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Wallet')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
            <WalletStatsCard user={user} loading={userLoading} />
            <Alert className='border-primary/20 bg-primary/5 p-4 sm:p-5'>
              <MessageCircle aria-hidden='true' />
              <AlertTitle className='text-base font-semibold'>
                {t('Please contact the administrator to top up')}
              </AlertTitle>
              <AlertDescription>
                {t(
                  'To add funds to your account, please contact the administrator.'
                )}
              </AlertDescription>
            </Alert>
            <div
              ref={historyRef}
              id='wallet-history'
              className='min-w-0 scroll-mt-4'
            >
              <BillingHistoryCard />
            </div>
            <SubscriptionPlansCard
              topupInfo={topupInfo}
              userQuota={user?.quota}
              onPurchaseSuccess={fetchUser}
            />
            <AffiliateRewardsCard
              user={user}
              affiliateLink={affiliateLink}
              onTransfer={() => setTransferDialogOpen(true)}
              complianceConfirmed={
                topupInfo?.payment_compliance_confirmed !== false
              }
              loading={affiliateLoading}
            />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <TransferDialog
        open={transferDialogOpen}
        onOpenChange={setTransferDialogOpen}
        onConfirm={handleTransfer}
        availableQuota={user?.aff_quota ?? 0}
        transferring={transferring}
      />
    </>
  )
}
