import type { Resource } from '@opencloud-eu/web-client'
import type { SidebarPanelExtension } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import FileSidebar from '../components/FileSidebar.vue'
import { useSynaplanBrandIcon } from '../composables/useSynaplanBrandIcon'

/** The Synaplan tab in the file sidebar: who made it searchable, and how to remove it. */
export const useSidebarExtension = (): SidebarPanelExtension<Resource, Resource, Resource> => {
  const { $gettext } = useGettext()

  return {
    id: 'com.synaplan.sidebar',
    type: 'sidebarPanel',
    extensionPointIds: ['global.files.sidebar'],
    panel: {
      name: 'synaplan',
      icon: useSynaplanBrandIcon(),
      title: () => $gettext('Synaplan'),
      isVisible: (context) => (context.items?.length ?? 0) === 1,
      component: FileSidebar,
      componentAttrs: (context) => ({ resource: context.items?.[0] })
    }
  }
}
