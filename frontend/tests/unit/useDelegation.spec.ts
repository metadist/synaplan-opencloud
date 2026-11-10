import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const get = vi.fn()
const post = vi.fn()
const del = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, post, delete: del } })
}))
vi.mock('../../src/callback.html?url', () => ({
  default: 'https://apps.example.com/synaplan/assets/callback-abc.html'
}))

import {
  SignInClosedError,
  SignInDeclinedError,
  SignInRefusedError,
  useDelegation
} from '../../src/composables/useDelegation'

const CALLBACK = 'https://apps.example.com/synaplan/assets/callback-abc.html'
const CALLBACK_ORIGIN = 'https://apps.example.com'

const available = {
  available: true,
  connected: false,
  clientId: 'synaplan-opencloud',
  authorizeEndpoint: 'https://idp.example.com/auth'
}

type Reply = {
  from?: 'frame' | 'elsewhere'
  origin?: string
  state?: (sent: string) => string
  error?: string
}

function post_(source: MessageEventSource | null, origin: string, data: unknown) {
  window.dispatchEvent(new MessageEvent('message', { source, origin, data }))
}

/** Answers the silent frame as callback.html would, once it is added. */
function answerFrame(reply: Reply = {}) {
  const observer = new MutationObserver(() => {
    const frame = document.querySelector<HTMLIFrameElement>(
      '[data-testid="synaplan-connect-frame"]'
    )
    if (!frame) return
    observer.disconnect()
    const sent = new URL(frame.src).searchParams.get('state') ?? ''
    post_(
      reply.from === 'elsewhere' ? window : frame.contentWindow,
      reply.origin ?? CALLBACK_ORIGIN,
      {
        type: 'synaplan-connect',
        code: reply.error ? null : 'the-code',
        state: reply.state ? reply.state(sent) : sent,
        error: reply.error ?? null
      }
    )
  })
  observer.observe(document.body, { childList: true })
}

