<script setup lang="ts">
import { ref } from 'vue'
import type { CandidateCountryPayload, DedicatedStandbyPayload, ProxyGroupPayload } from '../api/client'
import { countryDisplayName } from './errorMessages'

defineProps<{rows:DedicatedStandbyPayload[];countries:CandidateCountryPayload[];groups:ProxyGroupPayload[];busy:boolean}>()
const emit=defineEmits<{assign:[index:number,candidateId:string];retry:[index:number]}>()
const expanded=ref(false)
const manualCandidates=ref<Record<number,string>>({})
function targetLabel(target:string) {
 if(target==='main')return '主连接'
 const slot=/^slot:(\d+)$/u.exec(target)
 return slot ? `出口 ${Number(slot[1])+1}` : '未启用'
}
function stateText(row:DedicatedStandbyPayload) {
 return {disabled:'未启用',preparing:'正在准备',ready:'已就绪',degraded:'检查异常',waiting_manual:'等待人工处理',retry_wait:'等待重试'}[row.status]
}
function timeText(value?:number) {
 return value ? new Date(value*1000).toLocaleString('zh-CN',{hour12:false}) : '尚无记录'
}
function reason(row:DedicatedStandbyPayload) {
 const messages:Record<string,string>={resource_pressure:'VPS 可用资源不足，已延后拨号。',no_usable_candidate:'本轮候选未通过，按退避策略继续恢复。',recovery_budget_exhausted:'恢复预算已耗尽，可重新开始或人工指定候选。',recovery_internal_error:'恢复执行异常，请检查恢复日志后重试。',manual_candidate_failed:'指定候选未通过真实出网验证。'}
 return row.last_error_code ? (messages[row.last_error_code] || `恢复未完成（${row.last_error_code}），请查看恢复日志。`) : ''
}
</script>

<template>
 <section data-dedicated-standbys class="standby-panel">
  <header><div><p class="eyebrow">DEDICATED HOT STANDBY</p><h2>专属备用 · {{ rows.length }}</h2><p>每个活动出口自动绑定一条热备；当前国家优先、住宅优先，故障提升后持续补齐。</p></div><button data-toggle-standbys type="button" class="secondary" :aria-expanded="expanded" @click="expanded=!expanded">{{ expanded?'收起详情':'查看详情' }}</button></header>
  <div class="standby-grid">
   <article v-for="row in rows" :key="row.index" :data-standby-summary="!expanded?row.index:undefined" :data-standby-index="expanded?row.index:undefined" :class="{ready:row.egress_ok,fault:row.status==='waiting_manual'}">
    <div class="title"><strong>备用 {{ row.index+1 }} · 保护{{ targetLabel(row.target) }}</strong><span>{{ stateText(row) }}</span></div>
    <div class="runtime"><span>{{ row.country?countryDisplayName(row.country,countries):'等待分配' }}</span><strong>{{ row.exit_ip || row.candidate_ip || '尚未分配 IP' }}</strong><span>{{ row.egress_ok?'真实出口有效':'真实出口未就绪' }}</span></div>
    <template v-if="expanded">
     <p v-if="reason(row)" class="reason">{{ reason(row) }}</p>
     <dl><div><dt>最近检测</dt><dd>{{ timeText(row.checked_at) }}</dd></div><div><dt>恢复轮次</dt><dd>{{ row.attempt_count ?? 0 }}</dd></div><div v-if="row.next_attempt_at"><dt>下次重试</dt><dd>{{ timeText(row.next_attempt_at) }}</dd></div><div><dt>IP 质量检测</dt><dd>暂未启用</dd></div></dl>
     <div v-if="row.status==='waiting_manual'" class="manual">
      <button data-retry-standby :disabled="busy" @click="emit('retry',row.index)">重新开始自动恢复</button>
      <label>或指定候选<select v-model="manualCandidates[row.index]" :disabled="busy"><option value="">选择可用节点</option><option v-for="candidate in groups.filter(item=>item.status==='standby')" :key="candidate.id" :value="candidate.id">{{ countryDisplayName(candidate.countryCode,countries) }} · {{ candidate.exitIp || candidate.candidateIp }}</option></select></label>
      <button data-assign-standby :disabled="busy || !manualCandidates[row.index]" @click="emit('assign',row.index,manualCandidates[row.index])">验证并设为备用</button>
     </div>
    </template>
   </article>
  </div>
 </section>
</template>

<style scoped>
.standby-panel{margin-bottom:16px;padding:20px;border:1px solid var(--border);border-radius:14px;background:var(--panel)}header,.title,.runtime{display:flex;align-items:center;justify-content:space-between;gap:12px}header p:not(.eyebrow){color:var(--muted-text);font-size:12px;line-height:1.6;margin:6px 0 0}.eyebrow{margin:0 0 4px;font-size:10px;letter-spacing:.12em;color:var(--accent);font-weight:800}h2{margin:0;font-size:19px}header button{flex-shrink:0}.standby-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,290px),1fr));gap:12px;margin-top:16px}article{padding:14px;border:1px solid var(--border);border-radius:10px;background:var(--subtle);min-width:0}article.ready{border-color:color-mix(in srgb,var(--healthy) 40%,var(--border))}article.fault{border-color:var(--danger)}.title strong{font-size:13px}.title span{font-size:11px;white-space:nowrap;color:var(--muted-text)}.runtime{margin-top:12px;flex-wrap:wrap;font-size:11px;color:var(--muted-text)}.runtime strong{color:var(--text);overflow-wrap:anywhere}.reason{font-size:12px;color:var(--warning);line-height:1.6}dl{font-size:11px;margin:14px 0 0}dl div{display:flex;justify-content:space-between;gap:10px;margin-top:8px}dt{color:var(--muted-text)}dd{margin:0;text-align:right}.manual{display:grid;gap:10px;border-top:1px solid var(--border);margin-top:16px;padding-top:14px}.manual label{display:grid;gap:6px;font-size:12px}select{width:100%;min-width:0;padding:9px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text)}@media(max-width:620px){header{align-items:flex-start;flex-direction:column}}
</style>
