<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { apiFetch, openBackend, type AimiliSettingsPayload, type CountryRefreshPayload } from '../api/client'
import AppShell from '../components/AppShell.vue'
import UiNotice from '../components/UiNotice.vue'
import type { NoticeKind, UiNoticeData } from '../components/errorMessages'

const data = ref<AimiliSettingsPayload | null>(null)
const refresh = ref<CountryRefreshPayload | null>(null)
const busy = ref('')
const notice = ref<UiNoticeData | null>(null)
let noticeSequence = 0

function makeNotice(kind: NoticeKind, title: string, message = ''): UiNoticeData {
  noticeSequence += 1
  return { id: `aimili-notice-${noticeSequence}`, kind, title, message }
}

onMounted(load)

async function load(): Promise<void> {
  try {
    const [summary, refreshStatus] = await Promise.all([
      apiFetch<AimiliSettingsPayload>('/api/v1/settings/aimilivpn'),
      apiFetch<CountryRefreshPayload>('/api/v1/settings/aimilivpn/refresh'),
    ])
    data.value = summary
    refresh.value = refreshStatus
  }
  catch { notice.value = makeNotice('error', 'AimiliVPN 信息读取失败', 'AimiliVPN 维护信息暂时不可用。') }
}

async function run(path: string, action: string): Promise<void> {
  busy.value = action
  notice.value = makeNotice('progress', '正在同步 AimiliVPN 代理状态', '正在核对主连接和受管槽位。')
  try {
    data.value = await apiFetch<AimiliSettingsPayload>(path, { method: 'POST' })
    notice.value = makeNotice('success', 'AimiliVPN 代理状态已同步', '已重新读取实际运行状态。')
  }
  catch { notice.value = makeNotice('error', 'AimiliVPN 同步失败', '操作失败，请稍后重试。') }
  finally { busy.value = '' }
}

async function openOriginal(): Promise<void> {
  busy.value = 'open'
  notice.value = makeNotice('progress', '正在进入 AimiliVPN 原后台', '正在创建短期后台会话。')
  try {
    const destination = await openBackend('/api/v1/backends/aimilivpn/login')
    if (destination) window.location.assign(destination)
  } catch { notice.value = makeNotice('error', 'AimiliVPN 原后台打开失败', '请改用原后台登录页手动登录。') }
  finally { busy.value = '' }
}

function formatTime(value?: string): string { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚无记录' }
function refreshTitle(): string {
  if (!refresh.value || refresh.value.state === 'idle') return '尚未执行国家刷新'
  if (refresh.value.state === 'running') return `${refresh.value.country} 正在刷新`
  if (refresh.value.state === 'completed') return `${refresh.value.country} 刷新已完成`
  return `${refresh.value.country || '节点'} 刷新失败`
}
</script>

<template>
  <AppShell>
    <div class="subpage">
      <RouterLink class="back-link" to="/settings">← 返回高级设置</RouterLink>
      <header><div><p>AIMILIVPN</p><h1>AimiliVPN 设置</h1><span>候选目录与 Gateway 受管出口槽位。</span></div><button data-open-aimili class="secondary" :disabled="!!busy" @click="openOriginal">进入 AimiliVPN 原后台</button></header>
      <UiNotice v-if="notice" :key="notice.id" data-aimili-notice class="page-notice" :notice="notice" @close="notice=null" />
      <section class="metric-grid">
        <article><span>有效候选</span><strong>{{ data?.candidateCount ?? '—' }}</strong><small>当前可用出口</small></article>
        <article><span>住宅 IP</span><strong>{{ data?.residentialCount ?? '—' }}</strong><small>Residential</small></article>
        <article><span>机房 IP</span><strong>{{ data?.datacenterCount ?? '—' }}</strong><small>Datacenter</small></article>
        <article><span>受管槽位</span><strong>{{ data?.managedSlotCount ?? '—' }}</strong><small>Gateway 在线出口</small></article>
      </section>
      <section class="action-panel"><div><h2>目录与槽位维护</h2><p>{{ refreshTitle() }}<template v-if="refresh?.state === 'completed'"> · 精验 {{ refresh.testedCount }} 个，保留 {{ refresh.validCount }} 个</template></p><small>代理目录最近探测：{{ formatTime(data?.lastRefreshedAt) }}</small></div><div class="actions"><button data-check-aimili :disabled="!!busy" @click="run('/api/v1/settings/aimilivpn/check','check')">{{ busy === 'check' ? '正在同步' : '同步代理状态' }}</button></div></section>
      <p class="boundary">原后台保留自身完整功能与独立会话。自动进入由 Gateway 服务端创建短期会话，浏览器不处理管理凭据，也不是真正 SSO。</p>
    </div>
  </AppShell>
</template>

<style scoped>
.subpage{max-width:1000px;margin:0 auto}.back-link{display:inline-block;margin-bottom:16px;color:var(--muted-text);font-size:12px;font-weight:700;text-decoration:none}.subpage header{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:20px}.subpage header p{margin:0 0 6px;color:var(--accent);font-size:10px;font-weight:850;letter-spacing:.16em}.subpage h1{margin:0;font-size:29px;letter-spacing:-.03em}.subpage header span{display:block;margin-top:7px;color:var(--muted-text);font-size:13px}.page-notice{margin-bottom:14px}.metric-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:12px}.metric-grid article,.action-panel{border:1px solid var(--border);border-radius:13px;background:var(--panel);box-shadow:var(--shadow-soft)}.metric-grid article{display:grid;gap:7px;padding:17px}.metric-grid span,.metric-grid small{color:var(--muted-text);font-size:11px}.metric-grid strong{font-size:27px}.action-panel{display:flex;align-items:center;justify-content:space-between;gap:24px;margin-top:14px;padding:20px}.action-panel h2{margin:0;font-size:17px}.action-panel p{margin:6px 0;color:var(--muted-text);font-size:13px}.action-panel small{color:var(--muted-text);font-size:11px}.actions{display:flex;gap:9px;white-space:nowrap}.boundary{margin:14px 2px;color:var(--muted-text);font-size:11px;line-height:1.6}@media(max-width:760px){.metric-grid{grid-template-columns:1fr 1fr}.action-panel,.subpage header{align-items:stretch;flex-direction:column}.actions{display:grid;grid-template-columns:1fr 1fr}.subpage header button{align-self:flex-start}}@media(max-width:460px){.metric-grid,.actions{grid-template-columns:1fr}}
</style>
