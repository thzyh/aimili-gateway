import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  push: vi.fn(),
}))

vi.mock('../api/client', () => ({
  apiFetch: mocks.apiFetch,
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push }),
}))

import OverviewView from './OverviewView.vue'

beforeEach(() => {
  mocks.apiFetch.mockReset()
  mocks.push.mockReset()
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/overview') {
      return Promise.resolve({
        gateway: { health: 'healthy' },
        services: [
          { service: 'aimili-vpn', health: 'degraded', capabilities: [], checkedAt: '2026-08-24T00:00:00Z' },
          { service: '3x-ui', health: 'unavailable', capabilities: [], errorCode: 'connection_failed', checkedAt: '2026-08-24T00:00:00Z' },
        ],
        expertModeAvailable: true,
      })
    }
    if (path === '/api/v1/navigation') {
      return Promise.resolve({ expertModeUrl: '/expert-fixture/' })
    }
    if (path === '/api/v1/auth/logout') {
      return Promise.resolve(undefined)
    }
    return Promise.reject(new Error('unexpected API path'))
  })
})

it('logs out through the gateway session API', async () => {
  const wrapper = mount(OverviewView)
  await flushPromises()
  await wrapper.get('button[data-logout]').trigger('click')
  await flushPromises()
  if (!mocks.apiFetch.mock.calls.some(([path]) => path === '/api/v1/auth/logout')) {
    throw new Error('logout API was not called')
  }
  expect(mocks.push).toHaveBeenCalledWith('/login')
})

it('renders all service layers and opens expert mode outside an iframe', async () => {
  const wrapper = mount(OverviewView)
  await flushPromises()

  expect(wrapper.find('[data-health="healthy"]').exists()).toBe(true)
  expect(wrapper.find('[data-health="degraded"]').exists()).toBe(true)
  expect(wrapper.find('[data-health="unavailable"]').exists()).toBe(true)
  expect(wrapper.find('iframe').exists()).toBe(false)
  const expertLink = wrapper.get('a[data-expert-mode]')
  expect(expertLink.attributes('href')).toBe('/expert-fixture/')
  expect(expertLink.attributes('target')).toBe('_blank')
  expect(wrapper.text()).toContain('3x-ui 会要求单独登录')
})
