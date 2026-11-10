export type Translate = (source: string) => string

/**
 * One sentence for a failed Synaplan action. A code the Go service sends
 * maps to that sentence; anything else is the fallback, never the raw
 * error ("Request failed with status code 502").
 *
 * `$gettext` is passed in by the caller, captured during component setup:
 * this runs after an awaited request, where vue3-gettext has no injection
 * context and `useGettext()` would throw.
 */
export function sentenceFor($gettext: Translate, error: unknown, fallback: string): string {
  const code = (error as { response?: { data?: { code?: unknown } } } | undefined)?.response?.data
    ?.code
  switch (code) {
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
    case 'synaplan_error':
      return $gettext(
        'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
      )
    default:
      return fallback
  }
}
