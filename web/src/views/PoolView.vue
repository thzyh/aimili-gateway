<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { apiDownloadText, apiFetch, idempotencyHeaders, type CandidateCountryPayload, type ConnectionsPayload, type CountryRefreshPayload, type ProtocolMode, type ProtocolModePayload, type ProxyGroupPayload, type ProxyType, type SubscriptionPayload } from '../api/client'
import AppShell from '../components/AppShell.vue'
import PoolFilters from '../components/PoolFilters.vue'
import PoolTable from '../components/PoolTable.vue'
import { poolStatusGroup, type PoolStatusGroup } from '../components/poolStatus'

const props = defineProps<{ protocol: 'vless' | 'socks5h' }>()
const groups = ref<ProxyGroupPayload[]>([])
const candidateCountries = ref<CandidateCountryPayload[]>([])
const refreshState = ref<CountryRefreshPayload | null>(null)
const country = ref('')
const supplementCountry = ref('')
const proxyType = ref<'' | ProxyType>('')
const status = ref<'' | PoolStatusGroup>('')
const sort = ref('latency')
const busy = ref('')
const loading = ref(true)
const notice = ref('')
const keepEnabledVisible = ref(true)
const replacementCandidate = ref<ProxyGroupPayload | null>(null)
const replacementTarget = ref('')
let refreshTimer: ReturnType<typeof setTimeout> | undefined

const title = computed(() => props.protocol === 'vless' ? 'VPN 节点池' : 'SOCKS5H 代理池')
const description = computed(() => props.protocol === 'vless' ? '每个逻辑出口可独立使用 TCP/Vision、XHTTP/REALITY 或 Hysteria2；mixed/SOCKS5H 始终保持不变。' : '每个在线出口对应一个支持代理 DNS 的 SOCKS5H 地址。')
const countries = computed(() => {
  const merged = new Map(groups.value.map(row => [row.countryCode, { code: row.countryCode, name: row.countryName }]))
  return [...merged.values()].sort((a, b) => a.code.localeCompare(b.code))
})
const officialCountries = computed(() => candidateCountries.value.map(item => ({ code: item.code, name: item.name })).sort((a, b) => a.code.localeCompare(b.code)))
const poolStats = computed(() => candidateCountries.value[0])
function runtimeRank(row: ProxyGroupPayload): number | null {
  if (row.egressSource === 'main' || row.id === 'agw-main') return 0
  const slot = row.slotNumber ?? 0
  return slot >= 1 && slot <= 3 ? slot : null
}
const rows = computed(() => groups.value.filter(row => {
  if (runtimeRank(row) !== null) return true
  return (!country.value || row.countryCode === country.value) && (!proxyType.value || row.proxyType === proxyType.value) && (!status.value || poolStatusGroup(row.status) === status.value)
}).sort((a, b) => {
  const leftRank = runtimeRank(a)
  const rightRank = runtimeRank(b)
  if (leftRank !== null || rightRank !== null) return (leftRank ?? 100) - (rightRank ?? 100)
  if (sort.value === 'country') return a.countryCode.localeCompare(b.countryCode)
  if (sort.value === 'updated') return (b.lastCheckedAt ?? '').localeCompare(a.lastCheckedAt ?? '')
  const left = props.protocol === 'vless' ? a.vlessLatencyMs : a.socksLatencyMs
  const right = props.protocol === 'vless' ? b.vlessLatencyMs : b.socksLatencyMs
  return (left || Number.MAX_SAFE_INTEGER) - (right || Number.MAX_SAFE_INTEGER)
}))
const replacementTargets = computed(() => groups.value.filter(row => row.status === 'ready' && (row.egressSource === 'main' || (row.slotNumber ?? 0) > 0)).sort((a, b) => {
  if (a.egressSource === 'main') return -1
  if (b.egressSource === 'main') return 1
  return (a.slotNumber ?? 0) - (b.slotNumber ?? 0)
}))
const subscriptionReady = computed(() => groups.value.some(row => row.status === 'ready' && row.protocolState === 'ready' && row.subscriptionState === 'ready'))

