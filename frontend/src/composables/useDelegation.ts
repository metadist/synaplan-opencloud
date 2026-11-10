import { ref, type Ref } from 'vue'
import { buildUrl, useClientService, useRouter } from '@opencloud-eu/web-pkg'
import { z } from 'zod'

/**
 * Lets the signed-in person allow Synaplan to work for them while they are
 * away.
 *
 * OpenCloud signs people in with its own public client, whose tokens the Go
 * service may only use while a request is running. For later work the
 * service needs an offline token of its own confidential client. The
 * browser obtains an authorization code for that client from the same
 * identity provider: first silently in a hidden iframe (the person is
 * signed in there already), and only when the provider needs the person
 * (consent, a new login) by a full-page redirect. The callback page the
 * provider returns to ships with this extension (public/callback.html).
 * The service redeems the code with its secret; the browser never sees a
 * token.
 */

const statusSchema = z.object({
  available: z.boolean(),
  connected: z.boolean(),
  clientId: z.string().default(''),
  authorizeEndpoint: z.string().default('')
})
export type DelegationStatus = z.infer<typeof statusSchema>

const answerSchema = z.object({
  code: z.string().nullable(),
  state: z.string().nullable(),
  error: z.string().nullable()
})
type Answer = z.infer<typeof answerSchema>
const pendingSchema = z.object({ state: z.string(), verifier: z.string() })

function parseStored<T>(schema: z.ZodType<T>, raw: string): T | null {
  try {
    const result = schema.safeParse(JSON.parse(raw))
    return result.success ? result.data : null
  } catch {
    return null
  }
}

const ANSWER_KEY = 'synaplan-connect-answer'
const RETURN_KEY = 'synaplan-connect-return'
const PENDING_KEY = 'synaplan-connect-pending'
const SILENT_TIMEOUT_MS = 10_000

function base64url(bytes: ArrayBuffer | Uint8Array): string {
  const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes)
  let text = ''
  view.forEach((b) => (text += String.fromCharCode(b)))
  return btoa(text).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function randomString(): string {
  return base64url(crypto.getRandomValues(new Uint8Array(32)))
}

async function challengeFor(verifier: string): Promise<string> {
  return base64url(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier)))
}

export function useDelegation(): {
  status: Ref<DelegationStatus | null>
  busy: Ref<boolean>
  load: () => Promise<void>
  connect: () => Promise<void>
  disconnect: () => Promise<void>
  resumeRedirect: () => Promise<boolean>
} {
  const { httpAuthenticated } = useClientService()
  const router = useRouter()
  const status = ref<DelegationStatus | null>(null)
  const busy = ref(false)

  const redirectUri = () => buildUrl(router, 'assets/apps/synaplan/callback.html')

  async function load() {
    const res = await httpAuthenticated.get('/api/synaplan/delegation', { schema: statusSchema })
    status.value = res.data
  }

  async function authorizeUrl(prompt: 'none' | undefined) {
    const current = status.value
    if (!current?.authorizeEndpoint || !current.clientId) {
      throw new Error('working while away is not available')
    }
    const verifier = randomString()
    const state = randomString()
    const url = new URL(current.authorizeEndpoint)
    url.search = new URLSearchParams({
      client_id: current.clientId,
      response_type: 'code',
      scope: 'openid offline_access',
      redirect_uri: redirectUri(),
      state,
      code_challenge: await challengeFor(verifier),
      code_challenge_method: 'S256',
      ...(prompt ? { prompt } : {})
    }).toString()
    return { url: url.toString(), verifier, state }
  }

  function silently(url: string): Promise<Answer | null> {
    return new Promise((resolve) => {
      const frame = document.createElement('iframe')
      frame.style.display = 'none'
      frame.setAttribute('data-testid', 'synaplan-connect-frame')
      const finish = (answer: Answer | null) => {
        window.removeEventListener('message', onMessage)
        clearTimeout(timer)
        frame.remove()
        resolve(answer)
      }
      const onMessage = (event: MessageEvent) => {
        if (event.origin === window.location.origin && event.data?.type === 'synaplan-connect') {
          finish(answerSchema.safeParse(event.data).data ?? null)
        }
      }
      const timer = setTimeout(() => finish(null), SILENT_TIMEOUT_MS)
      window.addEventListener('message', onMessage)
      frame.src = url
      document.body.appendChild(frame)
    })
  }

  async function redeem(code: string, verifier: string) {
    await httpAuthenticated.post(
      '/api/synaplan/delegation',
      { code, verifier, redirectUri: redirectUri() },
      { schema: z.object({ connected: z.boolean() }) }
    )
    await load()
  }

  async function connect() {
    busy.value = true
    try {
      if (!status.value) await load()
      const silent = await authorizeUrl('none')
      const answer = await silently(silent.url)
      if (answer?.code && answer.state === silent.state) {
        await redeem(answer.code, silent.verifier)
        return
      }
      const interactive = await authorizeUrl(undefined)
      sessionStorage.setItem(
        PENDING_KEY,
        JSON.stringify({ state: interactive.state, verifier: interactive.verifier })
      )
      sessionStorage.setItem(RETURN_KEY, window.location.href)
      window.location.assign(interactive.url)
    } finally {
      busy.value = false
    }
  }

  async function resumeRedirect(): Promise<boolean> {
    const raw = sessionStorage.getItem(ANSWER_KEY)
    const pending = sessionStorage.getItem(PENDING_KEY)
    sessionStorage.removeItem(ANSWER_KEY)
    sessionStorage.removeItem(PENDING_KEY)
    sessionStorage.removeItem(RETURN_KEY)
    if (!raw || !pending) return false
    const answer = parseStored(answerSchema, raw)
    const expected = parseStored(pendingSchema, pending)
    if (!answer?.code || !expected || answer.state !== expected.state) return false
    busy.value = true
    try {
      await redeem(answer.code, expected.verifier)
      return true
    } finally {
      busy.value = false
    }
  }

  async function disconnect() {
    busy.value = true
    try {
      await httpAuthenticated.delete('/api/synaplan/delegation', {
        schema: z.object({ connected: z.boolean() })
      })
      await load()
    } finally {
      busy.value = false
    }
  }

  return { status, busy, load, connect, disconnect, resumeRedirect }
}
