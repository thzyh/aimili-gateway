<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import {
  APIError, apiFetch, idempotencyHeaders,
  type ConnectionsPayload, type CountryPayload, type NavigationPayload,
  type OverviewPayload, type ProxyGroupPayload, type ProxyType,
} from '../api/client'
import ServiceCard from '../components/ServiceCard.vue'

const router = useRouter()
const overview = ref<OverviewPayload | null>(null)
const navigation = ref<NavigationPayload | null>(null)
const countries = ref<CountryPayload[]>([])
const groups = ref<ProxyGroupPayload[]>([])
const connections = ref<Record<string, ConnectionsPayload>>({})
const loading = ref(true)
const busy = ref('')
const notice = ref('')
const password = ref('')
const totp = ref('')
const cidrs = ref('')
const reauthenticated = ref(false)

const services = computed(() => overview.value ? [
  { service: 'gateway', health: overview.value.gateway.health, capabilities: [] },
  ...overview.value.services,
] : [])

const displayCountries = computed(() => {
  const result = new Map(countries.value.map((country) => [country.code, country]))
  for (const group of groups.value) {
    if (!result.has(group.countryCode)) {
      result.set(group.countryCode, { code: group.countryCode, name: group.countryName, residentialCount: 0, datacenterCount: 0 })
    }
  }
  return [...result.values()].sort((left, right) => left.code.localeCompare(right.code))
})

onMounted(refresh)

async function refresh(): Promise<void> {
  loading.value = true
  try {
    const [overviewPayload, navigationPayload, countryPayload, groupPayload] = await Promise.all([
      apiFetch<OverviewPayload>('/api/v1/overview'),
      apiFetch<NavigationPayload>('/api/v1/navigation'),
      apiFetch<CountryPayload[]>('/api/v1/countries'),
      apiFetch<ProxyGroupPayload[]>('/api/v1/proxy-groups'),
    ])
    overview.value = overviewPayload
    navigation.value = navigationPayload
    countries.value = countryPayload
    groups.value = groupPayload
    notice.value = ''
  } catch {
    notice.value = '暂时无法读取国家代理目录。'
  } finally {
    loading.value = false
  }
}

function groupFor(country: string, proxyType: ProxyType): ProxyGroupPayload | undefined {
  return groups.value.find((group) => group.countryCode === country && group.proxyType === proxyType)
}

async function enable(countryCode: string, proxyType: ProxyType): Promise<void> {
  await mutate(`enable-${countryCode}-${proxyType}`, '/api/v1/proxy-groups', {
    method: 'POST', headers: idempotencyHeaders(), body: JSON.stringify({ countryCode, proxyType }),
  })
}

async function check(group: ProxyGroupPayload): Promise<void> {
  await mutate(`check-${group.id}`, `/api/v1/proxy-groups/${group.id}/check`, { method: 'POST' })
}

async function rotate(group: ProxyGroupPayload): Promise<void> {
  await mutate(`rotate-${group.id}`, `/api/v1/proxy-groups/${group.id}/rotate`, { method: 'POST', headers: idempotencyHeaders() })
}

async function disable(group: ProxyGroupPayload): Promise<void> {
  await mutate(`disable-${group.id}`, `/api/v1/proxy-groups/${group.id}`, { method: 'DELETE' })
}

async function mutate(key: string, path: string, init: RequestInit): Promise<void> {
  busy.value = key
  notice.value = ''
  try {
    await apiFetch(path, init)
    await refresh()
  } catch (error) {
    notice.value = error instanceof APIError && error.message === 'reauthentication_required'
      ? '请先在下方完成安全确认，再执行此操作。'
      : `操作失败：${error instanceof Error ? error.message : 'request_failed'}`
  } finally {
    busy.value = ''
  }
}

async function reauthenticate(): Promise<void> {
  busy.value = 'reauth'
  try {
    await apiFetch('/api/v1/auth/reauth', { method: 'POST', body: JSON.stringify({ password: password.value, ...(totp.value ? { totp: totp.value } : {}) }) })
    password.value = ''
    totp.value = ''
    reauthenticated.value = true
    notice.value = '安全确认有效期为五分钟。'
  } catch {
    notice.value = '安全确认失败，请检查密码或 TOTP。'
  } finally {
    busy.value = ''
  }
}

