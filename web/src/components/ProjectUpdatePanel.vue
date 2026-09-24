<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { APIError, apiFetch, type UpdateResultPayload, type UpdateSummaryPayload, type UpdateVersionPayload } from '../api/client'

const props = defineProps<{ initial: UpdateSummaryPayload }>()
const summary = ref(props.initial)
const result = ref<UpdateResultPayload | null>(null)
const candidate = ref<UpdateVersionPayload | null>(null)
const confirm = ref(false)
const busy = ref(false)
const message = ref('')
const failed = ref(false)
let alive = true
let timer: ReturnType<typeof setTimeout> | undefined
const storageKey = 'aimili-project-update-run'
const terminal = (state: string) => ['success', 'failed', 'rolled_back', 'repair_required'].includes(state)
const phases: Record<string, string> = { discover: '查询最新发布并验证签名', download: '下载更新文件', signature: '复验签名及文件摘要', backup: '保存升级前备份', install: '安装项目文件', restart: '启动服务并检查健康', identities: '核对数据库和连接身份', catalog: '生成可更新版本', rollback: '恢复升级前版本' }
const phase = computed(() => phases[result.value?.phase ?? ''] ?? '等待更新服务处理')
const percent = computed(() => Math.max(0, Math.min(100, result.value?.percent ?? 0)))
const errors: Record<string, string> = {
  invalid_signature: '发布签名验证失败，未安装。', invalid_payload: '发布文件校验失败，未安装。',
  download_failed: 'GitHub 下载失败，请稍后重新检测。', release_missing: '尚无正式发布版本。',
  unsupported_release: '最新发布没有网页升级描述，需要维护者补齐发布包。',
  unsupported_layout: '当前安装目录不符合本版本的升级要求，未改动现有服务。',
  disk_full: '可用磁盘空间不足 1 GiB，未开始安装。', version_invalid: '目标版本校验失败或已经安装。',
  update_busy: '已有更新事务运行中，请刷新查看。', updates_disabled: '版本列表已过期，请重新检测。',
}

function newRunID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(32)), x => x.toString(16).padStart(2, '0')).join('')
}

async function poll(runId: string): Promise<void> {
  if (!alive) return
  try {
    const current = await apiFetch<UpdateResultPayload>(`/api/v1/system/updates/${runId}`)
    if (!alive) return
    result.value = current
    if (terminal(current.state)) {
      failed.value = current.state !== 'success'
      if (current.state === 'success') {
        summary.value = await apiFetch<UpdateSummaryPayload>('/api/v1/system/updates')
        if (current.action === 'check') {
          candidate.value = summary.value.available.find(x => x.kind === 'project' && x.compatible) ?? null
          confirm.value = !!candidate.value
          message.value = candidate.value ? `发现新版本 ${candidate.value.version}，等待您确认。` : '当前已是最新正式版本。'
        } else {
          message.value = `更新成功，当前版本 ${summary.value.currentGateway}。请刷新页面载入新界面。`
          candidate.value = null
        }
      } else if (current.state === 'rolled_back') message.value = '更新未通过验证，已自动恢复升级前版本和数据库。'
      else if (current.state === 'repair_required') message.value = '更新恢复未完成，已阻止再次安装。请保留本次记录并联系维护者。'
      else message.value = errors[current.errorCode ?? ''] ?? `更新未完成（${current.errorCode ?? 'operation_failed'}），请保留本次记录。`
      busy.value = false
      localStorage.removeItem(storageKey)
      return
    }
    message.value = current.action === 'check' ? '正在检测最新版本…' : '正在更新，请勿关闭或重启 VPS。页面刷新后可继续查看。'
  } catch {
    message.value = '暂时无法读取进度，正在重新连接；服务重启期间可能出现此提示，不会重复提交更新。'
  }
  if (alive) timer = setTimeout(() => void poll(runId), 2000)
}

async function submit(action: 'check' | 'apply'): Promise<void> {
  if (busy.value || (action === 'apply' && !candidate.value)) return
  confirm.value = false; busy.value = true; failed.value = false
  const runId = newRunID()
  localStorage.setItem(storageKey, runId)
  result.value = { runId, kind: 'project', state: 'pending', action, percent: 0 }
  const endpoint = action === 'check' ? '/api/v1/system/updates/check' : `/api/v1/system/updates/project/${candidate.value!.version}/apply`
  try {
    await apiFetch<UpdateResultPayload>(endpoint, { method: 'POST', body: JSON.stringify({ runId }) })
  } catch (error) {
    if (error instanceof APIError && (error.status >= 400 && error.status < 500 || error.message === 'updates_disabled')) {
      localStorage.removeItem(storageKey); busy.value = false; failed.value = true
      message.value = errors[error.message] ?? '请求被拒绝，安装未开始。'
      return
    }
  }
  await poll(runId)
}

onMounted(() => {
  const runId = summary.value.activeRunId || localStorage.getItem(storageKey)
  if (runId && /^[a-f0-9]{64}$/.test(runId)) { busy.value = true; void poll(runId) }
})
onUnmounted(() => { alive = false; if (timer) clearTimeout(timer) })
</script>

<template>
  <section aria-labelledby="project-update-title">
    <h2 id="project-update-title">Aimili Gateway 更新</h2>
    <p>当前版本：<strong>{{ summary.currentGateway }}</strong></p>
    <p>检测最新正式版本，确认后下载并安装。更新会短暂重启 Gateway、出口服务和 3x-ui；域名、账户、订阅及连接身份保留，失败时自动恢复备份。</p>
    <button data-check-project :disabled="busy || !summary.enabled" @click="submit('check')">{{ busy && result?.action === 'check' ? '正在检测…' : '检测更新' }}</button>
    <button v-if="candidate && !confirm && !busy" data-show-confirm @click="confirm = true">更新到 {{ candidate.version }}</button>
    <p v-if="message" role="status" :class="{ error: failed }">{{ message }}</p>
    <div v-if="busy" class="progress-box" aria-live="polite">
      <p>{{ phase }} · {{ percent }}%</p>
      <progress :value="percent" max="100" :aria-label="phase" />
    </div>
    <small v-if="result">事务：{{ result.runId.slice(0, 12) }} · {{ result.state }}</small>
    <div v-if="confirm && candidate" class="modal-backdrop" @keydown.esc="confirm = false">
      <section role="alertdialog" aria-modal="true" aria-labelledby="update-confirm-title" class="confirm-dialog">
        <h3 id="update-confirm-title">发现新版本 {{ candidate.version }}</h3>
        <p>是否将当前 {{ summary.currentGateway }} 更新到此版本？</p>
        <p>确认后才开始下载、备份、安装和验证。服务及代理连接会短暂中断；升级失败会自动恢复。</p>
        <div class="actions"><button data-cancel-project @click="confirm = false">暂不更新</button><button data-confirm-project @click="submit('apply')">确认更新</button></div>
      </section>
    </div>
  </section>
</template>

<style scoped>
h2{margin:0;font-size:18px}p{font-size:13px;line-height:1.7;color:var(--muted-text)}button+button{margin-left:8px}progress{width:100%;height:16px;accent-color:var(--accent)}.progress-box{margin-top:14px}.error{color:var(--danger)}small{overflow-wrap:anywhere;color:var(--muted-text)}.modal-backdrop{position:fixed;inset:0;z-index:100;background:#0008;display:grid;place-items:center;padding:20px}.confirm-dialog{width:min(100%,500px);padding:24px;border-radius:14px;background:var(--panel);border:1px solid var(--border);box-shadow:var(--shadow-soft)}.actions{display:flex;justify-content:flex-end;gap:10px}
</style>
