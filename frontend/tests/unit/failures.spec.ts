import { describe, expect, it } from 'vitest'
import { sentenceFor } from '../../src/failures'

const $gettext = (s: string) => s

describe('sentenceFor', () => {
  it.each([
    ['file_not_found', 'This file is no longer there. Reload the folder and try again.'],
    ['file_empty', 'This file is empty.'],
    ['translation_unavailable', 'This Synaplan cannot translate a file. Ask your administrator.'],
    ['file_too_large', 'This file is too large to copy here.'],
    [
      'shared_key',
      'From Synaplan needs each person signed in to Synaplan. This installation uses one shared key.'
    ],
    ['copy_failed', 'The file was not copied. It is still in Synaplan.'],
    [
      'synaplan_unreachable',
      'Synaplan cannot be reached. Try again in a minute, or ask your administrator.'
    ],
    ['unsupported_type', 'Synaplan cannot read this kind of file.'],
    ['bad_file_name', 'This name cannot be used for a file.'],
    [
      'no_text',
      'Synaplan found no text in this file. It may be a scan or protected by a password.'
    ],
    ['image_unavailable', 'This Synaplan has no model that makes images. Ask your administrator.'],
    [
      'image_rate_limited',
      'You have made as many images as Synaplan allows right now. Try again later.'
    ],
    [
      'image_shared_key',
      'Making and saving images needs each person signed in to Synaplan. This installation uses one shared key.'
    ],
    [
      'synaplan_error',
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
    ],
    ['rate_limited', 'Synaplan is busy or your limit is reached. Try again later.'],
    ['identity_check_failed', 'Synaplan could not check who you are. Try again.'],
    ['idp_unreachable', 'The sign-in service cannot be reached. Try again later.'],
    ['permission_unavailable', 'Synaplan cannot check this permission right now. Try again later.'],
    ['store_unavailable', 'Synaplan could not update this permission. Try again.'],
    ['permission_busy', 'This permission was changing at the same time. Try again.'],
    [
      'revoke_unconfirmed',
      'Synaplan no longer works for you, but the sign-in service could not confirm it. Try again.'
    ],
    [
      'settings_need_person',
      'Your own Synaplan settings need your own login. Ask your administrator.'
    ],
    [
      'settings_unavailable',
      'Your Synaplan settings are not available right now. Try again later.'
    ],
    [
      'settings_not_kept',
      'Synaplan follows the new setting, but this page could not keep it. Save again.'
    ],
    ['folder_check_failed', 'OpenCloud could not check this folder. Try again.'],
    ['folder_not_personal', 'Only folders in your personal space can be allowed.'],
    ['too_many_folders', 'Allow at most 100 folders.'],
    ['invalid_setting', 'The setting could not be read. Reload the page and try again.'],
    ['not_confirmed', 'Synaplan did not confirm this setting. Save again to be sure it applies.'],
    ['cloud_ambiguous', 'Synaplan knows this OpenCloud more than once. Ask your administrator.'],
    ['admin_only', 'Only an administrator can change this.'],
    ['unsupported_language', 'Choose one of the listed languages.'],
    ['watch_cap', 'You are watching as many folders and files as you can. Stop a watch first.'],
    ['cloud_not_linked', 'Synaplan does not know this OpenCloud yet. Ask your administrator.'],
    ['watches_off', 'Watching folders is turned off in Synaplan. Ask your administrator.'],
    [
      'reads_unavailable',
      'Synaplan cannot read folders in this OpenCloud. Ask your administrator.'
    ],
    ['reads_personal_only', 'Synaplan can only read folders in your personal space.'],
    ['reads_changed', 'The setting was changed at the same time. Close this and try again.'],
    ['folder_not_found', 'This folder is no longer there. Reload the folder and try again.'],
    ['reads_everything', 'Synaplan may read all your folders, so one cannot be left out.'],
    [
      'watch_shared_key',
      'Watching needs each person signed in to Synaplan. This installation uses one shared key.'
    ]
  ])('maps %s to its sentence', (code, sentence) => {
    const error = { response: { data: { code, error: 'ignored' } } }
    expect(sentenceFor($gettext, error, 'fallback')).toBe(sentence)
  })

  it('uses the fallback for an error without a known code', () => {
    expect(
      sentenceFor($gettext, new Error('Request failed with status code 502'), 'fallback')
    ).toBe('fallback')
    expect(
      sentenceFor($gettext, { response: { data: { code: 'other', error: 'x' } } }, 'fallback')
    ).toBe('fallback')
  })

  it('translates with the translator it is given', () => {
    const error = { response: { data: { code: 'file_empty' } } }
    expect(sentenceFor((s) => `[${s}]`, error, 'fallback')).toBe('[This file is empty.]')
  })
})
