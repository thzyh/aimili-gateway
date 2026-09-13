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

  it('explains that a failed SOCKS5H rotation kept the old credential pair', () => {
    const message = messageForCode('mixed_credentials_apply_failed', '更换失败')
    expect(message).toContain('已恢复原来的账号密码')
    expect(message).not.toContain('mixed_credentials_apply_failed')
  })
})

describe('countryDisplayName', () => {
  it('uses the catalog country name and never exposes a bare known code', () => {
    expect(countryDisplayName('us', [{ code: 'US', name: '美国' }])).toBe('美国')
  })

  it('falls back to the country code when localization is unavailable', () => {
    expect(countryDisplayName('ZZ', [])).toBe('ZZ')
  })
})
