<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppShell from '../components/AppShell.vue'
import UiNotice from '../components/UiNotice.vue'
import { apiFetch, idempotencyHeaders, type FreesubBackupPayload } from '../api/client'
import type { UiNoticeData } from '../components/errorMessages'

const backup = ref<FreesubBackupPayload | null>(null)
const busy = ref('')
const notice = ref<UiNoticeData | null>(null)
let sequence = 0

const statusLabel = computed(() => ({
  standby: '尚未配置', provisioning: '正在启用', ready: '运行正常', degraded: '检测失败',
  repair_required: '需要修复', waiting_manual: '自动替换失败，等待人工处理',
}[backup.value?.status || 'standby']))
const canReplace = computed(() => !!backup.value?.candidateId && backup.value.repairAttempts < 1 && ['degraded', 'repair_required'].includes(backup.value.status))

function show(kind: UiNoticeData['kind'], title: string, message = ''): void {
  sequence += 1
  notice.value = { id: `freesub-${sequence}`, kind, title, message }
}

async function load(): Promise<void> {
  try { backup.value = await apiFetch<FreesubBackupPayload>('/api/v1/freesub/backup') }
  catch { show('error', '备用连接读取失败', '无法读取 freesub 独立状态。') }
}

async function run(action: 'check' | 'replace' | 'manual-provision'): Promise<void> {
  busy.value = action
  const manual = action === 'manual-provision'
  show('progress', action === 'check' ? '正在检测备用连接' : manual ? '正在人工重新配置' : '正在更换备用节点', manual ? '将串行复检有限候选，可能由人工操作更换国家。' : '操作只针对 freesub 独立进程。')
  try {
    backup.value = await apiFetch<FreesubBackupPayload>(`/api/v1/freesub/backup/${action}`, { method: 'POST', headers: idempotencyHeaders() })
    show('success', action === 'check' ? '检测完成' : manual ? '人工重新配置完成' : '备用节点已更换')
  } catch {
    await load()
    show('error', action === 'check' ? '检测失败' : manual ? '人工重新配置失败' : '更换失败', backup.value?.status === 'waiting_manual' ? '仍在等待人工处理；没有触发循环自动替换。' : '运行时尚未配置或本地复检失败。')
  } finally { busy.value = '' }
}

onMounted(load)
</script>

<template>
  <AppShell>
    <section class="page">
      <header><div><p class="eyebrow">INDEPENDENT BACKUP</p><h1>freesub 备用连接</h1><span>不属于主连接或出口 1–4；检测、更换与进程生命周期完全独立。</span></div></header>
      <UiNotice v-if="notice" :notice="notice" @dismiss="notice = null" />
      <article v-if="backup" data-freesub-card class="backup-card" :data-status="backup.status">
        <div class="card-heading"><div><small>独立连接</small><h2>{{ statusLabel }}</h2></div><span class="status-dot" /></div>
        <dl>
          <div><dt>国家</dt><dd>{{ backup.countryCode || '—' }}</dd></div>
          <div><dt>协议</dt><dd>{{ backup.protocol || '—' }}</dd></div>
          <div><dt>实际出口 IP</dt><dd>{{ backup.exitIp || '—' }}</dd></div>
          <div><dt>本机 SOCKS</dt><dd>{{ backup.socksPort ? `127.0.0.1:${backup.socksPort}` : '—' }}</dd></div>
          <div><dt>自动替换</dt><dd>{{ backup.repairAttempts }}/1</dd></div>
          <div><dt>最近检测</dt><dd>{{ backup.lastCheckedAt || '—' }}</dd></div>
        </dl>
        <p v-if="backup.status === 'waiting_manual'" class="manual">自动替换失败，等待人工处理。卡片与故障记录会继续保留。</p>
        <div class="actions">
          <button data-check-freesub :disabled="!!busy || !backup.candidateId" type="button" @click="run('check')">{{ busy === 'check' ? '检测中' : '检测' }}</button>
          <button data-replace-freesub class="secondary" :disabled="!!busy || !canReplace" type="button" @click="run('replace')">{{ busy === 'replace' ? '更换中' : '更换备用节点' }}</button>
          <button v-if="backup.status === 'waiting_manual'" data-manual-provision-freesub class="secondary" :disabled="!!busy" type="button" @click="run('manual-provision')">{{ busy === 'manual-provision' ? '配置中' : '人工重新配置（可能更换国家）' }}</button>
        </div>
      </article>
    </section>
  </AppShell>
</template>

<style scoped>
.page{max-width:920px;margin:0 auto}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:10px;font-weight:850;letter-spacing:.16em}h1{margin:0;font-size:30px}header span{display:block;margin:8px 0 20px;color:var(--muted-text);font-size:13px}.backup-card{margin-top:16px;padding:22px;border:1px solid var(--border);border-radius:14px;background:var(--panel);box-shadow:var(--shadow-soft)}.card-heading{display:flex;align-items:center;justify-content:space-between}.card-heading small{color:var(--muted-text)}h2{margin:5px 0;font-size:20px}.status-dot{width:10px;height:10px;border-radius:50%;background:var(--warning);box-shadow:0 0 0 5px color-mix(in srgb,var(--warning) 15%,transparent)}.backup-card[data-status=ready] .status-dot{background:var(--healthy)}.backup-card[data-status=degraded] .status-dot,.backup-card[data-status=repair_required] .status-dot,.backup-card[data-status=waiting_manual] .status-dot{background:var(--danger)}dl{display:grid;grid-template-columns:repeat(3,1fr);gap:10px;margin:20px 0}dl div{padding:12px;border:1px solid var(--border-soft);border-radius:9px;background:var(--subtle)}dt{color:var(--muted-text);font-size:11px}dd{margin:5px 0 0;font-weight:750}.manual{padding:12px;border:1px solid color-mix(in srgb,var(--danger) 35%,var(--border));border-radius:9px;color:var(--danger);background:color-mix(in srgb,var(--danger) 7%,var(--panel));font-size:13px}.actions{display:flex;gap:8px;justify-content:flex-end}@media(max-width:700px){dl{grid-template-columns:1fr 1fr}.actions{flex-direction:column}}@media(max-width:420px){dl{grid-template-columns:1fr}}
</style>
