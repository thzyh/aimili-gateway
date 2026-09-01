import { describe, expect, it } from 'vitest'

import { countryDisplayName, messageForCode } from './errorMessages'

describe('messageForCode', () => {
  it('maps backend codes to safe Chinese text without exposing the raw code', () => {
    const message = messageForCode('egress_unavailable', '操作失败')
    expect(message).toContain('当前出口不可用')
    expect(message).not.toContain('egress_unavailable')
  })

  it('uses a safe fallback for unknown codes', () => {
    expect(messageForCode('unrecognized_internal_code', '刷新失败')).toBe('刷新失败')
  })
})

describe('countryDisplayName', () => {
  it('uses the catalog country name and never exposes a bare known code', () => {
    expect(countryDisplayName('us', [{ code: 'US', name: '美国' }])).toBe('美国')
  })

  it('uses a neutral label when the country is absent from the catalog', () => {
    expect(countryDisplayName('ZZ', [])).toBe('所选国家')
  })
})