async function saveCIDRs(): Promise<void> {
  const values = cidrs.value.split(/[\s,]+/).map((value) => value.trim()).filter(Boolean)
  await mutate('cidrs', '/api/v1/settings/mixed-cidrs', { method: 'PUT', body: JSON.stringify({ cidrs: values }) })
}

async function reveal(group: ProxyGroupPayload): Promise<void> {
  busy.value = `connections-${group.id}`
  try {
    connections.value[group.id] = await apiFetch<ConnectionsPayload>(`/api/v1/proxy-groups/${group.id}/connections`)
  } catch (error) {
    notice.value = error instanceof Error ? `无法显示连接信息：${error.message}` : '无法显示连接信息。'
  } finally {
    busy.value = ''
  }
}

async function copy(value: string): Promise<void> {
  await navigator.clipboard.writeText(value)
  notice.value = '已复制到剪贴板；连接信息不会保存在浏览器存储中。'
}

async function logout(): Promise<void> {
  await apiFetch('/api/v1/auth/logout', { method: 'POST' })
  await router.push('/login')
}

const typeLabel = (value: ProxyType) => value === 'residential' ? '住宅' : '机房'
const statusLabel = (value: string) => ({ ready: '可用', provisioning: '正在启用', rotating: '正在换 IP', degraded: '异常', repair_required: '需要修复', disabling: '正在禁用' }[value] ?? value)
</script>

<template>
  <main class="shell">
    <header class="topbar">
      <div><p class="eyebrow">Aimili Gateway</p><h1>国家代理</h1><p class="subtitle">一个国家出口，同时提供 VLESS Reality 与 SOCKS5H。</p></div>
      <div class="top-actions"><button class="secondary" @click="refresh">刷新目录</button><button data-logout class="secondary" @click="logout">退出</button></div>
    </header>

    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <p v-if="loading" class="notice">正在读取可用国家与代理状态…</p>

    <template v-else>
      <section v-if="services.length" class="status-strip" aria-label="服务状态">
        <ServiceCard v-for="service in services" :key="service.service" :service="service" />
      </section>

      <section class="catalog">
        <article v-for="country in displayCountries" :key="country.code" class="country-card" :data-country="country.code">
          <header><div><span class="country-code">{{ country.code }}</span><h2>{{ country.name || country.code }}</h2></div><span class="candidate-count">住宅 {{ country.residentialCount }} · 机房 {{ country.datacenterCount }}</span></header>
          <div class="type-grid">
            <section v-for="proxyType in (['residential', 'datacenter'] as ProxyType[])" :key="proxyType" class="proxy-type">
              <div class="type-heading"><h3>{{ typeLabel(proxyType) }}</h3><span>{{ proxyType === 'residential' ? country.residentialCount : country.datacenterCount }} 个可用出口</span></div>
              <template v-for="group in [groupFor(country.code, proxyType)]" :key="`${country.code}-${proxyType}`">
                <div v-if="group" class="group" :data-group="group.id">
                  <div class="group-primary"><span class="status" :data-status="group.status">{{ statusLabel(group.status) }}</span><strong>{{ group.exitIp || '尚未检测出口' }}</strong></div>
                  <p>VLESS :{{ group.vlessPort }} · mixed :{{ group.mixedPort }}</p>
                  <div class="button-row">
                    <button :data-check="group.id" class="secondary" :disabled="busy !== ''" @click="check(group)">检测</button>
                    <button :disabled="busy !== ''" @click="rotate(group)">换 IP</button>
                    <button class="secondary danger" :disabled="busy !== ''" @click="disable(group)">禁用</button>
                    <button v-if="group.status === 'ready'" class="secondary" :disabled="busy !== ''" @click="reveal(group)">显示连接</button>
                  </div>
                  <div v-if="connections[group.id]" class="connections">
                    <label>VLESS<input readonly :value="connections[group.id].vlessUri"><button @click="copy(connections[group.id].vlessUri)">复制</button></label>
                    <label>SOCKS5H<input readonly :value="connections[group.id].socks5hUri"><button @click="copy(connections[group.id].socks5hUri)">复制</button></label>
                  </div>
                </div>
                <button v-else :disabled="busy !== '' || (proxyType === 'residential' ? country.residentialCount : country.datacenterCount) === 0" @click="enable(country.code, proxyType)">启用该出口</button>
              </template>
            </section>
          </div>
        </article>
        <p v-if="displayCountries.length === 0" class="empty">目前没有经过探测且可用的国家出口。</p>
      </section>

      <section class="settings">
        <div><h2>安全确认与 mixed 白名单</h2><p>启用、换 IP、禁用、显示或复制连接信息前，需要一次 Gateway 密码确认。SOCKS5H 本身不加密，必须限制来源 CIDR。</p></div>
        <form @submit.prevent="reauthenticate"><input v-model="password" required type="password" autocomplete="current-password" placeholder="Gateway 密码"><input v-model="totp" inputmode="numeric" autocomplete="one-time-code" placeholder="TOTP（如已启用）"><button :disabled="busy !== ''">{{ reauthenticated ? '重新确认' : '安全确认' }}</button></form>
        <form @submit.prevent="saveCIDRs"><input v-model="cidrs" placeholder="例如：203.0.113.24/32"><button :disabled="busy !== ''">保存来源 CIDR</button></form>
      </section>

      <section class="expert-panel">
        <div><h2>高级设置</h2><p>AimiliVPN 与 3x-ui 继续独立运行。原版 3x-ui 会要求单独登录，这不是覆盖全部页面的单点登录。</p></div>
        <a v-if="overview?.expertModeAvailable && navigation?.expertModeUrl" data-expert-mode :href="navigation.expertModeUrl" target="_blank" rel="noopener noreferrer">打开 3x-ui 专家模式</a>
        <span v-else>当前未配置专家模式入口</span>
      </section>
    </template>
  </main>
