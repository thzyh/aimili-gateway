<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { CandidateCountryPayload, DedicatedStandbyConfigPayload, DedicatedStandbyPayload, ProxyGroupPayload } from '../api/client'
import { countryDisplayName, messageForCode } from './errorMessages'

const props = defineProps<{
  rows: DedicatedStandbyPayload[]
  countries: CandidateCountryPayload[]
  groups: ProxyGroupPayload[]
  busy: boolean
}>()
const emit = defineEmits<{
  save: [configs: DedicatedStandbyConfigPayload[]]
  assign: [index: number, candidateId: string]
}>()

const drafts = ref<DedicatedStandbyConfigPayload[]>([])
const manualCandidates = ref<Record<number, string>>({})
const expanded = ref(false)
const serverConfigSignature = computed(() => JSON.stringify(props.rows.map(row => ({
  index: row.index,
  target: row.target,
  countries: row.countries,
}))))

watch(serverConfigSignature, () => {
  drafts.value = props.rows.map(row => ({ index: row.index, target: row.target, countries: [...row.countries] }))
}, { immediate: true })

const targets = computed(() => props.groups
  .filter(row => row.egressSource === 'main' || typeof row.slotNumber === 'number')
  .sort((a, b) => (a.egressSource === 'main' ? -1 : (a.slotNumber ?? 99)) - (b.egressSource === 'main' ? -1 : (b.slotNumber ?? 99)))
  .map(row => ({ value: row.egressSource === 'main' ? 'main' : `slot:${Math.max(0, (row.slotNumber ?? 1) - 1)}`, label: row.egressSource === 'main' ? '主连接' : `出口 ${row.slotNumber}` })))

const countryOptions = computed(() => {
  const catalog = new Map(props.countries.map(item => [item.code.trim().toUpperCase(), item]))
  const options = new Map<string, { code: string; name: string; count: number }>()
  for (const candidate of props.groups) {
    if (candidate.status !== 'standby') continue
    const code = candidate.countryCode.trim().toUpperCase()
    if (!code) continue
    const current = options.get(code)
    const source = catalog.get(code)
    options.set(code, {
      code,
      name: countryDisplayName(code, source ? [source] : [{ code, name: candidate.countryName || code }]),
      count: (current?.count ?? 0) + 1,
    })
  }
  for (const row of props.rows) {
    for (const rawCode of row.countries) {
      const code = rawCode.trim().toUpperCase()
      if (!code || options.has(code)) continue
      const source = catalog.get(code)
      options.set(code, { code, name: countryDisplayName(code, source ? [source] : []), count: 0 })
    }
  }
  return [...options.values()].sort((left, right) => left.name.localeCompare(right.name, 'zh-CN'))
})

function stateText(row: DedicatedStandbyPayload): string {
  return ({ disabled: '未启用', preparing: '正在准备', ready: '已就绪', degraded: '检查异常', waiting_manual: '等待人工指定' } as const)[row.status]
}

function stateDescription(row: DedicatedStandbyPayload): string {
  if (row.status === 'disabled') return '未分配保护对象，不会占用 OpenVPN 连接。'
  if (row.status === 'preparing') return '正在启动备用连接并验证真实出口。'
  if (row.status === 'ready' && row.egress_ok) return '出口有效，可随时接替。'
  if (row.status === 'ready') return '备用状态异常，等待下一轮检查。'
  if (row.status === 'degraded') return '定期检测异常，系统正在寻找替代节点。'
  return '自动替换未成功，等待人工指定。'
}

function targetLabel(target: string): string {
  if (target === 'main') return '主连接'
  const slot = /^slot:(\d+)$/u.exec(target)
  return slot ? `出口 ${Number(slot[1]) + 1}` : '未设置'
}

function proxyTypeLabel(value?: string): string {
  if (value === 'residential') return '住宅'
  if (value === 'datacenter') return '机房'
  return '—'
}

function checkedAt(value?: number): string {
  if (!value) return '尚未检测'
  const milliseconds = value < 1_000_000_000_000 ? value * 1000 : value
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(milliseconds))
}

function errorText(row: DedicatedStandbyPayload): string {
  return row.last_error_code ? messageForCode(row.last_error_code, '备用连接检查失败，请调整候选范围或人工指定节点。') : ''
}

function selectAllCountries(position: number): void {
  drafts.value[position].countries = countryOptions.value.map(item => item.code)
}