onMounted(loadInitial)
onBeforeUnmount(() => { if (refreshTimer !== undefined) clearTimeout(refreshTimer) })

async function loadInitial(): Promise<void> {
  await Promise.all([loadGroups(), loadCatalog(), readRefreshStatus()])
}

async function loadGroups(showLoading = true): Promise<void> {
  if (showLoading) loading.value = true
  try { groups.value = await apiFetch<ProxyGroupPayload[]>('/api/v1/proxy-groups') }
  catch { notice.value = '暂时无法读取代理池。' }
  finally { if (showLoading) loading.value = false }
}

async function loadCatalog(): Promise<void> {
  try { candidateCountries.value = await apiFetch<CandidateCountryPayload[]>('/api/v1/settings/aimilivpn/countries') }
  catch { candidateCountries.value = [] }
}

async function readRefreshStatus(): Promise<void> {
  try {
    refreshState.value = await apiFetch<CountryRefreshPayload>('/api/v1/settings/aimilivpn/refresh')
    if (refreshState.value.state === 'running') scheduleRefreshPoll()
  } catch { /* 代理池仍可独立使用 */ }
}

function scheduleRefreshPoll(): void {
  if (refreshTimer !== undefined) clearTimeout(refreshTimer)
  refreshTimer = setTimeout(pollRefreshStatus, 2_000)
}

async function pollRefreshStatus(): Promise<void> {
  refreshTimer = undefined
  try {
    const current = await apiFetch<CountryRefreshPayload>('/api/v1/settings/aimilivpn/refresh')
    refreshState.value = current
    if (current.state === 'running') {
      scheduleRefreshPoll()
    } else if (current.state === 'completed') {
      notice.value = `${current.country} 节点刷新已完成：精验 ${current.testedCount} 个，保留 ${current.validCount} 个。`
      await Promise.all([loadGroups(false), loadCatalog()])
    } else if (current.state === 'failed') {
      notice.value = `节点刷新失败：${current.errorCode || 'refresh_failed'}`
    }
  } catch { notice.value = '暂时无法读取节点刷新状态。' }
}

async function refreshPool(): Promise<void> {
  busy.value = 'refresh'; notice.value = ''
  try {
    await apiFetch('/api/v1/proxy-groups/reconcile', { method: 'POST' })
    notice.value = '代理状态已同步；在线节点不会被整批中断。'
    await loadGroups(false)
  } catch (error) { notice.value = messageFor(error, '同步失败') }
  finally { busy.value = '' }
}

async function refreshCountry(): Promise<void> {
  if (!supplementCountry.value) {
    notice.value = '请先选择要刷新的国家。'
    return
  }
  busy.value = 'country-refresh'; notice.value = ''
  try {
    refreshState.value = await apiFetch<CountryRefreshPayload>('/api/v1/settings/aimilivpn/refresh', {
      method: 'POST', headers: idempotencyHeaders(), body: JSON.stringify({ country: supplementCountry.value }),
    })
    notice.value = `${supplementCountry.value} 节点刷新已开始；当前在线代理不会中断。`
    if (refreshState.value.state === 'running') scheduleRefreshPoll()
  } catch (error) { notice.value = messageFor(error, '国家刷新失败') }
  finally { busy.value = '' }
}

async function copyAddress(row: ProxyGroupPayload): Promise<void> {
  busy.value = `copy-${row.id}`; notice.value = ''
  try {
    const value = await apiFetch<ConnectionsPayload>(`/api/v1/proxy-groups/${row.id}/connections`)
    const address = props.protocol === 'vless' ? value.publicUri : value.socks5hUri
    if (!address) throw new Error(value.vlessError || 'protocol_not_ready')
    await navigator.clipboard.writeText(address)
    notice.value = '地址已复制；连接秘密不会保存在浏览器存储中。'
  } catch (error) { notice.value = messageFor(error, '复制失败') }
  finally { busy.value = '' }
}

