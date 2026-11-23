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
    case 'image_unavailable':
      return $gettext('This Synaplan has no model that makes images. Ask your administrator.')
    case 'image_rate_limited':
      return $gettext('You have made as many images as Synaplan allows right now. Try again later.')
    case 'image_shared_key':
      return $gettext(
        'Making and saving images needs each person signed in to Synaplan. This installation uses one shared key.'
      )
    case 'file_too_large':
      return $gettext('This file is too large to copy here.')
    case 'copy_failed':
      return $gettext('The file was not copied. It is still in Synaplan.')
    case 'relation_not_kept':
      return $gettext(
        'The file is in your folder, but which Synaplan file it came from was not recorded.'
      )
    case 'shared_key':
      return $gettext(
        'From Synaplan needs each person signed in to Synaplan. This installation uses one shared key.'
      )
    case 'synaplan_unreachable':
      return $gettext(
        'Synaplan cannot be reached. Try again in a minute, or ask your administrator.'
      )
    case 'synaplan_error':
      return $gettext(
        'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
      )
    case 'rate_limited':
      return $gettext('Synaplan is busy or your limit is reached. Try again later.')
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
    case 'settings_need_person':
      return $gettext('Your own Synaplan settings need your own login. Ask your administrator.')
    case 'settings_unavailable':
      return $gettext('Your Synaplan settings are not available right now. Try again later.')
    case 'settings_not_kept':
      return $gettext(
        'Synaplan follows the new setting, but this page could not keep it. Save again.'
      )
    case 'folder_check_failed':
      return $gettext('OpenCloud could not check this folder. Try again.')
    case 'folder_not_personal':
      return $gettext('Only folders in your personal space can be allowed.')
    case 'too_many_folders':
      return $gettext('Allow at most 100 folders.')
    case 'invalid_setting':
      return $gettext('The setting could not be read. Reload the page and try again.')
    case 'not_confirmed':
      return $gettext('Synaplan did not confirm this setting. Save again to be sure it applies.')
    case 'cloud_ambiguous':
      return $gettext('Synaplan knows this OpenCloud more than once. Ask your administrator.')
    case 'admin_only':
      return $gettext('Only an administrator can change this.')
    case 'unsupported_language':
      return $gettext('Choose one of the listed languages.')
    case 'watch_cap':
      return $gettext('You are watching as many folders and files as you can. Stop a watch first.')
    case 'cloud_not_linked':
      return $gettext('Synaplan does not know this OpenCloud yet. Ask your administrator.')
    case 'watches_off':
      return $gettext('Watching folders is turned off in Synaplan. Ask your administrator.')
    case 'reads_unavailable':
      return $gettext('Synaplan cannot read folders in this OpenCloud. Ask your administrator.')
    case 'reads_personal_only':
      return $gettext('Synaplan can only read folders in your personal space.')
    case 'reads_changed':
      return $gettext('The setting was changed at the same time. Close this and try again.')
    case 'folder_not_found':
      return $gettext('This folder is no longer there. Reload the folder and try again.')
    case 'reads_everything':
      return $gettext('Synaplan may read all your folders, so one cannot be left out.')
    case 'watch_shared_key':
      return $gettext(
        'Watching needs each person signed in to Synaplan. This installation uses one shared key.'
      )
    default:
      return fallback
  }
}
