import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

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
  { id: 'agw-main', countryCode: 'SG', countryName: '新加坡', proxyType: 'datacenter', status: 'ready', egressSource: 'main', fixed: true, publicPort: 8443, vlessPort: 8443, mixedPort: 31000, exitIp: '203.0.113.9', candidateLatencyMs: 18, vlessLatencyMs: 76, socksLatencyMs: 66, protocolMode: 'vless_tcp_reality_vision', desiredProtocolMode: 'vless_tcp_reality_vision', protocolState: 'ready', subscriptionState: 'ready', availableProtocolModes: ['vless_tcp_reality_vision', 'vless_xhttp_reality', 'hysteria2_quic_tls'], version: 2 },
  { id: 'jp-one', countryCode: 'JP', countryName: '日本', proxyType: 'datacenter', status: 'ready', slotNumber: 1, fixed: true, publicPort: 20000, vlessPort: 20000, mixedPort: 30000, exitIp: '203.0.113.10', candidateLatencyMs: 20, vlessLatencyMs: 81, socksLatencyMs: 70, protocolMode: 'vless_xhttp_reality', desiredProtocolMode: 'vless_xhttp_reality', protocolState: 'ready', subscriptionState: 'ready', availableProtocolModes: ['vless_tcp_reality_vision', 'vless_xhttp_reality', 'hysteria2_quic_tls'], version: 2, lastCheckedAt: '2026-08-26T00:00:00Z' },
  { id: 'kr-one', countryCode: 'KR', countryName: '韩国', proxyType: 'residential', status: 'degraded', vlessPort: 20001, mixedPort: 30001, exitIp: '203.0.113.11', candidateLatencyMs: 30, vlessLatencyMs: 0, socksLatencyMs: 0, version: 2 },
  { id: 'us-standby', countryCode: 'US', countryName: '美国', proxyType: 'datacenter', status: 'standby', vlessPort: 0, mixedPort: 0, exitIp: '', candidateLatencyMs: 44, vlessLatencyMs: 0, socksLatencyMs: 0, version: 1 },
  { id: 'fr-provisioning', countryCode: 'FR', countryName: '法国', proxyType: 'datacenter', status: 'provisioning', vlessPort: 0, mixedPort: 0, exitIp: '', candidateLatencyMs: 50, vlessLatencyMs: 0, socksLatencyMs: 0, version: 1 },
  { id: 'de-rotating', countryCode: 'DE', countryName: '德国', proxyType: 'residential', status: 'rotating', vlessPort: 20002, mixedPort: 30002, exitIp: '', candidateLatencyMs: 51, vlessLatencyMs: 0, socksLatencyMs: 0, version: 2 },
  { id: 'gb-disabling', countryCode: 'GB', countryName: '英国', proxyType: 'datacenter', status: 'disabling', vlessPort: 20003, mixedPort: 30003, exitIp: '', candidateLatencyMs: 52, vlessLatencyMs: 0, socksLatencyMs: 0, version: 2 },
  { id: 'ca-repair', countryCode: 'CA', countryName: '加拿大', proxyType: 'datacenter', status: 'repair_required', vlessPort: 20004, mixedPort: 30004, exitIp: '', candidateLatencyMs: 53, vlessLatencyMs: 0, socksLatencyMs: 0, lastErrorCode: 'compensation_failed', version: 2 },
]

beforeEach(() => {
  mocks.apiFetch.mockReset()
  mocks.apiDownloadText.mockReset()
  mocks.clipboard.mockReset()
  mocks.push.mockReset()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: mocks.clipboard } })
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
    if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([
      { code: 'JP', name: '日本', candidateCount: 8, observedAt: 1_700_000_000 },
      { code: 'SG', name: '新加坡', candidateCount: 6, observedAt: 1_700_000_000 },
    ])
    if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
    if (path === '/api/v1/proxy-groups/jp-one/connections') return Promise.resolve({ protocolMode: 'vless_xhttp_reality', publicUri: 'vless://masked-public', vlessUri: 'vless://masked-public', socks5hUri: 'socks5h://masked-test' })
    return Promise.resolve(undefined)
  })
  mocks.apiDownloadText.mockResolvedValue('vless://masked-test\n')
})

afterEach(() => vi.useRealTimers())

