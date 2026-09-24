<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { APIError, apiFetch, type MixedSourcePolicyPayload, type SettingsSummaryPayload, type UpdateKind, type UpdateResultPayload, type UpdateSummaryPayload, type UpdateVersionPayload } from '../api/client'
import AppShell from '../components/AppShell.vue'
import ProjectUpdatePanel from '../components/ProjectUpdatePanel.vue'
import UiNotice from '../components/UiNotice.vue'
import type { NoticeKind, UiNoticeData } from '../components/errorMessages'

const summary = ref<SettingsSummaryPayload | null>(null)
const policy = ref<MixedSourcePolicyPayload | null>(null)
const enabled = ref(false)
const cidrs = ref('')
const notice = ref<UiNoticeData | null>(null)
const loading = ref(true)
const saving = ref(false)
const updates = ref<UpdateSummaryPayload | null>(null)
const updateNotice = ref<UiNoticeData | null>(null)
const gatewayCandidate = ref<UpdateVersionPayload | null>(null)
const checkingUpdates = ref(false)
const releaseNotes = ref('')
type UpdateAction = 'apply' | 'rollback'
type UpdateRequest = { action: 'apply'; candidate: UpdateVersionPayload } | { action: 'rollback'; kind: UpdateKind }
const activeUpdate = ref<{ runId: string; kind: UpdateKind; action: UpdateAction } | null>(null)
const updating = ref(false)
let noticeSequence = 0

const accountLabel = computed(() => ({
  synced: '三账户已同步', reset_required: '等待统一重置', checking: '正在核对', repair_required: '需要修复', incompatible: '版本不兼容',
}[summary.value?.accountSyncStatus ?? 'checking']))

const policyApplyLabel = computed(() => ({
  applied: '已生效', applying: '应用中', pending: '待应用', failed: '应用失败', repair_required: '需要修复',
}[policy.value?.applyStatus ?? 'pending'] ?? '待应用'))

onMounted(async () => {
  try {
    const [loadedSummary, loadedPolicy, loadedUpdates] = await Promise.all([
      apiFetch<SettingsSummaryPayload>('/api/v1/settings/summary'),
      apiFetch<MixedSourcePolicyPayload>('/api/v1/settings/mixed-source-policy'),
      apiFetch<UpdateSummaryPayload>('/api/v1/system/updates').catch(() => null),
    ])
    summary.value = loadedSummary
    policy.value = loadedPolicy
    enabled.value = loadedPolicy.enabled
    cidrs.value = loadedPolicy.cidrs.join('\n')
    updates.value = loadedUpdates
    gatewayCandidate.value = loadedUpdates?.available.find(item => item.kind === 'gateway' && item.compatible) ?? null
  } catch (error) {
    notice.value = makeNotice('error', '高级设置读取失败', messageFor(error, '高级设置暂时不可用'))
  } finally {
    loading.value = false
  }
})

const updatesEnabled = computed(() => updates.value?.enabled === true)
const availableGateway = computed(() => updatesEnabled.value ? gatewayCandidate.value : null)

