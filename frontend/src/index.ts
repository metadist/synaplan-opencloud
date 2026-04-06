import '@opencloud-eu/extension-sdk/tailwind.css'
import {
  defineWebApplication,
  type AppMenuItemExtension,
  type ApplicationInformation,
  type Extension
} from '@opencloud-eu/web-pkg'
import { urlJoin } from '@opencloud-eu/web-client'
import translations from '../l10n/translations.json'
import { useGettext } from 'vue3-gettext'
import { computed } from 'vue'
import Synaplan from './views/Synaplan.vue'
import { useTranslationExtension } from './extensions/useTranslationExtension'
import { useSummarizeExtension } from './extensions/useSummarizeExtension'
import { useKnowledgeExtension } from './extensions/useKnowledgeExtension'

const appId = 'synaplan'

export default defineWebApplication({
  setup({ applicationConfig }) {
    const { $gettext } = useGettext()

    // applicationConfig is the merged config.* block from the
    // extension's manifest.json plus overrides from src/config.json
    // (the latter is git-ignored and provides deployment-specific
    // values). synaplan_url points at the Synaplan install this
    // extension is paired with — used by the Synaplan view to link
    // the headline back to the underlying install.
    const synaplanUrl = (applicationConfig?.synaplan_url as string | undefined) ?? ''

    const routes = [
      {
        name: `${appId}-index`,
        path: '/',
        component: Synaplan,
        props: () => ({ synaplanUrl }),
        meta: {
          authContext: 'hybrid'
        }
      }
    ]

    const appInfo = {
      name: $gettext('Synaplan'),
      id: appId,
      icon: 'magic'
    } satisfies ApplicationInformation

    const translationExtension = useTranslationExtension()
    const summarizeExtension = useSummarizeExtension()
    const knowledgeExtension = useKnowledgeExtension()

    const extensions = computed<Extension[]>(() => [
      {
        id: `app.${appInfo.id}.menuItem`,
        type: 'appMenuItem',
        label: () => appInfo.name,
        color: '#00b79d',
        icon: appInfo.icon,
        priority: 50,
        path: urlJoin(appInfo.id)
      } satisfies AppMenuItemExtension,
      translationExtension,
      summarizeExtension,
      knowledgeExtension
    ])

    return {
      appInfo,
      routes,
      translations,
      extensions
    }
  }
})
