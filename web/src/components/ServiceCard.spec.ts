import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ServiceCard from './ServiceCard.vue'

describe('ServiceCard', () => {
  for (const testCase of [
    { health: 'healthy', label: '正常' },
    { health: 'degraded', label: '降级' },
    { health: 'unavailable', label: '不可用' },
  ] as const) {
    it(`renders ${testCase.health} distinctly`, () => {
      const wrapper = mount(ServiceCard, {
        props: {
          service: {
            service: 'fixture',
            health: testCase.health,
            capabilities: [],
            checkedAt: '2026-08-24T00:00:00Z',
          },
        },
      })
      expect(wrapper.attributes('data-health')).toBe(testCase.health)
      expect(wrapper.text()).toContain(testCase.label)
    })
  }
})
