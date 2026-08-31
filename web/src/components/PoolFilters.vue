<script setup lang="ts">
import type { ProxyType } from '../api/client'
import type { PoolStatusGroup } from './poolStatus'

defineProps<{ countries: { code: string; name: string }[]; officialCountries: { code: string; name: string }[]; country: string; supplementCountry: string; proxyType: '' | ProxyType; status: '' | PoolStatusGroup; sort: string }>()
const emit = defineEmits<{ country: [value: string]; supplementCountry: [value: string]; proxyType: [value: '' | ProxyType]; status: [value: '' | PoolStatusGroup]; sort: [value: string] }>()
</script>

<template>
  <div class="filters" aria-label="节点筛选">
    <select data-country-filter :value="country" aria-label="只看现有国家" @change="emit('country', ($event.target as HTMLSelectElement).value)">
      <option value="">只看现有国家：全部</option>
      <option v-for="item in countries" :key="item.code" :value="item.code">{{ item.name || item.code }}</option>
    </select>
    <select data-country-supplement :value="supplementCountry" aria-label="手动补充国家" @change="emit('supplementCountry', ($event.target as HTMLSelectElement).value)">
      <option value="">手动补充国家</option>
      <option v-for="item in officialCountries" :key="item.code" :value="item.code">{{ item.name || item.code }}</option>
    </select>
    <select :value="proxyType" aria-label="IP 类型" @change="emit('proxyType', ($event.target as HTMLSelectElement).value as '' | ProxyType)">
      <option value="">全部类型</option><option value="residential">住宅</option><option value="datacenter">机房</option>
    </select>
    <select data-status-filter :value="status" aria-label="状态" @change="emit('status', ($event.target as HTMLSelectElement).value as '' | PoolStatusGroup)">
      <option value="">全部状态</option><option value="standby">可选节点</option><option value="ready">已启用</option><option value="processing">处理中</option><option value="fault">故障</option>
    </select>
    <select :value="sort" aria-label="排序" @change="emit('sort', ($event.target as HTMLSelectElement).value)">
      <option value="latency">延迟从低到高</option><option value="country">按国家排序</option><option value="updated">最近检测优先</option>
    </select>
  </div>
</template>

<style scoped>
.filters{display:flex;flex-wrap:wrap;gap:8px}.filters select{height:36px;min-width:132px;padding:0 34px 0 11px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text);font:inherit;font-size:13px}.filters select:focus{outline:3px solid var(--focus);border-color:var(--accent)}@media(max-width:600px){.filters{display:grid;grid-template-columns:1fr 1fr}.filters select{min-width:0;width:100%}}
</style>
