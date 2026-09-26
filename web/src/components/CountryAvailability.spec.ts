import { mount } from '@vue/test-utils'
import { expect,it } from 'vitest'
import CountryAvailability from './CountryAvailability.vue'
it('keeps dialable, fresh checks and unimplemented quality separate',()=>{
 const wrapper=mount(CountryAvailability,{props:{rows:[{code:'JP',name:'Japan',candidateCount:30,observedAt:1,dialableCount:5,freshEgressCount:2,ipQualityPassCount:null,ipQualityStatus:'not_implemented'}]}})
 const row=wrapper.get('[data-country-availability="JP"]')
 expect(row.text()).toContain('5')
 expect(row.text()).toContain('2')
 expect(row.text()).toContain('暂未启用')
 expect(wrapper.text()).toContain('近期出网通过')
})
