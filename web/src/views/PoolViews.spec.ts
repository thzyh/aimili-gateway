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
import SocksPoolView from './SocksPoolView.vue'

const rows = [
  { id: 'agw-main', countryCode: 'SG', countryName: '新加坡', proxyType: 'datacenter', status: 'ready', egressSource: 'main', fixed: true, publicPort: 8443, vlessPort: 8443, mixedPort: 31000, candidateIp: '198.51.100.9', exitIp: '203.0.113.9', exitIpCheckedAt: 1_700_000_010, candidateLatencyMs: 18, vlessLatencyMs: 76, socksLatencyMs: 66, protocolMode: 'vless_tcp_reality_vision', desiredProtocolMode: 'vless_tcp_reality_vision', protocolState: 'ready', subscriptionState: 'ready', availableProtocolModes: ['vless_tcp_reality_vision', 'vless_xhttp_reality', 'hysteria2_quic_tls'], version: 2 },
  { id: 'jp-one', countryCode: 'JP', countryName: '日本', proxyType: 'datacenter', status: 'ready', slotNumber: 1, fixed: true, publicPort: 20000, vlessPort: 20000, mixedPort: 30000, exitIp: '203.0.113.10', candidateLatencyMs: 20, vlessLatencyMs: 81, socksLatencyMs: 70, protocolMode: 'vless_xhttp_reality', desiredProtocolMode: 'vless_xhttp_reality', protocolState: 'ready', subscriptionState: 'ready', availableProtocolModes: ['vless_tcp_reality_vision', 'vless_xhttp_reality', 'hysteria2_quic_tls'], version: 2, lastCheckedAt: '2026-08-26T00:00:00Z' },
  { id: 'kr-one', countryCode: 'KR', countryName: '韩国', proxyType: 'residential', status: 'degraded', slotNumber: 2, fixed: true, publicPort: 20001, vlessPort: 20001, mixedPort: 30001, exitIp: '203.0.113.11', candidateLatencyMs: 30, vlessLatencyMs: 0, socksLatencyMs: 0, version: 2 },
  { id: 'us-standby', countryCode: 'US', countryName: '美国', proxyType: 'datacenter', status: 'standby', vlessPort: 0, mixedPort: 0, candidateIp: '198.51.100.12', exitIp: '203.0.113.12', exitIpCheckedAt: 1_700_000_020, candidateLatencyMs: 44, vlessLatencyMs: 0, socksLatencyMs: 0, version: 1 },
  { id: 'fr-provisioning', countryCode: 'FR', countryName: '法国', proxyType: 'datacenter', status: 'provisioning', vlessPort: 0, mixedPort: 0, candidateIp: '198.51.100.13', exitIp: '', candidateLatencyMs: 50, vlessLatencyMs: 0, socksLatencyMs: 0, version: 1 },
  { id: 'de-rotating', countryCode: 'DE', countryName: '德国', proxyType: 'residential', status: 'rotating', slotNumber: 3, fixed: true, publicPort: 20002, vlessPort: 20002, mixedPort: 30002, exitIp: '', candidateLatencyMs: 51, vlessLatencyMs: 0, socksLatencyMs: 0, version: 2 },
  { id: 'gb-disabling', countryCode: 'GB', countryName: '英国', proxyType: 'datacenter', status: 'disabling', vlessPort: 20003, mixedPort: 30003, exitIp: '', candidateLatencyMs: 52, vlessLatencyMs: 0, socksLatencyMs: 0, version: 2 },
  { id: 'ca-repair', countryCode: 'CA', countryName: '加拿大', proxyType: 'datacenter', status: 'repair_required', vlessPort: 20004, mixedPort: 30004, exitIp: '', candidateLatencyMs: 53, vlessLatencyMs: 0, socksLatencyMs: 0, lastErrorCode: 'compensation_failed', version: 2 },
]

