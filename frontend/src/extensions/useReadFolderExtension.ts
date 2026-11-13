import {
  type ActionExtension,
  type FileActionOptions,
  type Modal,
  useModals,
  useMessages,
  useUserStore
} from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { defineAsyncComponent } from 'vue'

const ReadFolderDialog = defineAsyncComponent(() => import('../components/ReadFolderDialog.vue'))

/**
 * Adds "Let Synaplan read this folder…" to the folder context menu.
 * A task in Synaplan can ask this OpenCloud for the files in a folder
 * (a folder read, or "pickup"); the Go service answers only for folders
 * the person allowed, folders they watch, or everything if they allowed
 * that. Synaplan keeps the setting; the dialog shows it for one folder
 * and allows or stops reading it.
 */
export const useReadFolderExtension = (): ActionExtension => {
  const { $gettext } = useGettext()
  const { dispatchModal } = useModals()
  const { showErrorMessage } = useMessages()
  const userStore = useUserStore()

  const handler = ({ resources }: FileActionOptions) => {
    const resource = resources?.[0]
    if (!resource) {
      showErrorMessage({ title: $gettext('No folder selected') })
      return
    }

    const modal: Omit<Modal, 'id'> = {
      title: $gettext('Let Synaplan read this folder'),
      hideActions: true,
      customComponent: ReadFolderDialog,
      customComponentAttrs: () => ({ resource })
    }
    dispatchModal(modal)
  }

  return {
    id: 'com.synaplan.read-folder',
    type: 'action',
    action: {
      name: 'read-folder',
      icon: 'folder-open',
      iconFillType: 'line',
      label: () => $gettext('Let Synaplan read this folder…'),
      class: 'oc-files-actions-read-folder-trigger',
      handler,
      isVisible: ({ resources }: FileActionOptions) =>
        !!userStore.user && resources?.length === 1 && !!resources[0].isFolder
    }
  }
}
