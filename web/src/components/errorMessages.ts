export type NoticeKind = 'success' | 'error' | 'progress' | 'info'

export interface UiNoticeData {
  id: string
  kind: NoticeKind
  title: string
  message: string
}

const codeMessages: Record<string, string> = {
  egress_unavailable: '当前出口不可用或身份已经变化，请重新检测后重试',
  operation_busy: '当前有维护或切换任务正在进行，请稍后重试',
  maintenance_busy: '节点维护正在进行，请稍后重试',
  no_official_candidates: '该国家当前没有官方候选节点',
  no_usable_nodes: '检测完成，但没有找到可用节点',
  upstream_unavailable: '官方节点服务暂时不可用，请稍后重试',
  candidate_dial_failed: '候选节点无法建立 VPN 连接，已从可用缓存移除',
  candidate_egress_failed: '候选节点的真实出口检测失败，已从可用缓存移除',
  egress_check_failed: '出口节点本身仍能联网，但本地代理或路由不可用，因此没有更换 IP，请检查该出口服务',
  no_same_country_candidate: '已执行本次故障唯一一次自动修复，但没有找到同国家可用节点。出口会保留为未连接，请人工选择新的出口 IP',
  no_standby_candidate: '没有找到可用的专属备用候选，请调整候选国家或人工指定节点',
  replacement_failed: '已自动尝试一个同国家候选，但新节点也无法连通。出口会保留为未连接，请人工选择新的出口 IP',
  repair_interrupted: '上次自动修复在服务重启前未完成，已保留唯一一次尝试记录，不会继续自动更换节点，请人工选择新的出口 IP',
  manual_repair_required: '本次故障此前已经自动尝试过一次，系统不会因重复检测或服务重启继续换节点，请人工选择新的出口 IP',
  manual_replacement_required: '本次故障已经自动尝试一次但未成功，出口会保留为未连接，请人工选择新的出口 IP',
  mixed_credentials_apply_failed: '随机用户名和密码未能应用到全部出口，系统已恢复原来的账号密码',
  slot_rotate_failed: '没有找到可恢复该出口的可用候选，当前故障状态已保留，可稍后重试',
  protocol_failed: '协议链路检测失败，请稍后重试',
  protocol_rollback_failed: '旧协议恢复事务未完成，已锁定切换以保护当前配置',
  protocol_rollback_subscription_failed: '旧协议恢复后订阅验证未通过，已锁定切换以保护当前配置',
  protocol_rollback_validation_failed: '旧协议恢复后链路验证未通过，已锁定切换以保护当前配置',
  protocol_repair_validation_failed: '当前协议复核未通过，请先重新检测或更换健康出口',
  config_invalid: '当前协议配置无效，请先重新检测或修复配置',
  subscription_incomplete: '节点订阅尚未完整更新，请稍后重试',
  not_ready: '当前节点尚未准备就绪',
  request_failed: '请求失败，请稍后重试',
  invalid_response: '服务返回了无法识别的结果',
  refresh_failed: '国家节点刷新失败，请稍后重试',
}

export function codeFromError(error: unknown): string {
  return error instanceof Error ? error.message.trim() : ''
}

export function messageForCode(code: string, fallback: string): string {
  return codeMessages[code.trim()] ?? fallback
}

export function countryDisplayName(code: string, catalog: ReadonlyArray<{ code: string; name: string }>): string {
  const normalized = code.trim().toUpperCase()
  const match = catalog.find(item => item.code.trim().toUpperCase() === normalized)
  const known: Record<string, string> = {
    AU: '澳大利亚', BR: '巴西', CA: '加拿大', CL: '智利', CN: '中国', CO: '哥伦比亚', DE: '德国',
    ES: '西班牙', FR: '法国', GD: '格林纳达', HK: '香港', HR: '克罗地亚', HU: '匈牙利', IN: '印度',
    JP: '日本', KR: '韩国', LA: '老挝', MP: '北马里亚纳群岛', MX: '墨西哥', NL: '荷兰', PL: '波兰',
    RO: '罗马尼亚', RU: '俄罗斯', TH: '泰国', UA: '乌克兰', US: '美国', VN: '越南',
  }
  if (known[normalized]) return known[normalized]
  const raw = (match?.name || '').trim().replace(/\s*\([^)]*\)\s*$/u, '').trim()
  if (raw && /[\u3400-\u9fff]/u.test(raw)) return raw
  try {
    const localized = new Intl.DisplayNames(['zh-CN'], { type: 'region' }).of(normalized)
    if (localized && localized !== normalized && !/^未知(?:地区|区域|国家)$/u.test(localized)) return localized
  } catch { /* 不支持 Intl.DisplayNames 时回退代码 */ }
  // 有代码但无法本地化时保留代码，便于用户识别并后续补充字典；仅完全缺失时才使用中性提示。
  return raw || normalized || '所选国家'
}
