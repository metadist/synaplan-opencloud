import {
  type ActionExtension,
  type FileActionOptions,
  useModals,
  useMessages,
  useUserStore
} from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { defineAsyncComponent } from 'vue'
import { isSingleSupportedFile } from './supportedMimes'

const ChatDialog = defineAsyncComponent(() => import('../components/ChatDialog.vue'))

/** "Chat about this file" opens one question about the selected document. */
export const useChatExtension = (): ActionExtension => {
  const { $gettext } = useGettext()
  const { dispatchModal } = useModals()
  const { showErrorMessage } = useMessages()
  const userStore = useUserStore()

  return {
    id: 'com.synaplan.chat',
    type: 'action',
    action: {
      name: 'chat-about-file',
      icon: 'chat-1',
      iconFillType: 'line',
      label: () => $gettext('Chat about this file…'),
      class: 'oc-files-actions-chat-about-file-trigger',
      handler: ({ resources }: FileActionOptions) => {
        const resource = resources?.[0]
        if (!resource) {
          showErrorMessage({ title: $gettext('No file selected') })
          return
        }
        dispatchModal({
          title: $gettext('Chat about this file'),
          hideActions: true,
          customComponent: ChatDialog,
          customComponentAttrs: () => ({ resource })
        })
      },
      isVisible: (options: FileActionOptions) => isSingleSupportedFile(userStore.user, options)
    }
  }
}
