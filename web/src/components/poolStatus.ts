import type { ProxyGroupPayload, ProxyGroupStatus } from '../api/client'

export type PoolStatusGroup = 'standby' | 'ready' | 'processing' | 'fault'

export function poolStatusGroup(status: ProxyGroupStatus): PoolStatusGroup {
  if (status === 'standby') return 'standby'
  if (status === 'ready') return 'ready'
  if (status === 'degraded' || status === 'repair_required') return 'fault'
  return 'processing'
}

export function poolStatusLabel(status: ProxyGroupStatus): string {
  return ({ standby: '可选节点', ready: '已启用', processing: '处理中', fault: '故障' } as const)[poolStatusGroup(status)]
}

export function poolStatusDetail(row: ProxyGroupPayload): string {
  if (row.status === 'repair_required') return '需要修复'
  if (row.status === 'degraded') return '链路检测失败'
  if (row.status === 'provisioning') return '正在创建'
  if (row.status === 'rotating') return '正在更换出口 IP'
  if (row.status === 'disabling') return '正在回收资源'
  return ''
}