async function copyAll(): Promise<void> {
  busy.value = 'copy-all'; notice.value = ''
  try {
    const text = (await apiDownloadText(exportPath())).replace(/\s+$/u, '')
    if (!text) {
      notice.value = '当前没有可用地址。'
      return
    }
    await navigator.clipboard.writeText(text)
    notice.value = `已复制 ${text.split('\n').length} 条当前筛选结果。`
  } catch (error) { notice.value = messageFor(error, '批量复制失败') }
  finally { busy.value = '' }
}

async function copySubscription(): Promise<void> {
  busy.value = 'copy-subscription'; notice.value = ''
  try {
    const value = await apiFetch<SubscriptionPayload>('/api/v1/proxy-groups/subscription')
    if (!value.url) { notice.value = '当前没有可用的节点订阅。'; return }
    await navigator.clipboard.writeText(value.url)
    notice.value = `节点订阅已复制，包含 ${value.inboundCount} 个独立节点。`
  } catch (error) { notice.value = messageFor(error, '订阅复制失败') }
  finally { busy.value = '' }
}

async function exportRows(): Promise<void> {
  busy.value = 'export'; notice.value = ''
  try {
    const text = await apiDownloadText(exportPath())
    if (typeof URL.createObjectURL === 'function') {
      const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
      const link = document.createElement('a'); link.href = url; link.download = `aimili-${props.protocol}.txt`; link.click(); URL.revokeObjectURL(url)
    }
    notice.value = `已导出 ${rows.value.filter(row => row.status === 'ready').length} 条可用地址。`
  } catch (error) { notice.value = messageFor(error, '导出失败') }
  finally { busy.value = '' }
}

function exportPath(): string {
  const query = new URLSearchParams({ protocol: props.protocol })
  if (country.value) query.set('country', country.value)
  if (proxyType.value) query.set('proxyType', proxyType.value)
  if (status.value === 'standby' || status.value === 'ready') query.set('status', status.value)
  return `/api/v1/proxy-groups/export?${query}`
}

async function mutate(row: ProxyGroupPayload, action: 'activate' | 'check' | 'rotate'): Promise<void> {
  busy.value = `${action}-${row.id}`; notice.value = ''
  try {
    await apiFetch(`/api/v1/proxy-groups/${row.id}/${action}`, { method: 'POST', ...(['activate', 'rotate'].includes(action) ? { headers: idempotencyHeaders() } : {}) })
    await loadGroups(false)
  } catch (error) { notice.value = messageFor(error, action === 'activate' ? '启用失败' : action === 'check' ? '检测失败' : '换 IP 失败') }
  finally { busy.value = '' }
}

function openReplacement(row: ProxyGroupPayload): void {
  replacementCandidate.value = row
  replacementTarget.value = replacementTargets.value[0]?.id ?? ''
}

function closeReplacement(): void {
  replacementCandidate.value = null
  replacementTarget.value = ''
}

async function confirmReplacement(): Promise<void> {
  if (!replacementCandidate.value || !replacementTarget.value) return
  const candidate = replacementCandidate.value
  const target = replacementTarget.value
  busy.value = `replace-${candidate.id}`; notice.value = ''
  try {
    await apiFetch(`/api/v1/proxy-groups/${candidate.id}/replace`, { method: 'POST', headers: idempotencyHeaders(), body: JSON.stringify({ targetGroupId: target }) })
    closeReplacement()
    notice.value = target === 'agw-main' ? '主连接替换成功；失败回滚边界已保留。' : '出口位替换成功，端口与入站保持不变。'
    await loadGroups(false)
  } catch (error) { notice.value = messageFor(error, '出口位替换失败') }
  finally { busy.value = '' }
}

