import {
  type ActionExtension,
  type FileActionOptions,
  useModals,
  useMessages,
  useUserStore
} from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { defineAsyncComponent } from 'vue'

const FromSynaplanDialog = defineAsyncComponent(
  () => import('../components/FromSynaplanDialog.vue')
)

/** Copies a Synaplan file into the folder that is open. */
export const useFromSynaplanExtension = (): ActionExtension => {
  const { $gettext } = useGettext()
  const { dispatchModal } = useModals()
  const { showErrorMessage } = useMessages()
  const userStore = useUserStore()

  return {
    id: 'com.synaplan.from',
    type: 'action',
    action: {
      name: 'from-synaplan',
      icon: 'folder-shared',
      iconFillType: 'line',
      label: () => $gettext('From Synaplan…'),
      class: 'oc-files-actions-from-synaplan-trigger',
      isExternal: true,
      handler: ({ resources }: FileActionOptions) => {
        const resource = resources?.[0]
        if (!resource) {
          showErrorMessage({ title: $gettext('No file selected') })
          return
        }
        dispatchModal({
          title: $gettext('From Synaplan'),
          hideActions: true,
          customComponent: FromSynaplanDialog,
          customComponentAttrs: () => ({ resource })
        })
      },
      isVisible: (options: FileActionOptions) => {
        if (!userStore.user || !options.resources || options.resources.length !== 1) return false
        return options.resources[0].isFolder
      }
    }
  }
}
