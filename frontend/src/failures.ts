export type Translate = (source: string) => string

/**
 * One sentence for a failed Synaplan action. A code the app knows maps to
 * that sentence. Anything else is the fallback, never the raw error.
 *
 * `$gettext` is passed in by the caller, captured during component setup:
 * this runs after an awaited request, where vue3-gettext has no injection
 * context and `useGettext()` would throw.
 */
export function sentenceFor($gettext: Translate, error: unknown, fallback: string): string {
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
    case 'file_too_large':
      return $gettext('This file is too large for Synaplan. Try a smaller file.')
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
    case 'platform_disconnected':
      return $gettext(
        'This login is not connected to Synaplan yet. Connect it on the Synaplan page, or ask your administrator.'
      )
    case 'platform_ambiguous':
      return $gettext(
        'Synaplan knows more than one cloud for this login. Ask a Synaplan administrator to remove the one no longer in use.'
      )
    case 'saved_not_reported':
      return $gettext(
        'The setting was saved here, but Synaplan did not receive it. Save again in a minute.'
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
