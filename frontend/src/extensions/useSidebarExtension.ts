import type { Resource } from '@opencloud-eu/web-client'
import type { SidebarPanelExtension } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import FileSidebar from '../components/FileSidebar.vue'
import { cloudFilesEnabled } from '../composables/cloudStatus'
import { useSynaplanBrandIcon } from '../composables/useSynaplanBrandIcon'

/**
 * Synaplan panel in OpenCloud's file sidebar (`global.files.sidebar`):
 * whether the file is searchable by AI, in which knowledge folder, and a
 * way to remove it. Shown for one selected file, and only once cloud/status
 * has answered that cloud file state is on. Folders wait until watches exist.
 */
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
      isVisible: (context) => {
        if (cloudFilesEnabled.value !== true) return false
        const items = context.items ?? []
        if (items.length !== 1) return false
        const item = items[0]
        return item.isFolder !== true && item.type !== 'folder'
      },
      component: FileSidebar,
      componentAttrs: (context) => ({ resource: context.items?.[0] })
    }
  }
}