async function switchProtocol(row: ProxyGroupPayload, protocolMode: ProtocolMode): Promise<void> {
  if (protocolMode === row.protocolMode) return
  busy.value = `protocol-${row.id}`; notice.value = ''
  try {
    await apiFetch<ProtocolModePayload>(`/api/v1/proxy-groups/${row.id}/protocol-mode`, { method: 'PUT', headers: idempotencyHeaders(), body: JSON.stringify({ protocolMode }) })
    notice.value = '公网协议切换成功；mixed/SOCKS5H 未变化。'
    await loadGroups(false)
  } catch (error) {
    notice.value = messageFor(error, '协议切换失败，已请求恢复旧协议')
    await loadGroups(false)
  } finally { busy.value = '' }
}

async function checkRow(row: ProxyGroupPayload): Promise<void> {
  busy.value = `check-${row.id}`; notice.value = ''
  try {
    const path = row.egressSource === 'main' ? '/api/v1/proxy-groups/agw-main/check' : `/api/v1/proxy-groups/${row.id}/check`
    await apiFetch(path, { method: 'POST', ...(row.egressSource === 'main' ? { headers: idempotencyHeaders() } : {}) })
    await loadGroups(false)
  } catch (error) { notice.value = messageFor(error, '检测失败') }
  finally { busy.value = '' }
}

function refreshStateLabel(): string {
  if (!refreshState.value || refreshState.value.state === 'idle') return ''
  if (refreshState.value.state === 'running') return `${refreshState.value.country} 正在刷新 · 已精验 ${refreshState.value.testedCount} 个`
  if (refreshState.value.state === 'completed') return `${refreshState.value.country} 已完成 · 精验 ${refreshState.value.testedCount} 个，保留 ${refreshState.value.validCount} 个`
  return `${refreshState.value.country || '节点'} 刷新失败`
}

function messageFor(error: unknown, fallback: string): string {
  return `${fallback}：${error instanceof Error ? error.message : 'request_failed'}`
}
</script>

<template>
  <AppShell>
    <section class="page-heading">
      <div><p class="eyebrow">ONLINE EGRESS POOL</p><h1>{{ title }}</h1><p>{{ description }}</p></div>
      <div class="heading-actions"><button data-sync-pool class="secondary" :disabled="busy !== ''" @click="refreshPool">{{ busy === 'refresh' ? '正在同步…' : '同步代理状态' }}</button><button data-refresh-country class="secondary" :disabled="busy !== '' || !supplementCountry" @click="refreshCountry">{{ busy === 'country-refresh' ? '正在刷新…' : '补充所选国家' }}</button><button v-if="protocol === 'vless'" data-copy-subscription :disabled="busy !== '' || !subscriptionReady" @click="copySubscription">复制节点订阅</button><button data-copy-all class="secondary" :disabled="busy !== ''" @click="copyAll">复制节点列表</button><button data-export class="secondary" :disabled="busy !== ''" @click="exportRows">导出</button></div>
    </section>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <p v-if="refreshStateLabel()" class="refresh-state">{{ refreshStateLabel() }}</p>
    <section class="pool-toolbar">
      <PoolFilters :countries="countries" :official-countries="officialCountries" :country="country" :supplement-country="supplementCountry" :proxy-type="proxyType" :status="status" :sort="sort" @country="country=$event" @supplement-country="supplementCountry=$event" @proxy-type="proxyType=$event" @status="status=$event" @sort="sort=$event" />
      <label class="fixed-toggle"><input v-model="keepEnabledVisible" data-fixed-enabled type="checkbox"> 始终显示运行节点</label><span data-pool-stats>官方 {{ poolStats?.officialCandidateTotal ?? candidateCountries.reduce((sum,item) => sum + item.candidateCount, 0) }} · 当前有效 {{ poolStats?.validNodeCount ?? groups.length }} · {{ poolStats?.validCountryCount ?? countries.length }} 国</span>
    </section>
    <div v-if="loading" class="loading">正在读取代理池…</div>
    <PoolTable v-else :rows="rows" :protocol="protocol" :busy="busy" @copy="copyAddress" @replace="openReplacement" @check="checkRow" @protocol="switchProtocol" />
    <div v-if="replacementCandidate" class="dialog-backdrop" @click.self="closeReplacement">
      <section data-replace-dialog class="replace-dialog" role="dialog" aria-modal="true" aria-labelledby="replace-title">
        <button class="dialog-close" type="button" aria-label="关闭" @click="closeReplacement">×</button>
        <p class="eyebrow">REPLACE EGRESS SLOT</p><h2 id="replace-title">替换到出口位</h2>
        <p>将 {{ replacementCandidate.countryName || replacementCandidate.countryCode }} {{ replacementCandidate.proxyType === 'residential' ? '住宅' : '机房' }}候选装载到现有出口位。原端口和 VLESS/SOCKS5H 入站保持不变，失败时自动回滚。</p>
        <label>目标逻辑出口<select v-model="replacementTarget" data-replace-target><option v-for="target in replacementTargets" :key="target.id" :value="target.id">{{ target.egressSource === 'main' ? '主连接' : `出口位 ${target.slotNumber}` }} · {{ target.countryName || target.countryCode }} · {{ target.exitIp }}</option></select></label>
        <div class="dialog-actions"><button class="secondary" type="button" @click="closeReplacement">取消</button><button data-confirm-replace type="button" :disabled="!replacementTarget || busy !== ''" @click="confirmReplacement">{{ busy.startsWith('replace-') ? '正在替换…' : '确认替换' }}</button></div>
      </section>
    </div>
  </AppShell>
