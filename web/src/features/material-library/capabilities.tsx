import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { MobileCloudConfigSettings } from './mobile-cloud-config'
import { TopenRouterConfigSettings } from './topenrouter-config'

export function MaterialCapabilities() {
  const { t } = useTranslation()
  const [activeProvider, setActiveProvider] = useState('mobile-cloud')
  const [isTesting, setIsTesting] = useState(false)

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Material integrations')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs
          // Keep the active panel mounted when both provider tabs are disabled.
          value={activeProvider}
          onValueChange={(value) => {
            if (isTesting) return
            if (value === 'mobile-cloud' || value === 'topenrouter') {
              setActiveProvider(value)
            }
          }}
          className='mx-auto max-w-3xl gap-4'
        >
          <TabsList aria-label={t('Material integrations')}>
            <TabsTrigger value='mobile-cloud' disabled={isTesting}>
              {t('Mobile Cloud')}
            </TabsTrigger>
            <TabsTrigger value='topenrouter' disabled={isTesting}>
              {t('TopenRouter')}
            </TabsTrigger>
          </TabsList>
          <TabsContent value='mobile-cloud'>
            <MobileCloudConfigSettings onTestingChange={setIsTesting} />
          </TabsContent>
          <TabsContent value='topenrouter'>
            <TopenRouterConfigSettings onTestingChange={setIsTesting} />
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
