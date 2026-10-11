import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { AffiliateRewardsCard } from '../components/affiliate-rewards-card'
import { RechargeFormCard } from '../components/recharge-form-card'
import type { UserWalletData } from '../types'

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

test('without compliance fields referral rewards can be transferred', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  const transfer = vi.fn()
  render(
    <AffiliateRewardsCard
      user={{ aff_quota: 100 } as UserWalletData}
      affiliateLink='https://example.com/invite'
      onTransfer={transfer}
    />
  )
  await userEvent
    .setup()
    .click(screen.getByRole('button', { name: 'Transfer to Balance' }))
  expect(transfer).toHaveBeenCalledOnce()
})

test.each([1, 10, 100])(
  'referral program visibility follows role %s',
  (role) => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'preview', role })
    render(
      <AffiliateRewardsCard
        user={null}
        affiliateLink=''
        onTransfer={() => {}}
      />
    )
    if (role === 1) {
      expect(screen.queryByText('Referral Program')).not.toBeInTheDocument()
      expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    } else {
      expect(screen.getByText('Referral Program')).toBeInTheDocument()
    }
  }
)

test.each([false, true])(
  'redemption pending=%s preserves the pending guard without compliance fields',
  async (redeeming) => {
    const redeem = vi.fn()
    render(
      <RechargeFormCard
        topupInfo={null}
        presetAmounts={[]}
        selectedPreset={null}
        onSelectPreset={() => {}}
        topupAmount={1}
        onTopupAmountChange={() => {}}
        paymentAmount={1}
        calculating={false}
        onPaymentMethodSelect={() => {}}
        paymentLoading={null}
        redemptionCode='code'
        onRedemptionCodeChange={() => {}}
        onRedeem={redeem}
        redeeming={redeeming}
      />
    )
    expect(screen.getByRole('textbox', { name: 'Have a Code?' })).toHaveValue(
      'code'
    )
    const button = screen.getByRole('button', { name: 'Redeem' })
    if (redeeming) {
      expect(button).toBeDisabled()
      return
    }
    await userEvent.setup().click(button)
    expect(redeem).toHaveBeenCalledOnce()
  }
)
