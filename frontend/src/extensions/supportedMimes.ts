import type { FileActionOptions } from '@opencloud-eu/web-pkg'

// Mirrors the list used by the synaplan-nextcloud integration and
// enforced independently by the backend in internal/handler/summary.go.
export const SUPPORTED_MIMES = [
  'text/',
  'application/pdf',
  'application/json',
  'application/xml',
  'application/rtf',
  'application/msword',
  'application/vnd.openxmlformats-officedocument',
  'application/vnd.oasis.opendocument'
]

// Text files Synaplan reads by name. OpenCloud stores a note as
// application/octet-stream, so the MIME list alone hides it.
export const TEXT_EXTENSIONS = ['txt', 'md', 'csv', 'ocnote']

export function mimeIsSupported(mime: string): boolean {
  return SUPPORTED_MIMES.some((prefix) => mime.startsWith(prefix))
}

type NamedResource = {
  mimeType?: string
  name?: string
  extension?: string
}

export function resourceExtension(resource: NamedResource): string {
  const declared = resource.extension?.trim().replace(/^\./, '')
  if (declared) return declared.toLowerCase()
  const base = resource.name?.split(/[/\\]/).pop() ?? ''
  const dot = base.lastIndexOf('.')
  if (dot <= 0) return ''
  return base.slice(dot + 1).toLowerCase()
}

export function fileIsSupported(resource: NamedResource): boolean {
  if (mimeIsSupported(resource.mimeType ?? '')) return true
  return TEXT_EXTENSIONS.includes(resourceExtension(resource))
}

/**
 * The shared isVisible predicate for the Synaplan file actions:
 * a single non-folder resource Synaplan can read, and the user must
 * be signed in. A text file counts by its extension when OpenCloud
 * does not give it a text MIME type.
 */
export function isSingleSupportedFile(user: unknown, options: FileActionOptions): boolean {
  if (!user) return false
  if (!options.resources || options.resources.length !== 1) return false
  const resource = options.resources[0]
  if (resource.isFolder) return false
  return fileIsSupported(resource)
}
