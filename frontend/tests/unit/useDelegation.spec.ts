import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const get = vi.fn()
const post = vi.fn()
const del = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, post, delete: del } }),
  useConfigStore: () => ({ serverUrl: 'https://cloud.example.com/' })
}))

import { useDelegation } from '../../src/composables/useDelegation'

const available = {
  available: true,
  connected: false,
  clientId: 'synaplan-opencloud',
  authorizeEndpoint: 'https://idp.example.com/auth'
}

function answerFrameWith(answer: (url: URL) => Record<string, string | null>) {
  const observer = new MutationObserver(() => {
    const frame = document.querySelector<HTMLIFrameElement>(
      '[data-testid="synaplan-connect-frame"]'
    )
    if (!frame) return
    observer.disconnect()
    const url = new URL(frame.src)
    window.dispatchEvent(
      new MessageEvent('message', {
        origin: 'https://cloud.example.com',
        data: { type: 'synaplan-connect', ...answer(url) }
      })
    )
  })
  observer.observe(document.body, { childList: true })
}

describe('useDelegation', () => {
  const assign = vi.fn()

  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: available })
    post.mockReset().mockResolvedValue({ data: { connected: true } })
    del.mockReset().mockResolvedValue({ data: { connected: false } })
    sessionStorage.clear()
    assign.mockReset()
    vi.stubGlobal('location', {
      origin: 'https://cloud.example.com',
      href: 'https://cloud.example.com/synaplan',
      assign
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('asks silently for a code with PKCE and hands it to the service', async () => {
    let asked: URL | undefined
    answerFrameWith((url) => {
      asked = url
      return { code: 'the-code', state: url.searchParams.get('state'), error: null }
    })
    const access = useDelegation()
    await access.connect()

    expect(asked?.searchParams.get('prompt')).toBe('none')
    expect(asked?.searchParams.get('scope')).toBe('openid offline_access')
    expect(asked?.searchParams.get('code_challenge_method')).toBe('S256')
    expect(asked?.searchParams.get('redirect_uri')).toBe(
      'https://cloud.example.com/assets/apps/synaplan/callback.html'
    )
    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/delegation',
      expect.objectContaining({ code: 'the-code', verifier: expect.any(String) }),
      expect.anything()
    )
    expect(assign).not.toHaveBeenCalled()
  })

  it('redirects when the provider needs the person, and finishes after the return', async () => {
    answerFrameWith((url) => ({
      code: null,
      state: url.searchParams.get('state'),
      error: 'consent_required'
    }))
    const access = useDelegation()
    await access.connect()

    expect(post).not.toHaveBeenCalled()
    const target = new URL(assign.mock.calls[0][0])
    expect(target.searchParams.get('prompt')).toBeNull()
    sessionStorage.setItem(
      'synaplan-connect-answer',
      JSON.stringify({ code: 'late-code', state: target.searchParams.get('state'), error: null })
    )

    expect(await useDelegation().resumeRedirect()).toBe(true)
    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/delegation',
      expect.objectContaining({ code: 'late-code' }),
      expect.anything()
    )
  })

  it('ignores a returned code whose state does not match', async () => {
    sessionStorage.setItem(
      'synaplan-connect-pending',
      JSON.stringify({ state: 'a', verifier: 'v' })
    )
    sessionStorage.setItem(
      'synaplan-connect-answer',
      JSON.stringify({ code: 'c', state: 'b', error: null })
    )
    expect(await useDelegation().resumeRedirect()).toBe(false)
    expect(post).not.toHaveBeenCalled()
  })

  it('ignores a stored answer that is not one', async () => {
    sessionStorage.setItem('synaplan-connect-pending', '{"state":"a"}')
    sessionStorage.setItem('synaplan-connect-answer', 'not json')
    expect(await useDelegation().resumeRedirect()).toBe(false)
    expect(post).not.toHaveBeenCalled()
  })

  it('withdraws the permission on request', async () => {
    const access = useDelegation()
    await access.disconnect()
    expect(del).toHaveBeenCalledWith('/api/synaplan/delegation', expect.anything())
    expect(access.status.value?.connected).toBe(false)
  })
})
