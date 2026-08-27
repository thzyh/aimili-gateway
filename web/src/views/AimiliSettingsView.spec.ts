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
  mocks.apiFetch.mockResolvedValue({ candidateCount: 18, residentialCount: 7, datacenterCount: 11, managedSlotCount: 1, lastRefreshedAt: '2026-08-27T00:00:00Z' })
})

it('shows only approved AimiliVPN maintenance fields and fixed actions', async () => {
  const wrapper = mount(AimiliSettingsView)
  await flushPromises()
  expect(wrapper.text()).toContain('18')
  expect(wrapper.text()).toContain('7')
  expect(wrapper.text()).toContain('11')
  expect(wrapper.text()).not.toContain('密码')
  expect(wrapper.text()).not.toContain('Cookie')

  await wrapper.get('[data-refresh-aimili]').trigger('click')
  await wrapper.get('[data-check-aimili]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/aimilivpn/refresh', { method: 'POST' })
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/aimilivpn/check', { method: 'POST' })
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
