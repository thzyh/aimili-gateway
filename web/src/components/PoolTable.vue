<script setup lang="ts">
import type { ProxyGroupPayload } from '../api/client'
import { poolStatusDetail, poolStatusGroup, poolStatusLabel } from './poolStatus'

const props = defineProps<{ rows: ProxyGroupPayload[]; protocol: 'vless' | 'socks5h'; busy: string }>()
const emit = defineEmits<{ copy: [row: ProxyGroupPayload]; activate: [row: ProxyGroupPayload]; check: [row: ProxyGroupPayload]; rotate: [row: ProxyGroupPayload] }>()
const typeLabel = (value: string) => value === 'residential' ? '住宅' : '机房'
const latency = (row: ProxyGroupPayload) => props.protocol === 'vless' ? row.vlessLatencyMs : row.socksLatencyMs
const checkedAt = (value?: string) => value ? new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}).format(new Date(value)) : '尚未检测'
</script>

<template>
  <div class="table-wrap">
    <table>
      <thead><tr><th>国家</th><th>IP 类型</th><th>出口 IP</th><th>实测延迟</th><th>状态</th><th>最近检测</th><th class="actions-head">操作</th></tr></thead>
      <tbody>
        <tr v-for="row in rows" :key="row.id" data-pool-row>
          <td class="country-cell"><span class="country-code">{{ row.countryCode }}</span><strong>{{ row.countryName || row.countryCode }}</strong></td>
          <td class="type-cell"><span class="type-chip" :data-type="row.proxyType">{{ typeLabel(row.proxyType) }}</span></td>
          <td class="ip-cell"><code>{{ row.exitIp || '等待出口' }}</code></td>
          <td class="latency-cell"><span :class="['latency', { muted: !latency(row) }]">{{ latency(row) ? `${latency(row)} ms` : '—' }}</span></td>
          <td class="status-cell"><span class="status" :data-status="poolStatusGroup(row.status)" :data-row-status="row.id"><i />{{ poolStatusLabel(row.status) }}</span><small v-if="poolStatusDetail(row)" :data-row-detail="row.id">{{ poolStatusDetail(row) }}</small></td>
          <td class="muted-cell checked-cell">{{ checkedAt(row.lastCheckedAt) }}</td>
          <td class="row-actions">
            <button :data-copy="row.id" class="primary-small" :disabled="row.status !== 'ready' || busy !== ''" @click="emit('copy', row)">复制{{ protocol === 'vless' ? '节点' : '代理' }}</button>
            <button v-if="row.status === 'standby'" :data-activate="row.id" class="icon-button activate-button" :disabled="busy !== ''" title="将此候选切换为当前在线出口" @click="emit('activate', row)">启用</button>
			<span v-if="row.egressSource === 'main'" class="main-label">主连接</span>
			<template v-else>
              <button class="icon-button" :disabled="busy !== ''" title="重新检测" @click="emit('check', row)">检测</button>
              <button class="icon-button" :disabled="busy !== ''" title="更换同分类出口" @click="emit('rotate', row)">换 IP</button>
            </template>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-if="rows.length === 0" class="empty"><strong>没有符合条件的代理</strong><span>调整筛选条件，或刷新节点池获取最新出口。</span></div>
  </div>
</template>

<style scoped>
.main-label{color:var(--muted-text);font-size:11px;font-weight:700}
.table-wrap{overflow:auto;border:1px solid var(--border);border-radius:12px;background:var(--panel);box-shadow:var(--shadow-soft)}table{width:100%;min-width:880px;border-collapse:collapse}th{height:42px;padding:0 16px;border-bottom:1px solid var(--border);background:var(--subtle);color:var(--muted-text);font-size:12px;font-weight:700;text-align:left;letter-spacing:.02em}td{height:56px;padding:0 16px;border-bottom:1px solid var(--border-soft);font-size:13px}tbody tr:last-child td{border-bottom:0}tbody tr:hover{background:var(--hover)}td:first-child{display:flex;align-items:center;gap:9px}.country-code{display:grid;place-items:center;width:28px;height:24px;border-radius:6px;background:var(--accent-soft);color:var(--accent);font-size:10px;font-weight:850}.type-chip{padding:4px 8px;border-radius:6px;background:var(--subtle);font-size:12px;font-weight:650}.type-chip[data-type=residential]{color:#7c3aed;background:#f3e8ff}.type-chip[data-type=datacenter]{color:#0369a1;background:#e0f2fe}code{color:var(--text);font:600 12px ui-monospace,SFMono-Regular,Consolas,monospace}.latency{font-weight:700;color:var(--healthy)}.latency.muted,.muted-cell{color:var(--muted-text);font-weight:400}.status-cell{display:flex;flex-direction:column;align-items:flex-start;justify-content:center;gap:2px}.status-cell small{color:var(--muted-text);font-size:10px}.status{display:inline-flex;align-items:center;gap:7px;font-weight:650}.status i{width:7px;height:7px;border-radius:50%;background:var(--warning)}.status[data-status=ready] i{background:var(--healthy)}.status[data-status=standby] i{background:var(--muted-text)}.status[data-status=fault] i{background:var(--danger)}.actions-head{text-align:right}.row-actions{display:flex;align-items:center;justify-content:flex-end;gap:6px}.primary-small,.icon-button{height:32px;padding:0 11px;border-radius:7px;font-size:12px}.icon-button{border:1px solid var(--border);background:var(--panel);color:var(--text)}.activate-button{border-color:var(--accent);color:var(--accent);font-weight:700}.empty{display:grid;place-items:center;gap:6px;padding:64px 20px;color:var(--muted-text)}.empty strong{color:var(--text)}@media(prefers-color-scheme:dark){.type-chip[data-type=residential]{color:#d8b4fe;background:#3b1f50}.type-chip[data-type=datacenter]{color:#7dd3fc;background:#123447}}
@media(max-width:760px){.table-wrap{overflow:visible}table{min-width:0}thead{display:none}tbody{display:grid}tbody tr{display:grid;grid-template-columns:1fr 1fr;padding:12px 10px;border-bottom:1px solid var(--border)}tbody tr:last-child{border-bottom:0}td,td:first-child{height:auto;min-width:0;padding:6px 8px;border:0;display:flex;align-items:center}.country-cell{gap:8px}.type-cell{justify-content:flex-end}.ip-cell{grid-column:1/-1;padding-top:10px}.ip-cell::before{content:'出口 IP';margin-right:auto;color:var(--muted-text);font-size:11px}.latency-cell::before{content:'延迟';margin-right:8px;color:var(--muted-text);font-size:11px}.status-cell{justify-content:flex-end}.checked-cell{display:none}.row-actions{grid-column:1/-1;padding-top:10px}.row-actions .primary-small{flex:1}.row-actions .icon-button{flex:0 0 auto}}
</style>