function clearCountries(position: number): void {
  drafts.value[position].countries = []
}

function candidateOptions(row: DedicatedStandbyPayload) {
  const allowed = new Set(row.countries)
  return props.groups.filter(candidate => candidate.status === 'standby' && (!allowed.size || allowed.has(candidate.countryCode)))
}
</script>

<template>
  <section data-dedicated-standbys class="standby-panel">
    <div class="standby-heading">
      <div>
        <p class="eyebrow">DEDICATED HOT STANDBY</p>
        <h2>专属备用</h2>
        <p>两条备用连接持续检测，正式出口故障时优先快速接替。总 OpenVPN 上限固定为 9 个。</p>
      </div>
      <div class="heading-actions">
        <button v-if="expanded" data-save-standbys :disabled="busy || drafts.length !== 2" @click="emit('save', drafts)">{{ busy ? '正在处理…' : '保存备用设置' }}</button>
        <button data-toggle-standbys class="secondary toggle-button" type="button" :aria-expanded="expanded" aria-controls="dedicated-standby-details" @click="expanded = !expanded">{{ expanded ? '收起设置' : '展开设置' }}<span aria-hidden="true">{{ expanded ? '⌃' : '⌄' }}</span></button>
      </div>
    </div>

    <div v-if="!expanded" class="compact-grid">
      <article v-for="row in rows" :key="row.index" :data-standby-summary="row.index" class="compact-card" :class="`state-${row.status}`">
        <div class="compact-title">
          <strong>备用 {{ row.index + 1 }} · {{ row.target ? `保护${targetLabel(row.target)}` : '未启用' }}</strong>
          <span class="state-badge"><i />{{ stateText(row) }}</span>
        </div>
        <div class="compact-runtime">
          <span>{{ row.country ? countryDisplayName(row.country, countries) : '未分配国家' }}</span>
          <strong>{{ row.exit_ip || row.candidate_ip || '尚未分配 IP' }}</strong>
          <span :class="row.egress_ok ? 'health-ok' : 'health-bad'">真实出口{{ row.egress_ok ? '有效' : '无效' }}</span>
        </div>
      </article>
    </div>

    <div v-else id="dedicated-standby-details" class="standby-grid">
      <article v-for="(row, position) in rows" :key="row.index" :data-standby-index="row.index" class="standby-card" :class="`state-${row.status}`">
        <header class="card-title">
          <div><span class="standby-number">备用 {{ row.index + 1 }}</span><strong>{{ row.target ? `保护${targetLabel(row.target)}` : '尚未启用' }}</strong></div>
          <span class="state-badge"><i />{{ stateText(row) }}</span>
        </header>

        <section class="health-summary">
          <div class="health-main">
            <strong>{{ stateDescription(row) }}</strong>
            <span v-if="errorText(row)" class="error-detail">{{ errorText(row) }}</span>
          </div>
          <dl class="runtime-grid">
            <div><dt>当前 IP</dt><dd>{{ row.exit_ip || row.candidate_ip || '尚未分配' }}</dd></div>
            <div><dt>国家</dt><dd>{{ row.country ? countryDisplayName(row.country, countries) : '—' }}</dd></div>
            <div><dt>IP 类型</dt><dd>{{ proxyTypeLabel(row.proxy_type) }}</dd></div>
            <div><dt>真实出口</dt><dd :class="row.egress_ok ? 'health-ok' : 'health-bad'">{{ row.egress_ok ? '有效' : '无效' }}</dd></div>
            <div class="checked-row"><dt>最近检测</dt><dd>{{ checkedAt(row.checked_at) }}</dd></div>
          </dl>
        </section>

        <div class="settings-block">
          <label>专属保护对象
            <select v-model="drafts[position].target">
              <option value="">不启用</option>
              <option v-for="target in targets" :key="target.value" :value="target.value">{{ target.label }}</option>
            </select>
          </label>

          <fieldset class="country-picker">
            <legend>候选国家</legend>
            <div class="country-picker-heading">
              <span>直接点击可多选 · 已选 {{ drafts[position].countries.length }} 个国家</span>
              <span class="country-actions"><button type="button" class="text-button" :disabled="busy" @click="selectAllCountries(position)">全选</button><button type="button" class="text-button" :disabled="busy" @click="clearCountries(position)">清空</button></span>
            </div>
            <div v-if="countryOptions.length" class="country-options">
              <label v-for="item in countryOptions" :key="item.code" class="country-option" :class="{ selected: drafts[position].countries.includes(item.code) }">
                <input v-model="drafts[position].countries" type="checkbox" :value="item.code" :data-country-option="item.code">
                <span><strong>{{ item.name }}</strong><small>{{ item.code }} · {{ item.count }} 个节点</small></span>
              </label>
            </div>
            <p v-else class="empty-countries">候选池中暂时没有可选国家。</p>
            <small class="country-help">留空时自动跟随被保护出口当前国家。</small>
          </fieldset>
        </div>

        <div v-if="row.status === 'waiting_manual'" class="manual-box">
          <div><strong>需要人工指定备用节点</strong><span>系统不会因刷新页面或服务重启而循环更换。</span></div>
          <select v-model="manualCandidates[row.index]">
            <option value="">选择候选池中的可用节点</option>
            <option v-for="candidate in candidateOptions(row)" :key="candidate.id" :value="candidate.id">{{ countryDisplayName(candidate.countryCode, countries) }} · {{ candidate.exitIp || candidate.candidateIp }}</option>
          </select>
          <button data-assign-standby :disabled="busy || !manualCandidates[row.index]" @click="emit('assign', row.index, manualCandidates[row.index])">验证并设为备用</button>
        </div>
      </article>
    </div>
  </section>