async function checkGatewayUpdate(): Promise<void> {
  if (!updatesEnabled.value || checkingUpdates.value || updating.value || activeUpdate.value) return
  checkingUpdates.value = true
  gatewayCandidate.value = null
  releaseNotes.value = ''
  updateNotice.value = makeUpdateNotice('progress', '正在检测更新', '正在查询 Aimili Gateway 的 GitHub 正式发布版本。')
  try {
    const response = await fetch('https://api.github.com/repos/thzyh/aimili-gateway/releases?per_page=30', {
      headers: { Accept: 'application/vnd.github+json' },
    })
    if (!response.ok) throw new Error(response.status === 404 ? 'release_missing' : 'github_unavailable')
    const payload: unknown = await response.json()
    if (!Array.isArray(payload)) throw new Error('github_unavailable')
    const fullRelease = payload.filter(isVPSRelease).sort((left, right) => compareGatewayVersions(right.tag_name.slice(0, -4), left.tag_name.slice(0, -4)))[0]
    const fullReleaseHint = fullRelease ? `GitHub 最新完整部署版为 ${fullRelease.tag_name}，涉及 AimiliVPN、3x-ui/Xray、Caddy 等组件。升级请查看项目 README 的 VPS 部署章节；旧布局 VPS 须先备份并核对数据迁移。` : ''
    const release = payload.filter(isGatewayRelease).sort((left, right) => compareGatewayVersions(right.tag_name, left.tag_name))[0]
    if (!release) {
      updateNotice.value = makeUpdateNotice('success', '检测完成', fullReleaseHint || 'GitHub 暂无可用于网页普通更新的 Gateway 发布。')
      return
    }
    const current = updates.value?.currentGateway ?? ''
    if (!/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(current)) {
      updateNotice.value = makeUpdateNotice('success', '检测完成', `当前 Gateway 版本 ${current || '未知'} 无法与发布版本安全比较；网页普通更新不可用。${fullReleaseHint}`)
      return
    }
    const comparison = compareGatewayVersions(release.tag_name, current)
    if (comparison <= 0) {
      updateNotice.value = makeUpdateNotice('success', '普通更新已是最新', `当前 Gateway ${current}，暂无更新的 Gateway 单组件发布。${fullReleaseHint}`)
      return
    }
    gatewayCandidate.value = { kind: 'gateway', version: release.tag_name, compatible: true }
    releaseNotes.value = typeof release.body === 'string' ? release.body.trim().slice(0, 500) : ''
    updateNotice.value = makeUpdateNotice('success', `发现 Gateway 普通更新 ${release.tag_name}`, `所需发布资产齐全；安装器会再次校验签名和兼容性。${fullReleaseHint}`)
  } catch {
    const message = '暂时无法查询 GitHub 发布版本，请稍后重试。'
    updateNotice.value = makeUpdateNotice('error', '检测更新失败', message)
  } finally {
    checkingUpdates.value = false
  }
}

function isGatewayRelease(value: unknown): value is { tag_name: string; body?: string; draft: boolean; prerelease: boolean; assets: Array<{ name: string }> } {
  if (typeof value !== 'object' || value === null) return false
  const release = value as Record<string, unknown>
  if (release.draft !== false || release.prerelease !== false || typeof release.tag_name !== 'string' || !Array.isArray(release.assets)) return false
  if (!/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(release.tag_name)) return false
  const names = new Set(release.assets.flatMap(asset => typeof asset === 'object' && asset !== null && typeof (asset as Record<string, unknown>).name === 'string' ? [(asset as Record<string, string>).name] : []))
  return ['manifest.json', 'manifest.sig', 'aimili-gateway'].every(name => names.has(name))
}

function isVPSRelease(value: unknown): value is { tag_name: string; draft: boolean; prerelease: boolean; assets: Array<{ name: string }> } {
  if (typeof value !== 'object' || value === null) return false
  const release = value as Record<string, unknown>
  if (release.draft !== false || release.prerelease !== false || typeof release.tag_name !== 'string' || !Array.isArray(release.assets)) return false
  if (!/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-vps$/.test(release.tag_name)) return false
  const names = new Set(release.assets.flatMap(asset => typeof asset === 'object' && asset !== null && typeof (asset as Record<string, unknown>).name === 'string' ? [(asset as Record<string, string>).name] : []))
  return ['manifest.json', 'manifest.sig', 'aimili-vps-package.tar.gz'].every(name => names.has(name))
}

function compareGatewayVersions(left: string, right: string): number {
  const pattern = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/
  const l = pattern.exec(left)
  const r = pattern.exec(right)
  if (!l || !r) throw new Error('version_invalid')
  for (let index = 1; index <= 3; index += 1) {
    const difference = Number(l[index]) - Number(r[index])
    if (difference !== 0) return difference
  }
  return 0
}

