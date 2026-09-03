<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { apiFetch, openBackend, type XUISettingsPayload } from '../api/client'
import AppShell from '../components/AppShell.vue'

const data = ref<XUISettingsPayload | null>(null)
const busy = ref('')
const notice = ref('')

onMounted(load)

async function load(): Promise<void> {
  try { data.value = await apiFetch<XUISettingsPayload>('/api/v1/settings/3x-ui') }
  catch { notice.value = '3x-ui 维护信息暂时不可用。' }
}

async function run(path: string, action: string): Promise<void> {
  busy.value = action
  notice.value = ''
  try { data.value = await apiFetch<XUISettingsPayload>(path, { method: 'POST' }); notice.value = action === 'repair' ? '受管资源修复完成。' : '受管资源检测完成。' }
  catch { notice.value = action === 'repair' ? '修复失败，代理数据面保持不变，请查看运维记录。' : '检测失败，请稍后重试。' }
  finally { busy.value = '' }
}

async function openOriginal(): Promise<void> {
  busy.value = 'open'
  notice.value = ''
  try {
    const destination = await openBackend('/api/v1/backends/3x-ui/login')
    if (destination) window.location.assign(destination)
  } catch { notice.value = '自动进入专家模式失败，请使用 3x-ui 登录页手动登录。' }
  finally { busy.value = '' }
}

function formatTime(value?: string): string { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚无记录' }
</script>

<template>
  <AppShell>
    <div class="subpage">
      <RouterLink class="back-link" to="/settings">← 返回高级设置</RouterLink>
      <header><div><p>3X-UI</p><h1>3x-ui 设置</h1><span>只维护 Aimili Gateway 所有权范围内的资源。</span></div><button data-open-xui class="secondary" :disabled="!!busy" @click="openOriginal">进入 3x-ui 专家模式</button></header>
      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <section class="ownership" :class="{ warning: data && !data.ownershipMatches }"><span class="status-dot" /><div><strong>{{ data?.ownershipMatches ? '所有权核对通过' : '发现受管资源漂移' }}</strong><p>{{ data?.ownershipMatches ? 'Gateway 受管入站与出站均可识别。' : '只会修复 agw- 命名空间，不会修改其他 3x-ui 资源。' }}</p></div><small>最近检测：{{ formatTime(data?.lastCheckedAt) }}</small></section>
      <section class="metric-grid">
        <article><span>受管公网协议</span><strong>{{ data?.managedPublicCount ?? data?.managedVlessCount ?? '—' }}</strong><small>VLESS / Hysteria2 入站</small></article>
        <article><span>受管 mixed</span><strong>{{ data?.managedMixedCount ?? '—' }}</strong><small>SOCKS5H 入站</small></article>
        <article><span>受管出站</span><strong>{{ data?.managedOutboundCount ?? '—' }}</strong><small>AimiliVPN 出口路由</small></article>
      </section>
      <section class="action-panel"><div><h2>受管资源维护</h2><p>检测为只读操作；修复仅重建或校正 Gateway 自有资源。</p></div><div class="actions"><button data-check-xui class="secondary" :disabled="!!busy" @click="run('/api/v1/settings/3x-ui/check','check')">{{ busy === 'check' ? '检测中' : '检测受管资源' }}</button><button data-repair-xui :disabled="!!busy" @click="run('/api/v1/settings/3x-ui/repair','repair')">{{ busy === 'repair' ? '修复中' : '修复资源漂移' }}</button></div></section>
      <p class="boundary">专家模式保留 3x-ui 原生登录与完整功能。请通过统一账户命令修改用户名或密码，避免三服务账户漂移。</p>
    </div>
  </AppShell>
</template>

<style scoped>
.subpage{max-width:1000px;margin:0 auto}.back-link{display:inline-block;margin-bottom:16px;color:var(--muted-text);font-size:12px;font-weight:700;text-decoration:none}.subpage header{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:20px}.subpage header p{margin:0 0 6px;color:var(--accent);font-size:10px;font-weight:850;letter-spacing:.16em}.subpage h1{margin:0;font-size:29px;letter-spacing:-.03em}.subpage header span{display:block;margin-top:7px;color:var(--muted-text);font-size:13px}.notice{padding:10px 12px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--muted-text);font-size:13px}.ownership{display:grid;grid-template-columns:auto 1fr auto;align-items:center;gap:12px;padding:15px 17px;border:1px solid color-mix(in srgb,var(--healthy) 30%,var(--border));border-radius:12px;background:color-mix(in srgb,var(--healthy) 6%,var(--panel))}.ownership.warning{border-color:color-mix(in srgb,var(--warning) 35%,var(--border));background:color-mix(in srgb,var(--warning) 7%,var(--panel))}.status-dot{width:9px;height:9px;border-radius:50%;background:var(--healthy);box-shadow:0 0 0 4px color-mix(in srgb,var(--healthy) 13%,transparent)}.warning .status-dot{background:var(--warning)}.ownership p{margin:3px 0 0;color:var(--muted-text);font-size:12px}.ownership small{color:var(--muted-text);font-size:11px}.metric-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin-top:14px}.metric-grid article,.action-panel{border:1px solid var(--border);border-radius:13px;background:var(--panel);box-shadow:var(--shadow-soft)}.metric-grid article{display:grid;gap:7px;padding:17px}.metric-grid span,.metric-grid small{color:var(--muted-text);font-size:11px}.metric-grid strong{font-size:27px}.action-panel{display:flex;align-items:center;justify-content:space-between;gap:24px;margin-top:14px;padding:20px}.action-panel h2{margin:0;font-size:17px}.action-panel p{margin:6px 0;color:var(--muted-text);font-size:13px}.actions{display:flex;gap:9px;white-space:nowrap}.boundary{margin:14px 2px;color:var(--muted-text);font-size:11px;line-height:1.6}@media(max-width:760px){.metric-grid{grid-template-columns:1fr}.action-panel,.subpage header{align-items:stretch;flex-direction:column}.actions{display:grid;grid-template-columns:1fr 1fr}.ownership{grid-template-columns:auto 1fr}.ownership small{grid-column:2}.subpage header button{align-self:flex-start}}@media(max-width:460px){.actions{grid-template-columns:1fr}}
</style>
