import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), openBackend: vi.fn(), push: vi.fn() }))

vi.mock('../api/client', () => ({ apiFetch: mocks.apiFetch, openBackend: mocks.openBackend }))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push }),
  RouterLink: { props: ['to'], template: '<a :data-to="to"><slot /></a>' },
}))

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
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/settings/summary') return Promise.resolve({ accountSyncStatus: 'synced', candidateCount: 24, onlineCount: 1, maxOnline: 1 })
    if (path === '/api/v1/settings/mixed-source-policy') return Promise.resolve({ enabled: true, cidrs: ['198.51.100.0/24'], applyStatus: 'applied' })
    if (path === '/api/v1/settings/mixed-source-policy/authorize-current') return Promise.resolve({ enabled: true, cidrs: ['198.51.100.0/24', '198.51.100.7/32'], applyStatus: 'applied' })
    return Promise.resolve(undefined)
  })
  const wrapper = mount(SettingsView)
  await flushPromises()

  await wrapper.get('[data-authorize-current-network]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/mixed-source-policy/authorize-current', { method: 'POST' })
  expect((wrapper.get('[data-cidr-editor]').element as HTMLTextAreaElement).value).toContain('198.51.100.7/32')
})
