const MAX_BASE = 60

function extensionFor(url: string, mimeType: string): string {
  const fromUrl = /\.([A-Za-z0-9]{2,5})$/.exec(url.split(/[?#]/)[0])?.[1]
  if (fromUrl) return fromUrl.toLowerCase()
  const fromMime = /^image\/([a-z0-9]+)/.exec(mimeType)?.[1]
  return fromMime === 'jpeg' ? 'jpg' : (fromMime ?? 'png')
}

/**
 * The file name a generated image is saved under: the prompt it was made
 * from, without characters a file name cannot hold, and the extension of
 * the file Synaplan made. The Go service adds " (2)" when the name is taken.
 */
export function imageName(prompt: string, url: string, mimeType: string): string {
  const base =
    Array.from(prompt)
      .map((c) => (c < ' ' || '\\/:*?"<>|'.includes(c) ? ' ' : c))
      .join('')
      .replace(/\s+/g, ' ')
      .trim()
      .slice(0, MAX_BASE)
      .trim() || 'Synaplan image'
  return `${base}.${extensionFor(url, mimeType)}`
}
