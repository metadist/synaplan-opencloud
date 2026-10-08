import { type ActionExtension, type FileActionOptions } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { useTranslationExtension } from './useTranslationExtension'
import { useSummarizeExtension } from './useSummarizeExtension'
import { useKnowledgeExtension } from './useKnowledgeExtension'

export const useSynaplanActionsExtension = (): ActionExtension => {
  const { $gettext } = useGettext()

  const children = [
    useTranslationExtension(),
    useSummarizeExtension(),
    useKnowledgeExtension()
  ].map(({ action }) => action)

  return {
    id: 'com.synaplan.actions',
    type: 'action',
    extensionPointIds: ['global.files.context-actions'],
    action: {
      name: 'synaplan',
      icon: 'magic',
      label: () => $gettext('Synaplan'),
      children,
      isVisible: (options: FileActionOptions) => children.some((child) => child.isVisible(options))
    }
  }
}
