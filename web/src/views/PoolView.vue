<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { apiDownloadText, apiFetch, idempotencyHeaders, type CandidateCountryPayload, type ConnectionsPayload, type CountryRefreshPayload, type DedicatedStandbyPayload, type ProtocolMode, type ProtocolModePayload, type ProxyGroupPayload, type ProxyType, type SubscriptionPayload } from '../api/client'
import AppShell from '../components/AppShell.vue'
import PoolFilters from '../components/PoolFilters.vue'
import PoolTable from '../components/PoolTable.vue'
import UiNotice from '../components/UiNotice.vue'
import { codeFromError, countryDisplayName, messageForCode, type NoticeKind, type UiNoticeData } from '../components/errorMessages'
import { poolStatusGroup, type PoolStatusGroup } from '../components/poolStatus'
import CountryAvailability from '../components/CountryAvailability.vue'

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
const checkProgress = ref('')
const loading = ref(true)
const topNotice = ref<UiNoticeData | null>(null)
const refreshNotice = ref<UiNoticeData | null>(null)
const replacementNotice = ref<UiNoticeData | null>(null)
const replacementCandidate = ref<ProxyGroupPayload | null>(null)
const replacementCandidateID = ref('')
const replacementTarget = ref('')
const replacementMenuOpen = ref(false)
const refreshNoticeFingerprint = ref('')
const dedicatedStandbys = ref<DedicatedStandbyPayload[]>([])
const standbyBusy = ref(false)
const standbyManualIndex = ref<number | null>(null)
const standbyManualCandidateID = ref('')
const standbyManualNotice = ref<UiNoticeData | null>(null)
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let standbyTimer: ReturnType<typeof setTimeout> | undefined
let noticeSequence = 0

const dismissedRefreshStorageKey = 'aimili-gateway:pool-refresh-notice-dismissed:v1'

