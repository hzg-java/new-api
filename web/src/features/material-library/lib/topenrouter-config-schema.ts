import { z } from 'zod'

export const TOPENROUTER_DEFAULT_ENDPOINT =
  'https://tp-api.chinadatapay.com:8000'

export function topenRouterConfigSchema(credentials: {
  token_configured: boolean
}) {
  return z.object({
    enabled: z.boolean(),
    channel_id: z
      .number()
      .int('Select a valid channel ID.')
      .positive('Select a valid channel ID.'),
    endpoint: z
      .string()
      .trim()
      .refine((value) => {
        if (!URL.canParse(value)) return false
        const url = new URL(value)
        return (
          url.protocol === 'https:' &&
          /^https:\/\//i.test(value) &&
          !url.username &&
          !url.password &&
          !/^https:\/\/[^/?#]*@/i.test(value) &&
          !value.includes('#')
        )
      }, 'Enter an HTTPS URL without credentials or a fragment.'),
    token: z
      .string()
      .trim()
      .refine(
        (value) => Boolean(value || credentials.token_configured),
        'Token is required for initial setup.'
      ),
  })
}

export type TopenRouterConfigFormValues = z.infer<
  ReturnType<typeof topenRouterConfigSchema>
>
