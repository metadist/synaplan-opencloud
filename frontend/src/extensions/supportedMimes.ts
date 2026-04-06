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

export function mimeIsSupported(mime: string): boolean {
  return SUPPORTED_MIMES.some((prefix) => mime.startsWith(prefix))
}

/**
 * The shared isVisible predicate for the Synaplan file actions:
 * a single non-folder resource with a supported mime type, and the
 * user must be signed in.
 */
export function isSingleSupportedFile(user: unknown, options: FileActionOptions): boolean {
  if (!user) return false
  if (!options.resources || options.resources.length !== 1) return false
  const resource = options.resources[0]
  if (resource.isFolder) return false
  return mimeIsSupported(resource.mimeType ?? '')
}
