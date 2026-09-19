const reloadTargetKey = 'aimili-gateway:reload-target-entry:v1'

function normalizeEntry(value: string, baseUrl: string): string {
  try {
    const url = new URL(value, baseUrl)
    return `${url.origin}${url.pathname}${url.search}`
  } catch {
    return ''
  }
}

function entryScriptFromDocument(documentValue: Document = document): string {
  const script = documentValue.querySelector<HTMLScriptElement>('script[type="module"][src]')
  return script?.src ?? ''
}

export function entryScriptFromHTML(html: string, baseUrl: string): string {
  const parsed = new DOMParser().parseFromString(html, 'text/html')
  const source = parsed.querySelector<HTMLScriptElement>('script[type="module"][src]')?.getAttribute('src') ?? ''
  return source ? normalizeEntry(source, baseUrl) : ''
}

interface VersionCheckOptions {
  currentEntry?: string
  fetchImpl?: typeof fetch
  reload?: () => void
  storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>
  baseUrl?: string
}

export async function checkForAppUpdate(options: VersionCheckOptions = {}): Promise<boolean> {
  const baseUrl = options.baseUrl ?? window.location.href
  const currentEntry = normalizeEntry(options.currentEntry ?? entryScriptFromDocument(), baseUrl)
  if (!currentEntry) return false

  try {
    const response = await (options.fetchImpl ?? fetch)('/', {
      method: 'GET',
      cache: 'no-store',
      credentials: 'same-origin',
      headers: { Accept: 'text/html' },
    })
    if (!response.ok) return false

    const deployedEntry = entryScriptFromHTML(await response.text(), baseUrl)
    if (!deployedEntry) return false

    const storage = options.storage ?? window.sessionStorage
    if (deployedEntry === currentEntry) {
      if (storage.getItem(reloadTargetKey) === deployedEntry) storage.removeItem(reloadTargetKey)
      return false
    }
    if (storage.getItem(reloadTargetKey) === deployedEntry) return false

    storage.setItem(reloadTargetKey, deployedEntry)
    ;(options.reload ?? (() => window.location.reload()))()
    return true
  } catch {
    return false
  }
}

export function startAppVersionWatcher(intervalMs = 60_000): void {
  const check = () => { void checkForAppUpdate() }
  window.setInterval(check, intervalMs)
  const onVisibilityChange = () => {
    if (document.visibilityState === 'visible') check()
  }
  document.addEventListener('visibilitychange', onVisibilityChange)
  check()
}
