const MAX_BASE = 60

const KNOWN_EXTENSIONS = new Set([
  'png',
  'jpg',
  'jpeg',
  'gif',
  'webp',
  'svg',
  'avif',
  'bmp',
  'tif',
  'tiff'
])

function extensionFor(url: string, mimeType: string): string {
  const fromUrl = /\.([A-Za-z0-9]{2,5})$/.exec(url.split(/[?#]/)[0])?.[1]?.toLowerCase()
  if (fromUrl && KNOWN_EXTENSIONS.has(fromUrl)) return fromUrl
  if (mimeType === 'image/svg+xml') return 'svg'
  const fromMime = /^image\/([a-z0-9]+)/.exec(mimeType)?.[1]
  if (fromMime === 'jpeg') return 'jpg'
  if (fromMime && KNOWN_EXTENSIONS.has(fromMime)) return fromMime
  return 'png'
}

/**
 * The file name a generated image is saved under: the prompt it was made
 * from, without characters a file name cannot hold or leading/trailing
 * dots, and the extension of the file Synaplan made. The Go service adds
 * " (2)" when the name is taken.
 */
export function imageName(prompt: string, url: string, mimeType: string): string {
  const base =
    Array.from(prompt)
      .map((c) => (c < ' ' || '\\/:*?"<>|'.includes(c) ? ' ' : c))
      .join('')
      .replace(/\s+/g, ' ')
      .replace(/^[.\s]+|[.\s]+$/g, '')
      .slice(0, MAX_BASE)
      .replace(/[.\s]+$/g, '') || 'Synaplan image'
  return `${base}.${extensionFor(url, mimeType)}`
}
