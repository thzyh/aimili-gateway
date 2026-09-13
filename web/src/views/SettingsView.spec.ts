import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), openBackend: vi.fn(), push: vi.fn() }))

vi.mock('../api/client', () => ({
  apiFetch: mocks.apiFetch,
  openBackend: mocks.openBackend,
  APIError: class APIError extends Error {
    constructor(public readonly status: number, code: string) { super(code) }
  },
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push }),
  RouterLink: { props: ['to'], template: '<a :data-to="to"><slot /></a>' },
}))

import { APIError } from '../api/client'
import SettingsView from './SettingsView.vue'

beforeEach(() => {
  mocks.apiFetch.mockReset()
  mocks.openBackend.mockReset()
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
    return Promise.resolve(undefined)
  })
})

it('removes security confirmation and shows the disabled SOCKS5H risk state', async () => {
  const wrapper = mount(SettingsView)
  await flushPromises()

  expect(wrapper.text()).not.toContain('安全确认')
  expect(wrapper.text()).not.toContain('Gateway 密码')
  expect(wrapper.text()).not.toContain('TOTP')
  expect(wrapper.text()).toContain('任何公网来源都可以尝试认证')
  expect(wrapper.find('[data-cidr-editor]').exists()).toBe(false)
  expect(wrapper.text()).toContain('1 / 1')
  expect(wrapper.find('[data-to="/settings/aimilivpn"]').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('AimiliVPN 设置')
  expect(wrapper.find('[data-to="/settings/3x-ui"]').exists()).toBe(true)
  expect(wrapper.text()).toContain('3x-ui 专家模式')
})

it('requires a CIDR when enabling the source restriction and saves only the approved contract', async () => {
  const wrapper = mount(SettingsView)
  await flushPromises()

  await wrapper.get('[data-source-toggle]').setValue(true)
  expect(wrapper.find('[data-cidr-editor]').exists()).toBe(true)
  await wrapper.get('[data-policy-form]').trigger('submit')
  await flushPromises()
  expect(wrapper.text()).toContain('至少填写一个')
  expect(mocks.apiFetch).not.toHaveBeenCalledWith('/api/v1/settings/mixed-source-policy', expect.objectContaining({ method: 'PUT' }))

  await wrapper.get('[data-cidr-editor]').setValue('198.51.100.0/24')
  await wrapper.get('[data-policy-form]').trigger('submit')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/mixed-source-policy', {
    method: 'PUT',
    body: JSON.stringify({ enabled: true, cidrs: ['198.51.100.0/24'] }),
  })
})

it('reports a failed source-policy application instead of presenting it as pending', async () => {
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: true, cidrs: ['198.51.100.7/32'], applyStatus: 'failed' })
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  expect(wrapper.text()).toContain('当前状态：应用失败')
  expect(wrapper.text()).not.toContain('当前状态：待处理')
})

it('authorizes only the current network through the dedicated safe action', async () => {
  let policyReads = 0
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy') {
      policyReads += 1
      return Promise.resolve(policyReads === 1
        ? { enabled: true, cidrs: ['198.51.100.0/24'], applyStatus: 'applied' }
        : { enabled: true, cidrs: ['198.51.100.0/24', '198.51.100.7/32'], applyStatus: 'applied' })
    }
    if (path === '/api/v1/settings/mixed-source-policy/authorize-current') return Promise.resolve({ enabled: true, cidrs: ['198.51.100.0/24', '198.51.100.7/32'], applyStatus: 'applied' })
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  await wrapper.get('[data-authorize-current-network]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/mixed-source-policy/authorize-current', { method: 'POST' })
  expect(policyReads).toBe(2)
  expect((wrapper.get('[data-cidr-editor]').element as HTMLTextAreaElement).value).toContain('198.51.100.7/32')
})

