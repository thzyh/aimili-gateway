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
const guideHref = ref('')
const remedy = ref('')
let alive = true
let timer: ReturnType<typeof setTimeout> | undefined
const storageKey = 'aimili-project-update-run'
const terminal = (state: string) => ['success', 'failed', 'rolled_back', 'repair_required'].includes(state)
const phases: Record<string, string> = { discover: '查询最新发布并验证签名', download: '下载更新文件', signature: '复验签名及文件摘要', updater: '升级独立更新引擎', backup: '保存升级前备份', install: '安装项目文件', restart: '启动服务并检查健康', identities: '核对数据库和连接身份', catalog: '生成可更新版本', rollback: '恢复升级前版本' }
const phase = computed(() => phases[result.value?.phase ?? ''] ?? '等待更新服务处理')
const percent = computed(() => Math.max(0, Math.min(100, result.value?.percent ?? 0)))
const errors: Record<string, string> = {
  invalid_signature: '发布签名验证失败，未安装。', invalid_payload: '发布文件校验失败，未安装。',
  download_failed: 'GitHub 下载失败，请稍后重新检测。', release_missing: '尚无正式发布版本。',
  unsupported_release: '最新发布没有网页升级描述，需要维护者补齐发布包。',
  unsupported_layout: '当前安装目录不符合本版本的升级要求，未改动现有服务。',
  disk_full: '可用磁盘空间低于目标发布包要求，未开始安装。', version_invalid: '目标版本校验失败或已经安装。',
  update_busy: '已有更新事务运行中，请刷新查看。', updates_disabled: '版本列表已过期，请重新检测。',
  platform_unsupported: '当前操作系统或 CPU 架构不在目标版本的支持范围。',
  database_too_new: '现有 Gateway 数据库版本高于目标发布包支持的版本。',
  database_too_old: '现有 Gateway 数据库过旧，缺少可验证的连续迁移路径。',
  updater_upgrade_required: '目标版本需要更新的安装器，但发布包缺少可验签的更新器资产。',
  updater_too_new: '本机更新器高于目标版本声明的兼容范围。',
  unsupported_component: '目标版本更改了当前更新器尚不能安全安装的运行组件。',
  release_incomplete: '正式发布资产不完整，请等待维护者发布新版本。',
  service_unhealthy: '当前有服务或数据库预检未通过，请先恢复运行状态。',
  manifest_incompatible: '签名清单缺少必要的兼容性说明，无法安全安装。',
  rollback_failed: '自动恢复没有通过验证，请先按受限修复步骤检查。',
  updater_engine_failed: '新更新引擎启动失败，已切回旧引擎；项目尚未更新。',
  database_invalid: '数据库完整性检查失败，更新器已尝试恢复事务快照。',
  identity_changed: '升级后的连接身份与升级前不一致，更新器已尝试回滚。',
}
const componentNames: Record<string,string> = { caddy: 'Caddy', xray: 'Xray', openvpn: 'OpenVPN', systemd: 'systemd', gateway: 'Gateway', 'aimili-egress': 'aimili-egress', '3x-ui': '3x-ui' }
const remedies: Record<string,string> = {
  platform_unsupported: '在 Ubuntu 24.04、x86_64 VPS 上使用对应签名包，或等待维护者发布当前平台的版本。',
  unsupported_layout: '备份现有数据，请维护者先提供旧布局的签名迁移包；不要重跑空白 VPS 安装脚本。',
  database_too_new: '选择支持当前数据库模式的较新版本；回退时必须同时恢复该事务的数据库快照。',
  database_too_old: '先安装包含连续数据库迁移的签名过渡版本，再检测目标版本。',
  disk_full: '运行 df -h /var/lib/aimili-gateway，扩容或清理确认无用的文件后重试。',
  updater_upgrade_required: '先安装兼容旧更新器的签名过渡版本，再重新检测目标版本。',
  updater_too_new: '选择支持当前更新器的较新发布版本。',
  unsupported_component: '请维护者先发布该组件的安全执行器过渡版本，再通过页面升级。',
  service_unhealthy: '运行 systemctl --failed，先恢复故障服务和数据库，再重新检测。',
  release_incomplete: '等待维护者使用新版本号发布完整资产。',
  manifest_incompatible: '等待维护者发布包含完整兼容信息的签名版本。',
  invalid_signature: '核对发布来源与公钥轮换公告，不要关闭验签。',
  invalid_payload: '等待维护者以新版本号重新发布完整资产。',
  download_failed: '检查 VPS 到 GitHub Release 的网络，恢复后重新检测。',
  rollback_failed: '保留事务与备份，核对服务状态后由维护者按同一快照恢复。',
  updater_engine_failed: '查看更新引擎日志和失败事务；维护者需重新发布修正后的签名版本，不能反复安装同一坏引擎。',
  database_invalid: '保留数据库及事务快照，核对终态和 SQLite 完整性后再处理。',
  identity_changed: '核对 Gateway、3x-ui 数据库和客户端保存的连接身份；若需要修复，按同一事务快照恢复。',
}
function safeGuide(path?: string): string {
  return path && /^https:\/\/github\.com\/thzyh\/aimili-gateway\/blob\/main\/docs\/upgrade\.md#[a-z-]+$/.test(path) ? path : ''
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
          const latest = summary.value.available.find(x => x.kind === 'project')
          candidate.value = latest?.compatible ? latest : null
          confirm.value = !!candidate.value
          guideHref.value = safeGuide(latest?.upgradePath)
          remedy.value = latest && !latest.compatible ? remedies[latest.reasonCode ?? ''] ?? '保留现状，请维护者核对签名发布和服务器状态。' : ''
          failed.value = !!latest && !latest.compatible
          message.value = candidate.value
            ? `发现新版本 ${candidate.value.version}。${candidate.value.requiresUpdaterUpgrade ? '将先自动升级更新器，再继续同一更新事务。' : '等待您确认。'}`
            : latest ? `发现 ${latest.version}，暂不能自动更新：${errors[latest.reasonCode ?? ''] ?? latest.reasonCode ?? '不兼容'}${latest.component ? `（组件：${componentNames[latest.component] ?? latest.component}）` : ''}`
              : '当前已是最新正式版本。'
        } else {
          message.value = `更新成功，当前版本 ${summary.value.currentGateway}。请刷新页面载入新界面。`
          candidate.value = null
        }
      } else if (current.state === 'rolled_back') message.value = `更新失败并已自动恢复升级前版本和数据库。原因：${errors[current.errorCode ?? ''] ?? current.errorCode ?? '未分类错误'}`
      else if (current.state === 'repair_required') message.value = `更新恢复未完成，已阻止再次安装。原因：${errors[current.errorCode ?? ''] ?? current.errorCode ?? '未分类错误'}`
      else message.value = errors[current.errorCode ?? ''] ?? `更新未完成（${current.errorCode ?? 'operation_failed'}），请保留本次记录。`
      if (current.state !== 'success') {
        guideHref.value = safeGuide(current.upgradePath)
        remedy.value = remedies[current.errorCode ?? ''] ?? '保留事务编号并核对本次安装结果。'
      }
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
  confirm.value = false; busy.value = true; failed.value = false; guideHref.value = ''; remedy.value = ''
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
    <p v-if="remedy" data-update-remedy>升级路径：{{ remedy }}</p>
    <p v-if="guideHref"><a data-update-guide :href="guideHref" target="_blank" rel="noopener noreferrer">查看具体原因与升级路径</a></p>
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
