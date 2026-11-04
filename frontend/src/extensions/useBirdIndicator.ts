import type { Resource } from '@opencloud-eu/web-client'
import type { ResourceIndicatorExtension } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { useClientService } from '@opencloud-eu/web-pkg'
import { useSynaplanBrandIcon } from '../composables/useSynaplanBrandIcon'
import { markFor, queueCloudStatus } from '../composables/cloudStatus'

type Marked = Resource & { etag?: string }

/** The bird on a file Synaplan can search, and eyes when the file or folder is watched. */
export const useBirdIndicator = (): ResourceIndicatorExtension => {
  const { $gettext } = useGettext()
  const { httpAuthenticated } = useClientService()
  const icon = useSynaplanBrandIcon()

  // The same words the Synaplan tab uses for that state.
  const stateLabel = (state: string) => {
    switch (state) {
      case 'adding':
      case 'updating':
        return $gettext('Changed — updating.')
      case 'changed':
        return $gettext('Changed since it was made searchable.')
      case 'failed':
        return $gettext('Synaplan could not make this file searchable.')
      default:
        return $gettext('Searchable by AI')
    }
  }

  return {
    id: 'com.synaplan.bird',
    type: 'resourceIndicator',
    extensionPointIds: ['global.files.resource-indicator'],
    getResourceIndicators: (resource) => {
      const item = resource as Marked
      if (!item.id) return
      queueCloudStatus(httpAuthenticated, { id: item.id, version: item.etag ?? '' })
      const mark = markFor(item.id)
      if (!mark || (mark.state === 'none' && !mark.watched)) return
      const label = mark.watched ? $gettext('Watched by Synaplan') : stateLabel(mark.state)
      return [
        {
          id: `synaplan-${item.id}`,
          kind: 'icon' as const,
          accessibleDescription: label,
          label,
          icon,
          fillType: 'line' as const,
          type: mark.watched ? 'synaplan-watched' : 'synaplan-searchable',
          category: 'system' as const
        }
      ]
    }
  }
}
