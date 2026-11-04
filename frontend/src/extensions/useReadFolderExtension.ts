import {
  type ActionExtension,
  type FileActionOptions,
  useClientService,
  useMessages,
  useUserStore
} from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'

const prefsSchema = z.object({
  readScope: z.object({
    mode: z.string(),
    folders: z.array(z.object({ id: z.string(), path: z.string() })).default([])
  }),
  copyFolder: z.string().default('')
})

/** Adds the open folder to what Synaplan may read. */
export const useReadFolderExtension = (): ActionExtension => {
  const { $gettext } = useGettext()
  const { showMessage, showErrorMessage } = useMessages()
  const userStore = useUserStore()
  const { httpAuthenticated } = useClientService()

  return {
    id: 'com.synaplan.read-folder',
    type: 'action',
    action: {
      name: 'let-synaplan-read',
      icon: 'folder-open',
      iconFillType: 'line',
      label: () => $gettext('Let Synaplan read this folder'),
      class: 'oc-files-actions-read-folder-trigger',
      handler: async ({ resources }: FileActionOptions) => {
        const resource = resources?.[0]
        if (!resource) return
        try {
          const current = await httpAuthenticated.get('/api/synaplan/preferences', {
            schema: prefsSchema
          })
          const folders = current.data.readScope.folders.filter(
            (folder: { id: string; path: string }) => folder.id !== resource.id
          )
          // The service replaces the path with the one OpenCloud knows for the id.
          folders.push({ id: resource.id, path: resource.path ?? resource.name ?? '' })
          const saved = await httpAuthenticated.put(
            '/api/synaplan/preferences',
            { mode: 'allowed', folders, copyFolder: current.data.copyFolder },
            { schema: z.object({ saved: z.boolean(), reported: z.boolean().default(false) }) }
          )
          if (saved.data.reported) {
            showMessage({ title: $gettext('Synaplan may read this folder.') })
          } else {
            showErrorMessage({
              title: $gettext(
                'Saved here only: this OpenCloud is not connected to Synaplan yet. Ask your administrator.'
              )
            })
          }
        } catch {
          showErrorMessage({
            title: $gettext(
              'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
            )
          })
        }
      },
      isVisible: (options: FileActionOptions) => {
        if (!userStore.user || !options.resources || options.resources.length !== 1) return false
        return options.resources[0].isFolder
      }
    }
  }
}