it('explains why the current network source cannot be identified', async () => {
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
	if (path === '/api/v1/system/updates') return Promise.resolve({ enabled: true, currentGateway: 'v1.2.2', currentUi: 'a'.repeat(64), available: [] })
    if (path === '/api/v1/settings/mixed-source-policy/authorize-current') return Promise.reject(new APIError(403, 'client_forwarded_for_missing'))
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  await wrapper.get('[data-authorize-current-network]').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('反向代理没有传递访问者地址')
  const notice = wrapper.get('[data-policy-notice]')
  expect(notice.attributes('data-notice-kind')).toBe('error')
  expect(notice.text()).toContain('当前网络授权失败')
  await notice.get('[aria-label="关闭提示"]').trigger('click')
  expect(wrapper.find('[data-policy-notice]').exists()).toBe(false)
})

it('explains the component boundary for ordinary Gateway updates', async () => {
	mocks.apiFetch.mockImplementation((path: string) => {
		if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
		if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
		if (path === '/api/v1/system/updates') return Promise.resolve({
			enabled: true, currentGateway: 'v1.2.2', currentUi: 'a'.repeat(64),
			available: [{ kind: 'ui', version: 'b'.repeat(64), compatible: true }, { kind: 'gateway', version: 'v1.2.3', compatible: true }],
		})
		return Promise.resolve(undefined)
	})
	const wrapper = mount(SettingsView)
	await flushPromises()
	expect(wrapper.text()).toContain('只短暂重启 Gateway，代理节点继续运行')
	expect(wrapper.text()).toContain('涉及 AimiliVPN、3x-ui/Xray 或 Caddy 的版本会拒绝普通更新')
})

it('requires password reauthentication and submits only a closed version plus run id', async () => {
	let submittedRunId = ''
	mocks.apiFetch.mockImplementation((path: string, options?: { method?: string; body?: string }) => {
		if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
		if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
		if (path === '/api/v1/system/updates') return Promise.resolve({ enabled: true, currentGateway: 'v1.2.2', available: [{ kind: 'gateway', version: 'v1.2.3', compatible: true }] })
		if (path === '/api/v1/system/updates/gateway/v1.2.3/apply' && options?.method === 'POST') {
			submittedRunId = JSON.parse(options.body as string).runId
			return Promise.resolve({ runId: submittedRunId, kind: 'gateway', version: 'v1.2.3', state: 'pending' })
		}
		if (path === '/api/v1/system/updates/' + submittedRunId) return Promise.resolve({ runId: submittedRunId, kind: 'gateway', version: 'v1.2.3', state: 'success' })
		return Promise.resolve(undefined)
	})
	const wrapper = mount(SettingsView)
	await flushPromises()
	await wrapper.get('[data-gateway-update]').trigger('click')
	expect(wrapper.find('[data-update-password]').exists()).toBe(true)
	await wrapper.get('[data-update-password]').setValue('test-password')
	await wrapper.get('[data-update-confirm]').trigger('click')
	await flushPromises()
	const call = mocks.apiFetch.mock.calls.find(([path]) => path === '/api/v1/system/updates/gateway/v1.2.3/apply')
	expect(call?.[1]).toMatchObject({ method: 'POST' })
	const body = JSON.parse(call?.[1].body)
	expect(Object.keys(body).sort()).toEqual(['password', 'runId'])
	expect(body.password).toBe('test-password')
	expect(body.runId).toMatch(/^[0-9a-f]{64}$/)
})

