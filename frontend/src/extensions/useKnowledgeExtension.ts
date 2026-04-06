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

const KnowledgeDialog = defineAsyncComponent(() => import('../components/KnowledgeDialog.vue'))

/**
 * Adds an "Add to Synaplan knowledge…" entry to the file context
 * menu. The dialog lets the user pick (or create) a knowledge
 * group, and the backend uploads the file to Synaplan with
 * process_level=vectorize for RAG-powered retrieval.
 */
export const useKnowledgeExtension = (): ActionExtension => {
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
      title: $gettext('Add to Synaplan knowledge'),
      hideActions: true,
      customComponent: KnowledgeDialog,
      customComponentAttrs: () => ({ resource })
    }
    dispatchModal(modal)
  }

  return {
    id: 'com.synaplan.knowledge',
    type: 'action',
    extensionPointIds: ['global.files.context-actions'],
    action: {
      name: 'add-to-knowledge',
      icon: 'brain',
      iconFillType: 'line',
      label: () => $gettext('Add to Synaplan knowledge…'),
      class: 'oc-files-actions-add-to-knowledge-trigger',
      handler,
      isVisible: (options: FileActionOptions) => isSingleSupportedFile(userStore.user, options)
    }
  }
}