async function requestUpdate(candidate: UpdateVersionPayload | null): Promise<void> {
  if (!candidate || updating.value || activeUpdate.value) return
  await submitUpdate({ action: 'apply', candidate })
}

async function requestRollback(kind: UpdateKind): Promise<void> {
  if (!updatesEnabled.value || updating.value || activeUpdate.value) return
  await submitUpdate({ action: 'rollback', kind })
}

async function submitUpdate(request: UpdateRequest): Promise<void> {
  updating.value = true
  const runId = newRunID()
  const kind = request.action === 'apply' ? request.candidate.kind : request.kind
  const subject = updateSubject(kind)
  const actionLabel = request.action === 'apply' ? '更新' : '回滚'
  const endpoint = request.action === 'apply'
    ? `/api/v1/system/updates/${kind}/${request.candidate.version}/apply`
    : `/api/v1/system/updates/${kind}/rollback`
  activeUpdate.value = { runId, kind, action: request.action }
  updateNotice.value = makeUpdateNotice('progress', `${subject}正在${actionLabel}`, kind === 'ui' ? '正在校验并切换界面资源，节点不会中断。' : '控制面会短暂重启，代理节点继续运行。')
  try {
    await apiFetch<UpdateResultPayload>(endpoint, {
      method: 'POST', body: JSON.stringify({ runId }),
    })
    await pollUpdate(runId, kind, request.action)
  } catch (error) {
    if (isDefiniteUpdateRejection(error)) {
      activeUpdate.value = null
      updateNotice.value = makeUpdateNotice('error', `${subject}${actionLabel}未开始`, messageForUpdate(error, '请求未被受理，现有版本保持不变。'))
    } else {
      await pollUpdate(runId, kind, request.action)
    }
  } finally {
    updating.value = false
  }
}

async function resumeUpdate(): Promise<void> {
  const update = activeUpdate.value
  if (!update || updating.value) return
  updating.value = true
  try {
    await pollUpdate(update.runId, update.kind, update.action)
  } finally {
    updating.value = false
  }
}

async function pollUpdate(runId: string, kind: UpdateKind, action: UpdateAction): Promise<void> {
  const delays = [0, 1000, 2000, 4000, 8000, 10000]
  let lastError: unknown
  for (const delay of delays) {
    if (delay > 0) await wait(delay)
    try {
      const result = await apiFetch<UpdateResultPayload>(`/api/v1/system/updates/${runId}`)
      if (!terminalUpdateState(result.state)) {
        updateNotice.value = makeUpdateNotice('progress', `${updateSubject(kind)}进行中`, updateProgressMessage(result.state, kind))
        continue
      }
      updateNotice.value = noticeForUpdate(result, action)
      activeUpdate.value = null
      try {
        updates.value = await apiFetch<UpdateSummaryPayload>('/api/v1/system/updates')
        if (result.kind === 'gateway' && result.state === 'success') gatewayCandidate.value = null
      } catch {
        // 已确认的终态是权威结果；列表刷新失败只保留该结果。
      }
      return
    } catch (error) {
      lastError = error
    }
  }
  updateNotice.value = makeUpdateNotice('error', `${updateSubject(kind)}状态未确认`, messageForUpdate(lastError, '连接暂未恢复，请稍后刷新；系统不会自动创建第二次更新。'))
}

function noticeForUpdate(result: UpdateResultPayload, action: UpdateAction = 'apply'): UiNoticeData {
  const subject = updateSubject(result.kind)
  if (result.state === 'success') return makeUpdateNotice('success', `${subject}成功`, result.kind === 'ui' ? '新界面已生效，Gateway 和代理节点均未重启。' : 'Gateway 控制面已恢复，代理节点继续运行。')
  if (result.state === 'rolled_back') return action === 'rollback'
    ? makeUpdateNotice('success', result.kind === 'ui' ? '界面已回滚' : 'Gateway 控制面已回滚', '已恢复上一版。')
    : makeUpdateNotice('error', `${subject}已自动回滚`, '新版本未通过健康检查，已恢复上一版。')
  if (result.state === 'repair_required') return makeUpdateNotice('error', `${subject}需要受限修复`, '自动回滚未能完整确认，请使用服务器上的固定回滚入口。')
  return makeUpdateNotice('error', `${subject}失败`, messageForUpdateCode(result.errorCode))
}