it('retries transient polling errors for the original run and shows a closable notice', async () => {
	let statusReads = 0
	let submittedRunId = ''
	mocks.apiFetch.mockImplementation((path: string, options?: { method?: string; body?: string }) => {
		if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
		if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
		if (path === '/api/v1/system/updates') return Promise.resolve({ enabled: true, currentGateway: 'v1.2.2', available: [{ kind: 'gateway', version: 'v1.2.3', compatible: true }] })
		if (path === '/api/v1/system/updates/gateway/v1.2.3/apply' && options?.method === 'POST') {
			submittedRunId = JSON.parse(options.body as string).runId
			return Promise.resolve({ runId: submittedRunId, kind: 'gateway', version: 'v1.2.3', state: 'pending' })
		}
		if (path === '/api/v1/system/updates/' + submittedRunId) {
			statusReads += 1
			return statusReads === 1 ? Promise.reject(new TypeError('temporary disconnect')) : Promise.resolve({ runId: submittedRunId, kind: 'gateway', version: 'v1.2.3', state: 'success' })
		}
		return Promise.resolve(undefined)
	})
	const wrapper = mount(SettingsView)
	await flushPromises()
	await wrapper.get('[data-gateway-update]').trigger('click')
	await wrapper.get('[data-update-password]').setValue('test-password')
	await wrapper.get('[data-update-confirm]').trigger('click')
	await new Promise(resolve => setTimeout(resolve, 1100))
	await flushPromises()
	expect(statusReads).toBe(2)
	expect(wrapper.get('[data-update-notice]').text()).toContain('Aimili Gateway 更新成功')
	expect(wrapper.get('[data-update-notice]').text()).not.toContain('temporary disconnect')
	await wrapper.get('[data-update-notice] [aria-label="关闭提示"]').trigger('click')
	expect(wrapper.find('[data-update-notice]').exists()).toBe(false)
})

it('renders repair-required updates in Chinese without exposing the internal error code', async () => {
  const wrapper = mount(SettingsView)
  await flushPromises()
  const exposed = wrapper.vm as unknown as { updateNotice: unknown; noticeForUpdate: (result: unknown) => unknown }
  exposed.updateNotice = exposed.noticeForUpdate({ runId: 'e'.repeat(64), kind: 'gateway', version: 'v1.2.3', state: 'repair_required', errorCode: 'internal_secret_detail' })
  await wrapper.vm.$nextTick()
  expect(wrapper.get('[data-update-notice]').text()).toContain('需要受限修复')
  expect(wrapper.get('[data-update-notice]').text()).not.toContain('internal_secret_detail')
})

