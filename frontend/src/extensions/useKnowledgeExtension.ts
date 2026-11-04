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
 * Adds "Make searchable by AI…" to the file context menu. The dialog
 * lets the person pick a knowledge folder. The file is then found when
 * they ask a question, here or in Synaplan.
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
      title: $gettext('Make searchable by AI'),
      hideActions: true,
      customComponent: KnowledgeDialog,
      customComponentAttrs: () => ({ resource })
    }
    dispatchModal(modal)
  }

  return {
    id: 'com.synaplan.knowledge',
    type: 'action',
    action: {
      name: 'add-to-knowledge',
      icon: 'brain',
      iconFillType: 'line',
      label: () => $gettext('Make searchable by AI…'),
      class: 'oc-files-actions-add-to-knowledge-trigger',
      handler,
      isVisible: (options: FileActionOptions) => isSingleSupportedFile(userStore.user, options)
    }
  }
}
