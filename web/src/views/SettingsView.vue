<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { APIError, apiFetch, type MixedSourcePolicyPayload, type SettingsSummaryPayload } from '../api/client'
import AppShell from '../components/AppShell.vue'
import UiNotice from '../components/UiNotice.vue'
import type { NoticeKind, UiNoticeData } from '../components/errorMessages'

const summary = ref<SettingsSummaryPayload | null>(null)
const policy = ref<MixedSourcePolicyPayload | null>(null)
const enabled = ref(false)
const cidrs = ref('')
const notice = ref<UiNoticeData | null>(null)
const loading = ref(true)
const saving = ref(false)
let noticeSequence = 0

const accountLabel = computed(() => ({
  synced: '三账户已同步', reset_required: '等待统一重置', checking: '正在核对', repair_required: '需要修复', incompatible: '版本不兼容',
}[summary.value?.accountSyncStatus ?? 'checking']))

const policyApplyLabel = computed(() => ({
  applied: '已生效', applying: '应用中', pending: '待应用', failed: '应用失败', repair_required: '需要修复',
}[policy.value?.applyStatus ?? 'pending'] ?? '待应用'))

onMounted(async () => {
  try {
    const [loadedSummary, loadedPolicy] = await Promise.all([
      apiFetch<SettingsSummaryPayload>('/api/v1/settings/summary'),
      apiFetch<MixedSourcePolicyPayload>('/api/v1/settings/mixed-source-policy'),
    ])
    summary.value = loadedSummary
    policy.value = loadedPolicy
    enabled.value = loadedPolicy.enabled
    cidrs.value = loadedPolicy.cidrs.join('\n')
  } catch (error) {
    notice.value = makeNotice('error', '高级设置读取失败', messageFor(error, '高级设置暂时不可用'))
  } finally {
    loading.value = false
  }
})

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
        <p class="capacity-help">当前 512 MiB 主机保持单在线节点，避免多个 OpenVPN、Xray 入站与探测任务争抢内存。</p>
      </aside>

      <section class="services-section">
        <div class="section-title"><p class="section-kicker">SERVICE MAINTENANCE</p><h2>后台管理</h2><p>常用维护在 Gateway 原生页面完成，需要时再进入原后台。</p></div>
        <div class="service-grid">
          <RouterLink class="service-card" to="/settings/aimilivpn">
            <span class="service-icon">A</span><span><strong>AimiliVPN 设置</strong><small>候选目录、受管槽位与原后台入口</small></span><b aria-hidden="true">→</b>
          </RouterLink>
          <RouterLink class="service-card" to="/settings/3x-ui">
            <span class="service-icon xui">3X</span><span><strong>3x-ui 设置</strong><small>受管资源核对、修复与专家模式</small></span><b aria-hidden="true">→</b>
          </RouterLink>
        </div>
        <p class="boundary-note">统一账户使用同一用户名和密码；原后台仍签发各自会话。服务端自动代登录不是覆盖全部页面的真正 SSO。</p>
      </section>
    </div>
  </AppShell>
</template>

