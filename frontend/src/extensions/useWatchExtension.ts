import {
  type ActionExtension,
  type FileActionOptions,
  useModals,
  useMessages,
  useUserStore
} from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { defineAsyncComponent } from 'vue'

const WatchDialog = defineAsyncComponent(() => import('../components/WatchDialog.vue'))

function oneResource(user: unknown, options: FileActionOptions) {
  if (!user || !options.resources || options.resources.length !== 1) return undefined
  return options.resources[0]
}

/** Watch a folder or a file. New files, or a new version, are handled in Synaplan. */
export const useWatchExtension = (): ActionExtension => {
  const { $gettext } = useGettext()
  const { dispatchModal } = useModals()
  const { showErrorMessage } = useMessages()
  const userStore = useUserStore()

  return {
    id: 'com.synaplan.watch',
    type: 'action',
    action: {
      name: 'watch',
      icon: 'eye',
      iconFillType: 'line',
      label: () => $gettext('Watch with Synaplan…'),
      class: 'oc-files-actions-watch-trigger',
      handler: ({ resources }: FileActionOptions) => {
        const resource = resources?.[0]
        if (!resource) {
          showErrorMessage({ title: $gettext('No file selected') })
          return
        }
        dispatchModal({
          title: $gettext('Watch with Synaplan'),
          hideActions: true,
          customComponent: WatchDialog,
          customComponentAttrs: () => ({
            resource,
            kind: resource.isFolder ? 'folder' : 'file'
          })
        })
      },
      isVisible: (options: FileActionOptions) => !!oneResource(userStore.user, options)
    }
  }
}