it('renders a compact pool without service status cards and filters by country', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  expect(wrapper.text()).not.toContain('AimiliVPN 正常')
  expect(wrapper.text()).not.toContain('3x-ui 正常')
  expect(wrapper.findAll('[data-pool-row]')).toHaveLength(8)
  await wrapper.get('[data-country-filter]').setValue('JP')
  expect(wrapper.findAll('[data-pool-row]')).toHaveLength(2)
  expect(wrapper.text()).toContain('81 ms')
})

it('maps exact backend states into four user-facing status groups without enabling standby copies', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  expect(wrapper.get('[data-row-status="us-standby"]').text()).toContain('可选节点')
  expect(wrapper.get('[data-row-status="jp-one"]').text()).toContain('已启用')
  for (const id of ['fr-provisioning', 'de-rotating', 'gb-disabling']) {
    expect(wrapper.get(`[data-row-status="${id}"]`).text()).toContain('处理中')
  }
  expect(wrapper.get('[data-row-status="kr-one"]').text()).toContain('故障')
  expect(wrapper.get('[data-row-status="ca-repair"]').text()).toContain('故障')
  expect(wrapper.get('[data-row-detail="kr-one"]').text()).toContain('链路检测失败')
  expect(wrapper.get('[data-row-detail="ca-repair"]').text()).toContain('需要修复')
  expect(wrapper.get('[data-copy="us-standby"]').attributes('disabled')).toBeDefined()

  await wrapper.get('[data-status-filter]').setValue('processing')
  expect(wrapper.findAll('[data-pool-row]')).toHaveLength(5)
  expect(wrapper.get('[data-row-status="jp-one"]').text()).toContain('已启用')
})

it('offers standby replacement instead of exposing an unusable address', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  expect(wrapper.get('[data-copy="us-standby"]').attributes('disabled')).toBeDefined()
  expect(wrapper.get('[data-replace="us-standby"]').text()).toContain('替换到出口位')
})

it('copies the selected protocol address only for a ready row', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  await wrapper.get('[data-copy="jp-one"]').trigger('click')
  await flushPromises()

  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/jp-one/connections')
  expect(mocks.clipboard).toHaveBeenCalledWith('vless://masked-public')
  expect(wrapper.get('[data-copy="kr-one"]').attributes('disabled')).toBeDefined()
})

it('copies a test-style VLESS subscription', async () => {
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
    if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
    if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
    if (path === '/api/v1/proxy-groups/subscription') return Promise.resolve({ url: 'https://example.test/sub/masked', inboundCount: 4, updatedAt: '2026-08-29T00:00:00Z' })
    return Promise.resolve(undefined)
  })
  const wrapper = mount(VpnPoolView)
  await flushPromises()
  await wrapper.get('[data-copy-subscription]').trigger('click')
  await flushPromises()
  expect(mocks.clipboard).toHaveBeenCalledWith('https://example.test/sub/masked')
  expect(wrapper.text()).toContain('复制节点订阅')
})

it('shows fixed ready slots and replaces a standby candidate through a closable dialog', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()
  expect(wrapper.text()).toContain('出口位 1')
  expect(wrapper.findAll('[data-rotate="jp-one"]')).toHaveLength(0)
  await wrapper.get('[data-replace="us-standby"]').trigger('click')
  expect(wrapper.findAll('[data-replace-dialog]')).toHaveLength(1)
	const targets = wrapper.findAll('[data-replace-target] option')
	expect(targets[0].text()).toContain('主连接')
	expect(targets[1].text()).toContain('出口位 1')
	expect(wrapper.get('[data-replace-target]').text()).not.toContain('出口位 4')
  await wrapper.get('[data-replace-target]').setValue('jp-one')
  await wrapper.get('[data-confirm-replace]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/us-standby/replace', { method: 'POST', headers: { 'Idempotency-Key': 'test-key' }, body: JSON.stringify({ targetGroupId: 'jp-one' }) })
})

it('replaces a standby candidate into the main connection with a main-specific result', async () => {
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-replace="us-standby"]').trigger('click')
	expect((wrapper.get('[data-replace-target]').element as HTMLSelectElement).value).toBe('agw-main')
	await wrapper.get('[data-confirm-replace]').trigger('click')
	await flushPromises()
	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/us-standby/replace', { method: 'POST', headers: { 'Idempotency-Key': 'test-key' }, body: JSON.stringify({ targetGroupId: 'agw-main' }) })
	expect(wrapper.text()).toContain('主连接替换成功')
})

