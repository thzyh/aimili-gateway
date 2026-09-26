import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ apiFetch: vi.fn() }))
vi.mock('../api/client', () => ({ apiFetch: mocks.apiFetch }))
import RecoverySettings from './RecoverySettings.vue'

it('shows editable recovery policy, paired counts and explicitly unavailable quality', async () => {
 const settings = {failureThreshold:3,healthIntervalSeconds:30,standbyIntervalSeconds:30,standbyFailureThreshold:2,dialTimeoutSeconds:25,candidatesPerRound:8,maxConcurrentDials:1,retryInitialSeconds:10,retryMaxSeconds:120,candidateCooldownSeconds:600,recoveryBudgetSeconds:1800,freshnessSeconds:120,allowCrossCountry:true,allowDatacenter:true}
 const response={settings,bounds:{retryInitialSeconds:[5,300]},activeTargetCount:4,standbyTargetCount:4,regularExitSlotsMax:3,ipQualityStatus:'not_implemented'}
 mocks.apiFetch.mockResolvedValue(response)
 const wrapper=mount(RecoverySettings)
 await flushPromises()
 expect(wrapper.text()).toContain('4 活动 / 4 备用')
 expect(wrapper.text()).toContain('IP 质量检测暂未启用')
 await wrapper.get('[data-recovery-field="retryInitialSeconds"]').setValue(20)
 await wrapper.get('form').trigger('submit')
 await flushPromises()
 expect(mocks.apiFetch).toHaveBeenCalledWith('/api/v1/settings/recovery', {method:'PUT',body:JSON.stringify({settings:{...settings,retryInitialSeconds:20}})})
 expect(wrapper.text()).toContain('已保存')
})

it('shows a concrete upgrade path when the runtime lacks the endpoint', async () => {
 mocks.apiFetch.mockRejectedValue(new Error('not_configured'))
 const wrapper=mount(RecoverySettings)
 await flushPromises()
 expect(wrapper.text()).toContain('升级整套项目')
 expect(wrapper.find('form').exists()).toBe(false)
})
