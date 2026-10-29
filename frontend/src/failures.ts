import { useGettext } from 'vue3-gettext'

/**
 * One sentence for a failed Synaplan action. A code the app knows maps to
 * that sentence. Anything else is the fallback, never the raw error.
 * Call this from a component event so vue3-gettext can see the instance.
 */
export function sentenceFor(error: unknown, fallback: string): string {
  const { $gettext } = useGettext()
  const data = (error as { response?: { data?: { error?: string; code?: string } } } | undefined)
    ?.response?.data
  const code = data?.code
  switch (code) {
    case 'not_connected':
      return $gettext('Connect this OpenCloud to Synaplan first. Ask your administrator.')
    case 'translation_unavailable':
      return $gettext('This Synaplan cannot translate a file. Ask your administrator.')
    case 'file_not_found':
      return $gettext('This file is no longer there. Reload the folder and try again.')
    case 'file_empty':
      return $gettext('This file is empty.')
    case 'unsupported_type':
      return $gettext('Synaplan cannot read this kind of file.')
    case 'no_text':
      return $gettext(
        'Synaplan found no text in this file. It may be a scan or protected by a password.'
      )
    case 'synaplan_unreachable':
      return $gettext(
        'Synaplan cannot be reached. Try again in a minute, or ask your administrator.'
      )
    case 'rate_limited':
      return $gettext('Synaplan is busy or your limit is reached. Try again later.')
    default:
      if (data?.error && !data.error.includes('Exception') && data.error.length < 300) {
        return data.error
      }
      return fallback
  }
}
