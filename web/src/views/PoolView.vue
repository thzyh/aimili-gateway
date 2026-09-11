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
const replacementCandidateID = ref('')
const replacementTarget = ref('')
const refreshNoticeFingerprint = ref('')
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let noticeSequence = 0

const dismissedRefreshStorageKey = 'aimili-gateway:pool-refresh-notice-dismissed:v1'

const title = computed(() => props.protocol === 'vless' ? 'VPN 节点池' : 'SOCKS5H 代理池')
const description = computed(() => props.protocol === 'vless' ? '每个逻辑出口可独立使用 TCP/Vision、XHTTP/REALITY 或 Hysteria2；mixed/SOCKS5H 始终保持不变。' : '每个在线出口对应一个支持代理 DNS 的 SOCKS5H 地址。')
const countries = computed(() => {
  const officialNames = new Map(candidateCountries.value.map(item => [item.code.trim().toUpperCase(), item.name.trim()]))
  const merged = new Map(groups.value.map(row => {
    const code = row.countryCode.trim().toUpperCase()
    return [code, { code, name: officialNames.get(code) || row.countryName || code }]
  }))
  return [...merged.values()].sort((a, b) => a.code.localeCompare(b.code))
})
const officialCountries = computed(() => candidateCountries.value.map(item => ({ code: item.code, name: item.name })).sort((a, b) => a.code.localeCompare(b.code)))
const poolStats = computed(() => candidateCountries.value[0])
function runtimeRank(row: ProxyGroupPayload): number | null {
  if (row.egressSource === 'main' || row.id === 'agw-main') return 0
  const slot = row.slotNumber
  return typeof slot === 'number' && slot >= 0 ? slot + 1 : null
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
const replacementTargets = computed(() => groups.value.filter(row => row.status !== 'standby' && (row.egressSource === 'main' || (row.slotNumber ?? 0) > 0)).sort((a, b) => {
  if (a.egressSource === 'main') return -1
  if (b.egressSource === 'main') return 1
  return (a.slotNumber ?? 0) - (b.slotNumber ?? 0)
}))
const automaticRepairFailed = (row: ProxyGroupPayload) => ['no_same_country_candidate', 'replacement_failed', 'manual_repair_required', 'manual_replacement_required'].includes(row.lastErrorCode || '')
const selectedReplacementTarget = computed(() => replacementTargets.value.find(row => row.id === replacementTarget.value) ?? null)
const replacementTargetWarning = computed(() => selectedReplacementTarget.value && automaticRepairFailed(selectedReplacementTarget.value) ? selectedReplacementTarget.value : null)
const replacementTargetName = (row: ProxyGroupPayload) => row.egressSource === 'main' ? '主连接' : `出口位 ${row.slotNumber}`
const replacementTargetLabel = (row: ProxyGroupPayload) => `${automaticRepairFailed(row) ? '【故障·自动修复失败】' : ''}${replacementTargetName(row)} · ${row.countryName || row.countryCode} · ${row.exitIp || '当前无可用出口 IP'}`
const subscriptionReady = computed(() => groups.value.some(row => row.status === 'ready' && row.protocolState === 'ready' && row.subscriptionState === 'ready'))

function makeNotice(kind: NoticeKind, title: string, message = ''): UiNoticeData {
  noticeSequence += 1
  return { id: `notice-${noticeSequence}`, kind, title, message }
}

function localizedError(error: unknown, fallback: string): string {
  return messageForCode(codeFromError(error), fallback)
}

function localizedProtocolError(error: unknown, row: ProxyGroupPayload): string {
  const code = codeFromError(error)
  if (code === 'repair_required' && row.lastErrorCode) return messageForCode(row.lastErrorCode, '协议状态需要修复，请同步状态后重试。')
  return messageForCode(code, '公网协议切换失败，请同步状态后重试。')
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
    showRefreshNotice(refreshState.value)
    if (refreshState.value.state === 'running') {
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
    showRefreshNotice(current)
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
    refreshNoticeFingerprint.value = ''
    refreshNotice.value = makeNotice('info', '请选择国家', '请先选择要刷新的国家。')
    return
  }
  const displayName = refreshCountryName()
  busy.value = 'country-refresh'
  refreshNoticeFingerprint.value = ''
  refreshNotice.value = makeNotice('progress', `${displayName}刷新已开始`, '当前在线代理不会中断。')
  try {
    refreshState.value = await apiFetch<CountryRefreshPayload>('/api/v1/settings/aimilivpn/refresh', {
      method: 'POST', headers: idempotencyHeaders(), body: JSON.stringify({ country: supplementCountry.value }),
    })
    showRefreshNotice(refreshState.value)
    if (refreshState.value.state === 'running') scheduleRefreshPoll()
  } catch (error) { refreshNoticeFingerprint.value = ''; refreshNotice.value = makeNotice('error', `${displayName}刷新失败`, localizedError(error, '国家节点刷新失败，请稍后重试。')) }
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
  replacementCandidateID.value = row.id
  replacementTarget.value = replacementTargets.value[0]?.id ?? ''
  replacementNotice.value = null
}

function closeReplacement(): void {
  replacementCandidate.value = null
  replacementCandidateID.value = ''
  replacementTarget.value = ''
  replacementNotice.value = null
}

async function confirmReplacement(): Promise<void> {
  if (!replacementCandidateID.value || !replacementTarget.value) return
  const candidate = groups.value.find(row => row.id === replacementCandidateID.value)
  if (!candidate || candidate.status !== 'standby') return
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
    await loadGroups(false)
    const current = groups.value.find(item => item.id === row.id) ?? row
    topNotice.value = makeNotice('error', '协议切换失败，已请求恢复旧协议', localizedProtocolError(error, current))
  } finally { busy.value = '' }
}

