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
  mocks.apiFetch.mockImplementation((path: string) => {
    if (path === '/api/v1/freesub/candidates') return Promise.resolve([
      { candidateId: 'fs-one', countryCode: 'TW', protocol: 'vmess', exitIp: '122.118.150.43', riskScore: 9, nativeIp: true, scenarioStars: { tiktok: 5, crossBorderEcommerce: 5, socialMedia: 5, ai: 5 } },
      { candidateId: 'fs-two', countryCode: 'TW', protocol: 'vmess', exitIp: '111.246.9.5', riskScore: 4, nativeIp: true, scenarioStars: { tiktok: 5, crossBorderEcommerce: 5, socialMedia: 5, ai: 5 } },
    ])
    return Promise.resolve({
      id: 'agw-freesub', candidateId: 'fs-one', countryCode: 'TW', protocol: 'vmess',
      exitIp: '122.118.150.43', socksPort: 19080, status: 'ready', repairAttempts: 0, version: 2,
      riskScore: 9, nativeIp: true, nativeLabel: '原生 IP',
      scenarioStars: { tiktok: 5, crossBorderEcommerce: 5, socialMedia: 5, ai: 5 },
    })
  })
})

it('shows strict quality and allows a healthy connection to be changed manually', async () => {
  const wrapper = mount(FreesubBackupView)
  await flushPromises()
  expect(wrapper.get('[data-freesub-card]').text()).toContain('122.118.150.43')
  expect(wrapper.get('[data-freesub-card]').text()).toContain('风控 9%')
  expect(wrapper.get('[data-freesub-card]').text()).toContain('原生 IP')
  expect(wrapper.get('[data-freesub-card]').text()).toContain('TikTok 5★')
  expect(wrapper.get('[data-freesub-card]').text()).toContain('0/1')
  expect(wrapper.get('[data-replace-freesub]').attributes()).toHaveProperty('disabled')
  await wrapper.get('[data-manual-candidate]').setValue('fs-two')
  await wrapper.get('[data-manual-provision-freesub]').trigger('click')
  await flushPromises()
  expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/freesub/backup/manual-provision', {
    method: 'POST', headers: { 'Idempotency-Key': 'test' }, body: JSON.stringify({ candidateId: 'fs-two' }),
  })
})

it('closes a successful check notice', async () => {
  const wrapper = mount(FreesubBackupView)
  await flushPromises()
  await wrapper.get('[data-check-freesub]').trigger('click')
  await flushPromises()
  expect(wrapper.get('[role="status"]').text()).toContain('检测完成')
  await wrapper.get('[aria-label="关闭提示"]').trigger('click')
  expect(wrapper.find('[role="status"]').exists()).toBe(false)
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

  expect(wrapper.find('[data-manual-provision-freesub]').exists()).toBe(true)
})