beforeEach(() => {
  localStorage.clear()
  mocks.apiFetch.mockReset()
  mocks.apiDownloadText.mockReset()
  mocks.clipboard.mockReset()
  mocks.push.mockReset()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: mocks.clipboard } })
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
    if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([
      { code: 'JP', name: '日本', candidateCount: 8, observedAt: 1_700_000_000, officialCandidateTotal: 100, validNodeCount: 66, validCountryCount: 5, targetValidNodeCount: 64, maxValidNodeCount: 150 },
      { code: 'SG', name: '新加坡', candidateCount: 6, observedAt: 1_700_000_000, officialCandidateTotal: 100, validNodeCount: 66, validCountryCount: 5, targetValidNodeCount: 64, maxValidNodeCount: 150 },
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
  expect(wrapper.findAll('[data-pool-row]')).toHaveLength(4)
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
  expect(wrapper.findAll('[data-pool-row]')).toHaveLength(6)
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

it('keeps the main connection and all five fixed exits pinned in slot order', async () => {
  const slotFour = { ...rows[1], id: 'slot-four', slotNumber: 4, publicPort: 20003, mixedPort: 30003 }
  const slotFive = { ...rows[1], id: 'slot-five', slotNumber: 5, publicPort: 20004, mixedPort: 30004 }
  const shuffled = [rows[3], slotFive, rows[5], rows[0], slotFour, rows[2], rows[1]]
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/proxy-groups') return Promise.resolve(shuffled)
    if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
    if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
    return Promise.resolve(undefined)
  })

  const wrapper = mount(VpnPoolView)
  await flushPromises()

  expect(wrapper.findAll('[data-pool-row]').map(row => row.attributes('data-row-id')).slice(0, 6)).toEqual([
    'agw-main', 'jp-one', 'kr-one', 'de-rotating', 'slot-four', 'slot-five',
  ])
})

it('offers a repair-required runtime slot a safe recheck and synchronization action', async () => {
	const repairRow = { ...rows[1], id: 'repair-slot', status: 'repair_required', lastErrorCode: 'rollback_failed' }
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve([repairRow])
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/proxy-groups/repair-slot/check') return Promise.resolve({ ...repairRow, status: 'ready' })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	expect(wrapper.get('[data-repair="repair-slot"]').text()).toContain('检测并自动修复')
	await wrapper.get('[data-repair="repair-slot"]').trigger('click')
	await flushPromises()
	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/repair-slot/check', { method: 'POST' })
})

it('offers a degraded runtime slot the same safe recheck and synchronization action', async () => {
	const degradedRow = { ...rows[1], id: 'degraded-slot', status: 'degraded' }
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve([degradedRow])
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/proxy-groups/degraded-slot/check') return Promise.resolve({ ...degradedRow, status: 'ready', autoRepairPerformed: true })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	expect(wrapper.get('[data-repair="degraded-slot"]').text()).toContain('检测并自动修复')
	await wrapper.get('[data-repair="degraded-slot"]').trigger('click')
	await flushPromises()
	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/degraded-slot/check', { method: 'POST' })
	expect(wrapper.get('[data-top-notice]').text()).toContain('出口 1 自动修复成功')
})

it('keeps failed automatic repairs selectable in the unified replacement flow', async () => {
	const manualMain = { ...rows[0], status: 'degraded', lastErrorCode: 'manual_repair_required' }
	const manualRow = { ...rows[1], id: 'manual-slot', status: 'degraded', lastErrorCode: 'manual_replacement_required' }
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve([manualMain, manualRow, rows[3]])
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	expect(wrapper.get('[data-row-id="manual-slot"]').attributes('data-row-id')).toBe('manual-slot')
	expect(wrapper.get('[data-row-detail="manual-slot"]').text()).toContain('自动修复已失败，等待人工更换')
	expect(wrapper.get('[data-repair="manual-slot"]').text()).toBe('复核状态')
	expect(wrapper.find('[data-manual-replace]').exists()).toBe(false)

	await wrapper.get('[data-replace="us-standby"]').trigger('click')
	expect(wrapper.get('[data-replace-dialog]').text()).toContain('替换到出口位')
	await wrapper.get('[data-replace-target]').trigger('click')
	const targets = wrapper.findAll('[data-replace-option]')
	expect(targets[0].text()).toContain('【故障·自动修复失败】主连接')
	expect(targets[1].text()).toContain('【故障·自动修复失败】出口位 1')
	expect(targets[0].text()).not.toContain('需明确选择')
	expect(wrapper.get('[data-replace-target]').classes()).toContain('fault-target')
	expect(wrapper.get('[data-replace-target-warning]').text()).toContain('出口位 1 自动修复已失败')

	await wrapper.get('[data-target-id="manual-slot"]').trigger('click')
	expect(wrapper.get('[data-replace-target-warning]').text()).toContain('出口位 1 自动修复已失败')
	await wrapper.get('[data-confirm-replace]').trigger('click')
	await flushPromises()
	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/us-standby/replace', {
		method: 'POST', headers: { 'Idempotency-Key': 'test-key' }, body: JSON.stringify({ targetGroupId: 'manual-slot' }),
	})
})

