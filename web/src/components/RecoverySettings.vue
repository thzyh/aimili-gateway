<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { apiFetch, type RecoveryPayload, type RecoverySettingsPayload } from '../api/client'

const state = ref<RecoveryPayload | null>(null)
const draft = ref<RecoverySettingsPayload | null>(null)
const busy = ref(false)
const message = ref('')
const failed = ref(false)
const fields = [
 ['failureThreshold','活动出口连续失败次数','连续失败才提升备用，避免瞬时抖动。'],
 ['healthIntervalSeconds','活动出口检测间隔（秒）','每条活动出口的真实出网检测周期。'],
 ['standbyIntervalSeconds','备用检测间隔（秒）','保持热备就绪；接替前仍再次验证。'],
 ['standbyFailureThreshold','备用连续失败次数','确认失效后开始重新补齐。'],
 ['dialTimeoutSeconds','单次拨号超时（秒）','候选 OpenVPN 握手等待上限。'],
 ['candidatesPerRound','每轮候选上限','逐个验证，住宅优先、机房兜底。'],
 ['maxConcurrentDials','最多并发恢复数','同时补齐的出口数，仍受 VPS 实时资源限制。'],
 ['retryInitialSeconds','首次重试间隔（秒）','失败后暂停，再开始下一轮。'],
 ['retryMaxSeconds','最大重试间隔（秒）','指数退避的上限。'],
 ['candidateCooldownSeconds','失败候选冷却（秒）','冷却期间不再自动选中同一候选。'],
 ['recoveryBudgetSeconds','恢复总时间预算（秒）','达到预算后等待人工重试，不无限消耗资源。'],
 ['freshnessSeconds','检测新鲜度窗口（秒）','国家“近期出网通过”统计使用的时间窗口。'],
] as const

async function load() {
 try {
  const result = await apiFetch<RecoveryPayload>('/api/v1/settings/recovery')
  if (result?.settings) { state.value=result; draft.value={...result.settings} }
 } catch { message.value='当前出口引擎未提供恢复策略接口，请在检测更新中升级整套项目后重试。'; failed.value=true }
}
async function save() {
 if (!draft.value || busy.value) return
 busy.value=true; failed.value=false
 try {
  const result=await apiFetch<RecoveryPayload>('/api/v1/settings/recovery',{method:'PUT',body:JSON.stringify({settings:draft.value})})
  state.value=result; draft.value={...result.settings}; message.value='恢复策略已保存，后续检测与恢复使用新设置。'
 } catch { failed.value=true; message.value='保存失败：请检查数值范围、最大重试间隔是否小于首次间隔，以及恢复预算是否小于拨号超时。原设置未被替换。' }
 finally { busy.value=false }
}
onMounted(load)
</script>

<template>
 <section class="recovery-panel" data-recovery-settings>
  <header><div><p class="eyebrow">RECOVERY POLICY</p><h2>故障恢复与专属热备</h2></div><strong v-if="state" class="counts">{{ state.activeTargetCount }} 活动 / {{ state.standbyTargetCount }} 备用</strong></header>
  <p class="description">每条活动出口自动绑定一个专属备用。故障时先提升已验证热备，再后台补齐；当前国家优先，住宅优先，资源压力大时延后拨号。</p>
  <p class="quality">IP 质量检测暂未启用，合格数量为未知。活动数量在“运行容量”中修改，备用数量随活动数量自动对应。</p>
  <p v-if="message" role="status" :class="{error:failed}">{{ message }}</p>
  <form v-if="draft && state" @submit.prevent="save">
   <div class="fields"><label v-for="[key,label,help] in fields" :key="key">{{ label }}<input v-model.number="draft[key]" :data-recovery-field="key" type="number" step="1" :min="state.bounds[key]?.[0]" :max="state.bounds[key]?.[1]" required><small>{{ help }}<span v-if="state.bounds[key]"> 范围 {{ state.bounds[key][0] }}–{{ state.bounds[key][1] }}。</span></small></label></div>
   <div class="toggles"><label><input v-model="draft.allowCrossCountry" type="checkbox">本国候选耗尽后允许跨国兜底</label><label><input v-model="draft.allowDatacenter" type="checkbox">住宅候选耗尽后允许机房兜底</label></div>
   <footer><span>停止或删除出口会释放它的备用；现有活动连接不会因资源上限下降而被删除。</span><button :disabled="busy">{{ busy ? '正在保存…' : '保存恢复策略' }}</button></footer>
  </form>
 </section>
</template>

<style scoped>
.recovery-panel{grid-column:1/-1;border:1px solid var(--border);border-radius:14px;background:var(--panel);padding:24px;min-width:0}header,footer{display:flex;justify-content:space-between;align-items:center;gap:16px}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:10px;letter-spacing:.12em;font-weight:800}h2{margin:0;font-size:18px}.counts{white-space:nowrap;font-size:13px;color:var(--healthy)}.description,.quality,small,footer span{font-size:12px;line-height:1.6;color:var(--muted-text)}.quality{padding:10px 12px;background:var(--muted-bg);border-radius:8px}.fields{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:18px;margin:20px 0}.fields label{display:grid;align-content:start;gap:7px;font-size:12px;font-weight:700}.fields input{width:100%;min-width:0;padding:10px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text)}small{font-weight:400}.toggles{display:flex;flex-wrap:wrap;gap:20px;font-size:13px;margin:18px 0}footer{border-top:1px solid var(--border);padding-top:16px}button{flex-shrink:0}.error{color:var(--danger)}@media(max-width:1050px){.fields{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:600px){.fields{grid-template-columns:1fr}header,footer{align-items:flex-start;flex-direction:column}}
</style>
