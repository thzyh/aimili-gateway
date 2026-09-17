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
  { id: 'vn-free', egressSource: 'candidate', status: 'standby', countryCode: 'VN', countryName: 'Vietnam', candidateIp: '203.0.113.9' },
  { id: 'in-free', egressSource: 'candidate', status: 'standby', countryCode: 'IN', countryName: 'India', candidateIp: '203.0.113.10' },
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
      busy: false,
    } as any,
  })

  const failed = wrapper.get('[data-standby-index="0"]')
  expect(failed.text()).toContain('等待人工指定')
  expect(failed.text()).toContain('203.0.113.8')
  expect(failed.get('[data-assign-standby]').attributes()).toHaveProperty('disabled')
})

it('lists every country in the candidate pool and supports direct checkbox multi-selection', async () => {
  const wrapper = mount(DedicatedStandbys, {
    props: {
      rows: [
        { index: 0, target: 'slot:0', countries: ['JP'], status: 'ready', country: 'JP', exit_ip: '198.51.100.10', candidate_ip: '', node_id: 'jp-1', proxy_type: 'datacenter', egress_ok: true, checked_at: 1_700_000_000, last_error_code: '' },
        { index: 1, target: '', countries: [], status: 'disabled', country: '', exit_ip: '', candidate_ip: '', node_id: '', proxy_type: '', egress_ok: false, checked_at: 0, last_error_code: '' },
      ],
      countries: [countries[0]],
      groups,
      busy: false,
    } as any,
  })

  const first = wrapper.get('[data-standby-index="0"]')
  expect(first.text()).toContain('日本')
  expect(first.text()).toContain('越南')
  expect(first.text()).toContain('印度')
  expect(first.text()).toContain('1 个节点')
  expect(first.find('select[multiple]').exists()).toBe(false)

  await first.get('[data-country-option="VN"]').setValue(true)
  await first.get('[data-country-option="IN"]').setValue(true)
  expect(first.text()).toContain('已选 3 个国家')
  await wrapper.get('[data-save-standbys]').trigger('click')
  expect((wrapper.emitted('save') as any)?.[0]?.[0]?.[0]).toEqual({ index: 0, target: 'slot:0', countries: ['JP', 'VN', 'IN'] })
})

it('shows useful runtime health details for ready, preparing, and failed standbys', () => {
  const wrapper = mount(DedicatedStandbys, {
    props: {
      rows: [
        { index: 0, target: 'slot:0', countries: ['JP'], status: 'ready', country: 'JP', exit_ip: '198.51.100.10', candidate_ip: '203.0.113.8', node_id: 'jp-1', proxy_type: 'datacenter', egress_ok: true, checked_at: 1_700_000_000, last_error_code: '' },
        { index: 1, target: 'slot:1', countries: ['JP'], status: 'waiting_manual', country: 'JP', exit_ip: '', candidate_ip: '203.0.113.9', node_id: 'jp-2', proxy_type: 'residential', egress_ok: false, checked_at: 1_700_000_100, last_error_code: 'no_standby_candidate' },
      ],
      countries,
      groups,
      busy: false,
    } as any,
  })

  const healthy = wrapper.get('[data-standby-index="0"]')
  expect(healthy.text()).toContain('出口有效，可随时接替')
  expect(healthy.text()).toContain('真实出口')
  expect(healthy.text()).toContain('有效')
  expect(healthy.text()).toContain('最近检测')
  expect(healthy.text()).toContain('机房')

  const failed = wrapper.get('[data-standby-index="1"]')
  expect(failed.text()).toContain('自动替换未成功，等待人工指定')
  expect(failed.text()).toContain('没有找到可用的专属备用候选')
  expect(failed.text()).toContain('无效')
})

it('does not overwrite unsaved country choices when a status poll updates the same server config', async () => {
  const initialRows = [
    { index: 0, target: 'slot:0', countries: ['JP'], status: 'preparing', country: 'JP', exit_ip: '', candidate_ip: '203.0.113.8', node_id: 'jp-1', proxy_type: 'datacenter', egress_ok: false, checked_at: 1_700_000_000, last_error_code: '' },
    { index: 1, target: '', countries: [], status: 'disabled', country: '', exit_ip: '', candidate_ip: '', node_id: '', proxy_type: '', egress_ok: false, checked_at: 0, last_error_code: '' },
  ] as any
  const wrapper = mount(DedicatedStandbys, {
    props: { rows: initialRows, countries, groups, busy: false } as any,
  })

  await wrapper.get('[data-standby-index="0"] [data-country-option="VN"]').setValue(true)
  await wrapper.setProps({ rows: [{ ...initialRows[0], status: 'ready', egress_ok: true, exit_ip: '198.51.100.10' }, initialRows[1]] })
  await wrapper.get('[data-save-standbys]').trigger('click')

  expect((wrapper.emitted('save') as any)?.[0]?.[0]?.[0].countries).toEqual(['JP', 'VN'])
})
