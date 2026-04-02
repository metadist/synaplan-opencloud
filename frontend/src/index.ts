import '@opencloud-eu/extension-sdk/tailwind.css'
import {
  defineWebApplication,
  type AppMenuItemExtension,
  type ApplicationInformation
} from '@opencloud-eu/web-pkg'
import { urlJoin } from '@opencloud-eu/web-client'
import translations from '../l10n/translations.json'
import { useGettext } from 'vue3-gettext'
import { computed } from 'vue'
import Synaplan from './views/Synaplan.vue'

const appId = 'synaplan'

export default defineWebApplication({
  setup({ applicationConfig }) {
    const { $gettext } = useGettext()

    const routes = [
      {
        name: `${appId}-index`,
        path: '/',
        component: Synaplan,
        meta: {
          authContext: 'hybrid'
        }
      }
    ]

    const appInfo = {
      name: $gettext('Synaplan'),
      id: appId,
      icon: 'magic-wand'
    } satisfies ApplicationInformation

    const menuItems = computed<AppMenuItemExtension[]>(() => [
      {
        id: `app.${appInfo.id}.menuItem`,
        type: 'appMenuItem',
        label: () => appInfo.name,
        color: '#00b79d',
        icon: appInfo.icon,
        priority: 50,
        path: urlJoin(appInfo.id)
      }
    ])

    return {
      appInfo,
      routes,
      translations,
      extensions: menuItems
    }
  }
})