describe('useDelegation', () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: available })
    post.mockReset().mockResolvedValue({ data: { connected: true } })
    del.mockReset().mockResolvedValue({ data: { connected: false } })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('asks silently for a code with PKCE and hands it to the service', async () => {
    answerFrame()
    const delegation = useDelegation()
    await delegation.connect()

    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/delegation',
      expect.objectContaining({ code: 'the-code', redirectUri: CALLBACK }),
      expect.anything()
    )
    expect(delegation.needsSignIn.value).toBe(false)
    expect(document.querySelector('[data-testid="synaplan-connect-frame"]')).toBeNull()
  })

  it.each<[string, Reply]>([
    ['another window', { from: 'elsewhere' }],
    ['another origin', { origin: 'https://evil.example.com' }],
    ['another state', { state: () => 'forged' }]
  ])('ignores an answer from %s and asks the person to sign in', async (_name, reply) => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'setInterval', 'clearTimeout', 'clearInterval'] })
    answerFrame(reply)
    const delegation = useDelegation()
    let finished = false
    const done = delegation.connect().finally(() => (finished = true))
    while (!finished && !document.querySelector('[data-testid="synaplan-connect-frame"]')) {
      await new Promise((resolve) => setImmediate(resolve))
    }
    await vi.advanceTimersByTimeAsync(10_000)
    await done

    expect(post).not.toHaveBeenCalled()
    expect(delegation.needsSignIn.value).toBe(true)
  })

  it('asks the person to sign in when the sign-in service needs them', async () => {
    answerFrame({ error: 'consent_required' })
    const delegation = useDelegation()
    await delegation.connect()

    expect(post).not.toHaveBeenCalled()
    expect(delegation.needsSignIn.value).toBe(true)
  })

  it('names a refusal only an administrator can fix', async () => {
    answerFrame({ error: 'unauthorized_client' })
    const delegation = useDelegation()

    await expect(delegation.connect()).rejects.toBeInstanceOf(SignInRefusedError)
    expect(post).not.toHaveBeenCalled()
    expect(delegation.needsSignIn.value).toBe(false)
  })

  it('does not take a refusal with another state for real', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'setInterval', 'clearTimeout', 'clearInterval'] })
    answerFrame({ error: 'unauthorized_client', state: () => 'forged' })
    const delegation = useDelegation()
    let finished = false
    const done = delegation.connect().finally(() => (finished = true))
    while (!finished && !document.querySelector('[data-testid="synaplan-connect-frame"]')) {
      await new Promise((resolve) => setImmediate(resolve))
    }
    await vi.advanceTimersByTimeAsync(10_000)
    await done

    expect(delegation.needsSignIn.value).toBe(true)
  })

  it('stops waiting soon after the sign-in service shows its own page', async () => {
    vi.useFakeTimers({
      toFake: ['setTimeout', 'setInterval', 'clearTimeout', 'clearInterval', 'Date']
    })
    const delegation = useDelegation()
    let finished = false
    const done = delegation.connect().finally(() => (finished = true))
    let frame: HTMLIFrameElement | null = null
    while (!finished && !frame) {
      await new Promise((resolve) => setImmediate(resolve))
      frame = document.querySelector('[data-testid="synaplan-connect-frame"]')
    }
    frame?.dispatchEvent(new Event('load'))
    await vi.advanceTimersByTimeAsync(1_500)

    expect(finished).toBe(true)
    await done
    expect(delegation.needsSignIn.value).toBe(true)
  })

  it('signs in through a popup the person opened', async () => {
    const popup = { closed: false, location: { href: '' }, close: vi.fn() }
    vi.stubGlobal(
      'open',
      vi.fn(() => popup)
    )
    const delegation = useDelegation()
    await delegation.load()

    const done = delegation.signIn()
    await vi.waitFor(() => expect(popup.location.href).toContain('https://idp.example.com/auth'))
    const sent = new URL(popup.location.href)
    expect(sent.searchParams.get('prompt')).toBeNull()
    expect(sent.searchParams.get('redirect_uri')).toBe(CALLBACK)
    post_(popup as unknown as Window, CALLBACK_ORIGIN, {
      type: 'synaplan-connect',
      code: 'popup-code',
      state: sent.searchParams.get('state'),
      error: null
    })
    await done

    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/delegation',
      expect.objectContaining({ code: 'popup-code' }),
      expect.anything()
    )
    expect(popup.close).toHaveBeenCalled()
  })

  it('says so when the sign-in window closes without an answer', async () => {
    const popup = { closed: false, location: { href: '' }, close: vi.fn() }
    vi.stubGlobal(
      'open',
      vi.fn(() => popup)
    )
    const delegation = useDelegation()
    await delegation.load()

    const done = delegation.signIn()
    await vi.waitFor(() => expect(popup.location.href).toContain('https://idp.example.com/auth'))
    popup.closed = true

    await expect(done).rejects.toBeInstanceOf(SignInClosedError)
    expect(post).not.toHaveBeenCalled()
  })

  it('says so when the person declines in the sign-in window', async () => {
    const popup = { closed: false, location: { href: '' }, close: vi.fn() }
    vi.stubGlobal(
      'open',
      vi.fn(() => popup)
    )
    const delegation = useDelegation()
    await delegation.load()

    const done = delegation.signIn()
    await vi.waitFor(() => expect(popup.location.href).toContain('https://idp.example.com/auth'))
    post_(popup as unknown as Window, CALLBACK_ORIGIN, {
      type: 'synaplan-connect',
      code: null,
      state: new URL(popup.location.href).searchParams.get('state'),
      error: 'access_denied'
    })

    await expect(done).rejects.toBeInstanceOf(SignInDeclinedError)
    expect(popup.close).toHaveBeenCalled()
  })

  it('says so when the browser blocks the sign-in window', async () => {
    vi.stubGlobal(
      'open',
      vi.fn(() => null)
    )
    await expect(useDelegation().signIn()).rejects.toThrow('blocked')
  })

  it('withdraws the permission on request', async () => {
    const delegation = useDelegation()
    await delegation.disconnect()
    expect(del).toHaveBeenCalledWith('/api/synaplan/delegation', expect.anything())
  })
})
