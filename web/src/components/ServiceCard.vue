<script setup lang="ts">
import { computed } from 'vue'

import type { ProbeResult } from '../api/client'

const props = defineProps<{
  service: ProbeResult
}>()

const names: Record<string, string> = {
  gateway: 'Aimili Gateway',
  'aimili-vpn': 'AimiliVPN',
  '3x-ui': '3x-ui',
}

const healthLabels = {
  healthy: '正常',
  degraded: '降级',
  unavailable: '不可用',
} as const

const displayName = computed(() => names[props.service.service] ?? props.service.service)
const healthLabel = computed(() => healthLabels[props.service.health])
</script>

<template>
  <article class="service-card" :class="`service-card--${service.health}`" :data-health="service.health">
    <div class="service-card__heading">
      <h2>{{ displayName }}</h2>
      <span class="status-pill">{{ healthLabel }}</span>
    </div>
    <p v-if="service.version" class="service-card__meta">版本 {{ service.version }}</p>
    <p v-if="service.errorCode" class="service-card__meta">检查结果：{{ service.errorCode }}</p>
    <p v-if="service.checkedAt" class="service-card__meta">最近检查：{{ new Date(service.checkedAt).toLocaleString() }}</p>
  </article>
</template>

<style scoped>
.service-card {
  min-height: 9rem;
  padding: 1.25rem;
  border: 1px solid var(--border);
  border-left-width: 0.35rem;
  border-radius: 1rem;
  background: var(--panel);
  box-shadow: var(--shadow);
}

.service-card--healthy { border-left-color: var(--healthy); }
.service-card--degraded { border-left-color: var(--degraded); }
.service-card--unavailable { border-left-color: var(--unavailable); }

.service-card__heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

h2 {
  margin: 0;
  font-size: 1.05rem;
}

.status-pill {
  padding: 0.25rem 0.6rem;
  border-radius: 999px;
  background: var(--muted-bg);
  color: var(--muted-text);
  font-size: 0.8rem;
  font-weight: 700;
}

.service-card__meta {
  margin: 0.75rem 0 0;
  color: var(--muted-text);
  font-size: 0.85rem;
}
</style>
