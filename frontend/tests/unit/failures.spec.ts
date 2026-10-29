import { describe, expect, it, vi } from 'vitest'
import { sentenceFor } from '../../src/failures'

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

describe('sentenceFor', () => {
  it('maps a known code to one sentence', () => {
    const error = { response: { data: { code: 'file_empty', error: 'ignored' } } }
    expect(sentenceFor(error, 'fallback')).toBe('This file is empty.')
  })

  it('uses the fallback when the body is not a sentence', () => {
    expect(sentenceFor(new Error('Exception at /var'), 'fallback')).toBe('fallback')
  })

  it('keeps a short sentence Synaplan sent', () => {
    const error = { response: { data: { error: 'Choose a folder or a file.' } } }
    expect(sentenceFor(error, 'fallback')).toBe('Choose a folder or a file.')
  })
})
