<script setup lang="ts">
import type { ProxyGroupStatus, ProxyType } from '../api/client'

defineProps<{ countries: { code: string; name: string }[]; country: string; proxyType: '' | ProxyType; status: '' | ProxyGroupStatus; sort: string }>()
const emit = defineEmits<{ country: [value: string]; proxyType: [value: '' | ProxyType]; status: [value: '' | ProxyGroupStatus]; sort: [value: string] }>()
</script>

<template>
  <div class="filters" aria-label="节点筛选">
    <select data-country-filter :value="country" aria-label="国家" @change="emit('country', ($event.target as HTMLSelectElement).value)">
      <option value="">全部国家</option>
      <option v-for="item in countries" :key="item.code" :value="item.code">{{ item.name || item.code }}</option>
    </select>
    <select :value="proxyType" aria-label="IP 类型" @change="emit('proxyType', ($event.target as HTMLSelectElement).value as '' | ProxyType)">
      <option value="">全部类型</option><option value="residential">住宅</option><option value="datacenter">机房</option>
    </select>
    <select :value="status" aria-label="状态" @change="emit('status', ($event.target as HTMLSelectElement).value as '' | ProxyGroupStatus)">
      <option value="">全部状态</option><option value="standby">待启用</option><option value="ready">可用</option><option value="provisioning">创建中</option><option value="degraded">异常</option><option value="repair_required">需要修复</option>
    </select>
    <select :value="sort" aria-label="排序" @change="emit('sort', ($event.target as HTMLSelectElement).value)">
      <option value="latency">延迟从低到高</option><option value="country">按国家排序</option><option value="updated">最近检测优先</option>
    </select>
  </div>
</template>

<style scoped>
.filters{display:flex;flex-wrap:wrap;gap:8px}.filters select{height:36px;min-width:132px;padding:0 34px 0 11px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text);font:inherit;font-size:13px}.filters select:focus{outline:3px solid var(--focus);border-color:var(--accent)}@media(max-width:600px){.filters{display:grid;grid-template-columns:1fr 1fr}.filters select{min-width:0;width:100%}}
</style>