</template>

<style scoped>
.standby-panel{margin:0 0 16px;padding:18px;border:1px solid var(--border);border-radius:16px;background:var(--panel);box-shadow:0 8px 28px rgba(15,23,42,.04)}
.standby-heading{display:flex;align-items:flex-end;justify-content:space-between;gap:20px}.eyebrow{margin:0 0 4px;color:var(--accent);font-size:10px;font-weight:800;letter-spacing:.14em}.standby-heading h2{margin:0;font-size:20px;letter-spacing:-.02em}.standby-heading p:not(.eyebrow){margin:6px 0 0;color:var(--muted-text);font-size:12px}.heading-actions{display:flex;align-items:center;gap:8px;flex:none}.toggle-button{display:inline-flex;align-items:center;justify-content:center;gap:7px}.toggle-button span{font-size:14px}.compact-grid,.standby-grid{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:14px;margin-top:16px}.compact-card{display:grid;gap:8px;min-width:0;padding:12px 14px;border:1px solid var(--border);border-radius:11px;background:var(--subtle);box-shadow:inset 3px 0 0 transparent}.compact-card.state-ready{border-color:rgba(34,197,94,.4);box-shadow:inset 3px 0 0 #22c55e}.compact-card.state-preparing{border-color:rgba(59,130,246,.35);box-shadow:inset 3px 0 0 #3b82f6}.compact-card.state-degraded,.compact-card.state-waiting_manual{border-color:rgba(239,68,68,.4);box-shadow:inset 3px 0 0 #ef4444}.compact-title,.compact-runtime{display:flex;align-items:center;gap:10px}.compact-title{justify-content:space-between}.compact-title>strong{font-size:13px}.compact-runtime{min-width:0;color:var(--muted-text);font-size:11px}.compact-runtime>strong{overflow:hidden;color:var(--text);font-size:12px;text-overflow:ellipsis;white-space:nowrap}.compact-runtime>span:last-child{margin-left:auto;white-space:nowrap}
.standby-card{display:grid;align-content:start;gap:14px;min-width:0;padding:16px;border:1px solid var(--border);border-radius:14px;background:var(--subtle);box-shadow:inset 3px 0 0 transparent}.standby-card.state-ready{border-color:rgba(34,197,94,.45);box-shadow:inset 3px 0 0 #22c55e}.standby-card.state-preparing{border-color:rgba(59,130,246,.4);box-shadow:inset 3px 0 0 #3b82f6}.standby-card.state-degraded,.standby-card.state-waiting_manual{border-color:rgba(239,68,68,.42);box-shadow:inset 3px 0 0 #ef4444}
.card-title{display:flex;align-items:flex-start;justify-content:space-between;gap:12px}.card-title>div{display:grid;gap:3px}.standby-number{color:var(--muted-text);font-size:11px;font-weight:700}.card-title strong{font-size:15px}.state-badge{display:inline-flex;align-items:center;gap:6px;padding:5px 9px;border-radius:999px;background:rgba(100,116,139,.12);color:var(--muted-text);font-size:11px;font-weight:800;white-space:nowrap}.state-badge i{width:7px;height:7px;border-radius:50%;background:#94a3b8}.state-ready .state-badge{background:rgba(34,197,94,.12);color:#159447}.state-ready .state-badge i{background:#22c55e}.state-preparing .state-badge{background:rgba(59,130,246,.12);color:#2877d2}.state-preparing .state-badge i{background:#3b82f6;box-shadow:0 0 0 3px rgba(59,130,246,.15)}.state-degraded .state-badge,.state-waiting_manual .state-badge{background:rgba(239,68,68,.11);color:var(--danger)}.state-degraded .state-badge i,.state-waiting_manual .state-badge i{background:#ef4444}
.health-summary{display:grid;gap:11px;padding:13px;border:1px solid var(--border);border-radius:11px;background:var(--panel)}.health-main{display:grid;gap:4px}.health-main strong{font-size:12px}.error-detail{color:var(--danger);font-size:11px;line-height:1.55}.runtime-grid{display:grid;grid-template-columns:1.35fr .8fr .8fr .7fr;gap:10px;margin:0}.runtime-grid>div{min-width:0}.runtime-grid dt{margin:0 0 3px;color:var(--muted-text);font-size:10px}.runtime-grid dd{overflow:hidden;margin:0;font-size:11px;font-weight:700;text-overflow:ellipsis;white-space:nowrap}.runtime-grid .checked-row{grid-column:1/-1}.health-ok{color:#159447}.health-bad{color:var(--danger)}
.settings-block{display:grid;gap:12px}.settings-block>label{display:grid;gap:6px;font-size:12px;font-weight:800}select{min-height:38px;padding:7px 10px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text)}.country-picker{min-width:0;margin:0;padding:0;border:0}.country-picker legend{margin:0 0 6px;padding:0;font-size:12px;font-weight:800}.country-picker-heading{display:flex;align-items:center;justify-content:space-between;gap:10px;margin-bottom:8px;color:var(--muted-text);font-size:10px}.country-actions{display:flex;gap:8px}.text-button{min-height:auto;padding:0;border:0;background:transparent;color:var(--accent);font-size:11px;font-weight:800}.country-options{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:7px;max-height:174px;overflow:auto;padding:2px}.country-option{display:flex;align-items:center;gap:7px;min-width:0;padding:8px;border:1px solid var(--border);border-radius:9px;background:var(--panel);cursor:pointer}.country-option:hover,.country-option.selected{border-color:var(--accent);background:rgba(94,112,255,.08)}.country-option input{flex:none;margin:0;accent-color:var(--accent)}.country-option span{display:grid;min-width:0}.country-option strong{overflow:hidden;font-size:11px;text-overflow:ellipsis;white-space:nowrap}.country-option small{color:var(--muted-text);font-size:9px}.country-help,.empty-countries{display:block;margin:7px 0 0;color:var(--muted-text);font-size:10px}.empty-countries{padding:12px;border:1px dashed var(--border);border-radius:9px;text-align:center}
.manual-box{display:grid;grid-template-columns:minmax(0,1fr) minmax(150px,1fr) auto;align-items:end;gap:9px;padding:12px;border:1px solid rgba(239,68,68,.28);border-radius:10px;background:rgba(239,68,68,.06)}.manual-box>div{display:grid;gap:3px;color:var(--danger);font-size:11px}.manual-box>div span{color:var(--muted-text);font-size:10px;line-height:1.4}.manual-box button{white-space:nowrap}
@media(max-width:1100px){.country-options{grid-template-columns:repeat(2,minmax(0,1fr))}.manual-box{grid-template-columns:1fr}.manual-box button{justify-self:start}}
@media(max-width:760px){.standby-panel{padding:14px}.standby-heading{align-items:flex-start;flex-direction:column}.heading-actions{width:100%}.heading-actions button{flex:1}.compact-grid,.standby-grid{grid-template-columns:1fr}.country-options{grid-template-columns:repeat(2,minmax(0,1fr))}.runtime-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.runtime-grid .checked-row{grid-column:1/-1}}
@media(max-width:520px){.compact-runtime{align-items:flex-start;flex-wrap:wrap}.compact-runtime>span:last-child{margin-left:0;width:100%}}
@media(max-width:420px){.country-picker-heading{align-items:flex-start;flex-direction:column}.country-options{grid-template-columns:1fr}}
</style>
