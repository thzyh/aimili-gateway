import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), openBackend: vi.fn(), push: vi.fn() }))
vi.mock('../api/client', () => ({ apiFetch: mocks.apiFetch, openBackend: mocks.openBackend }))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push }),
  RouterLink: { props: ['to'], template: '<a :data-to="to"><slot /></a>' },
}))

import XUISettingsView from './XUISettingsView.vue'

beforeEach(() => {
  mocks.apiFetch.mockReset()
  mocks.openBackend.mockReset()
  mocks.apiFetch.mockResolvedValue({ managedPublicCount: 4, managedVlessCount: 4, managedMixedCount: 4, managedOutboundCount: 4, ownershipMatches: true, lastCheckedAt: '2026-08-27T00:00:00Z' })
})

it('shows managed 3x-ui ownership and exposes only check and repair actions', async () => {
  const wrapper = mount(XUISettingsView)
  await flushPromises()
  expect(wrapper.text()).toContain('所有权核对通过')
  expect(wrapper.text()).toContain('受管公网协议')
  expect(wrapper.text()).not.toContain('账户密码')
  expect(wrapper.text()).not.toContain('随机路径')

  await wrapper.get('[data-check-xui]').trigger('click')
  await wrapper.get('[data-repair-xui]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/3x-ui/check', { method: 'POST' })
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/3x-ui/repair', { method: 'POST' })
})

it('opens expert mode through one fixed POST helper', async () => {
  const wrapper = mount(XUISettingsView)
  await flushPromises()
  await wrapper.get('[data-open-xui]').trigger('click')
  await flushPromises()
  expect(mocks.openBackend).toHaveBeenCalledWith('/api/v1/backends/3x-ui/login')
})

it('shows 3x-ui repair progress and a closable error notice', async () => {
  let rejectRepair!: (error: Error) => void
  const pendingRepair = new Promise((_resolve, reject) => { rejectRepair = reject })
  mocks.apiFetch.mockImplementation((path: string, init?: RequestInit) => {
    if (path === '/api/v1/settings/3x-ui/repair' && init?.method === 'POST') return pendingRepair
    return Promise.resolve({ managedPublicCount: 3, managedMixedCount: 4, managedOutboundCount: 4, ownershipMatches: false })
  })
  const wrapper = mount(XUISettingsView)
  await flushPromises()

  const action = wrapper.get('[data-repair-xui]').trigger('click')
  await Promise.resolve()
  expect(wrapper.get('[data-xui-notice]').attributes('data-notice-kind')).toBe('progress')
  expect(wrapper.get('[data-xui-notice]').text()).toContain('正在修复 3x-ui 受管资源')

  rejectRepair(new Error('repair_failed'))
  await action
  await flushPromises()
  const notice = wrapper.get('[data-xui-notice]')
  expect(notice.attributes('data-notice-kind')).toBe('error')
  expect(notice.text()).toContain('3x-ui 受管资源修复失败')
  expect(notice.text()).not.toContain('repair_failed')
  await notice.get('[aria-label="关闭提示"]').trigger('click')
  expect(wrapper.find('[data-xui-notice]').exists()).toBe(false)
})