const title = computed(() => props.protocol === 'vless' ? 'VPN 节点池' : 'SOCKS5H 代理池')
const description = computed(() => props.protocol === 'vless' ? '每个逻辑出口可独立使用 TCP/Vision、XHTTP/REALITY 或 Hysteria2；mixed/SOCKS5H 始终保持不变。' : '每个在线出口对应一个支持代理 DNS 的 SOCKS5H 地址。')
const countries = computed(() => {
  const officialNames = new Map(candidateCountries.value.map(item => [item.code.trim().toUpperCase(), countryDisplayName(item.code, [item])]))
  const merged = new Map<string, { code: string; name: string; count: number }>()
  for (const row of groups.value) {
    const code = row.countryCode.trim().toUpperCase()
    const current = merged.get(code)
    merged.set(code, { code, name: officialNames.get(code) || countryDisplayName(code, [{ code, name: row.countryName || code }]), count: (current?.count ?? 0) + 1 })
  }
  return [...merged.values()].sort((a, b) => a.code.localeCompare(b.code))
})
const officialCountries = computed(() => candidateCountries.value.map(item => ({ code: item.code, name: countryDisplayName(item.code, [item]), count: item.candidateCount })).sort((a, b) => a.code.localeCompare(b.code)))
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
const automaticRepairFailed = (row: ProxyGroupPayload) => ['no_same_country_candidate', 'replacement_failed', 'repair_interrupted', 'manual_repair_required', 'manual_replacement_required'].includes(row.lastErrorCode || '')
const selectedReplacementTarget = computed(() => replacementTargets.value.find(row => row.id === replacementTarget.value) ?? null)
const replacementTargetWarning = computed(() => selectedReplacementTarget.value && automaticRepairFailed(selectedReplacementTarget.value) ? selectedReplacementTarget.value : null)
const replacementTargetName = (row: ProxyGroupPayload) => row.egressSource === 'main' ? '主连接' : `出口位 ${row.slotNumber}`
const replacementTargetLabel = (row: ProxyGroupPayload) => `${automaticRepairFailed(row) ? '【故障·自动修复失败】' : ''}${replacementTargetName(row)} · ${countryDisplayName(row.countryCode, [{ code: row.countryCode, name: row.countryName || row.countryCode }])} · ${row.exitIp || '当前无可用出口 IP'}`
const subscriptionReady = computed(() => groups.value.some(row => row.status === 'ready' && row.protocolState === 'ready' && row.subscriptionState === 'ready'))
const standbyTargetCount = computed(() => dedicatedStandbys.value.filter(row => row.status !== 'disabled').length)
const standbyReadyCount = computed(() => dedicatedStandbys.value.filter(row => row.status === 'ready').length)
const standbyManualCount = computed(() => dedicatedStandbys.value.filter(row => row.status === 'waiting_manual').length)
const standbyManualRow = computed(() => standbyManualIndex.value === null ? null : dedicatedStandbys.value.find(row => row.index === standbyManualIndex.value) ?? null)
const standbyManualCandidates = computed(() => groups.value.filter(row => row.status === 'standby'))
const standbyTargetLabel = (row: DedicatedStandbyPayload) => row.target === 'main' ? '主连接' : /^slot:\d+$/.test(row.target) ? `出口位 ${Number(row.target.split(':')[1]) + 1}` : '未启用'
const standbyCandidateLabel = (row: ProxyGroupPayload) => `${countryDisplayName(row.countryCode, [{ code: row.countryCode, name: row.countryName || row.countryCode }])} · ${row.proxyType === 'residential' ? '住宅' : '机房'} · ${row.exitIp || row.candidateIp || '尚无出口 IP'}`
const standbySummaryState = (row: DedicatedStandbyPayload) => ({ disabled: '未启用', preparing: '正在准备', ready: '已就绪', degraded: '检查异常', waiting_manual: '等待人工处理', retry_wait: '等待重试' } as Record<string, string>)[row.status] || '尚无状态'
const standbySummaryMeta = (row: DedicatedStandbyPayload) => `${standbyTargetLabel(row)} · ${standbySummaryState(row)} · ${row.egress_ok ? '真实出口有效' : '真实出口未就绪'}${row.exit_ip || row.candidate_ip ? ` · ${row.exit_ip || row.candidate_ip}` : ''}`

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
  if (code.trim().toUpperCase() === 'ALL') return '所有国家'
  return countryDisplayName(code, candidateCountries.value)
}

onMounted(loadInitial)
onBeforeUnmount(() => {
  if (refreshTimer !== undefined) clearTimeout(refreshTimer)
  if (standbyTimer !== undefined) clearTimeout(standbyTimer)
})

async function loadInitial(): Promise<void> {
  await Promise.all([loadGroups(), loadCatalog(), readRefreshStatus(), loadDedicatedStandbys()])
  scheduleStandbyPoll()
}

async function loadDedicatedStandbys(preserveCurrent = false): Promise<void> {
  try {
    const result = await apiFetch<DedicatedStandbyPayload[]>('/api/v1/settings/aimilivpn/standbys')
    dedicatedStandbys.value = Array.isArray(result) ? result : []
  }
  catch { if (!preserveCurrent) dedicatedStandbys.value = [] }
}

function scheduleStandbyPoll(): void {
  if (standbyTimer !== undefined) clearTimeout(standbyTimer)
  standbyTimer = setTimeout(pollDedicatedStandbys, 15_000)
}

async function pollDedicatedStandbys(): Promise<void> {
  standbyTimer = undefined
  if (!standbyBusy.value && busy.value === '') await loadDedicatedStandbys(true)
  scheduleStandbyPoll()
}

