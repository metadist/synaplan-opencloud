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
    [
      'no_text',
      'Synaplan found no text in this file. It may be a scan or protected by a password.'
    ],
    [
      'synaplan_error',
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
    ],
    ['identity_check_failed', 'Synaplan could not check who you are. Try again.'],
    ['idp_unreachable', 'The sign-in service cannot be reached. Try again later.'],
    ['permission_unavailable', 'Synaplan cannot check this permission right now. Try again later.'],
    ['store_unavailable', 'Synaplan could not update this permission. Try again.'],
    ['permission_busy', 'This permission was changing at the same time. Try again.'],
    [
      'revoke_unconfirmed',
      'Synaplan no longer works for you, but the sign-in service could not confirm it. Try again.'
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
