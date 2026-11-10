import { ref, type Ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import callbackUrl from '../callback.html?url'
import { z } from 'zod'

/**
 * Lets the signed-in person allow Synaplan to work for them while they are
 * away.
 *
 * OpenCloud signs people in with its own public client, whose tokens the Go
 * service may only use while a request is running. For later work the
 * service needs an offline token of its own confidential client. The
 * browser obtains an authorization code for that client from the same
 * identity provider: silently in a hidden iframe (the person is signed in
 * there already), or, when the provider needs the person (consent, a new
 * login), in a popup the person opens with a second click (browsers allow
 * popups only from a click). Either way the provider returns to a static
 * page that ships with this extension (callback.html, wherever the
 * extension is served from) and that page posts the answer back. It is
 * accepted only from that window, from the callback's origin and with the
 * state sent. The service redeems the code with its secret; the browser
 * never sees a token.
 */

const statusSchema = z.object({
  available: z.boolean(),
  connected: z.boolean(),
  clientId: z.string().default(''),
  authorizeEndpoint: z.string().default('')
})
export type DelegationStatus = z.infer<typeof statusSchema>

const answerSchema = z.object({
  type: z.literal('synaplan-connect'),
  code: z.string().nullable(),
  state: z.string().nullable(),
  error: z.string().nullable()
})
type Answer = z.infer<typeof answerSchema>

/** The browser refused to open the sign-in window. */
export class PopupBlockedError extends Error {}

/** The sign-in window closed without an answer. */
export class SignInClosedError extends Error {}

/** The person said no in the sign-in window. */
export class SignInDeclinedError extends Error {}

/** The sign-in service refused for a reason only an administrator can fix. */
export class SignInRefusedError extends Error {}

const SILENT_TIMEOUT_MS = 10_000
/**
 * callback.html answers while it loads. A frame that finished loading and
 * stays silent shows the sign-in service's own page: an error, or a form
 * that prompt=none must not show.
 */
const SILENT_SETTLE_MS = 1_000
const POPUP_POLL_MS = 500

/** Errors with which the sign-in service says it needs the person. */
const NEEDS_PERSON = [
  'login_required',
  'interaction_required',
  'consent_required',
  'account_selection_required'
]

type Outcome = 'connected' | 'needs-person' | 'declined' | 'refused' | 'none'

const callback = new URL(callbackUrl, import.meta.url)

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

/**
 * The answer callback.html posts from source, or null when it does not
 * come: the timeout passed, or stillOpen says the window is gone.
 */
function answerFrom(
  source: () => Window | null,
  stillOpen: () => boolean,
  timeoutMs: number | null
): Promise<Answer | null> {
  return new Promise((resolve) => {
    const finish = (answer: Answer | null) => {
      window.removeEventListener('message', onMessage)
      clearInterval(poll)
      clearTimeout(timer)
      resolve(answer)
    }
    const onMessage = (event: MessageEvent) => {
      if (event.source !== source() || event.origin !== callback.origin) return
      const parsed = answerSchema.safeParse(event.data)
      if (parsed.success) finish(parsed.data)
    }
    const poll = setInterval(() => stillOpen() || finish(null), POPUP_POLL_MS)
    const timer = timeoutMs === null ? undefined : setTimeout(() => finish(null), timeoutMs)
    window.addEventListener('message', onMessage)
  })
}

export function useDelegation(): {
  status: Ref<DelegationStatus | null>
  busy: Ref<boolean>
  needsSignIn: Ref<boolean>
  load: () => Promise<void>
  connect: () => Promise<void>
  signIn: () => Promise<void>
  disconnect: () => Promise<void>
} {
  const { httpAuthenticated } = useClientService()
  const status = ref<DelegationStatus | null>(null)
  const busy = ref(false)
  const needsSignIn = ref(false)

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
      redirect_uri: callback.href,
      state,
      code_challenge: await challengeFor(verifier),
      code_challenge_method: 'S256',
      ...(prompt ? { prompt } : {})
    }).toString()
    return { url: url.toString(), verifier, state }
  }

  /**
   * Redeems a code, or names why there is none. An answer counts only
   * with the state that was sent, its error too.
   */
  async function settle(
    answer: Answer | null,
    sent: { state: string; verifier: string }
  ): Promise<Outcome> {
    if (!answer || answer.state !== sent.state) return 'none'
    if (answer.code) {
      await httpAuthenticated.post(
        '/api/synaplan/delegation',
        { code: answer.code, verifier: sent.verifier, redirectUri: callback.href },
        { schema: z.object({ connected: z.boolean() }) }
      )
      await load()
      return 'connected'
    }
    if (answer.error === 'access_denied') return 'declined'
    if (answer.error && NEEDS_PERSON.includes(answer.error)) return 'needs-person'
    return 'refused'
  }

  async function connect() {
    busy.value = true
    try {
      if (!status.value) await load()
      const request = await authorizeUrl('none')
      const frame = document.createElement('iframe')
      frame.style.display = 'none'
      frame.setAttribute('data-testid', 'synaplan-connect-frame')
      let loadedAt = 0
      frame.addEventListener('load', () => (loadedAt = Date.now()))
      const answer = answerFrom(
        () => frame.contentWindow,
        () => !loadedAt || Date.now() - loadedAt < SILENT_SETTLE_MS,
        SILENT_TIMEOUT_MS
      )
      frame.src = request.url
      document.body.appendChild(frame)
      const outcome = await settle(await answer, request).finally(() => frame.remove())
      if (outcome === 'refused') {
        throw new SignInRefusedError('the sign-in service refused')
      }
      needsSignIn.value = outcome !== 'connected'
    } finally {
      busy.value = false
    }
  }

  async function signIn() {
    const popup = window.open('about:blank', 'synaplan-connect', 'popup,width=520,height=680')
    if (!popup) {
      throw new PopupBlockedError('the sign-in window was blocked')
    }
    busy.value = true
    try {
      const request = await authorizeUrl(undefined)
      const answer = answerFrom(
        () => popup,
        () => !popup.closed,
        null
      )
      popup.location.href = request.url
      const outcome = await settle(await answer, request)
      if (outcome === 'connected') {
        needsSignIn.value = false
      } else if (outcome === 'declined') {
        throw new SignInDeclinedError('the person declined')
      } else if (outcome === 'refused') {
        throw new SignInRefusedError('the sign-in service refused')
      } else {
        throw new SignInClosedError('the sign-in window closed without an answer')
      }
    } finally {
      popup.close()
      busy.value = false
    }
  }

  async function disconnect() {
    busy.value = true
    try {
      await httpAuthenticated.delete('/api/synaplan/delegation', {
        schema: z.object({ connected: z.boolean() })
      })
    } finally {
      // The service forgets the token even when the identity provider
      // could not confirm the revocation, so the state is read either way.
      await load().catch(() => {})
      busy.value = false
    }
  }

  return { status, busy, needsSignIn, load, connect, signIn, disconnect }
}
