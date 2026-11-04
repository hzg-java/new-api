import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { MobileCloudConfigSettings } from './mobile-cloud-config'
import { TopenRouterConfigSettings } from './topenrouter-config'

export function MaterialCapabilities() {
  const { t } = useTranslation()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Material integrations')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs defaultValue='mobile-cloud' className='mx-auto max-w-3xl gap-4'>
          <TabsList aria-label={t('Material integrations')}>
            <TabsTrigger value='mobile-cloud'>{t('Mobile Cloud')}</TabsTrigger>
            <TabsTrigger value='topenrouter'>{t('TopenRouter')}</TabsTrigger>
          </TabsList>
          <TabsContent value='mobile-cloud'>
            <MobileCloudConfigSettings />
          </TabsContent>
          <TabsContent value='topenrouter'>
            <TopenRouterConfigSettings />
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
