import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  apiDownloadText: vi.fn(),
  clipboard: vi.fn(),
  push: vi.fn(),
}))

vi.mock('../api/client', () => ({
  apiFetch: mocks.apiFetch,
  apiDownloadText: mocks.apiDownloadText,
  idempotencyHeaders: () => ({ 'Idempotency-Key': 'test-key' }),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push }),
  RouterLink: { template: '<a><slot /></a>' },
}))

import VpnPoolView from './VpnPoolView.vue'

const rows = [
  { id: 'jp-one', countryCode: 'JP', countryName: '日本', proxyType: 'datacenter', status: 'ready', vlessPort: 20000, mixedPort: 30000, exitIp: '203.0.113.10', candidateLatencyMs: 20, vlessLatencyMs: 81, socksLatencyMs: 70, version: 2, lastCheckedAt: '2026-08-26T00:00:00Z' },
  { id: 'kr-one', countryCode: 'KR', countryName: '韩国', proxyType: 'residential', status: 'degraded', vlessPort: 20001, mixedPort: 30001, exitIp: '203.0.113.11', candidateLatencyMs: 30, vlessLatencyMs: 0, socksLatencyMs: 0, version: 2 },
  { id: 'us-standby', countryCode: 'US', countryName: '美国', proxyType: 'datacenter', status: 'standby', vlessPort: 0, mixedPort: 0, exitIp: '', candidateLatencyMs: 44, vlessLatencyMs: 0, socksLatencyMs: 0, version: 1 },
]

beforeEach(() => {
  mocks.apiFetch.mockReset()
  mocks.apiDownloadText.mockReset()
  mocks.clipboard.mockReset()
  mocks.push.mockReset()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: mocks.clipboard } })
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
    if (path === '/api/v1/proxy-groups/jp-one/connections') return Promise.resolve({ vlessUri: 'vless://masked-test', socks5hUri: 'socks5h://masked-test' })
    return Promise.resolve(undefined)
  })
  mocks.apiDownloadText.mockResolvedValue('vless://masked-test\n')
})

it('renders a compact pool without service status cards and filters by country', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  expect(wrapper.text()).not.toContain('AimiliVPN 正常')
  expect(wrapper.text()).not.toContain('3x-ui 正常')
  expect(wrapper.findAll('[data-pool-row]')).toHaveLength(3)
  await wrapper.get('[data-country-filter]').setValue('JP')
  expect(wrapper.findAll('[data-pool-row]')).toHaveLength(1)
  expect(wrapper.text()).toContain('81 ms')
})

it('activates a standby candidate instead of exposing an unusable address', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  expect(wrapper.get('[data-copy="us-standby"]').attributes('disabled')).toBeDefined()
  await wrapper.get('[data-activate="us-standby"]').trigger('click')
  await flushPromises()

  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/us-standby/activate', { method: 'POST', headers: { 'Idempotency-Key': 'test-key' } })
})

it('copies the selected protocol address only for a ready row', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  await wrapper.get('[data-copy="jp-one"]').trigger('click')
  await flushPromises()

  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/jp-one/connections')
  expect(mocks.clipboard).toHaveBeenCalledWith('vless://masked-test')
  expect(wrapper.get('[data-copy="kr-one"]').attributes('disabled')).toBeDefined()
})

it('exports the current filters as a text list', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()
  await wrapper.get('[data-country-filter]').setValue('JP')
  await wrapper.get('[data-export]').trigger('click')
  await flushPromises()

  expect(mocks.apiDownloadText).toHaveBeenCalledWith(expect.stringContaining('protocol=vless'))
  expect(mocks.apiDownloadText).toHaveBeenCalledWith(expect.stringContaining('country=JP'))
})
