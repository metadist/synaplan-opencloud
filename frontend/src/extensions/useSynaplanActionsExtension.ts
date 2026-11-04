import { type ActionExtension, type FileActionOptions } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { useTranslationExtension } from './useTranslationExtension'
import { useSummarizeExtension } from './useSummarizeExtension'
import { useKnowledgeExtension } from './useKnowledgeExtension'
import { useChatExtension } from './useChatExtension'
import { useWatchExtension } from './useWatchExtension'
import { useFromSynaplanExtension } from './useFromSynaplanExtension'
import { useReadFolderExtension } from './useReadFolderExtension'
import { useSynaplanBrandIcon } from '../composables/useSynaplanBrandIcon'

export const useSynaplanActionsExtension = (): ActionExtension => {
  const { $gettext } = useGettext()

  const children = [
    useTranslationExtension(),
    useSummarizeExtension(),
    useKnowledgeExtension(),
    useChatExtension(),
    useWatchExtension(),
    useFromSynaplanExtension(),
    useReadFolderExtension()
  ].map(({ action }) => action)

  return {
    id: 'com.synaplan.actions',
    type: 'action',
    extensionPointIds: ['global.files.context-actions'],
    action: {
      name: 'synaplan',
      icon: useSynaplanBrandIcon(),
      label: () => $gettext('Synaplan'),
      children,
      isVisible: (options: FileActionOptions) => children.some((child) => child.isVisible(options))
    }
  }
}
