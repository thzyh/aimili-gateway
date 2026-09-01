<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { apiDownloadText, apiFetch, idempotencyHeaders, type CandidateCountryPayload, type ConnectionsPayload, type CountryRefreshPayload, type ProtocolMode, type ProtocolModePayload, type ProxyGroupPayload, type ProxyType, type SubscriptionPayload } from '../api/client'
import AppShell from '../components/AppShell.vue'
import PoolFilters from '../components/PoolFilters.vue'
import PoolTable from '../components/PoolTable.vue'
import UiNotice from '../components/UiNotice.vue'
import { codeFromError, countryDisplayName, messageForCode, type NoticeKind, type UiNoticeData } from '../components/errorMessages'
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
const topNotice = ref<UiNoticeData | null>(null)
const refreshNotice = ref<UiNoticeData | null>(null)
const replacementNotice = ref<UiNoticeData | null>(null)
const replacementCandidate = ref<ProxyGroupPayload | null>(null)
const replacementTarget = ref('')
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let noticeSequence = 0

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

function makeNotice(kind: NoticeKind, title: string, message = ''): UiNoticeData {
  noticeSequence += 1
  return { id: `notice-${noticeSequence}`, kind, title, message }
}

function localizedError(error: unknown, fallback: string): string {
  return messageForCode(codeFromError(error), fallback)
}

function refreshCountryName(code = supplementCountry.value): string {
  return countryDisplayName(code, candidateCountries.value)
}

onMounted(loadInitial)
onBeforeUnmount(() => { if (refreshTimer !== undefined) clearTimeout(refreshTimer) })

async function loadInitial(): Promise<void> {
  await Promise.all([loadGroups(), loadCatalog(), readRefreshStatus()])
}

async function loadGroups(showLoading = true): Promise<void> {
  if (showLoading) loading.value = true
  try { groups.value = await apiFetch<ProxyGroupPayload[]>('/api/v1/proxy-groups') }
  catch { topNotice.value = makeNotice('error', '代理池读取失败', '暂时无法读取代理池。') }
  finally { if (showLoading) loading.value = false }
}

async function loadCatalog(): Promise<void> {
  try { candidateCountries.value = await apiFetch<CandidateCountryPayload[]>('/api/v1/settings/aimilivpn/countries') }
  catch { candidateCountries.value = [] }
}

async function readRefreshStatus(): Promise<void> {
  try {
    refreshState.value = await apiFetch<CountryRefreshPayload>('/api/v1/settings/aimilivpn/refresh')
    if (refreshState.value.state === 'running') {
      refreshNotice.value = refreshNoticeFor(refreshState.value)
      scheduleRefreshPoll()
    }
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
    refreshNotice.value = refreshNoticeFor(current)
    if (current.state === 'running') {
      scheduleRefreshPoll()
    } else if (current.state === 'completed') {
      await Promise.all([loadGroups(false), loadCatalog()])
    }
  } catch { refreshNotice.value = makeNotice('error', '刷新状态读取失败', '暂时无法读取节点刷新状态。') }
}

async function refreshPool(): Promise<void> {
  busy.value = 'refresh'; topNotice.value = makeNotice('progress', '正在同步代理状态', '在线节点不会被整批中断。')
  try {
    await apiFetch('/api/v1/proxy-groups/reconcile', { method: 'POST' })
    topNotice.value = makeNotice('success', '代理状态已同步', '在线节点未被整批中断。')
    await loadGroups(false)
  } catch (error) { topNotice.value = makeNotice('error', '同步失败', localizedError(error, '暂时无法同步代理状态。')) }
  finally { busy.value = '' }
}

async function refreshCountry(): Promise<void> {
  if (!supplementCountry.value) {
    refreshNotice.value = makeNotice('info', '请选择国家', '请先选择要刷新的国家。')
    return
  }
  const displayName = refreshCountryName()
  busy.value = 'country-refresh'
  refreshNotice.value = makeNotice('progress', `${displayName}刷新已开始`, '当前在线代理不会中断。')
  try {
    refreshState.value = await apiFetch<CountryRefreshPayload>('/api/v1/settings/aimilivpn/refresh', {
      method: 'POST', headers: idempotencyHeaders(), body: JSON.stringify({ country: supplementCountry.value }),
    })
    refreshNotice.value = refreshNoticeFor(refreshState.value)
    if (refreshState.value.state === 'running') scheduleRefreshPoll()
  } catch (error) { refreshNotice.value = makeNotice('error', `${displayName}刷新失败`, localizedError(error, '国家节点刷新失败，请稍后重试。')) }
  finally { busy.value = '' }
}

