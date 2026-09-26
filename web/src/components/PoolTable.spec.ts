import { mount } from '@vue/test-utils'
import { expect, it } from 'vitest'
import PoolTable from './PoolTable.vue'
import type { DedicatedStandbyPayload, ProxyGroupPayload } from '../api/client'

const rows = [
  { id: 'agw-main', countryCode: 'SG', countryName: '新加坡', proxyType: 'datacenter', status: 'ready', egressSource: 'main', fixed: true, publicPort: 8443, vlessPort: 8443, mixedPort: 31000, exitIp: '203.0.113.9', vlessLatencyMs: 76, socksLatencyMs: 66, protocolMode: 'vless_tcp_reality_vision', protocolState: 'ready', subscriptionState: 'ready' },
  { id: 'slot-one', countryCode: 'JP', countryName: '日本', proxyType: 'residential', status: 'ready', slotNumber: 1, fixed: true, publicPort: 20000, vlessPort: 20000, mixedPort: 30000, exitIp: '203.0.113.10', vlessLatencyMs: 81, socksLatencyMs: 70, protocolMode: 'vless_xhttp_reality', protocolState: 'ready', subscriptionState: 'ready' },
] as ProxyGroupPayload[]

const standbys: DedicatedStandbyPayload[] = [
  { index: 0, target: 'main', countries: [], status: 'ready', country: 'JP', proxy_type: 'residential', candidate_ip: '198.51.100.20', exit_ip: '203.0.113.20', egress_ok: true, checked_at: 1_700_000_000, last_error_code: '' },
  { index: 1, target: 'slot:0', countries: [], status: 'waiting_manual', country: 'KR', proxy_type: 'datacenter', candidate_ip: '', exit_ip: '', egress_ok: false, checked_at: 1_700_000_000, last_error_code: 'recovery_budget_exhausted' },
]

it('shows the dedicated standby inside each fixed exit row and exposes manual replacement', async () => {
  const wrapper = mount(PoolTable, { props: { rows, protocol: 'vless', busy: '', standbys } })

  expect(wrapper.get('[data-standby-cell="agw-main"]').text()).toContain('已就绪')
  expect(wrapper.get('[data-standby-cell="slot-one"]').text()).toContain('等待人工处理')
  await wrapper.get('[data-standby-manual="slot-one"]').trigger('click')
  expect(wrapper.emitted('standby-manual')?.[0]).toEqual([1])
})

it('keeps the same standby presentation for the SOCKS5H pool', () => {
  const wrapper = mount(PoolTable, { props: { rows, protocol: 'socks5h', busy: '', standbys } })
  expect(wrapper.get('[data-standby-cell="agw-main"]').text()).toContain('203.0.113.20')
  expect(wrapper.get('[data-standby-cell="slot-one"]').text()).toContain('备用出口未就绪')
})
