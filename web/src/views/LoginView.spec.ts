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

import LoginView from './LoginView.vue'

beforeEach(() => {
  mocks.apiFetch.mockReset()
  mocks.push.mockReset()
})

it('hides TOTP for password-only accounts and does not persist credentials', async () => {
  mocks.apiFetch.mockResolvedValueOnce({ totpRequired: false }).mockResolvedValueOnce(undefined)
  const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
  const password = 'local-test-password'
  const wrapper = mount(LoginView)
  await flushPromises()

  expect(mocks.apiFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/options')
  expect(wrapper.find('input[name="totp"]').exists()).toBe(false)
  await wrapper.get('input[name="username"]').setValue('owner')
  await wrapper.get('input[name="password"]').setValue(password)
  await wrapper.get('form').trigger('submit')
  await flushPromises()

  const [path, init] = mocks.apiFetch.mock.calls[1] as [string, RequestInit]
  expect(path).toBe('/api/v1/auth/login')
  expect(init.method).toBe('POST')
  const submitted = JSON.parse(init.body as string) as Record<string, string>
  expect(submitted).toEqual({ username: 'owner', password })
  expect(storageWrite).not.toHaveBeenCalled()
  expect((wrapper.get('input[name="password"]').element as HTMLInputElement).value).toBe('')
  expect((wrapper.get('input[name="username"]').element as HTMLInputElement).value).toBe('owner')
  expect(mocks.push).toHaveBeenCalledWith('/')
})

it('shows and validates TOTP only when the account requires it', async () => {
  mocks.apiFetch.mockResolvedValueOnce({ totpRequired: true }).mockResolvedValueOnce(undefined)
  const wrapper = mount(LoginView)
  await flushPromises()

  expect(wrapper.find('input[name="totp"]').exists()).toBe(true)
  await wrapper.get('input[name="username"]').setValue('owner')
  await wrapper.get('input[name="password"]').setValue('local-test-password')
  await wrapper.get('form').trigger('submit')
  expect(mocks.apiFetch).toHaveBeenCalledTimes(1)
  expect(wrapper.get('[role="alert"]').text()).toContain('6 位动态验证码')

  await wrapper.get('input[name="totp"]').setValue('287082')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  const [, init] = mocks.apiFetch.mock.calls[1] as [string, RequestInit]
  const submitted = JSON.parse(init.body as string) as Record<string, string>
  expect(submitted.totp).toBe('287082')
  expect((wrapper.get('input[name="totp"]').element as HTMLInputElement).value).toBe('')
})

it('fails safely when authentication options cannot be loaded', async () => {
  mocks.apiFetch.mockRejectedValueOnce(new Error('unavailable'))
  const wrapper = mount(LoginView)
  await flushPromises()

  expect(wrapper.get('[role="alert"]').text()).toContain('暂时无法获取登录方式')
  expect(wrapper.get('button').attributes('disabled')).toBeDefined()
  await wrapper.get('form').trigger('submit')
  expect(mocks.apiFetch).toHaveBeenCalledTimes(1)
})
