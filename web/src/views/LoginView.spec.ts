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
  mocks.apiFetch.mockResolvedValue(undefined)
  mocks.push.mockReset()
})

it('submits credentials without persisting them', async () => {
  const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
  const password = 'local-test-password'
  const totp = '287082'
  const wrapper = mount(LoginView)

  await wrapper.get('input[name="username"]').setValue('owner')
  await wrapper.get('input[name="password"]').setValue(password)
  await wrapper.get('input[name="totp"]').setValue(totp)
  await wrapper.get('form').trigger('submit')
  await flushPromises()

  if (mocks.apiFetch.mock.calls.length !== 1) {
    throw new Error('login API was not called exactly once')
  }
  const [path, init] = mocks.apiFetch.mock.calls[0] as [string, RequestInit]
  if (path !== '/api/v1/auth/login' || init.method !== 'POST' || typeof init.body !== 'string') {
    throw new Error('login request shape is incorrect')
  }
  const submitted = JSON.parse(init.body) as Record<string, string>
  if (submitted.username !== 'owner' || submitted.password !== password || submitted.totp !== totp) {
    throw new Error('login request did not contain the entered credentials')
  }
  expect(storageWrite).not.toHaveBeenCalled()
  expect((wrapper.get('input[name="password"]').element as HTMLInputElement).value).toBe('')
  expect((wrapper.get('input[name="totp"]').element as HTMLInputElement).value).toBe('')
  expect(mocks.push).toHaveBeenCalledWith('/')
})