async function copyAddress(row: ProxyGroupPayload): Promise<void> {
  busy.value = `copy-${row.id}`; topNotice.value = null
  try {
    const value = await apiFetch<ConnectionsPayload>(`/api/v1/proxy-groups/${row.id}/connections`)
    const address = props.protocol === 'vless' ? value.publicUri : value.socks5hUri
    if (!address) throw new Error(value.vlessError || 'protocol_not_ready')
    await navigator.clipboard.writeText(address)
    topNotice.value = makeNotice('success', '地址已复制', '连接秘密不会保存在浏览器存储中。')
  } catch (error) { topNotice.value = makeNotice('error', '复制失败', localizedError(error, '暂时无法复制地址。')) }
  finally { busy.value = '' }
}

async function copyAll(): Promise<void> {
  busy.value = 'copy-all'; topNotice.value = null
  try {
    const text = (await apiDownloadText(exportPath())).replace(/\s+$/u, '')
    if (!text) {
      topNotice.value = makeNotice('info', '当前没有可用地址', '当前筛选结果中没有可复制的地址。')
      return
    }
    await navigator.clipboard.writeText(text)
    topNotice.value = makeNotice('success', '节点列表已复制', `已复制 ${text.split('\n').length} 条当前筛选结果。`)
  } catch (error) { topNotice.value = makeNotice('error', '批量复制失败', localizedError(error, '暂时无法复制节点列表。')) }
  finally { busy.value = '' }
}

async function copySubscription(): Promise<void> {
  busy.value = 'copy-subscription'; topNotice.value = null
  try {
    const value = await apiFetch<SubscriptionPayload>('/api/v1/proxy-groups/subscription')
    if (!value.url) { topNotice.value = makeNotice('info', '没有可用订阅', '当前没有可用的节点订阅。'); return }
    await navigator.clipboard.writeText(value.url)
    topNotice.value = makeNotice('success', '节点订阅已复制', `包含 ${value.inboundCount} 个独立节点。`)
  } catch (error) { topNotice.value = makeNotice('error', '订阅复制失败', localizedError(error, '暂时无法复制节点订阅。')) }
  finally { busy.value = '' }
}

async function exportRows(): Promise<void> {
  busy.value = 'export'; topNotice.value = null
  try {
    const text = await apiDownloadText(exportPath())
    if (typeof URL.createObjectURL === 'function') {
      const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
      const link = document.createElement('a'); link.href = url; link.download = `aimili-${props.protocol}.txt`; link.click(); URL.revokeObjectURL(url)
    }
    topNotice.value = makeNotice('success', '导出完成', `已导出 ${rows.value.filter(row => row.status === 'ready').length} 条可用地址。`)
  } catch (error) { topNotice.value = makeNotice('error', '导出失败', localizedError(error, '暂时无法导出地址。')) }
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
  busy.value = `${action}-${row.id}`; topNotice.value = null
  try {
    await apiFetch(`/api/v1/proxy-groups/${row.id}/${action}`, { method: 'POST', ...(['activate', 'rotate'].includes(action) ? { headers: idempotencyHeaders() } : {}) })
    await loadGroups(false)
  } catch (error) {
    const title = action === 'activate' ? '启用失败' : action === 'check' ? '检测失败' : '换 IP 失败'
    topNotice.value = makeNotice('error', title, localizedError(error, `${title}，请稍后重试。`))
  }
  finally { busy.value = '' }
}

function openReplacement(row: ProxyGroupPayload): void {
  replacementCandidate.value = row
  replacementTarget.value = replacementTargets.value[0]?.id ?? ''
  replacementNotice.value = null
}

function closeReplacement(): void {
  replacementCandidate.value = null
  replacementTarget.value = ''
  replacementNotice.value = null
}

async function confirmReplacement(): Promise<void> {
  if (!replacementCandidate.value || !replacementTarget.value) return
  const candidate = replacementCandidate.value
  const target = replacementTarget.value
  busy.value = `replace-${candidate.id}`
  replacementNotice.value = makeNotice('progress', '正在替换出口', '将保留原端口和入站，失败时自动回滚。')
  try {
    await apiFetch(`/api/v1/proxy-groups/${candidate.id}/replace`, { method: 'POST', headers: idempotencyHeaders(), body: JSON.stringify({ targetGroupId: target }) })
    closeReplacement()
    topNotice.value = target === 'agw-main'
      ? makeNotice('success', '主连接替换成功', '失败回滚边界已保留。')
      : makeNotice('success', '出口位替换成功', '端口与入站保持不变。')
    await loadGroups(false)
  } catch (error) {
    replacementNotice.value = makeNotice('error', '出口替换失败', localizedError(error, '出口替换失败，请重试或选择其他候选。'))
    await loadGroups(false)
  }
  finally { busy.value = '' }
}

