import { mount } from '@vue/test-utils'
import { expect, it } from 'vitest'
import DedicatedStandbys from './DedicatedStandbys.vue'
import type { DedicatedStandbyPayload, ProxyGroupPayload } from '../api/client'

const rows: DedicatedStandbyPayload[] = Array.from({length:4},(_,index)=>({
 index,target:index===0?'main':`slot:${index-1}`,countries:[],status:index===3?'waiting_manual':'ready',
 egress_ok:index!==3, country:'JP',exit_ip:index===3?'':'198.51.100.10',checked_at:1700000000,
 last_error_code:index===3?'recovery_budget_exhausted':'',attempt_count:4
}))
const groups=[{id:'candidate-a',status:'standby',countryCode:'JP',candidateIp:'198.51.100.20'}] as ProxyGroupPayload[]
const props={rows,countries:[],groups,busy:false}

it('shows one standby for each target and removes country/target selectors', async()=>{
 const wrapper=mount(DedicatedStandbys,{props})
 expect(wrapper.findAll('[data-standby-summary]')).toHaveLength(4)
 expect(wrapper.text()).toContain('保护主连接')
 await wrapper.get('[data-toggle-standbys]').trigger('click')
 expect(wrapper.findAll('[data-standby-index]')).toHaveLength(4)
 expect(wrapper.find('[data-country-option]').exists()).toBe(false)
 expect(wrapper.find('[data-save-standbys]').exists()).toBe(false)
 expect(wrapper.text()).not.toContain('固定为 9')
})

it('offers explicit retry and real candidate validation after budget exhaustion',async()=>{
 const wrapper=mount(DedicatedStandbys,{props})
 await wrapper.get('[data-toggle-standbys]').trigger('click')
 const failed=wrapper.get('[data-standby-index="3"]')
 expect(failed.text()).toContain('恢复预算')
 await failed.get('[data-retry-standby]').trigger('click')
 expect(wrapper.emitted('retry')?.[0]).toEqual([3])
 await failed.get('select').setValue('candidate-a')
 await failed.get('[data-assign-standby]').trigger('click')
 expect(wrapper.emitted('assign')?.[0]).toEqual([3,'candidate-a'])
})

it('shows retry timing, unavailable quality and preserves manual selection during polls',async()=>{
 const wrapper=mount(DedicatedStandbys,{props})
 await wrapper.get('[data-toggle-standbys]').trigger('click')
 await wrapper.get('[data-standby-index="3"] select').setValue('candidate-a')
 await wrapper.setProps({rows:rows.map(row=>({...row,attempt_count:5}))})
 expect((wrapper.get('[data-standby-index="3"] select').element as HTMLSelectElement).value).toBe('candidate-a')
 await wrapper.setProps({rows:[{...rows[0],status:'retry_wait',next_attempt_at:1700000010}]})
 expect(wrapper.text()).toContain('等待重试')
 expect(wrapper.text()).toContain('下次重试')
 expect(wrapper.text()).toContain('暂未启用')
})
