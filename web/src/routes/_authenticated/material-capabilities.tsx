import { createFileRoute, redirect } from '@tanstack/react-router'

import { MaterialCapabilities } from '@/features/material-library/capabilities'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/material-capabilities')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: MaterialCapabilities,
})