async function switchProtocol(row: ProxyGroupPayload, protocolMode: ProtocolMode): Promise<void> {
  if (protocolMode === row.protocolMode) return
  busy.value = `protocol-${row.id}`
  topNotice.value = makeNotice('progress', '正在切换公网协议', '切换期间 mixed/SOCKS5H 保持不变。')
  try {
    await apiFetch<ProtocolModePayload>(`/api/v1/proxy-groups/${row.id}/protocol-mode`, { method: 'PUT', headers: idempotencyHeaders(), body: JSON.stringify({ protocolMode }) })
    topNotice.value = makeNotice('success', '公网协议切换成功', 'mixed/SOCKS5H 未变化。')
    await loadGroups(false)
  } catch (error) {
    topNotice.value = makeNotice('error', '协议切换失败，已请求恢复旧协议', localizedError(error, '公网协议切换失败，请同步状态后重试。'))
    await loadGroups(false)
  } finally { busy.value = '' }
}

async function checkRow(row: ProxyGroupPayload): Promise<void> {
  busy.value = `check-${row.id}`; topNotice.value = null
  try {
    const path = row.egressSource === 'main' ? '/api/v1/proxy-groups/agw-main/check' : `/api/v1/proxy-groups/${row.id}/check`
    await apiFetch(path, { method: 'POST', ...(row.egressSource === 'main' ? { headers: idempotencyHeaders() } : {}) })
    await loadGroups(false)
  } catch (error) { topNotice.value = makeNotice('error', '检测失败', localizedError(error, '节点检测失败，请稍后重试。')) }
  finally { busy.value = '' }
}

function refreshStateLabel(): string {
  if (!refreshState.value || refreshState.value.state === 'idle') return ''
  const current = refreshState.value
  const name = refreshCountryName(current.country)
  if (current.state === 'running') return `${name}正在刷新 · 已精验 ${current.testedCount} 个`
  const official = current.officialCount ?? current.catalogCount ?? 0
  const usable = current.usableCount ?? current.validCount
  const retained = current.retainedCount ?? current.preservedCount ?? current.validCount
  const time = formatRefreshTime(current.finishedAt)
  if (current.state === 'completed' && (!current.resultCode || current.resultCode === 'success')) return `最后刷新：${name} · 成功 · 官方 ${official} · 可用 ${usable} · 保留 ${retained}${time ? ` · ${time}` : ''}`
  return `最后刷新：${name} · 失败 · 官方 ${official} · 可用 ${usable} · 保留 ${retained}${time ? ` · ${time}` : ''}`
}

function refreshNoticeFor(current: CountryRefreshPayload): UiNoticeData | null {
  if (current.state === 'idle') return null
  const name = refreshCountryName(current.country)
  if (current.state === 'running') {
    return makeNotice('progress', `${name}正在刷新`, `已精验 ${current.testedCount} 个候选，当前在线代理不会中断。`)
  }
  if (current.state === 'completed' && (!current.resultCode || current.resultCode === 'success')) {
    const usable = current.usableCount ?? current.validCount
    const retained = current.retainedCount ?? current.preservedCount ?? current.validCount
    return makeNotice('success', `${name}刷新已完成`, `当前可用 ${usable} 个，缓存保留 ${retained} 个。`)
  }
  const code = current.resultCode || current.errorCode || 'refresh_failed'
  return makeNotice('error', `${name}刷新失败`, messageForCode(code, '国家节点刷新失败，请稍后重试。'))
}

function formatRefreshTime(value?: number): string {
  if (!value || value < 0) return ''
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value * 1000))
}
</script>