it('reauthenticates before starting a Gateway rollback and follows its run id', async () => {
  let submittedRunId = ''
  mocks.apiFetch.mockImplementation((path: string, options?: { method?: string; body?: string }) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
    if (path === '/api/v1/system/updates') return Promise.resolve({ enabled: true, currentGateway: 'v1.2.3', available: [] })
    if (path === '/api/v1/system/updates/gateway/rollback' && options?.method === 'POST') {
      submittedRunId = JSON.parse(options.body as string).runId
      return Promise.resolve({ runId: submittedRunId, kind: 'gateway', state: 'pending' })
    }
    if (path === `/api/v1/system/updates/${submittedRunId}`) return Promise.resolve({ runId: submittedRunId, kind: 'gateway', state: 'rolled_back' })
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  await wrapper.get('[data-gateway-rollback]').trigger('click')
  expect(wrapper.find('[data-update-password]').exists()).toBe(true)
  await wrapper.get('[data-update-password]').setValue('test-password')
  await wrapper.get('[data-update-confirm]').trigger('click')
  await flushPromises()

  const call = mocks.apiFetch.mock.calls.find(([path]) => path === '/api/v1/system/updates/gateway/rollback')
  expect(call?.[1]).toMatchObject({ method: 'POST' })
  expect(JSON.parse(call?.[1].body)).toMatchObject({ password: 'test-password', runId: expect.stringMatching(/^[0-9a-f]{64}$/) })
  expect(mocks.apiFetch).toHaveBeenCalledWith(`/api/v1/system/updates/${submittedRunId}`)
  const notice = wrapper.get('[data-update-notice]')
  expect(notice.text()).toContain('Gateway 控制面已回滚')
  await notice.get('[aria-label="关闭提示"]').trigger('click')
  expect(wrapper.find('[data-update-notice]').exists()).toBe(false)
})

it('continues polling the generated run id when an update POST response is lost', async () => {
  let submittedRunId = ''
  mocks.apiFetch.mockImplementation((path: string, options?: { method?: string; body?: string }) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
    if (path === '/api/v1/system/updates') return Promise.resolve({ enabled: true, currentGateway: 'v1.2.2', available: [{ kind: 'gateway', version: 'v1.2.3', compatible: true }] })
    if (path === '/api/v1/system/updates/gateway/v1.2.3/apply' && options?.method === 'POST') {
      submittedRunId = JSON.parse(options.body ?? '{}').runId
      return Promise.reject(new TypeError('response lost after submit'))
    }
    if (path === `/api/v1/system/updates/${submittedRunId}`) return Promise.resolve({ runId: submittedRunId, kind: 'gateway', version: 'v1.2.3', state: 'success' })
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  await wrapper.get('[data-gateway-update]').trigger('click')
  await wrapper.get('[data-update-password]').setValue('test-password')
  await wrapper.get('[data-update-confirm]').trigger('click')
  await flushPromises()

  expect(submittedRunId).toMatch(/^[0-9a-f]{64}$/)
  expect(mocks.apiFetch.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1)
  expect(mocks.apiFetch).toHaveBeenCalledWith(`/api/v1/system/updates/${submittedRunId}`)
  expect(wrapper.get('[data-update-notice]').text()).toContain('Aimili Gateway 更新成功')
})

it('disables every update mutation when capability is unavailable', async () => {
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced' })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
    if (path === '/api/v1/system/updates') return Promise.reject(new APIError(503, 'updates_disabled'))
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()
  for (const selector of ['[data-check-update]', '[data-gateway-rollback]']) {
    expect(wrapper.get(selector).attributes('disabled')).toBeDefined()
    await wrapper.get(selector).trigger('click')
  }
  expect(wrapper.find('[data-update-password]').exists()).toBe(false)
  expect(mocks.apiFetch.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(0)
})

it('detects a newer signed Gateway release from GitHub before offering update', async () => {
  mocks.apiFetch.mockImplementation((path: string, options?: { method?: string; body?: string }) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced' })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
    if (path === '/api/v1/system/updates') return Promise.resolve({ enabled: true, currentGateway: 'v1.2.2', available: [] })
    return Promise.resolve(undefined)
  })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      tag_name: 'v1.2.3', draft: false, prerelease: false, body: '修复出口状态显示',
      assets: [{ name: 'manifest.json' }, { name: 'manifest.sig' }, { name: 'aimili-gateway' }],
    }),
  }))
  try {
    const wrapper = mount(SettingsView)
    await flushPromises()
    expect(wrapper.find('[data-gateway-update]').exists()).toBe(false)
    await wrapper.get('[data-check-update]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-gateway-update]').text()).toContain('v1.2.3')
    expect(wrapper.get('[data-update-notice]').text()).toContain('发现新版本 v1.2.3')
    expect(wrapper.text()).toContain('修复出口状态显示')
  } finally {
    vi.unstubAllGlobals()
  }
})

