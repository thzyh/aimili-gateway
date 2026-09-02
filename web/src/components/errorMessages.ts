export type NoticeKind = 'success' | 'error' | 'progress' | 'info'

export interface UiNoticeData {
  id: string
  kind: NoticeKind
  title: string
  message: string
}

const codeMessages: Record<string, string> = {
  egress_unavailable: '当前出口不可用或身份已经变化，请同步状态后重试',
  operation_busy: '当前有维护或切换任务正在进行，请稍后重试',
  maintenance_busy: '节点维护正在进行，请稍后重试',
  no_official_candidates: '该国家当前没有官方候选节点',
  no_usable_nodes: '检测完成，但没有找到可用节点',
  upstream_unavailable: '官方节点服务暂时不可用，请稍后重试',
  candidate_dial_failed: '候选节点无法建立 VPN 连接，已从可用缓存移除',
  candidate_egress_failed: '候选节点的真实出口检测失败，已从可用缓存移除',
  protocol_failed: '协议链路检测失败，请稍后重试',
  protocol_rollback_failed: '旧协议恢复事务未完成，已锁定切换以保护当前配置',
  protocol_rollback_subscription_failed: '旧协议恢复后订阅验证未通过，已锁定切换以保护当前配置',
  protocol_rollback_validation_failed: '旧协议恢复后链路验证未通过，已锁定切换以保护当前配置',
  protocol_repair_validation_failed: '当前协议复核未通过，请先同步状态或更换健康出口',
  config_invalid: '当前协议配置无效，请先同步或修复配置',
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
  return match?.name.trim() || '所选国家'
}
