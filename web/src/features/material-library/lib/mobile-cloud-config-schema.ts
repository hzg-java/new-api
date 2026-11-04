import { z } from 'zod'

export function mobileCloudConfigSchema(credentials: {
  access_key_masked: string
  secret_key_configured: boolean
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
          (url.protocol === 'http:' || url.protocol === 'https:') &&
          !url.username &&
          !url.password
        )
      }, 'Enter an HTTP or HTTPS URL without embedded credentials.'),
    access_key: z
      .string()
      .trim()
      .refine(
        (value) => Boolean(value || credentials.access_key_masked),
        'Access key is required for initial setup.'
      ),
    secret_key: z
      .string()
      .trim()
      .refine(
        (value) => Boolean(value || credentials.secret_key_configured),
        'Secret key is required for initial setup.'
      ),
    pool_id: z.string().trim().min(1, 'Pool-Id is required.'),
  })
}

export type MobileCloudConfigFormValues = z.infer<
  ReturnType<typeof mobileCloudConfigSchema>
>
