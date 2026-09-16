import { mount } from '@vue/test-utils'
import { expect, it } from 'vitest'

import DedicatedStandbys from './DedicatedStandbys.vue'

const countries = [
  { code: 'JP', name: 'Japan', candidateCount: 15 },
  { code: 'US', name: 'United States', candidateCount: 8 },
]

const groups = [
  { id: 'agw-main', egressSource: 'main', status: 'ready', countryCode: 'JP' },
  { id: 'agw-slot-1', egressSource: 'slot', slotNumber: 1, status: 'ready', countryCode: 'JP' },
  { id: 'agw-slot-2', egressSource: 'slot', slotNumber: 2, status: 'ready', countryCode: 'JP' },
  { id: 'jp-free', egressSource: 'candidate', status: 'standby', countryCode: 'JP', candidateIp: '203.0.113.8' },
]

it('shows two independent protection targets and emits both saved configurations', async () => {
  const wrapper = mount(DedicatedStandbys, {
    props: {
      rows: [
        { index: 0, target: 'slot:0', countries: ['JP'], status: 'ready', country: 'JP', exit_ip: '198.51.100.10', candidate_ip: '', node_id: 'jp-1', proxy_type: 'datacenter', egress_ok: true, checked_at: 1, last_error_code: '' },
        { index: 1, target: 'slot:1', countries: ['JP', 'US'], status: 'degraded', country: 'JP', exit_ip: '', candidate_ip: '198.51.100.11', node_id: 'jp-2', proxy_type: 'residential', egress_ok: false, checked_at: 1, last_error_code: '' },
      ],
      countries,
      groups,
      candidates: groups,
      busy: false,
    } as any,
  })

  expect(wrapper.findAll('[data-standby-index]')).toHaveLength(2)
  expect(wrapper.text()).toContain('总 OpenVPN 上限固定为 9')
  expect(wrapper.text()).toContain('出口 1')
  expect(wrapper.text()).toContain('出口 2')
  await wrapper.get('[data-save-standbys]').trigger('click')
  expect(wrapper.emitted('save')?.[0]?.[0]).toEqual([
    { index: 0, target: 'slot:0', countries: ['JP'] },
    { index: 1, target: 'slot:1', countries: ['JP', 'US'] },
  ])
})

it('keeps a failed standby visible and offers a matching manual candidate', () => {
  const wrapper = mount(DedicatedStandbys, {
    props: {
      rows: [
        { index: 0, target: 'slot:0', countries: ['JP'], status: 'waiting_manual', country: '', exit_ip: '', candidate_ip: '', node_id: '', proxy_type: '', egress_ok: false, checked_at: 0, last_error_code: 'no_standby_candidate' },
        { index: 1, target: '', countries: [], status: 'disabled', country: '', exit_ip: '', candidate_ip: '', node_id: '', proxy_type: '', egress_ok: false, checked_at: 0, last_error_code: '' },
      ],
      countries,
      groups,
      candidates: groups,
      busy: false,
    } as any,
  })

  const failed = wrapper.get('[data-standby-index="0"]')
  expect(failed.text()).toContain('等待人工指定')
  expect(failed.text()).toContain('203.0.113.8')
  expect(failed.find('button:last-child').attributes()).toHaveProperty('disabled')
})
