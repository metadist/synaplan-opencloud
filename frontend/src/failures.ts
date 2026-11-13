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
    case 'translation_unavailable':
      return $gettext('This Synaplan cannot translate a file. Ask your administrator.')
    case 'synaplan_error':
      return $gettext(
        'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
      )
    case 'identity_check_failed':
      return $gettext('Synaplan could not check who you are. Try again.')
    case 'idp_unreachable':
      return $gettext('The sign-in service cannot be reached. Try again later.')
    case 'permission_unavailable':
      return $gettext('Synaplan cannot check this permission right now. Try again later.')
    case 'store_unavailable':
      return $gettext('Synaplan could not update this permission. Try again.')
    case 'permission_busy':
      return $gettext('This permission was changing at the same time. Try again.')
    case 'revoke_unconfirmed':
      return $gettext(
        'Synaplan no longer works for you, but the sign-in service could not confirm it. Try again.'
      )
    case 'watch_cap':
      return $gettext('You are watching as many folders and files as you can. Stop a watch first.')
    case 'cloud_not_linked':
      return $gettext('Synaplan does not know this OpenCloud yet. Ask your administrator.')
    case 'watches_off':
      return $gettext('Watching folders is turned off in Synaplan. Ask your administrator.')
    default:
      return fallback
  }
}
