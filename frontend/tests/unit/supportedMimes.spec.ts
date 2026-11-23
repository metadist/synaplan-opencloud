import { describe, expect, it } from 'vitest'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'
import { fileIsSupported, isSingleSupportedFile } from '../../src/extensions/supportedMimes'

function resource(overrides: {
  name?: string
  extension?: string
  mimeType?: string
  isFolder?: boolean
}) {
  return {
    id: 'r1',
    name: 'file.bin',
    isFolder: false,
    mimeType: 'application/octet-stream',
    ...overrides
  } as unknown as FileActionOptions['resources'][number]
}

describe('fileIsSupported', () => {
  it.each([
    ['a note OpenCloud stored as bytes', { name: 'note1.ocnote' }, true],
    ['a note named by the extension field', { name: 'note1', extension: 'ocnote' }, true],
    ['markdown without a text mime', { name: 'readme.md', mimeType: '' }, true],
    ['a csv', { name: 'rows.csv', mimeType: 'application/octet-stream' }, true],
    ['plain text by mime', { name: 'file.bin', mimeType: 'text/plain' }, true],
    ['a pdf', { name: 'file.bin', mimeType: 'application/pdf' }, true],
    ['an image', { name: 'photo.png', mimeType: 'image/png' }, false],
    ['an unknown file', { name: 'archive.bin' }, false]
  ])('%s', (_label, fields, expected) => {
    expect(fileIsSupported(fields)).toBe(expected)
  })
})

describe('isSingleSupportedFile', () => {
  it('offers a note to a signed-in person', () => {
    expect(
      isSingleSupportedFile({ id: 'u1' }, {
        resources: [resource({ name: 'note1.ocnote' })]
      } as FileActionOptions)
    ).toBe(true)
  })

  it('hides a note when nobody is signed in', () => {
    expect(
      isSingleSupportedFile(null, {
        resources: [resource({ name: 'note1.ocnote' })]
      } as FileActionOptions)
    ).toBe(false)
  })
})
