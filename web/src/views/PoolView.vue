<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { APIError, apiDownloadText, apiFetch, idempotencyHeaders, type ConnectionsPayload, type ProxyGroupPayload, type ProxyType } from '../api/client'
import AppShell from '../components/AppShell.vue'
import PoolFilters from '../components/PoolFilters.vue'
import PoolTable from '../components/PoolTable.vue'
import { poolStatusGroup, type PoolStatusGroup } from '../components/poolStatus'

const props = defineProps<{ protocol: 'vless' | 'socks5h' }>()
const groups = ref<ProxyGroupPayload[]>([])
const country = ref('')
const proxyType = ref<'' | ProxyType>('')
const status = ref<'' | PoolStatusGroup>('')
const sort = ref('latency')
const busy = ref('')
const loading = ref(true)
const notice = ref('')

const title = computed(() => props.protocol === 'vless' ? 'VPN 节点池' : 'SOCKS5H 代理池')
const description = computed(() => props.protocol === 'vless' ? '复制或导出可直接用于代理客户端和代码的 VLESS Reality 节点。' : '每个在线出口对应一个支持代理 DNS 的 SOCKS5H 地址。')
const countries = computed(() => [...new Map(groups.value.map(row => [row.countryCode, { code: row.countryCode, name: row.countryName }])).values()].sort((a,b)=>a.code.localeCompare(b.code)))
const rows = computed(() => groups.value.filter(row => (!country.value || row.countryCode === country.value) && (!proxyType.value || row.proxyType === proxyType.value) && (!status.value || poolStatusGroup(row.status) === status.value)).sort((a,b) => {
  if (sort.value === 'country') return a.countryCode.localeCompare(b.countryCode)
  if (sort.value === 'updated') return (b.lastCheckedAt ?? '').localeCompare(a.lastCheckedAt ?? '')
  const left = props.protocol === 'vless' ? a.vlessLatencyMs : a.socksLatencyMs
  const right = props.protocol === 'vless' ? b.vlessLatencyMs : b.socksLatencyMs
  return (left || Number.MAX_SAFE_INTEGER) - (right || Number.MAX_SAFE_INTEGER)
}))

onMounted(load)

async function load(): Promise<void> {
  loading.value = true
  try { groups.value = await apiFetch<ProxyGroupPayload[]>('/api/v1/proxy-groups') }
  catch { notice.value = '暂时无法读取代理池。' }
  finally { loading.value = false }
}

async function refreshPool(): Promise<void> {
  busy.value = 'refresh'; notice.value = ''
  try {
    await apiFetch('/api/v1/proxy-groups/reconcile', { method: 'POST' })
    notice.value = '节点池刷新已在后台开始；在线节点不会被整批中断。'
    await load()
  } catch (error) { notice.value = messageFor(error, '刷新失败') }
  finally { busy.value = '' }
}

async function copyAddress(row: ProxyGroupPayload): Promise<void> {
  busy.value = `copy-${row.id}`; notice.value = ''
  try {
    const value = await apiFetch<ConnectionsPayload>(`/api/v1/proxy-groups/${row.id}/connections`)
    await navigator.clipboard.writeText(props.protocol === 'vless' ? value.vlessUri : value.socks5hUri)
    notice.value = '地址已复制；连接秘密不会保存在浏览器存储中。'
  } catch (error) { notice.value = messageFor(error, '复制失败') }
  finally { busy.value = '' }
}

async function exportRows(): Promise<void> {
  busy.value = 'export'; notice.value = ''
  const query = new URLSearchParams({ protocol: props.protocol })
  if (country.value) query.set('country', country.value)
  if (proxyType.value) query.set('proxyType', proxyType.value)
  if (status.value === 'standby' || status.value === 'ready') query.set('status', status.value)
  try {
    const text = await apiDownloadText(`/api/v1/proxy-groups/export?${query}`)
    if (typeof URL.createObjectURL === 'function') {
      const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
      const link = document.createElement('a'); link.href = url; link.download = `aimili-${props.protocol}.txt`; link.click(); URL.revokeObjectURL(url)
    }
    notice.value = `已导出 ${rows.value.filter(row => row.status === 'ready').length} 条可用地址。`
  } catch (error) { notice.value = messageFor(error, '导出失败') }
  finally { busy.value = '' }
}

async function mutate(row: ProxyGroupPayload, action: 'activate' | 'check' | 'rotate'): Promise<void> {
  busy.value = `${action}-${row.id}`; notice.value = ''
  try {
    await apiFetch(`/api/v1/proxy-groups/${row.id}/${action}`, { method: 'POST', ...(['activate', 'rotate'].includes(action) ? { headers: idempotencyHeaders() } : {}) })
    await load()
  } catch (error) { notice.value = messageFor(error, action === 'activate' ? '启用失败' : action === 'check' ? '检测失败' : '换 IP 失败') }
  finally { busy.value = '' }
}

function messageFor(error: unknown, fallback: string): string {
  return `${fallback}：${error instanceof Error ? error.message : 'request_failed'}`
}
</script>

<template>
  <AppShell>
    <section class="page-heading">
      <div><p class="eyebrow">ONLINE EGRESS POOL</p><h1>{{ title }}</h1><p>{{ description }}</p></div>
      <div class="heading-actions"><button class="secondary" :disabled="busy !== ''" @click="refreshPool">{{ busy === 'refresh' ? '正在刷新…' : '刷新节点池' }}</button><button data-export :disabled="busy !== ''" @click="exportRows">导出当前结果</button></div>
    </section>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <section class="pool-toolbar">
      <PoolFilters :countries="countries" :country="country" :proxy-type="proxyType" :status="status" :sort="sort" @country="country=$event" @proxy-type="proxyType=$event" @status="status=$event" @sort="sort=$event" />
      <span>{{ rows.length }} 个候选 · {{ rows.filter(row => row.status === 'ready').length }} 个在线</span>
    </section>
    <div v-if="loading" class="loading">正在读取代理池…</div>
    <PoolTable v-else :rows="rows" :protocol="protocol" :busy="busy" @copy="copyAddress" @activate="mutate($event,'activate')" @check="mutate($event,'check')" @rotate="mutate($event,'rotate')" />
  </AppShell>
</template>

<style scoped>
.page-heading{display:flex;align-items:flex-end;justify-content:space-between;gap:24px;margin-bottom:20px}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:11px;font-weight:800;letter-spacing:.14em}.page-heading h1{margin:0;font-size:28px;letter-spacing:-.035em}.page-heading p:not(.eyebrow){margin:8px 0 0;color:var(--muted-text);font-size:14px}.heading-actions{display:flex;gap:8px}.notice,.loading{margin:0 0 14px;padding:10px 13px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--muted-text);font-size:13px}.pool-toolbar{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:12px}.pool-toolbar>span{flex:none;color:var(--muted-text);font-size:12px}@media(max-width:760px){.page-heading{align-items:flex-start;flex-direction:column}.heading-actions{width:100%}.heading-actions button{flex:1}.pool-toolbar{align-items:stretch;flex-direction:column}.pool-toolbar>span{align-self:flex-end}}
</style>