it('treats an interrupted automatic repair as a finished one-shot attempt', async () => {
	const interruptedMain = { ...rows[0], status: 'degraded', lastErrorCode: 'repair_interrupted' }
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve([interruptedMain])
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		return Promise.resolve(undefined)
	})

	const wrapper = mount(VpnPoolView)
	await flushPromises()

	expect(wrapper.get('[data-repair="agw-main"]').text()).toContain('复核状态')
	expect(wrapper.get('[data-repair="agw-main"]').attributes('title')).toContain('不会再次自动更换节点')
})

it('rotates SOCKS5H credentials with visible progress and a safe success message', async () => {
	let finishRotation!: () => void
	const pendingRotation = new Promise<void>(resolve => { finishRotation = resolve })
	mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/settings/socks5h-credentials/rotate' && init?.method === 'POST') return pendingRotation
		return Promise.resolve(undefined)
	})
	const wrapper = mount(SocksPoolView)
	await flushPromises()

	await wrapper.get('[data-rotate-socks-credentials]').trigger('click')
	await wrapper.vm.$nextTick()
	expect(wrapper.get('[data-top-notice]').attributes('data-notice-kind')).toBe('progress')
	expect(wrapper.get('[data-top-notice]').text()).toContain('正在随机更换 SOCKS5H 用户名和密码')
	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/socks5h-credentials/rotate', {
		method: 'POST', headers: { 'Idempotency-Key': 'test-key' },
	})

	finishRotation()
	await flushPromises()
	expect(wrapper.get('[data-top-notice]').attributes('data-notice-kind')).toBe('success')
	expect(wrapper.get('[data-top-notice]').text()).toContain('旧代理地址已失效')
	expect(wrapper.text()).not.toContain('proxy-password')
})

it('shows closable progress and success feedback when a ready exit passes detection', async () => {
	let finishCheck!: (value: typeof rows[number]) => void
	const pendingCheck = new Promise<typeof rows[number]>(resolve => { finishCheck = resolve })
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/proxy-groups/jp-one/check') return pendingCheck
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	await wrapper.get('[data-check="jp-one"]').trigger('click')
	await wrapper.vm.$nextTick()
	let notice = wrapper.get('[data-top-notice]')
	expect(notice.attributes('data-notice-kind')).toBe('progress')
	expect(notice.text()).toContain('正在检测出口 1')
	expect(notice.text()).toContain('只会自动选择一个同国家候选修复一次')

	finishCheck(rows[1])
	await flushPromises()
	notice = wrapper.get('[data-top-notice]')
	expect(notice.attributes('data-notice-kind')).toBe('success')
	expect(notice.text()).toContain('出口 1 本机检测成功')
	expect(notice.text()).toContain('公网防火墙、客户端网络及客户端测速目标需另外验证')
	await notice.get('[aria-label="关闭提示"]').trigger('click')
	expect(wrapper.find('[data-top-notice]').exists()).toBe(false)
})

