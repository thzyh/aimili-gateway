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
export type ProtocolMode = 'vless_tcp_reality_vision' | 'vless_xhttp_reality' | 'hysteria2_quic_tls'
export type ProtocolState = 'ready' | 'switching' | 'subscription_pending' | 'rolling_back' | 'repair_required'
export type SubscriptionState = 'ready' | 'pending' | 'repair_required' | 'unavailable'

export interface CountryPayload { code: string; name: string; residentialCount: number; datacenterCount: number }
export interface ProxyGroupPayload {
  id: string; countryCode: string; countryName: string; proxyType: ProxyType; status: ProxyGroupStatus
  egressSource?: 'slot' | 'main'
	  slotNumber?: number; fixed?: boolean
  publicPort?: number
  protocolMode?: ProtocolMode; desiredProtocolMode?: ProtocolMode; protocolState?: ProtocolState; subscriptionState?: SubscriptionState
  availableProtocolModes?: ProtocolMode[]
  vlessPort: number; mixedPort: number; candidateIp?: string; exitIp: string; exitIpCheckedAt?: number; candidateLatencyMs: number; vlessLatencyMs: number; socksLatencyMs: number
  lastErrorCode?: string; version: number; lastCheckedAt?: string
}
export interface ConnectionsPayload { protocolMode: ProtocolMode; publicUri: string; vlessUri?: string; vlessError?: string; socks5hUri: string }
export interface ProtocolModePayload {
  protocolMode: ProtocolMode; desiredProtocolMode: ProtocolMode; protocolState: ProtocolState; subscriptionState: SubscriptionState
  availableProtocolModes: ProtocolMode[]; lastErrorCode?: string; updatedAt: string
}
export interface SubscriptionPayload { url: string; inboundCount: number; updatedAt: string }
export type AccountSyncStatus = 'reset_required' | 'synced' | 'checking' | 'repair_required' | 'incompatible'
export type MixedPolicyApplyStatus = 'pending' | 'applying' | 'applied' | 'failed' | 'repair_required'
export interface SettingsSummaryPayload { accountSyncStatus: AccountSyncStatus; candidateCount: number; onlineCount: number; maxOnline: number }
export interface MixedSourcePolicyPayload { enabled: boolean; cidrs: string[]; applyStatus: MixedPolicyApplyStatus }
export interface CandidateCountryPayload { code: string; name: string; candidateCount: number; observedAt: number; officialCandidateTotal?: number; validNodeCount?: number; validCountryCount?: number }
export type CountryRefreshState = 'idle' | 'running' | 'completed' | 'failed'
export interface CountryRefreshPayload {
  state: CountryRefreshState; country: string; phase: string
  resultCode?: 'success' | 'no_official_candidates' | 'no_usable_nodes' | 'operation_busy' | 'maintenance_busy' | 'upstream_unavailable'
  officialCount?: number; usableCount?: number; retainedCount?: number
  catalogCount?: number; countryCandidateCount?: number; testedCount: number; validCount: number; preservedCount?: number
  startedAt?: number; finishedAt?: number; errorCode?: string
  stopReason?: string; cacheTotal?: number; countryValidCount?: number
}
export interface XUISettingsPayload { managedPublicCount: number; managedVlessCount: number; managedMixedCount: number; managedOutboundCount: number; ownershipMatches: boolean; lastCheckedAt?: string }
export type UpdateKind = 'ui' | 'gateway'
export type UpdateState = 'pending' | 'downloading' | 'validating' | 'switching' | 'verifying' | 'rolled_back' | 'success' | 'failed' | 'repair_required'
export interface UpdateVersionPayload { kind: UpdateKind; version: string; compatible: boolean }
export interface UpdateSummaryPayload { enabled: boolean; currentGateway: string; currentUi?: string; available: UpdateVersionPayload[] }
export interface UpdateResultPayload { runId: string; kind: UpdateKind; version?: string; state: UpdateState; errorCode?: string }

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

const backendLoginPaths = new Set(['/api/v1/backends/3x-ui/login'])

export async function openBackend(path: string): Promise<string> {
  if (!backendLoginPaths.has(path)) {
    throw new APIError(400, 'invalid_backend_target')
  }
  if (csrfToken === null) {
    const session = await apiFetch<SessionPayload>('/api/v1/auth/session')
    csrfToken = session.csrfToken
  }
  const response = await fetch(path, {
    method: 'POST',
    headers: { 'X-CSRF-Token': csrfToken },
    credentials: 'same-origin',
    redirect: 'follow',
  })
  if (response.status === 401) {
    csrfToken = null
    unauthorizedHandler()
  }
  if (!response.ok) {
    let code = 'automatic_login_failed'
    const contentType = response.headers.get('Content-Type') ?? ''
    if (contentType.toLowerCase().includes('application/json')) {
      try {
        const payload: unknown = await response.json()
        if (isRecord(payload) && typeof payload.error === 'string') code = payload.error
      } catch {
        code = 'invalid_error_response'
      }
    }
    throw new APIError(response.status, code)
  }
  const destination = new URL(response.url, window.location.origin)
  if (destination.origin !== window.location.origin) {
    throw new APIError(502, 'unsafe_redirect')
  }
  return `${destination.pathname}${destination.search}${destination.hash}`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}