async function assignDedicatedStandby(index: number, candidateId: string): Promise<boolean> {
  if (standbyBusy.value || busy.value) return false
  standbyBusy.value = true
  standbyManualNotice.value = makeNotice('progress', '正在验证备用候选', '正在拨号并检测真实出口，请等待验证结果。')
  try {
    await apiFetch(`/api/v1/settings/aimilivpn/standbys/${index}/assign`, { method: 'POST', body: JSON.stringify({ candidateId }) })
    topNotice.value = makeNotice('success', `备用 ${index + 1} 已重新验证`, '备用节点已通过真实出口检测。')
    return true
  } catch (error) { standbyManualNotice.value = makeNotice('error', '备用节点设置失败', localizedError(error, '该候选没有通过真实出口检测。')); return false }
  finally { await Promise.all([loadDedicatedStandbys(true), loadGroups(false)]); standbyBusy.value = false }
}

async function retryDedicatedStandby(index: number): Promise<void> {
  if (standbyBusy.value || busy.value) return
  standbyBusy.value = true
  try {
    await apiFetch(`/api/v1/settings/aimilivpn/standbys/${index}/retry`, { method: 'POST', body: '{}' })
    topNotice.value = makeNotice('success', '已重新开始恢复', '后台按恢复策略验证候选，页面将持续更新备用状态。')
    standbyManualNotice.value = topNotice.value
  } catch (error) { standbyManualNotice.value = makeNotice('error', '重试未开始', localizedError(error, '请稍后重试并查看恢复日志。')) }
  finally { await loadDedicatedStandbys(true); standbyBusy.value = false }
}

function openStandbyManual(index: number): void {
  if (standbyBusy.value || busy.value) return
  standbyManualIndex.value = index
  standbyManualCandidateID.value = ''
  standbyManualNotice.value = null
}

function closeStandbyManual(): void {
  if (standbyBusy.value) return
  standbyManualIndex.value = null
  standbyManualCandidateID.value = ''
  standbyManualNotice.value = null
}

async function confirmStandbyManual(): Promise<void> {
  if (standbyManualIndex.value === null || !standbyManualCandidateID.value) return
  const ok = await assignDedicatedStandby(standbyManualIndex.value, standbyManualCandidateID.value)
  if (ok) closeStandbyManual()
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
  replacementTarget.value = replacementTargets.value.find(target => target.egressSource !== 'main')?.id ?? ''
  replacementMenuOpen.value = false
  replacementNotice.value = null
}

async function refreshAllCountries(): Promise<void> {
  busy.value = 'all-country-refresh'
  refreshNoticeFingerprint.value = ''
  refreshNotice.value = makeNotice('progress', '所有国家刷新已开始', '系统会逐个检测候选，当前在线代理不会中断。')
  try {
    refreshState.value = await apiFetch<CountryRefreshPayload>('/api/v1/settings/aimilivpn/refresh', {
      method: 'POST', headers: idempotencyHeaders(), body: JSON.stringify({ country: 'ALL' }),
    })
    showRefreshNotice(refreshState.value)
    if (refreshState.value.state === 'running') scheduleRefreshPoll()
  } catch (error) {
    refreshNoticeFingerprint.value = ''
    refreshNotice.value = makeNotice('error', '所有国家刷新失败', localizedError(error, '全部国家节点刷新失败，请稍后重试。'))
  } finally { busy.value = '' }
}

