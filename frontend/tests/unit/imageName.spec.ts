import { describe, expect, it } from 'vitest'
import { imageName } from '../../src/imageName'

describe('imageName', () => {
  it.each([
    ['a red square', '/api/v1/files/uploads/04/m.png', 'image/png', 'a red square.png'],
    ['a red square', '/api/v1/files/uploads/04/m.JPEG?x=1', 'image/jpeg', 'a red square.jpeg'],
    ['a red square', '/api/v1/files/42/download', 'image/jpeg', 'a red square.jpg'],
    ['a red square', '/api/v1/files/42/download', 'image/webp', 'a red square.webp'],
    ['a red square', '/api/v1/files/42/download', '', 'a red square.png'],
    ['../etc/passwd', '/m.png', 'image/png', '.. etc passwd.png'],
    ['a\nb\tc:d', '/m.png', 'image/png', 'a b c d.png'],
    ['  /  ', '/m.png', 'image/png', 'Synaplan image.png'],
    ['x'.repeat(100), '/m.png', 'image/png', `${'x'.repeat(60)}.png`]
  ])('names %j from %s', (prompt, url, mime, want) => {
    expect(imageName(prompt, url, mime)).toBe(want)
  })
})