it('checks every fixed egress sequentially and reports failures without stopping', async () => {
	const slotFour = { ...rows[1], id: 'slot-four', slotNumber: 4, publicPort: 20003, mixedPort: 30003 }
	const runtimeRows = [rows[0], rows[1], rows[2], rows[5], slotFour]
	let releaseMain!: () => void
	const mainPending = new Promise<void>(resolve => { releaseMain = resolve })
	let activeChecks = 0
	let maximumActiveChecks = 0
	const checkPaths: string[] = []
	mocks.apiFetch.mockImplementation(async (path: string) => {
		if (path === '/api/v1/proxy-groups') return runtimeRows
		if (path === '/api/v1/settings/aimilivpn/countries') return []
		if (path === '/api/v1/settings/aimilivpn/refresh') return { state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 }
		if (path.endsWith('/check')) {
			checkPaths.push(path)
			activeChecks++
			maximumActiveChecks = Math.max(maximumActiveChecks, activeChecks)
			if (path === '/api/v1/proxy-groups/agw-main/check') await mainPending
			activeChecks--
			if (path === '/api/v1/proxy-groups/kr-one/check') throw new Error('egress_unavailable')
			return runtimeRows.find(row => path.includes(row.id)) ?? rows[0]
		}
		return undefined
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	await wrapper.get('[data-check-all]').trigger('click')
	await wrapper.vm.$nextTick()
	expect(wrapper.get('[data-check-all]').text()).toContain('1/5')
	expect(checkPaths).toEqual(['/api/v1/proxy-groups/agw-main/check'])
	releaseMain()
	await flushPromises()

	expect(checkPaths).toEqual([
		'/api/v1/proxy-groups/agw-main/check',
		'/api/v1/proxy-groups/jp-one/check',
		'/api/v1/proxy-groups/kr-one/check',
		'/api/v1/proxy-groups/de-rotating/check',
		'/api/v1/proxy-groups/slot-four/check',
	])
	expect(maximumActiveChecks).toBe(1)
	const notice = wrapper.get('[data-top-notice]')
	expect(notice.attributes('data-notice-kind')).toBe('error')
	expect(notice.text()).toContain('全部出口检测完成')
	expect(notice.text()).toContain('正常 4')
	expect(notice.text()).toContain('故障 1')
	expect(notice.text()).toContain('出口 2')
})

it('runs detection and at most one automatic repair in a single request', async () => {
	const degradedRow = { ...rows[1], id: 'degraded-slot', status: 'degraded', slotNumber: 3 }
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve([degradedRow])
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/proxy-groups/degraded-slot/check') return Promise.reject(new Error('no_same_country_candidate'))
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	await wrapper.get('[data-repair="degraded-slot"]').trigger('click')
	await flushPromises()

	const mutations = mocks.apiFetch.mock.calls
		.filter(([path]) => String(path).includes('/degraded-slot/'))
		.map(([path]) => path)
	expect(mutations).toEqual([
		'/api/v1/proxy-groups/degraded-slot/check',
	])
	const notice = wrapper.get('[data-top-notice]')
	expect(notice.attributes('data-notice-kind')).toBe('error')
	expect(notice.text()).toContain('已执行本次故障唯一一次自动修复')
	expect(notice.text()).toContain('没有找到同国家可用节点')
	expect(notice.text()).not.toContain('no_same_country_candidate')
})

it('does not rotate a degraded exit when detection reports a non-runtime failure', async () => {
	const degradedRow = { ...rows[1], id: 'degraded-slot', status: 'degraded', slotNumber: 3 }
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve([degradedRow])
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/proxy-groups/degraded-slot/check') return Promise.reject(new Error('protocol_failed'))
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	await wrapper.get('[data-repair="degraded-slot"]').trigger('click')
	await flushPromises()

	expect(mocks.apiFetch.mock.calls.some(([path]) => path === '/api/v1/proxy-groups/degraded-slot/rotate')).toBe(false)
	const notice = wrapper.get('[data-top-notice]')
	expect(notice.attributes('data-notice-kind')).toBe('error')
	expect(notice.text()).toContain('协议链路检测失败')
	expect(notice.text()).not.toContain('protocol_failed')
})

it('reports a ready exit tunnel failure without rotating it or exposing the error code', async () => {
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/proxy-groups/jp-one/check') return Promise.reject(new Error('egress_check_failed'))
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	await wrapper.get('[data-check="jp-one"]').trigger('click')
	await flushPromises()

	expect(mocks.apiFetch.mock.calls.some(([path]) => path === '/api/v1/proxy-groups/jp-one/rotate')).toBe(false)
	const notice = wrapper.get('[data-top-notice]')
	expect(notice.attributes('data-notice-kind')).toBe('error')
	expect(notice.text()).toContain('出口节点本身仍能联网')
	expect(notice.text()).not.toContain('egress_check_failed')
})

it('keeps four runtime rows in logical order and shows only the current page port', async () => {
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/proxy-groups') return Promise.resolve([rows[5], rows[2], rows[1], rows[0], ...rows.slice(3, 5), ...rows.slice(6)])
    if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
    if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
    return Promise.resolve(undefined)
  })
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  expect(wrapper.findAll('[data-pool-row]').slice(0, 4).map(row => row.attributes('data-row-id'))).toEqual(['agw-main', 'jp-one', 'kr-one', 'de-rotating'])
  expect(wrapper.get('[data-row-ports="agw-main"]').text()).toContain('VPN 节点 8443')
  expect(wrapper.get('[data-row-ports="agw-main"]').text()).not.toContain('31000')

  const socks = mount(SocksPoolView)
  await flushPromises()
  expect(socks.get('[data-row-ports="agw-main"]').text()).toContain('SOCKS5H 31000')
  expect(socks.get('[data-row-ports="agw-main"]').text()).not.toContain('8443')
})

