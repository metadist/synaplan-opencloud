import { describe, expect, it } from 'vitest'
import { sentenceFor } from '../../src/failures'

const $gettext = (s: string) => s

describe('sentenceFor', () => {
  it.each([
    ['file_not_found', 'This file is no longer there. Reload the folder and try again.'],
    ['file_empty', 'This file is empty.'],
    ['unsupported_type', 'Synaplan cannot read this kind of file.'],
    [
      'no_text',
      'Synaplan found no text in this file. It may be a scan or protected by a password.'
    ],
    [
      'synaplan_error',
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
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