function makeUpdateNotice(kind: NoticeKind, title: string, message: string): UiNoticeData {
  noticeSequence += 1
  return { id: `update-notice-${noticeSequence}`, kind, title, message }
}

function updateSubject(kind: UpdateKind): string { return kind === 'ui' ? '界面更新' : 'Aimili Gateway 更新' }
function terminalUpdateState(state: UpdateResultPayload['state']): boolean { return ['success', 'failed', 'rolled_back', 'repair_required'].includes(state) }
function updateProgressMessage(state: UpdateResultPayload['state'], kind: UpdateKind): string {
  if (state === 'downloading') return '正在从固定可信来源下载签名文件。'
  if (state === 'validating') return '正在复验签名、摘要和兼容性。'
  if (state === 'switching') return kind === 'ui' ? '正在原子切换界面版本。' : '正在替换并重启 Gateway 控制面。'
  if (state === 'verifying') return '正在执行健康检查与数据面不变门。'
  return '更新请求已提交，正在等待处理。'
}
function messageForUpdateCode(code?: string): string {
  const messages: Record<string, string> = {
    download_failed: '下载失败，现有版本未改变。', invalid_signature: '签名验证失败，现有版本未改变。',
    invalid_payload: '文件摘要不一致，现有版本未改变。', disk_full: 'VPS 可用空间不足，更新未开始。',
    install_disabled: '后端替换尚未开放，本次只允许安全检查。', operation_busy: '当前有其他维护事务，请稍后重试。', updates_disabled: '安全更新尚未开放，请等待可信发布来源配置完成。',
  }
  return messages[code ?? ''] ?? '更新未完成，现有版本或自动回滚结果已由服务端保留。'
}
function messageForUpdate(error: unknown, fallback: string): string {
  if (error instanceof APIError) return messageForUpdateCode(error.message)
  return fallback
}
function isDefiniteUpdateRejection(error: unknown): error is APIError {
  return error instanceof APIError && ((error.status >= 400 && error.status < 500) || error.message === 'updates_disabled')
}
function wait(milliseconds: number): Promise<void> { return new Promise(resolve => setTimeout(resolve, milliseconds)) }
function newRunID(): string {
  const bytes = new Uint8Array(32)
  globalThis.crypto.getRandomValues(bytes)
  return Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
}

async function savePolicy(): Promise<void> {
  const values = cidrs.value.split(/[\s,]+/).map(value => value.trim()).filter(Boolean)
  if (enabled.value && values.length === 0) {
    notice.value = makeNotice('error', '来源策略未保存', '启用来源限制时至少填写一个具体 CIDR。')
    return
  }
  saving.value = true
  notice.value = makeNotice('progress', '正在应用来源策略', '正在应用到主连接和所有已启用 mixed 入站。')
  try {
    await apiFetch<MixedSourcePolicyPayload>('/api/v1/settings/mixed-source-policy', {
      method: 'PUT',
      body: JSON.stringify({ enabled: enabled.value, cidrs: values }),
    })
    const saved = await reloadPolicy()
    notice.value = noticeForPolicy(saved, '来源策略')
  } catch (error) {
    const recovered = await reloadPolicySafely()
    notice.value = makeNotice('error', '来源策略应用失败', `${messageFor(error, '来源策略应用失败')} ${recoveryMessage(recovered)}`)
  } finally {
    saving.value = false
  }
}