it('switches one ready egress protocol without changing mixed or SOCKS5H', async () => {
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	const selector = wrapper.get('[data-protocol="jp-one"]')
	expect(selector.attributes('disabled')).toBeUndefined()
	await selector.setValue('hysteria2_quic_tls')
	await flushPromises()
	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/jp-one/protocol-mode', {
		method: 'PUT', headers: { 'Idempotency-Key': 'test-key' }, body: JSON.stringify({ protocolMode: 'hysteria2_quic_tls' }),
	})
	expect(wrapper.text()).toContain('mixed/SOCKS5H 未变化')
})

it('disables public copy and protocol changes while subscription is pending or repair is required', async () => {
	const unsafeRows = rows.map(row => row.id === 'jp-one' ? { ...row, protocolState: 'subscription_pending', subscriptionState: 'pending' } : row)
	unsafeRows.push({ ...rows[1], id: 'repair-protocol', protocolState: 'repair_required', subscriptionState: 'repair_required' })
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(unsafeRows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	expect(wrapper.get('[data-protocol="jp-one"]').attributes('disabled')).toBeDefined()
	expect(wrapper.get('[data-copy="jp-one"]').attributes('disabled')).toBeDefined()
	expect(wrapper.get('[data-protocol="repair-protocol"]').attributes('disabled')).toBeDefined()
	expect(wrapper.get('[data-row-detail="jp-one"]').text()).toContain('订阅验证中')
	expect(wrapper.get('[data-row-detail="repair-protocol"]').text()).toContain('协议需要修复')
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

it('uses the official country catalog and refreshes only the selected country', async () => {
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	expect(wrapper.get('[data-country-filter]').text()).toContain('新加坡')
	expect(wrapper.get('[data-refresh-country]').attributes('disabled')).toBeDefined()
	await wrapper.get('[data-country-filter]').setValue('JP')
	await wrapper.get('[data-refresh-country]').trigger('click')
	await flushPromises()

	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/aimilivpn/refresh', {
		method: 'POST',
		headers: { 'Idempotency-Key': 'test-key' },
		body: JSON.stringify({ country: 'JP' }),
	})
	expect(wrapper.get('[data-sync-pool]').text()).toContain('同步代理状态')
})

it('polls a running country refresh and reloads the pool after completion', async () => {
	vi.useFakeTimers()
	let refreshReads = 0
	mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([{ code: 'JP', name: '日本', candidateCount: 8, observedAt: 1_700_000_000 }])
		if (path === '/api/v1/settings/aimilivpn/refresh' && init?.method === 'POST') return Promise.resolve({ state: 'running', country: 'JP', phase: 'fetching', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/settings/aimilivpn/refresh') {
			refreshReads++
			return Promise.resolve(refreshReads < 2 ? { state: 'running', country: 'JP', phase: 'probing', testedCount: 1, validCount: 0 } : { state: 'completed', country: 'JP', phase: '', testedCount: 5, validCount: 4 })
		}
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-country-filter]').setValue('JP')
	await wrapper.get('[data-refresh-country]').trigger('click')
	await flushPromises()
	await vi.advanceTimersByTimeAsync(4_000)
	await flushPromises()

	expect(refreshReads).toBeGreaterThanOrEqual(2)
	expect(wrapper.text()).toContain('已完成')
	expect(mocks.apiFetch.mock.calls.filter(([path]) => path === '/api/v1/proxy-groups').length).toBeGreaterThan(1)
	wrapper.unmount()
})

it('copies all filtered addresses without a trailing newline and leaves the clipboard unchanged for an empty export', async () => {
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-country-filter]').setValue('JP')
	mocks.apiDownloadText.mockResolvedValueOnce('vless://one\nvless://two\n')
	await wrapper.get('[data-copy-all]').trigger('click')
	await flushPromises()
	expect(mocks.clipboard).toHaveBeenCalledWith('vless://one\nvless://two')
	expect(mocks.apiDownloadText).toHaveBeenCalledWith(expect.stringContaining('country=JP'))

	mocks.clipboard.mockClear()
	mocks.apiDownloadText.mockResolvedValueOnce('\n')
	await wrapper.get('[data-copy-all]').trigger('click')
	await flushPromises()
	expect(mocks.clipboard).not.toHaveBeenCalled()
	expect(wrapper.text()).toContain('当前没有可用地址')
})
