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
  mocks.apiFetch.mockResolvedValue({ managedVlessCount: 1, managedMixedCount: 1, managedOutboundCount: 1, ownershipMatches: true, lastCheckedAt: '2026-08-27T00:00:00Z' })
})

it('shows managed 3x-ui ownership and exposes only check and repair actions', async () => {
  const wrapper = mount(XUISettingsView)
  await flushPromises()
  expect(wrapper.text()).toContain('所有权核对通过')
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
