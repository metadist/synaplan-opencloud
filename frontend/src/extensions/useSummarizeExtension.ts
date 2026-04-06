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
import { isSingleSupportedFile } from './supportedMimes'

// Lazy so the dialog + its deps don't land in the main bundle.
const SummarizeDialog = defineAsyncComponent(() => import('../components/SummarizeDialog.vue'))

/**
 * Adds a "Summarize with Synaplan…" entry to the file context menu.
 * Clicking opens a modal where the user picks a summary type and
 * length; the modal calls /api/synaplan/summarize.
 */
export const useSummarizeExtension = (): ActionExtension => {
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
      title: $gettext('Summarize with Synaplan'),
      hideActions: true,
      customComponent: SummarizeDialog,
      customComponentAttrs: () => ({ resource })
    }
    dispatchModal(modal)
  }

  return {
    id: 'com.synaplan.summarize',
    type: 'action',
    extensionPointIds: ['global.files.context-actions'],
    action: {
      name: 'summarize',
      icon: 'file-list-3',
      iconFillType: 'line',
      label: () => $gettext('Summarize with Synaplan…'),
      class: 'oc-files-actions-summarize-trigger',
      handler,
      isVisible: (options: FileActionOptions) => isSingleSupportedFile(userStore.user, options)
    }
  }
}
