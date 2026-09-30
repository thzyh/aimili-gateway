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
  resource_pressure: 'VPS 当前内存或 CPU 余量不足，已暂停新增检测并保留现有节点，请稍后重试',
  conflicting_capacity_fields: '候选池容量字段不一致，请刷新页面后重新保存',
  no_official_candidates: '该国家当前没有官方候选节点',
  no_usable_nodes: '检测完成，但没有找到可用节点',
  upstream_unavailable: '官方节点服务暂时不可用，请稍后重试',
  recovery_priority: '检测已让位给主连接故障恢复；现有节点保持不变，稍后可重新检测',
  maintenance_wait_timeout: '节点池维护占用时间过长，检测未开始；现有代理保持不变，请稍后重试',
  worker_start_failed: '检测任务未能启动；现有代理保持不变，请稍后重试',
  candidate_dial_failed: '候选节点无法建立 VPN 连接，已从可用缓存移除',
  candidate_egress_failed: '候选节点的真实出口检测失败，已从可用缓存移除',
  candidate_not_found: '该候选已不在当前节点池，请刷新候选列表后重新选择；现有备用保持不变',
  candidate_in_use: '该节点或相同出口 IP 已用于其他出口或备用，请选择另一个候选；现有备用保持不变',
  candidate_unavailable: '候选当前不可用，或未通过 VPN 拨号及真实出口验证；请查看恢复日志后选择其他候选',
  manual_candidate_failed: '手动指定的备用未通过验证，请选择其他候选或重新开始恢复',
  egress_check_failed: '出口节点本身仍能联网，但本地代理或路由不可用，因此没有更换 IP，请检查该出口服务',
  no_same_country_candidate: '旧版恢复没有找到同国家可用节点；专属热备用就绪后可接替，也可人工选择节点',
  no_standby_candidate: '没有找到可用的专属备用候选，后台将按恢复策略继续寻找，也可人工指定节点',
  replacement_failed: '自动恢复未成功，当前故障已保留，请检查备用状态或人工选择新的出口节点',
  repair_interrupted: '上次恢复在服务重启前未完成，请检查备用恢复状态或人工指定节点',
  manual_repair_required: '自动恢复已停止，请查看恢复日志并人工指定节点或重新开始恢复',
  manual_replacement_required: '当前出口等待人工处理，请选择新节点或在专属备用窗口重新开始恢复',
  mixed_credentials_apply_failed: '随机用户名和密码未能应用到全部出口，系统已恢复原来的账号密码',
  slot_rotate_failed: '没有找到可恢复该出口的可用候选，当前故障状态已保留，可稍后重试',
  protocol_failed: '本机 Xray/SOCKS5H 链路或检测目标未通过验证；此错误不能单独证明节点失效，详情见检测日志',
  recovery_budget_exhausted: '自动恢复已达到时间预算，请人工指定备用节点或重新开始恢复',
  recovery_pending: '出口正在等待专属热备用接替；后台会按恢复策略继续重试',
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
