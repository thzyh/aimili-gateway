<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { CandidateCountryPayload, DedicatedStandbyConfigPayload, DedicatedStandbyPayload, ProxyGroupPayload } from '../api/client'
import { countryDisplayName } from './errorMessages'

const props = defineProps<{
  rows: DedicatedStandbyPayload[]
  countries: CandidateCountryPayload[]
  groups: ProxyGroupPayload[]
  candidates: ProxyGroupPayload[]
  busy: boolean
}>()
const emit = defineEmits<{
  save: [configs: DedicatedStandbyConfigPayload[]]
  assign: [index: number, candidateId: string]
}>()

const drafts = ref<DedicatedStandbyConfigPayload[]>([])
const manualCandidates = ref<Record<number, string>>({})
watch(() => props.rows, rows => {
  drafts.value = rows.map(row => ({ index: row.index, target: row.target, countries: [...row.countries] }))
}, { immediate: true, deep: true })

const targets = computed(() => props.groups
  .filter(row => row.egressSource === 'main' || typeof row.slotNumber === 'number')
  .sort((a, b) => (a.egressSource === 'main' ? -1 : (a.slotNumber ?? 99)) - (b.egressSource === 'main' ? -1 : (b.slotNumber ?? 99)))
  .map(row => ({ value: row.egressSource === 'main' ? 'main' : `slot:${Math.max(0, (row.slotNumber ?? 1) - 1)}`, label: row.egressSource === 'main' ? '主连接' : `出口 ${row.slotNumber}` })))
const countryOptions = computed(() => props.countries.map(item => ({ code: item.code, name: countryDisplayName(item.code, [item]) })))

function stateText(row: DedicatedStandbyPayload): string {
  return ({ disabled: '未启用', preparing: '正在准备', ready: '已就绪', degraded: '检查异常', waiting_manual: '等待人工指定' } as const)[row.status]
}
function candidateOptions(row: DedicatedStandbyPayload) {
  const allowed = new Set(row.countries)
  return props.candidates.filter(candidate => candidate.status === 'standby' && (!allowed.size || allowed.has(candidate.countryCode)))
}
</script>

<template>
  <section data-dedicated-standbys class="standby-panel">
    <div class="standby-heading"><div><p class="eyebrow">DEDICATED HOT STANDBY</p><h2>专属备用</h2><p>两条备用连接会持续检测；正式出口故障时优先快速接替。总 OpenVPN 上限固定为 9。</p></div><button data-save-standbys :disabled="busy || drafts.length !== 2" @click="emit('save', drafts)">保存备用设置</button></div>
    <div class="standby-grid">
      <article v-for="(row, position) in rows" :key="row.index" :data-standby-index="row.index" class="standby-card" :class="`state-${row.status}`">
        <div class="card-title"><strong>备用 {{ row.index + 1 }}</strong><span>{{ stateText(row) }}</span></div>
        <label>专属保护对象<select v-model="drafts[position].target"><option value="">不启用</option><option v-for="target in targets" :key="target.value" :value="target.value">{{ target.label }}</option></select></label>
        <label>候选国家（可多选）<select v-model="drafts[position].countries" multiple><option v-for="item in countryOptions" :key="item.code" :value="item.code">{{ item.name }}（{{ item.code }}）</option></select><small>不选择时，自动跟随被保护出口当前国家。</small></label>
        <p class="runtime">当前备用：{{ row.country ? countryDisplayName(row.country, countries) : '—' }} · {{ row.exit_ip || row.candidate_ip || '尚未就绪' }}</p>
        <div v-if="row.status === 'waiting_manual'" class="manual-box">
          <strong>自动寻找未成功，请人工指定</strong>
          <select v-model="manualCandidates[row.index]"><option value="">选择可用候选</option><option v-for="candidate in candidateOptions(row)" :key="candidate.id" :value="candidate.id">{{ countryDisplayName(candidate.countryCode, countries) }} · {{ candidate.exitIp || candidate.candidateIp }}</option></select>
          <button :disabled="busy || !manualCandidates[row.index]" @click="emit('assign', row.index, manualCandidates[row.index])">验证并设为备用</button>
        </div>
      </article>
    </div>
  </section>
</template>

<style scoped>
.standby-panel{margin:0 0 14px;padding:15px;border:1px solid var(--border);border-radius:12px;background:var(--panel)}.standby-heading{display:flex;align-items:flex-end;justify-content:space-between;gap:16px}.eyebrow{margin:0 0 4px;color:var(--accent);font-size:10px;font-weight:800;letter-spacing:.12em}.standby-heading h2{margin:0;font-size:18px}.standby-heading p:not(.eyebrow){margin:5px 0 0;color:var(--muted-text);font-size:12px}.standby-grid{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin-top:13px}.standby-card{display:grid;gap:9px;padding:12px;border:1px solid var(--border);border-radius:10px;background:var(--subtle)}.standby-card.state-ready{border-color:#86efac}.standby-card.state-waiting_manual{border-color:#fca5a5}.card-title{display:flex;justify-content:space-between}.card-title span{font-size:12px;color:var(--muted-text)}label{display:grid;gap:5px;font-size:12px;font-weight:700}select{min-height:36px;padding:6px 9px;border:1px solid var(--border);border-radius:7px;background:var(--input);color:var(--text)}select[multiple]{height:82px}small,.runtime{margin:0;color:var(--muted-text);font-size:11px}.manual-box{display:grid;gap:7px;padding-top:9px;border-top:1px solid var(--border);color:var(--danger);font-size:12px}@media(max-width:760px){.standby-heading{align-items:flex-start;flex-direction:column}.standby-grid{grid-template-columns:1fr}}
</style>
