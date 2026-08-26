export type Health = 'healthy' | 'degraded' | 'unavailable'

export interface ProbeResult {
  service: string
  health: Health
  version?: string
  capabilities: string[]
  errorCode?: string
  checkedAt?: string
}

export interface OverviewPayload {
  gateway: { health: Health }
  services: ProbeResult[]
  expertModeAvailable: boolean
}

export interface NavigationPayload {
  expertModeUrl: string
}

export interface SessionPayload {
  authenticated: boolean
  csrfToken: string
  expiresAt: string
}

export interface AuthOptionsPayload {
  totpRequired: boolean
}

export type ProxyType = 'residential' | 'datacenter'
export type ProxyGroupStatus = 'standby' | 'provisioning' | 'ready' | 'rotating' | 'degraded' | 'repair_required' | 'disabling'

export interface CountryPayload { code: string; name: string; residentialCount: number; datacenterCount: number }
export interface ProxyGroupPayload {
  id: string; countryCode: string; countryName: string; proxyType: ProxyType; status: ProxyGroupStatus
  vlessPort: number; mixedPort: number; exitIp: string; candidateLatencyMs: number; vlessLatencyMs: number; socksLatencyMs: number
  lastErrorCode?: string; version: number; lastCheckedAt?: string
}
export interface ConnectionsPayload { vlessUri: string; socks5hUri: string }

export function idempotencyHeaders(): HeadersInit {
  const random = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}`
  return { 'Idempotency-Key': random }
}

export class APIError extends Error {
  constructor(
    public readonly status: number,
    code: string,
  ) {
    super(code)
    this.name = 'APIError'
  }
}

let csrfToken: string | null = null
let unauthorizedHandler: () => void = () => {
  if (window.location.pathname !== '/login') {
    window.location.assign('/login')
  }
}

export function setUnauthorizedHandler(handler: () => void): void {
  unauthorizedHandler = handler
}

export async function apiFetch<T = void>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? 'GET').toUpperCase()
  const mutation = !['GET', 'HEAD', 'OPTIONS'].includes(method)
  if (mutation && path !== '/api/v1/auth/login' && csrfToken === null) {
    const session = await apiFetch<SessionPayload>('/api/v1/auth/session')
    csrfToken = session.csrfToken
  }

  const headers = new Headers(init.headers)
  if (init.body !== undefined && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (mutation && path !== '/api/v1/auth/login' && csrfToken !== null) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  const response = await fetch(path, {
    ...init,
    method,
    headers,
    credentials: 'same-origin',
  })

  if (response.status === 401) {
    csrfToken = null
    unauthorizedHandler()
  }
  if (response.status === 204) {
    if (path === '/api/v1/auth/logout') {
      csrfToken = null
    }
    return undefined as T
  }

  const contentType = response.headers.get('Content-Type') ?? ''
  if (!contentType.toLowerCase().includes('application/json')) {
    throw new APIError(response.status, response.ok ? 'invalid_response' : 'invalid_error_response')
  }
  let payload: unknown
  try {
    payload = await response.json()
  } catch {
    throw new APIError(response.status, response.ok ? 'invalid_response' : 'invalid_error_response')
  }
  if (!response.ok) {
    const code = isRecord(payload) && typeof payload.error === 'string' ? payload.error : 'request_failed'
    throw new APIError(response.status, code)
  }
  if (path === '/api/v1/auth/session' && isRecord(payload) && typeof payload.csrfToken === 'string') {
    csrfToken = payload.csrfToken
  }
  return payload as T
}

export async function apiDownloadText(path: string): Promise<string> {
  const response = await fetch(path, { method: 'GET', credentials: 'same-origin' })
  if (response.status === 401) {
    csrfToken = null
    unauthorizedHandler()
  }
  if (!response.ok) {
    const contentType = response.headers.get('Content-Type') ?? ''
    if (contentType.toLowerCase().includes('application/json')) {
      try {
        const payload: unknown = await response.json()
        const code = isRecord(payload) && typeof payload.error === 'string' ? payload.error : 'request_failed'
        throw new APIError(response.status, code)
      } catch (error) {
        if (error instanceof APIError) throw error
      }
    }
    throw new APIError(response.status, 'request_failed')
  }
  const contentType = response.headers.get('Content-Type') ?? ''
  if (!contentType.toLowerCase().startsWith('text/plain')) {
    throw new APIError(response.status, 'invalid_response')
  }
  return response.text()
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}
