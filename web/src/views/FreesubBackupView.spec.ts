import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), push: vi.fn() }))
vi.mock('../api/client', () => ({ apiFetch: mocks.apiFetch, idempotencyHeaders: () => ({ 'Idempotency-Key': 'test' }) }))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push }),
  RouterLink: { props: ['to'], template: '<a :data-to="to"><slot /></a>' },
}))

import FreesubBackupView from './FreesubBackupView.vue'

beforeEach(() => {
  mocks.apiFetch.mockReset()
  mocks.apiFetch.mockResolvedValue({
    id: 'agw-freesub', candidateId: 'fs-one', countryCode: 'IN', protocol: 'vless',
    exitIp: '203.0.113.10', socksPort: 19080, status: 'degraded', repairAttempts: 0, version: 2,
  })
})

it('shows an independent backup card and exposes one replacement', async () => {
  const wrapper = mount(FreesubBackupView)
  await flushPromises()
  expect(wrapper.get('[data-freesub-card]').text()).toContain('203.0.113.10')
  expect(wrapper.get('[data-freesub-card]').text()).toContain('0/1')
  await wrapper.get('[data-replace-freesub]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/freesub/backup/replace', {
    method: 'POST', headers: { 'Idempotency-Key': 'test' },
  })
})

it('keeps the card visible after automatic replacement failure', async () => {
  mocks.apiFetch.mockResolvedValue({
    id: 'agw-freesub', candidateId: 'fs-one', countryCode: 'IN', protocol: 'vless',
    status: 'waiting_manual', repairAttempts: 1, lastErrorCode: 'replacement_failed', version: 3,
  })
  const wrapper = mount(FreesubBackupView)
  await flushPromises()
  expect(wrapper.get('[data-freesub-card]').text()).toContain('自动替换失败，等待人工处理')
  expect(wrapper.get('[data-replace-freesub]').attributes()).toHaveProperty('disabled')

  await wrapper.get('[data-manual-provision-freesub]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/freesub/backup/manual-provision', {
    method: 'POST', headers: { 'Idempotency-Key': 'test' },
  })
})