async function checkRow(row: ProxyGroupPayload): Promise<void> {
  const subject = row.egressSource === 'main' ? '主连接' : row.slotNumber ? `出口 ${row.slotNumber}` : `${row.countryName || row.countryCode}出口`
  busy.value = `check-${row.id}`
  topNotice.value = makeNotice('progress', `正在检测${subject}`, '正在核对节点隧道、真实出口和代理链路；若确认节点本身失效，只会自动选择一个同国家候选修复一次。')
  try {
    const path = row.egressSource === 'main' ? '/api/v1/proxy-groups/agw-main/check' : `/api/v1/proxy-groups/${row.id}/check`
    const result = await apiFetch<ProxyGroupPayload>(path, { method: 'POST', ...(row.egressSource === 'main' ? { headers: idempotencyHeaders() } : {}) })
    topNotice.value = result.autoRepairPerformed
      ? makeNotice('success', `${subject} 自动修复成功`, '已切换到一个同国家可用节点，并重新验证真实出口和代理链路。')
      : makeNotice('success', `${subject} 检测成功`, '真实出口、SOCKS5H 和当前公网协议链路正常；本次没有更换节点。')
    await loadGroups(false)
  } catch (error) {
    await loadGroups(false)
    const code = codeFromError(error)
    const repairResult = ['no_same_country_candidate', 'replacement_failed'].includes(code)
    const alreadyAttempted = ['manual_repair_required', 'manual_replacement_required'].includes(code)
    topNotice.value = makeNotice(
      'error',
      repairResult ? `${subject} 自动修复未成功` : alreadyAttempted ? `${subject} 等待人工更换` : `${subject} 检测失败`,
      localizedError(error, '节点检测失败，当前代理数据面保持不变，请稍后重试。'),
    )
  } finally { busy.value = '' }
}

async function rotateSOCKS5HCredentials(): Promise<void> {
  busy.value = 'rotate-socks-credentials'
  topNotice.value = makeNotice('progress', '正在随机更换 SOCKS5H 用户名和密码', '正在更新全部出口；故障出口也会保留新设置，健康出口会逐一验证。')
  try {
    await apiFetch('/api/v1/settings/socks5h-credentials/rotate', { method: 'POST', headers: idempotencyHeaders() })
    topNotice.value = makeNotice('success', 'SOCKS5H 用户名和密码已更换', '旧代理地址已失效，请重新复制新的 SOCKS5H 地址。')
    await loadGroups(false)
  } catch (error) {
    topNotice.value = makeNotice('error', 'SOCKS5H 凭据更换失败', localizedError(error, '系统已尝试恢复原用户名和密码，请稍后重试。'))
  } finally { busy.value = '' }
}

function refreshNoticeFor(current: CountryRefreshPayload): UiNoticeData | null {
  if (current.state === 'idle') return null
  const name = refreshCountryName(current.country)
  if (current.state === 'running') {
    return makeNotice('progress', `${name}正在刷新`, `已精验 ${current.testedCount} 个候选，当前在线代理不会中断。`)
  }
  const official = current.officialCount ?? current.catalogCount ?? 0
  const usable = current.usableCount ?? current.validCount
  const retained = current.retainedCount ?? current.preservedCount ?? current.validCount
  const time = formatRefreshTime(current.finishedAt)
  const counts = `官方 ${official} · 可用 ${usable} · 保留 ${retained}${time ? ` · ${time}` : ''}`
  if (current.state === 'completed' && (!current.resultCode || current.resultCode === 'success')) {
    return makeNotice('success', `最后刷新：${name} · 成功`, `刷新已完成 · ${counts}`)
  }
  const code = current.resultCode || current.errorCode || 'refresh_failed'
  return makeNotice('error', `最后刷新：${name} · 失败`, `${counts}。${messageForCode(code, '国家节点刷新失败，请稍后重试。')}`)
}

