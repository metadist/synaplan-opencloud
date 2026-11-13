import { useClientService } from '@opencloud-eu/web-pkg'
import { z } from 'zod'

/**
 * What the signed-in person lets Synaplan read of their OpenCloud files:
 * only what they watch, also folders they allow, or all of their files.
 *
 * The Go service reports the setting to Synaplan, which enforces it, and
 * keeps a copy for this page in OpenCloud's key-value store. A folder is
 * sent by its id; the service replaces the path with the one OpenCloud
 * knows, so a display name cannot widen what Synaplan may read.
 */

export const readModes = ['watched', 'allowed', 'all'] as const
export type ReadMode = (typeof readModes)[number]

const folderSchema = z.object({ id: z.string(), path: z.string() })
export type ReadFolder = z.infer<typeof folderSchema>

const scopeSchema = z.object({
  mode: z.enum(readModes),
  folders: z.array(folderSchema).default([])
})
export type ReadScope = z.infer<typeof scopeSchema>

const answerSchema = z.object({
  available: z.boolean(),
  readScope: scopeSchema.optional()
})
export type ReadScopeAnswer = z.infer<typeof answerSchema>

const connectionSchema = z.object({ login: z.enum(['person', 'shared-key']) })
export type Login = z.infer<typeof connectionSchema>['login']

const PREFERENCES = '/api/synaplan/preferences'

export function useReadScope() {
  const { httpAuthenticated } = useClientService()

  async function login(): Promise<Login> {
    const { data } = await httpAuthenticated.get('/api/synaplan/connection', {
      schema: connectionSchema
    })
    return data.login
  }

  async function load(): Promise<ReadScopeAnswer> {
    const { data } = await httpAuthenticated.get(PREFERENCES, { schema: answerSchema })
    return data
  }

  async function save(scope: ReadScope): Promise<ReadScope> {
    const { data } = await httpAuthenticated.put(PREFERENCES, scope, { schema: answerSchema })
    return data.readScope ?? scope
  }

  return { login, load, save }
}