<template>
  <AppShell>
    <section class="page-heading">
      <div><p class="eyebrow">ONLINE EGRESS POOL</p><h1>{{ title }}</h1><p>{{ description }}</p></div>
      <div class="heading-actions"><button data-sync-pool class="secondary" :disabled="busy !== ''" @click="refreshPool">{{ busy === 'refresh' ? '正在同步…' : '同步代理状态' }}</button><button data-refresh-country class="secondary" :disabled="busy !== '' || !supplementCountry" @click="refreshCountry">{{ busy === 'country-refresh' ? '正在刷新…' : '补充所选国家' }}</button><button v-if="protocol === 'vless'" data-copy-subscription :disabled="busy !== '' || !subscriptionReady" @click="copySubscription">复制节点订阅</button><button data-copy-all class="secondary" :disabled="busy !== ''" @click="copyAll">复制节点列表</button><button data-export class="secondary" :disabled="busy !== ''" @click="exportRows">导出</button></div>
    </section>
    <UiNotice v-if="topNotice" :key="topNotice.id" data-top-notice :notice="topNotice" @close="topNotice=null" />
    <p v-if="refreshStateLabel()" data-refresh-summary class="refresh-state">{{ refreshStateLabel() }}</p>
    <section class="pool-toolbar">
      <PoolFilters :countries="countries" :official-countries="officialCountries" :country="country" :supplement-country="supplementCountry" :proxy-type="proxyType" :status="status" :sort="sort" @country="country=$event" @supplement-country="supplementCountry=$event" @proxy-type="proxyType=$event" @status="status=$event" @sort="sort=$event" />
      <span data-pool-stats>官方 {{ poolStats?.officialCandidateTotal ?? candidateCountries.reduce((sum,item) => sum + item.candidateCount, 0) }} · 当前有效 {{ poolStats?.validNodeCount ?? groups.length }} · {{ poolStats?.validCountryCount ?? countries.length }} 国</span>
    </section>
    <UiNotice v-if="refreshNotice" :key="refreshNotice.id" data-refresh-notice class="refresh-notice" :notice="refreshNotice" @close="refreshNotice=null" />
    <div v-if="loading" class="loading">正在读取代理池…</div>
    <PoolTable v-else :rows="rows" :protocol="protocol" :busy="busy" @copy="copyAddress" @replace="openReplacement" @check="checkRow" @protocol="switchProtocol" />
    <div v-if="replacementCandidate" class="dialog-backdrop" @click.self="closeReplacement">
      <section data-replace-dialog class="replace-dialog" role="dialog" aria-modal="true" aria-labelledby="replace-title">
        <button class="dialog-close" type="button" aria-label="关闭" @click="closeReplacement">×</button>
        <p class="eyebrow">REPLACE EGRESS SLOT</p><h2 id="replace-title">替换到出口位</h2>
        <p>将 {{ replacementCandidate.countryName || replacementCandidate.countryCode }} {{ replacementCandidate.proxyType === 'residential' ? '住宅' : '机房' }}候选装载到现有出口位。原端口和 VLESS/SOCKS5H 入站保持不变，失败时自动回滚。</p>
        <label>目标逻辑出口<select v-model="replacementTarget" data-replace-target><option v-for="target in replacementTargets" :key="target.id" :value="target.id">{{ target.egressSource === 'main' ? '主连接' : `出口位 ${target.slotNumber}` }} · {{ target.countryName || target.countryCode }} · {{ target.exitIp }}</option></select></label>
        <UiNotice v-if="replacementNotice" :key="replacementNotice.id" data-replace-notice class="replacement-notice" :notice="replacementNotice" @close="replacementNotice=null" />
        <div class="dialog-actions"><button class="secondary" type="button" @click="closeReplacement">取消</button><button data-confirm-replace type="button" :disabled="!replacementTarget || busy !== ''" @click="confirmReplacement">{{ busy.startsWith('replace-') ? '正在替换…' : '确认替换' }}</button></div>
      </section>
    </div>
  </AppShell>
</template>

<style scoped>
.page-heading{display:flex;align-items:flex-end;justify-content:space-between;gap:24px;margin-bottom:20px}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:11px;font-weight:800;letter-spacing:.14em}.page-heading h1{margin:0;font-size:28px;letter-spacing:-.035em}.page-heading p:not(.eyebrow){margin:8px 0 0;color:var(--muted-text);font-size:14px}.heading-actions{display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px}[data-top-notice],.refresh-notice,.loading{margin:0 0 14px}.loading{padding:10px 13px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--muted-text);font-size:13px}.refresh-state{margin:-5px 0 14px;color:var(--muted-text);font-size:12px}.pool-toolbar{display:flex;align-items:center;gap:14px;margin-bottom:12px}.pool-toolbar>span{margin-left:auto;flex:none;color:var(--muted-text);font-size:12px}.fixed-toggle{display:flex;align-items:center;gap:6px;color:var(--muted-text);font-size:12px;white-space:nowrap}.dialog-backdrop{position:fixed;inset:0;z-index:20;display:grid;place-items:center;padding:20px;background:rgba(15,23,42,.52);backdrop-filter:blur(3px)}.replace-dialog{position:relative;width:min(460px,100%);padding:24px;border:1px solid var(--border);border-radius:14px;background:var(--panel);box-shadow:0 24px 70px rgba(15,23,42,.28)}.replace-dialog h2{margin:0 0 10px;font-size:22px}.replace-dialog>p:not(.eyebrow){color:var(--muted-text);font-size:13px;line-height:1.65}.replace-dialog label{display:grid;gap:7px;margin-top:18px;font-size:12px;font-weight:700}.replace-dialog select{height:40px;padding:0 10px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text)}.replacement-notice{margin-top:14px}.dialog-close{position:absolute;top:12px;right:12px;width:32px;height:32px;padding:0;border:0;background:transparent;color:var(--muted-text);font-size:22px}.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:20px}@media(max-width:760px){.page-heading{align-items:flex-start;flex-direction:column}.heading-actions{width:100%;justify-content:flex-start}.heading-actions button{flex:1}.pool-toolbar{align-items:stretch;flex-direction:column}.pool-toolbar>span{align-self:flex-end;margin-left:0}}
</style>
