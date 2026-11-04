import { describe, expect, it } from 'vitest'
import { sentenceFor } from '../../src/failures'

const $gettext = (s: string) => s

describe('sentenceFor', () => {
  it('maps a known code to one sentence', () => {
    const error = { response: { data: { code: 'file_empty', error: 'ignored' } } }
    expect(sentenceFor($gettext, error, 'fallback')).toBe('This file is empty.')
  })

  it('uses the fallback when the body is not a sentence', () => {
    expect(sentenceFor($gettext, new Error('Exception at /var'), 'fallback')).toBe('fallback')
  })

  it('keeps a short sentence Synaplan sent', () => {
    const error = { response: { data: { error: 'Choose a folder or a file.' } } }
    expect(sentenceFor($gettext, error, 'fallback')).toBe('Choose a folder or a file.')
  })

  it('does not depend on an injection context', () => {
    // No vue3-gettext mock here on purpose: a sentence after an awaited
    // request must come from the translator captured during setup.
    const error = { response: { data: { code: 'synaplan_unreachable' } } }
    expect(sentenceFor((s) => `[${s}]`, error, 'fallback')).toMatch(/^\[Synaplan cannot be reached/)
  })
})

describe('sentenceFor partial writes', () => {
  it('keeps the saved-here-only sentence for a read scope Synaplan did not receive', () => {
    const t = (source: string) => source
    const error = { response: { data: { code: 'saved_not_reported', error: 'x' } } }
    expect(sentenceFor(t, error, 'fallback')).toContain('saved here')
  })
})
