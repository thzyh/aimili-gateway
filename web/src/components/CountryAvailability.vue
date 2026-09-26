<script setup lang="ts">
import type { CandidateCountryPayload } from '../api/client'
import { countryDisplayName } from './errorMessages'
defineProps<{rows:CandidateCountryPayload[]}>()
</script>
<template>
 <details v-if="rows.length" class="country-availability"><summary>国家可用性明细 · {{ rows.length }} 国</summary><p>已排除占用和冷却候选，并按出口 IP 去重。近期检测通过不等于永久可用，接替前仍需真实出网验证。</p><div class="table-wrap"><table><thead><tr><th>国家</th><th>官方候选</th><th>可拨号</th><th>近期出网通过</th><th>住宅 / 机房</th><th>IP 质量合格</th></tr></thead><tbody><tr v-for="row in rows" :key="row.code" :data-country-availability="row.code"><td>{{ countryDisplayName(row.code,rows) }}</td><td>{{ row.candidateCount }}</td><td>{{ row.dialableCount ?? '未知' }}</td><td>{{ row.freshEgressCount ?? '未知' }}</td><td>{{ row.residentialCount ?? '—' }} / {{ row.datacenterCount ?? '—' }}</td><td>{{ row.ipQualityPassCount == null ? '暂未启用' : row.ipQualityPassCount }}</td></tr></tbody></table></div></details>
</template>
<style scoped>
.country-availability{margin-bottom:16px;border:1px solid var(--border);border-radius:10px;padding:12px 16px;background:var(--panel);font-size:12px}summary{cursor:pointer;font-weight:700}p{color:var(--muted-text);line-height:1.6}.table-wrap{max-height:300px;overflow:auto}table{border-collapse:collapse;width:100%;white-space:nowrap;text-align:left}th,td{padding:10px 14px;border-bottom:1px solid var(--border)}th{font-weight:600;color:var(--muted-text)}td:first-child{font-weight:700}
</style>