</template>

<style scoped>
.page-heading{display:flex;align-items:flex-end;justify-content:space-between;gap:24px;margin-bottom:20px}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:11px;font-weight:800;letter-spacing:.14em}.page-heading h1{margin:0;font-size:28px;letter-spacing:-.035em}.page-heading p:not(.eyebrow){margin:8px 0 0;color:var(--muted-text);font-size:14px}.heading-actions{display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px}.notice,.loading{margin:0 0 14px;padding:10px 13px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--muted-text);font-size:13px}.refresh-state{margin:-5px 0 14px;color:var(--muted-text);font-size:12px}.pool-toolbar{display:flex;align-items:center;gap:14px;margin-bottom:12px}.pool-toolbar>span{margin-left:auto;flex:none;color:var(--muted-text);font-size:12px}.fixed-toggle{display:flex;align-items:center;gap:6px;color:var(--muted-text);font-size:12px;white-space:nowrap}.dialog-backdrop{position:fixed;inset:0;z-index:20;display:grid;place-items:center;padding:20px;background:rgba(15,23,42,.52);backdrop-filter:blur(3px)}.replace-dialog{position:relative;width:min(460px,100%);padding:24px;border:1px solid var(--border);border-radius:14px;background:var(--panel);box-shadow:0 24px 70px rgba(15,23,42,.28)}.replace-dialog h2{margin:0 0 10px;font-size:22px}.replace-dialog>p:not(.eyebrow){color:var(--muted-text);font-size:13px;line-height:1.65}.replace-dialog label{display:grid;gap:7px;margin-top:18px;font-size:12px;font-weight:700}.replace-dialog select{height:40px;padding:0 10px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text)}.dialog-close{position:absolute;top:12px;right:12px;width:32px;height:32px;padding:0;border:0;background:transparent;color:var(--muted-text);font-size:22px}.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:20px}@media(max-width:760px){.page-heading{align-items:flex-start;flex-direction:column}.heading-actions{width:100%;justify-content:flex-start}.heading-actions button{flex:1}.pool-toolbar{align-items:stretch;flex-direction:column}.pool-toolbar>span{align-self:flex-end;margin-left:0}}
</style>
