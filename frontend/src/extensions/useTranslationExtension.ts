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

// SUPPORTED_MIMES mirrors the list used by the synaplan-nextcloud
// integration. Anything prefix-matching one of these entries is shown
// in the context menu; others are hidden. The backend enforces the
// same set independently (never trust the frontend).
const SUPPORTED_MIMES = [
  'text/',
  'application/pdf',
  'application/json',
  'application/xml',
  'application/rtf',
  'application/msword',
  'application/vnd.openxmlformats-officedocument',
  'application/vnd.oasis.opendocument'
]

// TranslationDialog is loaded on demand — avoids pulling the dialog
// (and its transitive deps) into the main bundle just because the
// action is registered.
const TranslationDialog = defineAsyncComponent(() => import('../components/TranslationDialog.vue'))

/**
 * Action extension that adds a "Translate with Synaplan…" entry to
 * the file context menu. Clicking opens a modal where the user picks
 * a target language and kicks off the translation; the modal calls
 * the synaplan-opencloud backend, which reads the file from CS3 and
 * forwards it to Synaplan's /api/v1/summary/generate endpoint.
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
      // Follows the repo-wide convention for file action trigger
      // classes (e.g. .oc-files-actions-copy-trigger). Stable
      // selector for CSS targeting and E2E tests.
      class: 'oc-files-actions-translate-trigger',
      handler,
      isVisible: ({ resources }: FileActionOptions) => {
        if (!userStore.user) return false
        if (!resources || resources.length !== 1) return false
        const resource = resources[0]
        if (resource.isFolder) return false
        const mime = resource.mimeType ?? ''
        return SUPPORTED_MIMES.some((prefix) => mime.startsWith(prefix))
      }
    }
  }
}