function closeReplacement(): void {
  replacementCandidate.value = null
  replacementCandidateID.value = ''
  replacementTarget.value = ''
  replacementMenuOpen.value = false
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
    await Promise.all([loadGroups(false), loadDedicatedStandbys()])
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
    const code = codeFromError(error)
    topNotice.value = code === 'operation_busy' || code === 'maintenance_busy'
      ? makeNotice('info', '协议未更改', localizedProtocolError(error, current))
      : makeNotice('error', '协议切换失败，已请求恢复旧协议', localizedProtocolError(error, current))
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
      : makeNotice('success', `${subject} 本机检测成功`, '真实出口、SOCKS5H 和 VPS 本机代理链路正常；公网防火墙、客户端网络及客户端测速目标需另外验证。本次没有更换节点。')
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

async function checkAllRows(): Promise<void> {
  const targets = groups.value
    .filter(row => row.egressSource === 'main' || (typeof row.slotNumber === 'number' && row.slotNumber > 0))
    .sort((a, b) => (runtimeRank(a) ?? 100) - (runtimeRank(b) ?? 100))
  if (!targets.length) {
    topNotice.value = makeNotice('info', '没有可检测的出口', '当前没有已配置的固定出口。')
    return
  }
  busy.value = 'check-all'
  checkProgress.value = `1/${targets.length}`
  topNotice.value = makeNotice('progress', `正在检测出口 1/${targets.length}`, '系统会逐个检测，单个出口失败不会影响后续出口。')
  let passed = 0
  const failures: string[] = []
  for (let index = 0; index < targets.length; index += 1) {
    const row = targets[index]
    const subject = row.egressSource === 'main' ? '主连接' : `出口 ${row.slotNumber}`
    checkProgress.value = `${index + 1}/${targets.length}`
    topNotice.value = makeNotice('progress', `正在检测出口 ${index + 1}/${targets.length}`, `${subject} · 单个出口失败不会影响后续检测。`)
    const path = row.egressSource === 'main' ? '/api/v1/proxy-groups/agw-main/check' : `/api/v1/proxy-groups/${row.id}/check`
    try {
      await apiFetch<ProxyGroupPayload>(path, { method: 'POST', ...(row.egressSource === 'main' ? { headers: idempotencyHeaders() } : {}) })
      passed += 1
    } catch { failures.push(subject) }
  }
  await loadGroups(false)
  const failed = failures.length
  topNotice.value = makeNotice(failed ? 'error' : 'success', '全部出口检测完成', `正常 ${passed} · 故障 ${failed}${failed ? ` · ${failures.join('、')}` : ''}`)
  checkProgress.value = ''
  busy.value = ''
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
    const total = current.countryCandidateCount ?? 0
    const scope = current.country === 'ALL' ? '正在检查全部去重候选' : '正在检查该国全部官方候选'
    return makeNotice('progress', `${name}正在刷新`, `${scope} · 已检测 ${current.testedCount}${total ? `/${total}` : ''} · 通过 ${current.passedCount ?? 0} · 失败 ${current.failedCount ?? 0}，当前在线代理不会中断。`)
  }
  const official = current.officialCount ?? current.catalogCount ?? 0
  const usable = current.usableCount ?? current.validCount
  const time = formatRefreshTime(current.finishedAt)
  const poolTotal = current.cacheTotal ?? usable
  const counts = current.country === 'ALL'
    ? `官方候选 ${official} · 本次检测 ${current.testedCount} · 最终保留 ${poolTotal} · 节点池共 ${poolTotal}${time ? ` · ${time}` : ''}`
    : `官方原始 ${official} · 去重候选 ${current.countryCandidateCount ?? 0} · 本次检测 ${current.testedCount} · 检测通过 ${current.passedCount ?? 0} · 复验通过 ${current.revalidatedCount ?? 0} · 新增节点 ${current.newUsableCount ?? 0} · 检测失败 ${current.failedCount ?? 0} · 该国现有 ${current.countryValidCount ?? usable} · 节点池共 ${poolTotal}${time ? ` · ${time}` : ''}`
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
       <div class="heading-actions"><button data-refresh-all class="secondary" :disabled="busy !== ''" @click="refreshAllCountries">{{ busy === 'all-country-refresh' ? '正在刷新…' : '刷新所有国家' }}</button><button data-check-all class="secondary" :disabled="busy !== ''" @click="checkAllRows">{{ busy === 'check-all' ? `检测中 ${checkProgress}` : '检测全部出口' }}</button><button data-refresh-country class="secondary" :disabled="busy !== '' || !supplementCountry" :title="supplementCountry ? '检测该国全部官方候选；最终受全局保护上限限制' : '请先在“补充国家”中选择国家'" @click="refreshCountry">{{ busy === 'country-refresh' ? '正在检测…' : supplementCountry ? '优先检测该国家' : '请先选择补充国家' }}</button><button v-if="protocol === 'vless'" data-copy-subscription :disabled="busy !== '' || !subscriptionReady" @click="copySubscription">复制节点订阅</button><button v-else data-rotate-socks-credentials class="secondary" :disabled="busy !== ''" @click="rotateSOCKS5HCredentials">{{ busy === 'rotate-socks-credentials' ? '正在更换…' : '随机更换用户名和密码' }}</button><button data-copy-all class="secondary" :disabled="busy !== ''" @click="copyAll">复制节点列表</button><button data-export class="secondary" :disabled="busy !== ''" @click="exportRows">导出</button></div>
    </section>
    <UiNotice v-if="topNotice" :key="topNotice.id" data-top-notice :notice="topNotice" @close="topNotice=null" />
    <section class="pool-toolbar">
      <PoolFilters :countries="countries" :official-countries="officialCountries" :country="country" :supplement-country="supplementCountry" :proxy-type="proxyType" :status="status" :sort="sort" @country="country=$event" @supplement-country="supplementCountry=$event" @proxy-type="proxyType=$event" @status="status=$event" @sort="sort=$event" />
      <div data-pool-stats class="pool-stats"><span data-pool-stats-official class="pool-stat official">官方 <strong>{{ poolStats?.officialCandidateTotal ?? candidateCountries.reduce((sum,item) => sum + item.candidateCount, 0) }}</strong></span><span data-pool-stats-target class="pool-stat target">常规目标 <strong>{{ poolStats?.targetValidNodeCount ?? '—' }}</strong></span><span data-pool-stats-valid class="pool-stat valid">当前有效 <strong>{{ poolStats?.validNodeCount ?? '—' }}</strong></span><span data-pool-stats-maximum class="pool-stat maximum">紧急保护 <strong>{{ poolStats?.maxValidNodeCount ?? '—' }}</strong></span><span data-pool-stats-countries class="pool-stat countries"><strong>{{ poolStats?.validCountryCount ?? countries.length }}</strong> 国</span></div>
    </section>
    <UiNotice v-if="refreshNotice" :key="refreshNotice.id" data-refresh-notice class="refresh-notice" :notice="refreshNotice" @close="dismissRefreshNotice" />
    <CountryAvailability :rows="candidateCountries" />
    <section v-if="dedicatedStandbys.length > 0" data-dedicated-standbys class="standby-summary-bar">
      <div class="standby-summary-heading">
        <div><p class="eyebrow">DEDICATED HOT STANDBY</p><strong>专属热备用</strong><span>{{ standbyReadyCount }}/{{ standbyTargetCount }} 已就绪</span></div>
        <span v-if="standbyManualCount" class="standby-summary-warning">{{ standbyManualCount }} 个等待人工处理</span>
      </div>
      <div class="standby-summary-items">
        <span v-for="row in dedicatedStandbys" :key="row.index" :data-standby-summary="row.index" :class="['standby-summary-item', { ready: row.status === 'ready', fault: row.status === 'waiting_manual' }]">{{ standbySummaryMeta(row) }}</span>
      </div>
    </section>
    <div v-if="loading" class="loading">正在读取代理池…</div>
    <PoolTable v-else :rows="rows" :protocol="protocol" :busy="standbyBusy ? 'standby' : busy" :standbys="dedicatedStandbys" @copy="copyAddress" @replace="openReplacement" @check="checkRow" @protocol="switchProtocol" @standby-manual="openStandbyManual" />
    <div v-if="replacementCandidate" class="dialog-backdrop" @click.self="closeReplacement">
      <section data-replace-dialog class="replace-dialog" role="dialog" aria-modal="true" aria-labelledby="replace-title">
        <button class="dialog-close" type="button" aria-label="关闭" @click="closeReplacement">×</button>
        <p class="eyebrow">REPLACE EGRESS SLOT</p><h2 id="replace-title">替换到出口位</h2>
        <p>将 {{ countryDisplayName(replacementCandidate.countryCode, [{ code: replacementCandidate.countryCode, name: replacementCandidate.countryName || replacementCandidate.countryCode }]) }} {{ replacementCandidate.proxyType === 'residential' ? '住宅' : '机房' }}候选装载到现有出口位。默认只选择出口位；若要更换主连接，请在列表中主动选择。原端口和 VLESS/SOCKS5H 入站保持不变，失败时自动回滚。</p>
        <div class="target-field">
          <span id="replace-target-label">目标逻辑出口</span>
          <button data-replace-target class="target-trigger" :class="{ 'fault-target': replacementTargetWarning }" type="button" :aria-expanded="replacementMenuOpen" aria-controls="replace-target-options" aria-labelledby="replace-target-label" @click="replacementMenuOpen = !replacementMenuOpen">{{ selectedReplacementTarget ? replacementTargetLabel(selectedReplacementTarget) : '请选择出口位' }}<span aria-hidden="true">⌄</span></button>
          <div v-if="replacementMenuOpen" id="replace-target-options" data-replace-options class="target-options" role="group" aria-label="可替换的逻辑出口" @keydown.esc="replacementMenuOpen = false">
            <button v-for="target in replacementTargets" :key="target.id" data-replace-option :data-target-id="target.id" class="target-option" :class="{ selected: replacementTarget === target.id, 'fault-target-option': automaticRepairFailed(target) }" type="button" @click="replacementTarget = target.id; replacementMenuOpen = false">{{ replacementTargetLabel(target) }}</button>
          </div>
        </div>
        <p v-if="replacementTargetWarning" data-replace-target-warning class="fault-target-warning">{{ replacementTargetName(replacementTargetWarning) }}{{ replacementTargetWarning.egressSource === 'main' ? '' : ' ' }}自动修复已失败；你仍可将当前候选替换到这里，系统会重新验证完整链路。</p>
        <UiNotice v-if="replacementNotice" :key="replacementNotice.id" data-replace-notice class="replacement-notice" :notice="replacementNotice" @close="replacementNotice=null" />
        <div class="dialog-actions"><button class="secondary" type="button" @click="closeReplacement">取消</button><button data-confirm-replace type="button" :disabled="!replacementTarget || !replacementCandidateID || busy !== ''" @click="confirmReplacement">{{ busy.startsWith('replace-') ? '正在替换…' : '确认替换' }}</button></div>
      </section>
    </div>
    <div v-if="standbyManualIndex !== null && standbyManualRow" class="dialog-backdrop" @click.self="closeStandbyManual">
      <section data-standby-manual-dialog class="replace-dialog standby-manual-dialog" role="dialog" aria-modal="true" aria-labelledby="standby-manual-title">
        <button class="dialog-close" type="button" aria-label="关闭" :disabled="standbyBusy" @click="closeStandbyManual">×</button>
        <p class="eyebrow">MANUAL HOT STANDBY</p><h2 id="standby-manual-title">手动替换专属热备用</h2>
        <p>当前目标已锁定为 <strong>{{ standbyTargetLabel(standbyManualRow) }}</strong>。候选必须先通过真实出口验证，验证成功后才会写入备用绑定。</p>
        <p>本操作会重新拨号准备该出口的备用，期间该目标可能暂时没有热备保护；不会主动切换当前活动出口。</p>
        <dl class="standby-details">
          <div><dt>当前状态</dt><dd>{{ standbySummaryState(standbyManualRow) }}</dd></div>
          <div><dt>恢复轮次</dt><dd>{{ standbyManualRow.attempt_count ?? 0 }}</dd></div>
          <div><dt>最近检测</dt><dd>{{ formatRefreshTime(standbyManualRow.checked_at) || '尚无记录' }}</dd></div>
          <div v-if="standbyManualRow.next_attempt_at"><dt>下次重试</dt><dd>{{ formatRefreshTime(standbyManualRow.next_attempt_at) }}</dd></div>
        </dl>
        <p v-if="standbyManualRow.last_error_code" class="fault-target-warning">{{ standbyManualRow.last_error_code === 'recovery_budget_exhausted' ? '自动恢复预算已耗尽，请指定候选或重新开始自动恢复。' : '备用恢复未完成，可选择候选重试；具体原因见恢复日志。' }}</p>
        <label class="standby-candidate-field">备用候选
          <select v-model="standbyManualCandidateID" :disabled="standbyBusy || busy !== ''">
            <option value="">请选择可用候选</option>
            <option v-for="candidate in standbyManualCandidates" :key="candidate.id" :value="candidate.id">{{ standbyCandidateLabel(candidate) }}</option>
          </select>
        </label>
        <p v-if="standbyManualCandidates.length === 0">暂无可用候选，请先刷新节点池或重新开始自动恢复。</p>
        <UiNotice v-if="standbyManualNotice" :key="standbyManualNotice.id" data-standby-manual-notice :notice="standbyManualNotice" @close="standbyManualNotice=null" />
        <div class="dialog-actions standby-manual-actions">
          <button data-retry-standby class="secondary" type="button" :disabled="standbyBusy || busy !== ''" @click="retryDedicatedStandby(standbyManualIndex)">重新开始自动恢复</button>
          <button data-standby-manual-confirm type="button" :disabled="standbyBusy || busy !== '' || !standbyManualCandidateID" @click="confirmStandbyManual">{{ standbyBusy ? '正在验证…' : '验证并设为备用' }}</button>
        </div>
      </section>
    </div>
  </AppShell>
</template>

<style scoped>
.page-heading{display:flex;align-items:flex-end;justify-content:space-between;gap:24px;margin-bottom:20px}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:11px;font-weight:800;letter-spacing:.14em}.page-heading h1{margin:0;font-size:28px;letter-spacing:-.035em}.page-heading p:not(.eyebrow){margin:8px 0 0;color:var(--muted-text);font-size:14px}.heading-actions{display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px}[data-top-notice],.refresh-notice,.loading{margin:0 0 14px}.loading{padding:10px 13px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--muted-text);font-size:13px}.pool-toolbar{display:flex;align-items:center;gap:14px;margin-bottom:12px}.pool-stats{display:flex;align-items:center;gap:6px;margin-left:auto;flex:none;font-size:12px}.pool-stat{display:inline-flex;align-items:baseline;gap:3px;padding:5px 8px;border:1px solid var(--border);border-radius:999px;font-weight:700}.pool-stat strong{font-size:14px}.pool-stat.official{color:#788cff;background:rgba(94,112,255,.1)}.pool-stat.target{color:#287ca6;background:rgba(40,124,166,.1)}.pool-stat.valid{color:#18ae70;background:rgba(24,174,112,.1)}.pool-stat.maximum{color:#d58b20;background:rgba(213,139,32,.1)}.pool-stat.countries{color:#c27cfa;background:rgba(194,124,250,.1)}.fixed-toggle{display:flex;align-items:center;gap:6px;color:var(--muted-text);font-size:12px;white-space:nowrap}.standby-summary-bar{display:grid;gap:9px;margin:0 0 14px;padding:10px 13px;border:1px solid var(--border);border-radius:10px;background:linear-gradient(110deg,var(--panel),var(--subtle));box-shadow:var(--shadow-soft)}.standby-summary-heading{display:flex;align-items:center;justify-content:space-between;gap:12px}.standby-summary-heading>div{display:flex;align-items:baseline;gap:8px;min-width:0}.standby-summary-heading .eyebrow{margin:0;font-size:9px}.standby-summary-heading strong{font-size:13px}.standby-summary-heading span:not(.standby-summary-warning){color:var(--muted-text);font-size:12px}.standby-summary-warning{padding:3px 7px;border-radius:999px;background:rgba(239,68,68,.1);color:var(--danger);font-size:11px;font-weight:700;white-space:nowrap}.standby-summary-items{display:flex;flex-wrap:wrap;gap:6px}.standby-summary-item{max-width:100%;padding:4px 8px;border:1px solid var(--border);border-radius:999px;color:var(--muted-text);font-size:11px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.standby-summary-item.ready{border-color:color-mix(in srgb,var(--healthy) 40%,var(--border));color:var(--healthy);background:rgba(24,174,112,.08)}.standby-summary-item.fault{border-color:color-mix(in srgb,var(--danger) 45%,var(--border));color:var(--danger);background:rgba(239,68,68,.08)}.dialog-backdrop{position:fixed;inset:0;z-index:20;display:grid;place-items:center;padding:20px;background:rgba(15,23,42,.52);backdrop-filter:blur(3px)}.replace-dialog{position:relative;width:min(460px,100%);padding:24px;border:1px solid var(--border);border-radius:14px;background:var(--panel);box-shadow:0 24px 70px rgba(15,23,42,.28)}.replace-dialog h2{margin:0 0 10px;font-size:22px}.replace-dialog>p:not(.eyebrow){color:var(--muted-text);font-size:13px;line-height:1.65}.replace-dialog label{display:grid;gap:7px;margin-top:18px;font-size:12px;font-weight:700}.replace-dialog select{height:40px;padding:0 10px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text)}.replace-dialog select.fault-target{border-color:var(--danger);box-shadow:0 0 0 2px rgba(239,68,68,.12)}.fault-target-option,.fault-target-warning{color:var(--danger)}.replace-dialog .fault-target-warning{margin:8px 0 0;font-weight:700}.replacement-notice{margin-top:14px}.dialog-close{position:absolute;top:12px;right:12px;width:32px;height:32px;padding:0;border:0;background:transparent;color:var(--muted-text);font-size:22px}.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:20px}.standby-candidate-field{margin-top:18px}.standby-manual-actions{flex-wrap:wrap}.standby-manual-actions button:first-child{margin-right:auto}@media(max-width:1100px){.pool-stats{flex-wrap:wrap;justify-content:flex-end}}@media(max-width:760px){.page-heading{align-items:flex-start;flex-direction:column}.heading-actions{width:100%;justify-content:flex-start}.heading-actions button{flex:1}.pool-toolbar{align-items:stretch;flex-direction:column}.pool-stats{align-self:flex-end;margin-left:0}.standby-summary-heading{align-items:flex-start;flex-direction:column}.standby-summary-heading>div{flex-wrap:wrap}}
</style>

<style scoped>
.dialog-backdrop{overflow-y:auto}
.replace-dialog{max-height:calc(100dvh - 32px);overflow-y:auto}
.target-field{display:grid;gap:7px;margin-top:18px;font-size:12px;font-weight:700;min-width:0}
.target-trigger{display:flex;align-items:center;justify-content:space-between;gap:12px;width:100%;min-height:40px;padding:9px 10px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text);font:inherit;text-align:left;overflow-wrap:anywhere}
.target-trigger.fault-target{border-color:var(--danger);box-shadow:0 0 0 2px rgba(239,68,68,.12)}
.target-trigger span{flex:none;font-size:18px}
.target-options{display:grid;max-height:min(220px,35dvh);overflow-y:auto;border:1px solid var(--border);border-radius:8px;background:var(--input)}
.target-option{width:100%;min-height:40px;padding:8px 10px;border:0;border-bottom:1px solid var(--border-soft);background:transparent;color:var(--text);font:inherit;text-align:left;white-space:normal;overflow-wrap:anywhere;cursor:pointer}
.target-option:last-child{border-bottom:0}
.target-option:hover,.target-option:focus-visible,.target-option.selected{background:var(--hover)}
.target-option.fault-target-option{color:var(--danger)}
</style>