function refreshResultFingerprint(current: CountryRefreshPayload): string {
  if (current.state === 'idle' || current.state === 'running') return ''
  const timestamp = current.finishedAt || current.startedAt || 0
  if (!timestamp) return ''
  return JSON.stringify([current.state, current.country, current.resultCode || current.errorCode || '', timestamp])
}

function showRefreshNotice(current: CountryRefreshPayload): void {
  const fingerprint = refreshResultFingerprint(current)
  refreshNoticeFingerprint.value = fingerprint
  try {
    if (fingerprint && localStorage.getItem(dismissedRefreshStorageKey) === fingerprint) {
      refreshNotice.value = null
      return
    }
  } catch { /* 浏览器禁用存储时仍正常显示提示 */ }
  refreshNotice.value = refreshNoticeFor(current)
}

function dismissRefreshNotice(): void {
  const fingerprint = refreshNoticeFingerprint.value
  try {
    if (fingerprint) localStorage.setItem(dismissedRefreshStorageKey, fingerprint)
  } catch { /* 浏览器禁用存储时仅关闭当前页面提示 */ }
  refreshNotice.value = null
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
      <div class="heading-actions"><button data-sync-pool class="secondary" :disabled="busy !== ''" @click="refreshPool">{{ busy === 'refresh' ? '正在同步…' : '同步代理状态' }}</button><button data-refresh-country class="secondary" :disabled="busy !== '' || !supplementCountry" @click="refreshCountry">{{ busy === 'country-refresh' ? '正在刷新…' : '补充所选国家' }}</button><button v-if="protocol === 'vless'" data-copy-subscription :disabled="busy !== '' || !subscriptionReady" @click="copySubscription">复制节点订阅</button><button v-else data-rotate-socks-credentials class="secondary" :disabled="busy !== ''" @click="rotateSOCKS5HCredentials">{{ busy === 'rotate-socks-credentials' ? '正在更换…' : '随机更换用户名和密码' }}</button><button data-copy-all class="secondary" :disabled="busy !== ''" @click="copyAll">复制节点列表</button><button data-export class="secondary" :disabled="busy !== ''" @click="exportRows">导出</button></div>
    </section>
    <UiNotice v-if="topNotice" :key="topNotice.id" data-top-notice :notice="topNotice" @close="topNotice=null" />
    <section class="pool-toolbar">
      <PoolFilters :countries="countries" :official-countries="officialCountries" :country="country" :supplement-country="supplementCountry" :proxy-type="proxyType" :status="status" :sort="sort" @country="country=$event" @supplement-country="supplementCountry=$event" @proxy-type="proxyType=$event" @status="status=$event" @sort="sort=$event" />
      <div data-pool-stats class="pool-stats"><span data-pool-stats-official class="pool-stat official">官方 <strong>{{ poolStats?.officialCandidateTotal ?? candidateCountries.reduce((sum,item) => sum + item.candidateCount, 0) }}</strong></span><span data-pool-stats-valid class="pool-stat valid">当前有效 <strong>{{ poolStats?.validNodeCount ?? groups.length }}</strong></span><span data-pool-stats-countries class="pool-stat countries"><strong>{{ poolStats?.validCountryCount ?? countries.length }}</strong> 国</span></div>
    </section>
    <UiNotice v-if="refreshNotice" :key="refreshNotice.id" data-refresh-notice class="refresh-notice" :notice="refreshNotice" @close="dismissRefreshNotice" />
    <div v-if="loading" class="loading">正在读取代理池…</div>
    <PoolTable v-else :rows="rows" :protocol="protocol" :busy="busy" @copy="copyAddress" @replace="openReplacement" @check="checkRow" @protocol="switchProtocol" />
    <div v-if="replacementCandidate" class="dialog-backdrop" @click.self="closeReplacement">
      <section data-replace-dialog class="replace-dialog" role="dialog" aria-modal="true" aria-labelledby="replace-title">
        <button class="dialog-close" type="button" aria-label="关闭" @click="closeReplacement">×</button>
        <p class="eyebrow">REPLACE EGRESS SLOT</p><h2 id="replace-title">替换到出口位</h2>
        <p>将 {{ replacementCandidate.countryName || replacementCandidate.countryCode }} {{ replacementCandidate.proxyType === 'residential' ? '住宅' : '机房' }}候选装载到现有出口位。原端口和 VLESS/SOCKS5H 入站保持不变，失败时自动回滚。</p>
        <label>目标逻辑出口<select v-model="replacementTarget" data-replace-target :class="{ 'fault-target': replacementTargetWarning }"><option v-for="target in replacementTargets" :key="target.id" :value="target.id" :class="{ 'fault-target-option': automaticRepairFailed(target) }">{{ replacementTargetLabel(target) }}</option></select></label>
        <p v-if="replacementTargetWarning" data-replace-target-warning class="fault-target-warning">{{ replacementTargetName(replacementTargetWarning) }}{{ replacementTargetWarning.egressSource === 'main' ? '' : ' ' }}自动修复已失败；你仍可将当前候选替换到这里，系统会重新验证完整链路。</p>
        <UiNotice v-if="replacementNotice" :key="replacementNotice.id" data-replace-notice class="replacement-notice" :notice="replacementNotice" @close="replacementNotice=null" />
        <div class="dialog-actions"><button class="secondary" type="button" @click="closeReplacement">取消</button><button data-confirm-replace type="button" :disabled="!replacementTarget || !replacementCandidateID || busy !== ''" @click="confirmReplacement">{{ busy.startsWith('replace-') ? '正在替换…' : '确认替换' }}</button></div>
      </section>
    </div>
  </AppShell>
</template>

<style scoped>
.page-heading{display:flex;align-items:flex-end;justify-content:space-between;gap:24px;margin-bottom:20px}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:11px;font-weight:800;letter-spacing:.14em}.page-heading h1{margin:0;font-size:28px;letter-spacing:-.035em}.page-heading p:not(.eyebrow){margin:8px 0 0;color:var(--muted-text);font-size:14px}.heading-actions{display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px}[data-top-notice],.refresh-notice,.loading{margin:0 0 14px}.loading{padding:10px 13px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--muted-text);font-size:13px}.pool-toolbar{display:flex;align-items:center;gap:14px;margin-bottom:12px}.pool-stats{display:flex;align-items:center;gap:6px;margin-left:auto;flex:none;font-size:12px}.pool-stat{display:inline-flex;align-items:baseline;gap:3px;padding:5px 8px;border:1px solid var(--border);border-radius:999px;font-weight:700}.pool-stat strong{font-size:14px}.pool-stat.official{color:#788cff;background:rgba(94,112,255,.1)}.pool-stat.valid{color:#18ae70;background:rgba(24,174,112,.1)}.pool-stat.countries{color:#c27cfa;background:rgba(194,124,250,.1)}.fixed-toggle{display:flex;align-items:center;gap:6px;color:var(--muted-text);font-size:12px;white-space:nowrap}.dialog-backdrop{position:fixed;inset:0;z-index:20;display:grid;place-items:center;padding:20px;background:rgba(15,23,42,.52);backdrop-filter:blur(3px)}.replace-dialog{position:relative;width:min(460px,100%);padding:24px;border:1px solid var(--border);border-radius:14px;background:var(--panel);box-shadow:0 24px 70px rgba(15,23,42,.28)}.replace-dialog h2{margin:0 0 10px;font-size:22px}.replace-dialog>p:not(.eyebrow){color:var(--muted-text);font-size:13px;line-height:1.65}.replace-dialog label{display:grid;gap:7px;margin-top:18px;font-size:12px;font-weight:700}.replace-dialog select{height:40px;padding:0 10px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text)}.replace-dialog select.fault-target{border-color:var(--danger);box-shadow:0 0 0 2px rgba(239,68,68,.12)}.fault-target-option,.fault-target-warning{color:var(--danger)}.replace-dialog .fault-target-warning{margin:8px 0 0;font-weight:700}.replacement-notice{margin-top:14px}.dialog-close{position:absolute;top:12px;right:12px;width:32px;height:32px;padding:0;border:0;background:transparent;color:var(--muted-text);font-size:22px}.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:20px}@media(max-width:760px){.page-heading{align-items:flex-start;flex-direction:column}.heading-actions{width:100%;justify-content:flex-start}.heading-actions button{flex:1}.pool-toolbar{align-items:stretch;flex-direction:column}.pool-stats{align-self:flex-end;margin-left:0}}
</style>