<style scoped>
.settings-header{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:20px}.eyebrow,.section-kicker{margin:0 0 6px;color:var(--accent);font-size:10px;font-weight:850;letter-spacing:.16em}.settings-header h1{margin:0;font-size:30px;letter-spacing:-.03em}.settings-header>div>p:last-child,.section-title>p:last-child{margin:8px 0 0;color:var(--muted-text);font-size:14px}.account-chip{padding:7px 10px;border:1px solid var(--border);border-radius:999px;background:var(--panel);color:var(--muted-text);font-size:12px;font-weight:750}.account-chip.synced{border-color:color-mix(in srgb,var(--healthy) 28%,var(--border));color:var(--healthy);background:color-mix(in srgb,var(--healthy) 8%,var(--panel))}.policy-notice,.loading-panel{margin:0 0 16px}.loading-panel{padding:11px 13px;border:1px solid var(--border);border-radius:10px;background:var(--panel);color:var(--muted-text);font-size:13px}.settings-layout{display:grid;grid-template-columns:minmax(0,1.65fr) minmax(260px,.75fr);gap:16px}.panel,.services-section{border:1px solid var(--border);border-radius:14px;background:var(--panel);box-shadow:var(--shadow-soft)}.policy-panel{padding:20px}.panel-heading{display:flex;justify-content:space-between;gap:20px}.panel h2,.section-title h2{margin:0;font-size:18px}.panel-heading p:last-child{margin:6px 0 0;color:var(--muted-text);font-size:13px}.switch{display:flex;align-items:center;gap:8px;align-self:flex-start;cursor:pointer}.switch input{position:absolute;opacity:0;pointer-events:none}.switch span{position:relative;width:38px;height:22px;border-radius:999px;background:var(--muted-bg);box-shadow:inset 0 0 0 1px var(--border);transition:.2s}.switch span::after{content:"";position:absolute;top:3px;left:3px;width:16px;height:16px;border-radius:50%;background:var(--panel);box-shadow:0 1px 3px rgba(0,0,0,.18);transition:.2s}.switch input:checked+span{background:var(--accent);box-shadow:none}.switch input:checked+span::after{transform:translateX(16px);background:#fff}.switch b{min-width:42px;font-size:12px}.risk-note{display:grid;gap:4px;margin-top:18px;padding:12px;border:1px solid color-mix(in srgb,var(--warning) 35%,var(--border));border-radius:10px;background:color-mix(in srgb,var(--warning) 8%,var(--panel));font-size:12px}.risk-note strong{color:var(--warning)}.risk-note span{color:var(--muted-text);line-height:1.55}.policy-form{display:grid;gap:14px;margin-top:18px}.field{display:grid;gap:7px;font-size:12px;font-weight:750}.field textarea{width:100%;resize:vertical;padding:11px 12px;border:1px solid var(--border);border-radius:9px;background:var(--input);color:var(--text);font:13px/1.55 ui-monospace,SFMono-Regular,Consolas,monospace}.field small{color:var(--muted-text);font-weight:500}.form-footer{display:flex;align-items:center;justify-content:space-between;gap:12px}.apply-state{color:var(--muted-text);font-size:12px}.apply-state[data-apply-status=applied]{color:var(--healthy);font-weight:750}.apply-state[data-apply-status=failed],.apply-state[data-apply-status=repair_required]{color:var(--danger);font-weight:750}.policy-actions{display:flex;gap:8px}.capacity-panel{padding:20px}.capacity-value{display:grid;gap:2px;margin:20px 0}.capacity-value strong{font-size:32px;letter-spacing:-.05em}.capacity-value span{color:var(--muted-text);font-size:12px}.capacity-panel dl{display:grid;gap:8px;margin:0}.capacity-panel dl div{display:flex;justify-content:space-between;padding:9px 0;border-top:1px solid var(--border-soft);font-size:13px}.capacity-panel dt{color:var(--muted-text)}.capacity-panel dd{margin:0;font-weight:800}.capacity-help{margin:14px 0 0;color:var(--muted-text);font-size:12px;line-height:1.6}.services-section{grid-column:1/-1;padding:20px}.section-title{margin-bottom:14px}.service-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}.service-card{display:grid;grid-template-columns:auto 1fr auto;align-items:center;gap:12px;padding:14px;border:1px solid var(--border);border-radius:11px;color:var(--text);text-decoration:none;transition:.15s}.service-card:hover{border-color:color-mix(in srgb,var(--accent) 35%,var(--border));background:var(--hover);transform:translateY(-1px)}.service-icon{display:grid;place-items:center;width:36px;height:36px;border-radius:10px;background:var(--accent-soft);color:var(--accent);font-weight:850}.service-icon.xui{font-size:11px}.service-card span:nth-child(2){display:grid;gap:4px}.service-card strong{font-size:14px}.service-card small{color:var(--muted-text);font-size:12px}.service-card>b{color:var(--muted-text)}.boundary-note{margin:14px 0 0;color:var(--muted-text);font-size:11px;line-height:1.55}@media(max-width:820px){.settings-layout{grid-template-columns:1fr}.services-section{grid-column:auto}.service-grid{grid-template-columns:1fr}}@media(max-width:560px){.settings-header,.panel-heading{align-items:flex-start;flex-direction:column}.account-chip{align-self:flex-start}.service-card{padding:12px}.form-footer{align-items:stretch;flex-direction:column}.policy-actions{flex-direction:column}.form-footer button{width:100%}}
</style>
