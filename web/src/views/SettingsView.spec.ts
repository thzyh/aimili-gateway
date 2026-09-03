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
  expect(wrapper.find('[data-to="/settings/aimilivpn"]').exists()).toBe(true)
  expect(wrapper.find('[data-to="/settings/3x-ui"]').exists()).toBe(true)
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