it('shows verified exits for enabled and standby nodes and labels only legacy candidate fallback', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()

  expect(wrapper.get('[data-row-ip="agw-main"]').text()).toContain('203.0.113.9')
  expect(wrapper.get('[data-row-ip="us-standby"]').text()).toContain('203.0.113.12')
  expect(wrapper.get('[data-row-ip="us-standby"]').text()).not.toContain('节点 IP')
  expect(wrapper.get('[data-row-ip="fr-provisioning"]').text()).toContain('节点 IP 198.51.100.13')
  expect(wrapper.text()).not.toContain('等待出口')
})

it('shows fixed ready slots and replaces a standby candidate through a closable dialog', async () => {
  const wrapper = mount(VpnPoolView)
  await flushPromises()
  expect(wrapper.text()).toContain('出口位 1')
  expect(wrapper.findAll('[data-rotate="jp-one"]')).toHaveLength(0)
	await wrapper.get('[data-replace="us-standby"]').trigger('click')
  expect(wrapper.findAll('[data-replace-dialog]')).toHaveLength(1)
	await wrapper.get('[data-replace-target]').trigger('click')
	const targets = wrapper.findAll('[data-replace-option]')
	expect(targets[0].text()).toContain('主连接')
	expect(targets[0].text()).not.toContain('需明确选择')
	expect(targets[1].text()).toContain('出口位 1')
	expect(wrapper.get('[data-replace-options]').text()).not.toContain('出口位 4')
	await wrapper.get('[data-target-id="jp-one"]').trigger('click')
  await wrapper.get('[data-confirm-replace]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/us-standby/replace', { method: 'POST', headers: { 'Idempotency-Key': 'test-key' }, body: JSON.stringify({ targetGroupId: 'jp-one' }) })
})

it('replaces a standby candidate into the main connection with a main-specific result', async () => {
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-replace="us-standby"]').trigger('click')
	expect(wrapper.get('[data-replace-target]').text()).toContain('出口位 1')
	await wrapper.get('[data-replace-target]').trigger('click')
	await wrapper.get('[data-target-id="agw-main"]').trigger('click')
	await wrapper.get('[data-confirm-replace]').trigger('click')
	await flushPromises()
	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/proxy-groups/us-standby/replace', { method: 'POST', headers: { 'Idempotency-Key': 'test-key' }, body: JSON.stringify({ targetGroupId: 'agw-main' }) })
	expect(wrapper.text()).toContain('主连接替换成功')
})

it('keeps replacement failure inside the dialog and reloads rejected candidates', async () => {
	let groupReads = 0
	mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
		if (path === '/api/v1/proxy-groups') { groupReads++; return Promise.resolve(rows) }
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path.endsWith('/replace') && init?.method === 'POST') return Promise.reject(new Error('candidate_egress_failed'))
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-replace="us-standby"]').trigger('click')
	expect(wrapper.get('[data-replace-target]').text()).toContain('出口位 1')
	await wrapper.get('[data-confirm-replace]').trigger('click')
	await flushPromises()

	expect(wrapper.find('[data-replace-dialog]').exists()).toBe(true)
	expect(wrapper.get('[data-replace-target]').text()).toContain('出口位 1')
	expect(wrapper.get('[data-replace-notice]').text()).toContain('候选节点的真实出口检测失败')
	expect(wrapper.get('[data-replace-notice]').text()).not.toContain('candidate_egress_failed')
	expect(wrapper.get('[data-replace-notice]').attributes('data-notice-kind')).toBe('error')
	expect(groupReads).toBeGreaterThan(1)
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
	expect(wrapper.get('[data-top-notice]').attributes('data-notice-kind')).toBe('success')
	await wrapper.get('[data-top-notice] [aria-label="关闭提示"]').trigger('click')
	expect(wrapper.find('[data-top-notice]').exists()).toBe(false)
})

it('shows a closable Chinese protocol error without exposing backend codes', async () => {
	mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path.endsWith('/protocol-mode') && init?.method === 'PUT') return Promise.reject(new Error('egress_unavailable'))
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-protocol="jp-one"]').setValue('hysteria2_quic_tls')
	await flushPromises()

	const notice = wrapper.get('[data-top-notice]')
	expect(notice.attributes('data-notice-kind')).toBe('error')
	expect(notice.text()).toContain('协议切换失败，已请求恢复旧协议')
	expect(notice.text()).toContain('当前出口不可用')
	expect(notice.text()).not.toContain('egress_unavailable')
	expect(wrapper.find('[data-refresh-notice]').exists()).toBe(false)
})