async function authorizeCurrentNetwork(): Promise<void> {
  saving.value = true
  notice.value = makeNotice('progress', '正在识别当前网络', '识别成功后将仅授权当前公网来源。')
  try {
    await apiFetch<MixedSourcePolicyPayload>('/api/v1/settings/mixed-source-policy/authorize-current', { method: 'POST' })
    const saved = await reloadPolicy()
    notice.value = saved.applyStatus === 'applied'
      ? makeNotice('success', '当前网络授权已生效', '当前公网来源已按单个地址授权，SOCKS5H 未对其他公网来源开放。')
      : noticeForPolicy(saved, '当前网络授权')
  } catch (error) {
    const recovered = await reloadPolicySafely()
    notice.value = makeNotice('error', '当前网络授权失败', `${messageFor(error, '无法识别当前网络来源')} ${recoveryMessage(recovered)}`)
  } finally {
    saving.value = false
  }
}

function makeNotice(kind: NoticeKind, title: string, message: string): UiNoticeData {
  noticeSequence += 1
  return { id: `policy-notice-${noticeSequence}`, kind, title, message }
}

function applyPolicy(saved: MixedSourcePolicyPayload): void {
  policy.value = saved
  enabled.value = saved.enabled
  cidrs.value = saved.cidrs.join('\n')
}

async function reloadPolicy(): Promise<MixedSourcePolicyPayload> {
  const saved = await apiFetch<MixedSourcePolicyPayload>('/api/v1/settings/mixed-source-policy')
  applyPolicy(saved)
  return saved
}

async function reloadPolicySafely(): Promise<MixedSourcePolicyPayload | null> {
  try { return await reloadPolicy() } catch { return null }
}

function recoveryMessage(saved: MixedSourcePolicyPayload | null): string {
  if (!saved) return '暂时无法重新读取实际策略，请重新检测后再试。'
  if (saved.applyStatus === 'applied') return '已重新读取，原策略仍然生效。'
  if (saved.applyStatus === 'failed') return '原策略已恢复并保留，可以重新尝试。'
  if (saved.applyStatus === 'applying' || saved.applyStatus === 'pending') return '已重新读取实际状态，策略仍在处理中。'
  return '回滚结果尚未完整确认，请前往 3x-ui 设置执行受管资源修复。'
}

function noticeForPolicy(saved: MixedSourcePolicyPayload, subject: string): UiNoticeData {
  if (saved.applyStatus === 'applied') return makeNotice('success', `${subject}已生效`, '已重新读取并确认实际策略。')
  if (saved.applyStatus === 'applying' || saved.applyStatus === 'pending') return makeNotice('progress', `${subject}正在应用`, '已提交策略，正在等待全部 mixed 入站确认。')
  if (saved.applyStatus === 'failed') return makeNotice('error', `${subject}应用失败`, '原策略已恢复并保留，可以重新尝试。')
  return makeNotice('error', `${subject}需要修复`, '回滚结果尚未完整确认，请前往 3x-ui 设置执行受管资源修复。')
}

function messageFor(error: unknown, fallback: string): string {
  if (error instanceof APIError) {
    const details: Record<string, string> = {
      repair_required: '部分节点需要修复',
      client_peer_invalid: 'Gateway 无法读取本机反向代理连接来源',
      client_peer_untrusted: '请求没有经过受信任的本机反向代理',
      client_forwarded_for_missing: '反向代理没有传递访问者地址',
      client_forwarded_for_invalid: '反向代理传递的访问者地址格式无效',
      client_forwarded_for_non_public: '反向代理只传递了私网或回环地址，不能加入公网白名单',
    }
    if (details[error.message]) return `${fallback}：${details[error.message]}。`
  }
  return `${fallback}。`
}
</script>