</template>

<style scoped>
.shell{width:min(76rem,calc(100% - 2rem));margin:auto;padding:2rem 0 4rem}.topbar,.top-actions,.country-card>header,.type-heading,.group-primary,.button-row,.expert-panel{display:flex;align-items:center;justify-content:space-between;gap:1rem}.eyebrow{margin:0 0 .3rem;color:var(--accent);font-size:.78rem;font-weight:800;letter-spacing:.14em;text-transform:uppercase}h1,h2,h3,p{margin-top:0}.subtitle{margin:.4rem 0 0;color:var(--muted-text)}.top-actions{justify-content:flex-end}.notice,.empty{padding:1rem;border:1px solid var(--border);border-radius:.8rem;background:var(--panel)}.status-strip{display:grid;grid-template-columns:repeat(3,1fr);gap:.75rem;margin:1.2rem 0}.catalog{display:grid;gap:1rem}.country-card,.settings,.expert-panel{padding:1.25rem;border:1px solid var(--border);border-radius:1rem;background:var(--panel);box-shadow:var(--shadow)}.country-code{color:var(--accent);font-size:.75rem;font-weight:900;letter-spacing:.12em}.country-card h2{margin:.15rem 0}.candidate-count,.type-heading span,.group p,.settings p,.expert-panel p{color:var(--muted-text)}.type-grid{display:grid;grid-template-columns:1fr 1fr;gap:1rem;margin-top:1rem}.proxy-type{padding:1rem;border-radius:.8rem;background:var(--muted-bg)}.type-heading h3{margin:0}.type-heading span{font-size:.85rem}.group{margin-top:1rem}.group-primary strong{font-family:ui-monospace,monospace}.status{padding:.25rem .5rem;border-radius:999px;background:var(--panel);font-size:.8rem;font-weight:800}.status[data-status=ready]{color:var(--healthy)}.status[data-status=degraded],.status[data-status=repair_required]{color:var(--unavailable)}.button-row{justify-content:flex-start;flex-wrap:wrap}.danger{color:var(--unavailable)}.connections{display:grid;gap:.6rem;margin-top:1rem}.connections label{display:grid;grid-template-columns:5rem 1fr auto;align-items:center;gap:.5rem}.connections input,.settings input{min-width:0;padding:.72rem;border:1px solid var(--border);border-radius:.6rem;background:var(--input);color:inherit}.settings{display:grid;grid-template-columns:1.4fr 1fr 1fr;gap:1rem;margin-top:1rem}.settings form{display:grid;gap:.6rem}.expert-panel{margin-top:1rem}.expert-panel p{margin-bottom:0}.expert-panel a{flex:none;padding:.7rem 1rem;border-radius:.65rem;background:var(--accent);color:#fff;font-weight:750;text-decoration:none}@media(max-width:52rem){.topbar,.country-card>header,.expert-panel{align-items:flex-start;flex-direction:column}.status-strip,.type-grid,.settings{grid-template-columns:1fr}.connections label{grid-template-columns:1fr}.top-actions{width:100%;justify-content:flex-start}}
</style>