it('does not claim rollback when maintenance rejects a protocol switch before mutation', async () => {
	mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path.endsWith('/protocol-mode') && init?.method === 'PUT') return Promise.reject(new Error('operation_busy'))
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-protocol="jp-one"]').setValue('hysteria2_quic_tls')
	await flushPromises()

	const notice = wrapper.get('[data-top-notice]')
	expect(notice.text()).toContain('协议未更改')
	expect(notice.text()).toContain('当前有维护或切换任务正在进行')
	expect(notice.text()).not.toContain('恢复旧协议')
})

it('shows the recorded rollback validation reason when a protected protocol switch is rejected', async () => {
	let groupReads = 0
	mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
		if (path === '/api/v1/proxy-groups') {
			groupReads += 1
			return Promise.resolve(groupReads === 1 ? rows : rows.map(row => row.id === 'jp-one' ? { ...row, protocolState: 'repair_required', subscriptionState: 'repair_required', lastErrorCode: 'protocol_rollback_validation_failed' } : row))
		}
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path.endsWith('/protocol-mode') && init?.method === 'PUT') return Promise.reject(new Error('repair_required'))
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-protocol="jp-one"]').setValue('hysteria2_quic_tls')
	await flushPromises()

	const notice = wrapper.get('[data-top-notice]')
	expect(notice.text()).toContain('旧协议恢复后链路验证未通过')
	expect(notice.text()).not.toContain('repair_required')
})

it('keeps public copy disabled during repair but permits a safe protocol revalidation request', async () => {
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
	expect(wrapper.get('[data-protocol="repair-protocol"]').attributes()).not.toHaveProperty('disabled')
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

it('separates cached-country filtering from official-country supplementation', async () => {
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	expect(wrapper.get('[data-country-filter]').text()).toContain('现有国家：全部')
	expect(wrapper.get('[data-country-filter]').text()).toContain('日本（1 个节点）')
	expect(wrapper.get('[data-country-filter]').text()).toContain('新加坡（1 个节点）')
	expect(wrapper.get('[data-country-supplement]').text()).toContain('补充国家')
	expect(wrapper.get('[data-country-supplement]').text()).toContain('日本（8 个官方节点）')
	expect(wrapper.get('[data-refresh-country]').attributes('disabled')).toBeDefined()
	expect(wrapper.get('[data-refresh-country]').text()).toContain('请先选择补充国家')
	await wrapper.get('[data-country-supplement]').setValue('JP')
	expect(wrapper.get('[data-refresh-country]').attributes('title')).toContain('全部官方候选')
	expect(wrapper.get('[data-refresh-country]').text()).toContain('优先检测该国家')
	await wrapper.get('[data-refresh-country]').trigger('click')
	await flushPromises()

	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/aimilivpn/refresh', {
		method: 'POST',
		headers: { 'Idempotency-Key': 'test-key' },
		body: JSON.stringify({ country: 'JP' }),
	})
	expect(wrapper.find('[data-sync-pool]').exists()).toBe(false)
	expect(wrapper.get('[data-refresh-all]').text()).toContain('刷新所有国家')
	expect(wrapper.get('[data-pool-stats-official]').text()).toContain('官方 100')
	expect(wrapper.get('[data-pool-stats-target]').text()).toContain('常规目标 64')
	expect(wrapper.get('[data-pool-stats-valid]').text()).toContain('当前有效 66')
	expect(wrapper.get('[data-pool-stats-maximum]').text()).toContain('紧急保护 150')
	expect(wrapper.get('[data-pool-stats-countries]').text()).toContain('5 国')
})

