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

    // synaplanUrl points at the Synaplan install this extension is
    // paired with. Operators set it via OpenCloud's apps.yaml; the
    // default in manifest.json is empty so the link/heading just
    // hides itself when nothing is configured.
    const synaplanUrl = (applicationConfig?.synaplanUrl as string | undefined) ?? ''

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

    const menuItem: AppMenuItemExtension = {
      id: `app.${appInfo.id}.menuItem`,
      type: 'appMenuItem',
      label: () => appInfo.name,
      color: '#00b79d',
      icon: appInfo.icon,
      priority: 50,
      ...(synaplanUrl ? { url: synaplanUrl } : { path: urlJoin(appInfo.id) })
    }

    const extensions = computed<Extension[]>(() => [
      menuItem,
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
