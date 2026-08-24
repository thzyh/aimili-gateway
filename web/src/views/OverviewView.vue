<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { apiFetch, type NavigationPayload, type OverviewPayload, type ProbeResult } from '../api/client'
import ServiceCard from '../components/ServiceCard.vue'

const router = useRouter()
const overview = ref<OverviewPayload | null>(null)
const navigation = ref<NavigationPayload | null>(null)
const loading = ref(true)
const errorMessage = ref('')
const loggingOut = ref(false)

const gateway = computed<ProbeResult | null>(() => overview.value ? {
  service: 'gateway',
  health: overview.value.gateway.health,
  capabilities: [],
} : null)

onMounted(async () => {
  try {
    const [overviewPayload, navigationPayload] = await Promise.all([
      apiFetch<OverviewPayload>('/api/v1/overview'),
      apiFetch<NavigationPayload>('/api/v1/navigation'),
    ])
    overview.value = overviewPayload
    navigation.value = navigationPayload
  } catch {
    errorMessage.value = '暂时无法读取服务状态。'
  } finally {
    loading.value = false
  }
})

async function logout(): Promise<void> {
  loggingOut.value = true
  try {
    await apiFetch('/api/v1/auth/logout', { method: 'POST' })
    await router.push('/login')
  } finally {
    loggingOut.value = false
  }
}
</script>

<template>
  <main class="overview-shell">
    <header class="topbar">
      <div>
        <p class="eyebrow">Aimili Gateway</p>
        <h1>服务总览</h1>
      </div>
      <button data-logout class="secondary" :disabled="loggingOut" @click="logout">
        {{ loggingOut ? '正在退出…' : '退出' }}
      </button>
    </header>

    <p v-if="loading" class="notice">正在读取本地服务状态…</p>
    <p v-else-if="errorMessage" class="notice notice--error" role="alert">{{ errorMessage }}</p>
    <template v-else-if="overview && gateway">
      <section class="status-grid" aria-label="服务状态">
        <ServiceCard :service="gateway" />
        <ServiceCard v-for="service in overview.services" :key="service.service" :service="service" />
      </section>

      <section class="expert-panel">
        <div>
          <h2>专家模式</h2>
          <p>打开原版 3x-ui 后台处理高级设置。3x-ui 会要求单独登录，这不是统一控制台单点登录。</p>
        </div>
        <a
          v-if="overview.expertModeAvailable && navigation?.expertModeUrl"
          data-expert-mode
          :href="navigation.expertModeUrl"
          target="_blank"
          rel="noopener noreferrer"
        >打开专家模式</a>
        <span v-else class="unavailable-note">当前未配置专家模式入口</span>
      </section>
    </template>
  </main>
</template>

<style scoped>
.overview-shell { width: min(72rem, calc(100% - 2rem)); margin: 0 auto; padding: 2rem 0 4rem; }
.topbar { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: 2rem; }
.eyebrow { margin: 0 0 0.35rem; color: var(--accent); font-size: 0.8rem; font-weight: 800; letter-spacing: 0.12em; text-transform: uppercase; }
h1, h2 { margin: 0; }
.status-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 1rem; }
.expert-panel { display: flex; align-items: center; justify-content: space-between; gap: 2rem; margin-top: 1rem; padding: 1.25rem; border: 1px solid var(--border); border-radius: 1rem; background: var(--panel); }
.expert-panel p { margin: 0.5rem 0 0; color: var(--muted-text); line-height: 1.55; }
.expert-panel a { flex: 0 0 auto; padding: 0.7rem 1rem; border-radius: 0.65rem; background: var(--accent); color: white; font-weight: 700; text-decoration: none; }
.notice { padding: 1rem; border-radius: 0.75rem; background: var(--panel); }
.notice--error { color: var(--unavailable); }
.unavailable-note { color: var(--muted-text); }
@media (max-width: 48rem) {
  .status-grid { grid-template-columns: 1fr; }
  .expert-panel { align-items: flex-start; flex-direction: column; }
}
</style>