it('shows compact Chinese country names for known and future ISO codes', async () => {
	const englishRows = [
		{ ...rows[3], id: 'gd-node', countryCode: 'GD', countryName: 'Grenada' },
		{ ...rows[3], id: 'hr-node', countryCode: 'HR', countryName: 'Croatia (LOCAL Name: Hrvatska)' },
		{ ...rows[3], id: 'la-node', countryCode: 'LA', countryName: "Lao People's Democratic Republic" },
		{ ...rows[3], id: 'mp-node', countryCode: 'MP', countryName: 'Northern Mariana Islands' },
		{ ...rows[3], id: 'br-node', countryCode: 'BR', countryName: 'Brazil' },
	]
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(englishRows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([
			{ code: 'HR', name: 'Croatia (LOCAL Name: Hrvatska)', candidateCount: 2, observedAt: 1_700_000_000 },
			{ code: 'MP', name: 'Northern Mariana Islands', candidateCount: 1, observedAt: 1_700_000_000 },
			{ code: 'BR', name: 'Brazil', candidateCount: 1, observedAt: 1_700_000_000 },
		])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	const existing = wrapper.get('[data-country-filter]').text()
	const supplement = wrapper.get('[data-country-supplement]').text()
	for (const name of ['格林纳达', '克罗地亚', '老挝', '北马里亚纳群岛', '巴西']) expect(existing).toContain(name)
	for (const name of ['克罗地亚', '北马里亚纳群岛', '巴西']) expect(supplement).toContain(name)
	for (const english of ['Grenada', 'Croatia', 'Hrvatska', "Lao People's Democratic Republic", 'Northern Mariana Islands', 'Brazil']) {
		expect(existing + supplement).not.toContain(english)
	}
})

it('starts a durable all-country refresh from the pool page', async () => {
	mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([
			{ code: 'JP', name: '日本', candidateCount: 8, observedAt: 1_700_000_000 },
		])
		if (path === '/api/v1/settings/aimilivpn/refresh' && init?.method === 'POST') {
			return Promise.resolve({ state: 'running', country: 'ALL', phase: 'fetching', testedCount: 0, validCount: 0 })
		}
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-refresh-all]').trigger('click')
	await flushPromises()

	expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/aimilivpn/refresh', {
		method: 'POST',
		headers: { 'Idempotency-Key': 'test-key' },
		body: JSON.stringify({ country: 'ALL' }),
	})
	expect(wrapper.get('[data-refresh-notice]').text()).toContain('所有国家正在刷新')
})

it('shows country refresh feedback in a separate closable card with a Chinese country name', async () => {
	mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([{ code: 'US', name: '美国', candidateCount: 4, observedAt: 1_700_000_000 }])
		if (path === '/api/v1/settings/aimilivpn/refresh' && init?.method === 'POST') return Promise.reject(new Error('no_usable_nodes'))
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	await wrapper.get('[data-country-supplement]').setValue('US')
	await wrapper.get('[data-refresh-country]').trigger('click')
	await flushPromises()

	const notice = wrapper.get('[data-refresh-notice]')
	expect(notice.attributes('data-notice-kind')).toBe('error')
	expect(notice.text()).toContain('美国刷新失败')
	expect(notice.text()).toContain('没有找到可用节点')
	expect(notice.text()).not.toContain('no_usable_nodes')
	expect(wrapper.find('[data-top-notice]').exists()).toBe(false)
	await notice.get('[aria-label="关闭提示"]').trigger('click')
	expect(wrapper.find('[data-refresh-notice]').exists()).toBe(false)
})

it('shows the last structured refresh result, counts, and time', async () => {
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([{ code: 'US', name: '美国', candidateCount: 4, observedAt: 1_700_000_000 }])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'completed', country: 'US', phase: '', resultCode: 'success', officialCount: 12, countryCandidateCount: 8, usableCount: 5, passedCount: 5, failedCount: 1, revalidatedCount: 4, newUsableCount: 1, retainedCount: 4, testedCount: 6, validCount: 5, cacheTotal: 66, countryValidCount: 5, targetValidNodeCount: 64, maxValidNodeCount: 150, finishedAt: 1_700_000_000 })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	const summary = wrapper.get('[data-refresh-notice]')
	expect(summary.attributes('data-notice-kind')).toBe('success')
	expect(summary.text()).toContain('美国')
	expect(summary.text()).toContain('成功')
	expect(summary.text()).toContain('官方原始 12')
	expect(summary.text()).toContain('去重候选 8')
	expect(summary.text()).toContain('本次检测 6')
	expect(summary.text()).toContain('检测通过 5')
	expect(summary.text()).toContain('复验通过 4')
	expect(summary.text()).toContain('新增节点 1')
	expect(summary.text()).toContain('检测失败 1')
	expect(summary.text()).toContain('该国现有 5')
	expect(summary.text()).toContain('节点池共 66')
	expect(summary.text()).toMatch(/11\/|11月/)
	await summary.get('[aria-label="关闭提示"]').trigger('click')
	expect(wrapper.find('[data-refresh-notice]').exists()).toBe(false)
})

