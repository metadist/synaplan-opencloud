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

const WatchDialog = defineAsyncComponent(() => import('../components/WatchDialog.vue'))

/**
 * Adds "Watch with Synaplan…" to the file and folder context menu.
 * Synaplan then keeps the files searchable in a knowledge folder, or runs
 * one of the person's tasks on new ones. OpenCloud does not tell an
 * extension about file changes, so the Go service checks the watched
 * items and uploads what Synaplan asks for; the dialog can check now and
 * stop the watch.
 */
export const useWatchExtension = (): ActionExtension => {
  const { $gettext } = useGettext()
  const { dispatchModal } = useModals()
  const { showErrorMessage } = useMessages()
  const userStore = useUserStore()

  const handler = ({ resources }: FileActionOptions) => {
    const resource = resources?.[0]
    if (!resource) {
      showErrorMessage({ title: $gettext('No file selected') })
      return
    }

    const modal: Omit<Modal, 'id'> = {
      title: $gettext('Watch with Synaplan'),
      hideActions: true,
      customComponent: WatchDialog,
      customComponentAttrs: () => ({
        resource,
        kind: resource.isFolder ? 'folder' : 'file'
      })
    }
    dispatchModal(modal)
  }

  return {
    id: 'com.synaplan.watch',
    type: 'action',
    action: {
      name: 'watch',
      icon: 'eye',
      iconFillType: 'line',
      label: () => $gettext('Watch with Synaplan…'),
      class: 'oc-files-actions-watch-trigger',
      handler,
      isVisible: ({ resources }: FileActionOptions) => !!userStore.user && resources?.length === 1
    }
  }
}