<template>
  <AppShell>
    <header class="settings-header">
      <div><p class="eyebrow">ADVANCED SETTINGS</p><h1>高级设置</h1><p>集中处理日常维护；底层服务继续独立运行。</p></div>
      <span class="account-chip" :class="summary?.accountSyncStatus">{{ accountLabel }}</span>
    </header>

    <UiNotice v-if="notice" :key="notice.id" data-policy-notice class="policy-notice" :notice="notice" @close="notice=null" />
    <div v-if="loading" class="loading-panel">正在读取设置…</div>
    <div v-else class="settings-layout">
      <section class="panel policy-panel">
        <div class="panel-heading">
          <div><p class="section-kicker">ACCESS POLICY</p><h2>SOCKS5H 来源限制</h2><p>控制哪些公网来源可以尝试使用反向代理池。</p></div>
          <label class="switch"><input v-model="enabled" data-source-toggle type="checkbox"><span aria-hidden="true" /><b>{{ enabled ? '已启用' : '未启用' }}</b></label>
        </div>

        <div v-if="!enabled" class="risk-note"><strong>开放访问风险</strong><span>SOCKS5H 仍要求用户名和密码，但协议本身不加密，任何公网来源都可以尝试认证。</span></div>
        <form data-policy-form class="policy-form" @submit.prevent="savePolicy">
          <label v-if="enabled" class="field">允许的 CIDR
            <textarea v-model="cidrs" data-cidr-editor rows="5" placeholder="每行一个具体网络前缀，例如 198.51.100.24/32" />
            <small>支持规范 IPv4/IPv6 前缀；不接受 0.0.0.0/0 或 ::/0。</small>
          </label>
          <div class="form-footer"><span class="apply-state" :data-apply-status="policy?.applyStatus">当前状态：{{ policyApplyLabel }}</span><div class="policy-actions"><button data-authorize-current-network class="secondary" :disabled="saving" type="button" @click="authorizeCurrentNetwork">仅授权当前网络</button><button :disabled="saving" type="submit">{{ saving ? '正在应用' : '保存策略' }}</button></div></div>
        </form>
      </section>

      <aside class="panel capacity-panel">
        <p class="section-kicker">CAPACITY</p><h2>运行容量</h2>
        <div class="capacity-value"><strong>{{ summary?.onlineCount ?? 0 }} / {{ summary?.maxOnline ?? 1 }}</strong><span>在线节点</span></div>
        <dl><div><dt>可选节点</dt><dd>{{ summary?.candidateCount ?? 0 }}</dd></div><div><dt>生产上限</dt><dd>{{ summary?.maxOnline ?? 1 }}</dd></div></dl>
        <p class="capacity-help">当前按生产上限运行；增加出口前应逐个验证内存、连接和探测负载，避免多个 OpenVPN、Xray 入站与检测任务相互争抢资源。</p>
      </aside>

      <section class="services-section">
        <div class="section-title"><p class="section-kicker">SERVICE MAINTENANCE</p><h2>3x-ui 专家模式</h2><p>日常出口管理在 Gateway 页面完成；需要检查底层入站时再进入 3x-ui。</p></div>
        <div class="service-grid">
          <RouterLink class="service-card" to="/settings/3x-ui">
            <span class="service-icon xui">3X</span><span><strong>3x-ui 设置</strong><small>受管资源核对、修复与专家模式</small></span><b aria-hidden="true">→</b>
          </RouterLink>
        </div>
        <p class="boundary-note">出口引擎继续作为独立服务运行，但不再向 Gateway 用户展示 AimiliVPN 原后台。</p>
      </section>

      <ProjectUpdatePanel v-if="updates?.project" class="services-section" :initial="updates" />
      <section v-else class="services-section update-section">
        <div class="section-title"><p class="section-kicker">GATEWAY UPDATE</p><h2>检测更新</h2><p>从公开 GitHub 正式版本检测更新；安装前仍会校验签名、文件摘要和兼容性。</p></div>
        <UiNotice v-if="updateNotice" :key="updateNotice.id" data-update-notice class="update-notice" :notice="updateNotice" @close="updateNotice=null" />
        <div class="update-grid single">
          <article class="update-card"><strong>Aimili Gateway</strong><span>当前：{{ updates?.currentGateway ?? '未知' }}</span><p>普通更新同时更新 Gateway 程序和内嵌前端，只短暂重启 Gateway，代理节点继续运行。涉及 AimiliVPN、3x-ui/Xray 或 Caddy 的版本会拒绝普通更新并要求完整部署。</p><p v-if="releaseNotes" class="release-notes">更新说明：{{ releaseNotes }}</p><div class="update-actions"><button data-check-update :disabled="!updatesEnabled || checkingUpdates || updating || !!activeUpdate" type="button" @click="checkGatewayUpdate">{{ checkingUpdates ? '正在检测' : '检测更新' }}</button><button v-if="availableGateway" data-gateway-update :disabled="updating || !!activeUpdate" type="button" @click="requestUpdate(availableGateway)">更新到 {{ availableGateway.version }}</button><button data-gateway-rollback class="secondary" :disabled="!updatesEnabled || updating || !!activeUpdate" type="button" @click="requestRollback('gateway')">回滚上一版</button></div></article>
        </div>
        <div v-if="activeUpdate" class="update-recovery"><p>本次请求的响应尚未确认；将继续查询原事务，不会创建新的更新或回滚请求。</p><button data-update-resume type="button" :disabled="updating" @click="resumeUpdate">继续查询</button></div>
      </section>
    </div>
  </AppShell>
