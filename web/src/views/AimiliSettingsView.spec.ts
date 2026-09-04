import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), openBackend: vi.fn(), push: vi.fn() }))
vi.mock('../api/client', () => ({ apiFetch: mocks.apiFetch, openBackend: mocks.openBackend }))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push }),
  RouterLink: { props: ['to'], template: '<a :data-to="to"><slot /></a>' },
}))

import AimiliSettingsView from './AimiliSettingsView.vue'

beforeEach(() => {
  mocks.apiFetch.mockReset()
  mocks.openBackend.mockReset()
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/settings/aimilivpn') return Promise.resolve({ candidateCount: 18, residentialCount: 7, datacenterCount: 11, managedSlotCount: 1, lastRefreshedAt: '2026-08-27T00:00:00Z' })
    if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'completed', country: 'JP', phase: '', testedCount: 5, validCount: 4, errorCode: '' })
    return Promise.resolve({ candidateCount: 18, residentialCount: 7, datacenterCount: 11, managedSlotCount: 1, lastRefreshedAt: '2026-08-27T00:00:00Z' })
  })
})

it('shows only approved AimiliVPN maintenance fields and fixed actions', async () => {
  const wrapper = mount(AimiliSettingsView)
  await flushPromises()
  expect(wrapper.text()).toContain('18')
  expect(wrapper.text()).toContain('7')
  expect(wrapper.text()).toContain('11')
  expect(wrapper.text()).not.toContain('密码')
  expect(wrapper.text()).not.toContain('Cookie')

  await wrapper.get('[data-check-aimili]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/aimilivpn/refresh')
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/aimilivpn/check', { method: 'POST' })
  expect(wrapper.text()).toContain('刷新已完成')
  expect(wrapper.text()).toContain('精验 5 个，保留 4 个')
  expect(wrapper.get('[data-check-aimili]').text()).toContain('同步代理状态')
})

it('opens the original AimiliVPN backend through one fixed POST helper and does not retry failures', async () => {
  mocks.openBackend.mockRejectedValueOnce(new Error('automatic_login_failed'))
  const wrapper = mount(AimiliSettingsView)
  await flushPromises()
  await wrapper.get('[data-open-aimili]').trigger('click')
  await flushPromises()
  expect(mocks.openBackend).toHaveBeenCalledTimes(1)
  expect(mocks.openBackend).toHaveBeenCalledWith('/api/v1/backends/aimilivpn/login')
  expect(wrapper.text()).toContain('手动登录')
})

it('shows a closable AimiliVPN operation error notice', async () => {
  mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
    if (path === '/api/v1/settings/aimilivpn') return Promise.resolve({ candidateCount: 18, residentialCount: 7, datacenterCount: 11, managedSlotCount: 3 })
    if (path === '/api/v1/settings/aimilivpn/refresh') return Promise.resolve({ state: 'idle', country: '', phase: '', testedCount: 0, validCount: 0 })
    if (path === '/api/v1/settings/aimilivpn/check' && init?.method === 'POST') return Promise.reject(new Error('internal_failure'))
    return Promise.resolve(undefined)
  })
  const wrapper = mount(AimiliSettingsView)
  await flushPromises()
  await wrapper.get('[data-check-aimili]').trigger('click')
  await flushPromises()

  const notice = wrapper.get('[data-aimili-notice]')
  expect(notice.attributes('data-notice-kind')).toBe('error')
  expect(notice.text()).toContain('AimiliVPN 同步失败')
  expect(notice.text()).not.toContain('internal_failure')
  await notice.get('[aria-label="关闭提示"]').trigger('click')
  expect(wrapper.find('[data-aimili-notice]').exists()).toBe(false)
})
