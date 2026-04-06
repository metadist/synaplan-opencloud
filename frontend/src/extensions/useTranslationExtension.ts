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
const TranslationDialog = defineAsyncComponent(() => import('../components/TranslationDialog.vue'))

/**
 * Adds a "Translate with Synaplan…" entry to the file context menu.
 * Clicking opens a modal where the user picks a target language;
 * the modal calls /api/synaplan/translate.
 */
export const useTranslationExtension = (): ActionExtension => {
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
      title: $gettext('Translate with Synaplan'),
      hideActions: true,
      customComponent: TranslationDialog,
      customComponentAttrs: () => ({ resource })
    }
    dispatchModal(modal)
  }

  return {
    id: 'com.synaplan.translation',
    type: 'action',
    extensionPointIds: ['global.files.context-actions'],
    action: {
      name: 'translate',
      icon: 'translate-2',
      iconFillType: 'none',
      label: () => $gettext('Translate with Synaplan…'),
      class: 'oc-files-actions-translate-trigger',
      handler,
      isVisible: (options: FileActionOptions) => isSingleSupportedFile(userStore.user, options)
    }
  }
}