</template>

<style scoped>
.settings-header{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:20px}.eyebrow,.section-kicker{margin:0 0 6px;color:var(--accent);font-size:10px;font-weight:850;letter-spacing:.16em}.settings-header h1{margin:0;font-size:30px;letter-spacing:-.03em}.settings-header>div>p:last-child,.section-title>p:last-child{margin:8px 0 0;color:var(--muted-text);font-size:14px}.account-chip{padding:7px 10px;border:1px solid var(--border);border-radius:999px;background:var(--panel);color:var(--muted-text);font-size:12px;font-weight:750}.account-chip.synced{border-color:color-mix(in srgb,var(--healthy) 28%,var(--border));color:var(--healthy);background:color-mix(in srgb,var(--healthy) 8%,var(--panel))}.policy-notice,.loading-panel{margin:0 0 16px}.loading-panel{padding:11px 13px;border:1px solid var(--border);border-radius:10px;background:var(--panel);color:var(--muted-text);font-size:13px}.settings-layout{display:grid;grid-template-columns:minmax(0,1.65fr) minmax(260px,.75fr);gap:16px}.panel,.services-section{border:1px solid var(--border);border-radius:14px;background:var(--panel);box-shadow:var(--shadow-soft)}.policy-panel{padding:20px}.panel-heading{display:flex;justify-content:space-between;gap:20px}.panel h2,.section-title h2{margin:0;font-size:18px}.panel-heading p:last-child{margin:6px 0 0;color:var(--muted-text);font-size:13px}.switch{display:flex;align-items:center;gap:8px;align-self:flex-start;cursor:pointer}.switch input{position:absolute;opacity:0;pointer-events:none}.switch span{position:relative;width:38px;height:22px;border-radius:999px;background:var(--muted-bg);box-shadow:inset 0 0 0 1px var(--border);transition:.2s}.switch span::after{content:"";position:absolute;top:3px;left:3px;width:16px;height:16px;border-radius:50%;background:var(--panel);box-shadow:0 1px 3px rgba(0,0,0,.18);transition:.2s}.switch input:checked+span{background:var(--accent);box-shadow:none}.switch input:checked+span::after{transform:translateX(16px);background:#fff}.switch b{min-width:42px;font-size:12px}.risk-note{display:grid;gap:4px;margin-top:18px;padding:12px;border:1px solid color-mix(in srgb,var(--warning) 35%,var(--border));border-radius:10px;background:color-mix(in srgb,var(--warning) 8%,var(--panel));font-size:12px}.risk-note strong{color:var(--warning)}.risk-note span{color:var(--muted-text);line-height:1.55}.policy-form{display:grid;gap:14px;margin-top:18px}.field{display:grid;gap:7px;font-size:12px;font-weight:750}.field textarea{width:100%;resize:vertical;padding:11px 12px;border:1px solid var(--border);border-radius:9px;background:var(--input);color:var(--text);font:13px/1.55 ui-monospace,SFMono-Regular,Consolas,monospace}.field small{color:var(--muted-text);font-weight:500}.form-footer{display:flex;align-items:center;justify-content:space-between;gap:12px}.apply-state{color:var(--muted-text);font-size:12px}.apply-state[data-apply-status=applied]{color:var(--healthy);font-weight:750}.apply-state[data-apply-status=failed],.apply-state[data-apply-status=repair_required]{color:var(--danger);font-weight:750}.policy-actions{display:flex;gap:8px}.capacity-panel{padding:20px}.capacity-value{display:grid;gap:2px;margin:20px 0}.capacity-value strong{font-size:32px;letter-spacing:-.05em}.capacity-value span{color:var(--muted-text);font-size:12px}.capacity-panel dl{display:grid;gap:8px;margin:0}.capacity-panel dl div{display:flex;justify-content:space-between;padding:9px 0;border-top:1px solid var(--border-soft);font-size:13px}.capacity-panel dt{color:var(--muted-text)}.capacity-panel dd{margin:0;font-weight:800}.capacity-help{margin:14px 0 0;color:var(--muted-text);font-size:12px;line-height:1.6}.services-section{grid-column:1/-1;padding:20px}.section-title{margin-bottom:14px}.service-grid{display:grid;grid-template-columns:1fr;gap:12px}.service-card{display:grid;grid-template-columns:auto 1fr auto;align-items:center;gap:12px;padding:14px;border:1px solid var(--border);border-radius:11px;color:var(--text);text-decoration:none;transition:.15s}.service-card:hover{border-color:color-mix(in srgb,var(--accent) 35%,var(--border));background:var(--hover);transform:translateY(-1px)}.service-icon{display:grid;place-items:center;width:36px;height:36px;border-radius:10px;background:var(--accent-soft);color:var(--accent);font-weight:850}.service-icon.xui{font-size:11px}.service-card span:nth-child(2){display:grid;gap:4px}.service-card strong{font-size:14px}.service-card small{color:var(--muted-text);font-size:12px}.service-card>b{color:var(--muted-text)}.boundary-note{margin:14px 0 0;color:var(--muted-text);font-size:11px;line-height:1.55}@media(max-width:820px){.settings-layout{grid-template-columns:1fr}.services-section{grid-column:auto}.service-grid{grid-template-columns:1fr}}@media(max-width:560px){.settings-header,.panel-heading{align-items:flex-start;flex-direction:column}.account-chip{align-self:flex-start}.service-card{padding:12px}.form-footer{align-items:stretch;flex-direction:column}.policy-actions{flex-direction:column}.form-footer button{width:100%}}
.update-notice{margin:0 0 14px}.update-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}.update-grid.single{grid-template-columns:minmax(0,1fr)}.update-card{display:grid;gap:8px;padding:14px;border:1px solid var(--border);border-radius:11px}.update-card>span,.update-card>p{color:var(--muted-text);font-size:12px}.update-card>p{margin:0;line-height:1.5}.release-notes{white-space:pre-line}.update-actions{display:flex;align-items:center;flex-wrap:wrap;gap:8px}.update-recovery{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:14px;padding:14px;border:1px solid var(--border);border-radius:11px;background:var(--subtle)}.update-recovery p{margin:0;color:var(--muted-text);font-size:11px}@media(max-width:650px){.update-grid{grid-template-columns:1fr}.update-recovery{align-items:stretch;flex-direction:column}}
</style>