it('keeps the confirmed terminal notice when refreshing versions fails', async () => {
  vi.useFakeTimers()
  try {
    let updateListReads = 0
    let submittedRunId = ''
    mocks.apiFetch.mockImplementation((path: string, options?: { method?: string; body?: string }) => {
      if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
      if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
      if (path === '/api/v1/system/updates') {
        updateListReads += 1
        return updateListReads === 1
          ? Promise.resolve({ enabled: true, currentGateway: 'v1.2.2', available: [{ kind: 'gateway', version: 'v1.2.3', compatible: true }] })
          : Promise.reject(new TypeError('list refresh disconnected'))
      }
      if (path === '/api/v1/system/updates/gateway/v1.2.3/apply' && options?.method === 'POST') {
        submittedRunId = JSON.parse(options.body as string).runId
        return Promise.resolve({ runId: submittedRunId, kind: 'gateway', version: 'v1.2.3', state: 'pending' })
      }
      if (path === `/api/v1/system/updates/${submittedRunId}`) return Promise.resolve({ runId: submittedRunId, kind: 'gateway', version: 'v1.2.3', state: 'success' })
      return Promise.resolve(undefined)
    })
    const wrapper = mount(SettingsView)
    await flushPromises()
    await wrapper.get('[data-gateway-update]').trigger('click')
    await wrapper.get('[data-update-password]').setValue('test-password')
    await wrapper.get('[data-update-confirm]').trigger('click')
    await vi.runAllTimersAsync()
    await flushPromises()

    expect(wrapper.get('[data-update-notice]').text()).toContain('Aimili Gateway 更新成功')
    expect(wrapper.get('[data-update-notice]').text()).not.toContain('状态未确认')
  } finally {
    vi.useRealTimers()
  }
})

it('offers only compatible versions returned by the backend without URL or path input', async () => {
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: false, cidrs: [], applyStatus: 'applied' })
    if (path === '/api/v1/system/updates') return Promise.resolve({
      enabled: true, currentGateway: 'v1.2.2',
      available: [{ kind: 'gateway', version: 'v1.2.3', compatible: true }, { kind: 'gateway', version: 'https://untrusted.test/update', compatible: false }],
    })
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  expect(wrapper.get('[data-gateway-update]').text()).toContain('v1.2.3')
  expect(wrapper.text()).not.toContain('https://untrusted.test/update')
  expect(wrapper.find('[data-update-version]').exists()).toBe(false)
  expect(wrapper.find('input[type="url"]').exists()).toBe(false)
})

it('re-reads the effective policy and uses the shared success notice after saving', async () => {
  let policyReads = 0
  mocks.apiFetch.mockImplementation((path: string, options?: { method?: string }) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy' && options?.method === 'PUT') return Promise.resolve({ enabled: true, cidrs: ['198.51.100.0/24'], applyStatus: 'applying' })
    if (path === '/api/v1/settings/mixed-source-policy') {
      policyReads += 1
      return Promise.resolve(policyReads === 1
        ? { enabled: false, cidrs: [], applyStatus: 'applied' }
        : { enabled: true, cidrs: ['198.51.100.0/24'], applyStatus: 'applied' })
    }
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  await wrapper.get('[data-source-toggle]').setValue(true)
  await wrapper.get('[data-cidr-editor]').setValue('198.51.100.0/24')
  await wrapper.get('[data-policy-form]').trigger('submit')
  await flushPromises()

  expect(policyReads).toBe(2)
  expect(wrapper.get('[data-policy-notice]').attributes('data-notice-kind')).toBe('success')
  expect(wrapper.get('[data-policy-notice]').text()).toContain('来源策略已生效')
  expect(wrapper.text()).toContain('当前状态：已生效')
})

it('re-reads and reports that the original policy remains effective after a failed save', async () => {
  let policyReads = 0
  mocks.apiFetch.mockImplementation((path: string, options?: { method?: string }) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy' && options?.method === 'PUT') return Promise.reject(new APIError(409, 'mixed_policy_apply_failed'))
    if (path === '/api/v1/settings/mixed-source-policy') {
      policyReads += 1
      return Promise.resolve({ enabled: true, cidrs: ['198.51.100.0/24'], applyStatus: 'applied' })
    }
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  await wrapper.get('[data-cidr-editor]').setValue('198.51.100.7/32')
  await wrapper.get('[data-policy-form]').trigger('submit')
  await flushPromises()

  expect(policyReads).toBe(2)
  expect(wrapper.get('[data-policy-notice]').attributes('data-notice-kind')).toBe('error')
  expect(wrapper.get('[data-policy-notice]').text()).toContain('原策略仍然生效')
  expect((wrapper.get('[data-cidr-editor]').element as HTMLTextAreaElement).value).toBe('198.51.100.0/24')
})
