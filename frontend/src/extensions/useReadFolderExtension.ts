import {
  type ActionExtension,
  type FileActionOptions,
  useMessages,
  useUserStore
} from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { useReadScope } from '../composables/useReadScope'
import { sentenceFor } from '../failures'

/**
 * Adds "Let Synaplan read this folder" to a folder's Synaplan menu. It
 * adds the folder to what the person lets Synaplan read and switches to
 * "also folders I allow"; the Synaplan page lists the folders and removes
 * one again.
 */
export const useReadFolderExtension = (): ActionExtension => {
  const { $gettext } = useGettext()
  const { showMessage, showErrorMessage } = useMessages()
  const userStore = useUserStore()
  const { allowFolder } = useReadScope()

  const handler = async ({ resources }: FileActionOptions) => {
    const resource = resources?.[0]
    if (!resource) {
      showErrorMessage({ title: $gettext('No folder selected') })
      return
    }
    try {
      const scope = await allowFolder({ id: resource.id, path: resource.path ?? '' })
      if (!scope.folders.some((folder) => folder.id === resource.id)) {
        showErrorMessage({
          title: $gettext('This folder is no longer there. Reload the folder and try again.')
        })
        return
      }
      showMessage({ title: $gettext('Synaplan may read this folder.') })
    } catch (e) {
      showErrorMessage({
        title: sentenceFor(
          $gettext,
          e,
          $gettext(
            'Synaplan did not receive this setting, so nothing changed. Try again in a minute.'
          )
        )
      })
    }
  }

  return {
    id: 'com.synaplan.read-folder',
    type: 'action',
    action: {
      name: 'let-synaplan-read',
      icon: 'folder-open',
      iconFillType: 'line',
      label: () => $gettext('Let Synaplan read this folder'),
      class: 'oc-files-actions-let-synaplan-read-trigger',
      handler,
      isVisible: ({ resources }: FileActionOptions) =>
        !!userStore.user && resources?.length === 1 && !!resources[0].isFolder
    }
  }
}
