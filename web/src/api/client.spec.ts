import { beforeEach, expect, it, vi } from 'vitest'

beforeEach(() => {
  vi.resetModules()
  vi.unstubAllGlobals()
})

it('uses same-origin cookies and attaches session CSRF to mutations', async () => {
  const fetchMock = vi.fn()
  fetchMock
    .mockResolvedValueOnce(jsonResponse({ authenticated: true, csrfToken: 'test-csrf', expiresAt: '2026-08-24T00:00:00Z' }))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const { apiFetch } = await import('./client')

  await apiFetch('/api/v1/auth/logout', { method: 'POST' })

  if (fetchMock.mock.calls.length !== 2) {
    throw new Error('CSRF bootstrap and mutation requests were not both sent')
  }
  const sessionInit = fetchMock.mock.calls[0][1] as RequestInit
  const mutationInit = fetchMock.mock.calls[1][1] as RequestInit
  if (sessionInit.credentials !== 'same-origin' || mutationInit.credentials !== 'same-origin') {
    throw new Error('same-origin credentials were not configured')
  }
  const headers = new Headers(mutationInit.headers)
  if (headers.get('X-CSRF-Token') !== 'test-csrf') {
    throw new Error('CSRF header was not attached')
  }
})

it('rejects non-JSON API error responses', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('proxy error', {
    status: 502,
    headers: { 'Content-Type': 'text/plain' },
  })))
  const { apiFetch } = await import('./client')
  await expect(apiFetch('/api/v1/overview')).rejects.toThrow('invalid_error_response')
})

it('invokes the unauthorized handler on HTTP 401', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: 'unauthorized' }, 401)))
  const { apiFetch, setUnauthorizedHandler } = await import('./client')
  const unauthorized = vi.fn()
  setUnauthorizedHandler(unauthorized)
  await expect(apiFetch('/api/v1/overview')).rejects.toThrow('unauthorized')
  expect(unauthorized).toHaveBeenCalledOnce()
})

it('downloads authenticated text exports without treating them as invalid JSON', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('vless://masked\n', {
    status: 200,
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  })))
  const { apiDownloadText } = await import('./client')

  await expect(apiDownloadText('/api/v1/proxy-groups/export?protocol=vless')).resolves.toBe('vless://masked\n')
})

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}