it('keeps the same refresh result dismissed after remount but shows a newer result', async () => {
	let finishedAt = 1_700_000_000
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([{ code: 'US', name: '美国', candidateCount: 4, observedAt: 1_700_000_000 }])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'completed', country: 'US', phase: '', resultCode: 'success', officialCount: 12, usableCount: 5, retainedCount: 4, testedCount: 6, validCount: 5, startedAt: 1_699_999_900, finishedAt })
		return Promise.resolve(undefined)
	})

	const initial = mount(VpnPoolView)
	await flushPromises()
	await initial.get('[data-refresh-notice] [aria-label="关闭提示"]').trigger('click')
	initial.unmount()

	const sameResult = mount(VpnPoolView)
	await flushPromises()
	expect(sameResult.find('[data-refresh-notice]').exists()).toBe(false)
	sameResult.unmount()

	finishedAt += 1
	const newerResult = mount(VpnPoolView)
	await flushPromises()
	expect(newerResult.get('[data-refresh-notice]').text()).toContain('最后刷新：美国 · 成功')
})

it('does not describe a completed refresh with a failure result code as successful', async () => {
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([{ code: 'US', name: '美国', candidateCount: 0, observedAt: 1_700_000_000 }])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'completed', country: 'US', phase: '', resultCode: 'no_usable_nodes', officialCount: 12, usableCount: 0, retainedCount: 0, testedCount: 6, validCount: 0, finishedAt: 1_700_000_000 })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	const summary = wrapper.get('[data-refresh-notice]').text()
	expect(summary).toContain('失败')
	expect(summary).not.toContain('成功')
})

it('uses the official ISO country name when runtime metadata is stale', async () => {
	const staleRows = [
		{ ...rows[1], id: 'vn-slot', countryCode: 'VN', countryName: '越南' },
		{ ...rows[2], id: 'kr-slot', countryCode: 'KR', countryName: '越南' },
	]
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(staleRows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([
			{ code: 'KR', name: '韩国', candidateCount: 3, observedAt: 1_700_000_000 },
			{ code: 'VN', name: '越南', candidateCount: 2, observedAt: 1_700_000_000 },
		])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()

	const options = wrapper.findAll('[data-country-filter] option').map(option => option.text())
	expect(options.filter(text => text === '越南（1 个节点）')).toHaveLength(1)
	expect(options.filter(text => text === '韩国（1 个节点）')).toHaveLength(1)
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
	await wrapper.get('[data-country-supplement]').setValue('JP')
	await wrapper.get('[data-refresh-country]').trigger('click')
	await flushPromises()
	await vi.advanceTimersByTimeAsync(4_000)
	await flushPromises()

	expect(refreshReads).toBeGreaterThanOrEqual(2)
	expect(wrapper.text()).toContain('已完成')
	expect(mocks.apiFetch.mock.calls.filter(([path]) => path === '/api/v1/proxy-groups').length).toBeGreaterThan(1)
	wrapper.unmount()
})

it('refreshes dedicated standby health in place without reloading the whole page', async () => {
	vi.useFakeTimers()
	let standbyReads = 0
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/proxy-groups') return Promise.resolve(rows)
		if (path === '/api/v1/settings/aimilivpn/countries') return Promise.resolve([{ code: 'JP', name: '日本', candidateCount: 8, observedAt: 1_700_000_000 }])
		if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
		if (path === '/api/v1/settings/aimilivpn/standbys') {
			standbyReads++
			return Promise.resolve([
				{ index: 0, target: 'slot:0', countries: ['JP'], status: standbyReads === 1 ? 'preparing' : 'ready', country: 'JP', exit_ip: standbyReads === 1 ? '' : '198.51.100.20', candidate_ip: '203.0.113.20', node_id: 'jp-standby', proxy_type: 'datacenter', egress_ok: standbyReads > 1, checked_at: 1_700_000_000 + standbyReads, last_error_code: '' },
				{ index: 1, target: '', countries: [], status: 'disabled', country: '', exit_ip: '', candidate_ip: '', node_id: '', proxy_type: '', egress_ok: false, checked_at: 0, last_error_code: '' },
			])
		}
		return Promise.resolve(undefined)
	})
	const wrapper = mount(VpnPoolView)
	await flushPromises()
	expect(wrapper.get('[data-standby-summary="0"]').text()).toContain('正在准备')

	await vi.advanceTimersByTimeAsync(15_000)
	await flushPromises()

	expect(standbyReads).toBe(2)
	expect(wrapper.get('[data-standby-summary="0"]').text()).toContain('已就绪')
	expect(wrapper.get('[data-standby-summary="0"]').text()).toContain('真实出口有效')
	expect(wrapper.get('[data-standby-summary="0"]').text()).toContain('198.51.100.20')
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
