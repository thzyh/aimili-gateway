<script setup lang="ts">
import type { UiNoticeData } from './errorMessages'

defineProps<{ notice: UiNoticeData }>()
defineEmits<{ close: [] }>()
</script>

<template>
  <section class="ui-notice" :data-notice-kind="notice.kind" :role="notice.kind === 'error' ? 'alert' : 'status'">
    <span class="notice-marker" aria-hidden="true" />
    <div><strong>{{ notice.title }}</strong><p v-if="notice.message">{{ notice.message }}</p></div>
    <button type="button" aria-label="关闭提示" title="关闭提示" @click="$emit('close')">×</button>
  </section>
</template>

<style scoped>
.ui-notice{display:grid;grid-template-columns:8px minmax(0,1fr) 32px;align-items:start;gap:10px;margin:0;padding:11px 12px;border:1px solid var(--border);border-radius:10px;background:var(--panel);font-size:13px}.notice-marker{width:8px;height:8px;margin-top:5px;border-radius:50%;background:var(--muted-text)}.ui-notice strong{display:block;color:var(--text);font-size:13px}.ui-notice p{margin:3px 0 0;color:var(--muted-text);line-height:1.45}.ui-notice button{width:32px;height:32px;margin:-6px -6px 0 0;padding:0;border:0;background:transparent;color:var(--muted-text);font-size:20px}.ui-notice[data-notice-kind=success]{border-color:#86efac;background:#f0fdf4}.ui-notice[data-notice-kind=success] .notice-marker{background:var(--healthy)}.ui-notice[data-notice-kind=error]{border-color:#fca5a5;background:#fef2f2}.ui-notice[data-notice-kind=error] .notice-marker{background:var(--danger)}.ui-notice[data-notice-kind=progress]{border-color:#93c5fd;background:#eff6ff}.ui-notice[data-notice-kind=progress] .notice-marker{background:var(--accent)}.ui-notice[data-notice-kind=info]{background:var(--subtle)}
@media(prefers-color-scheme:dark){.ui-notice[data-notice-kind=success]{border-color:#166534;background:#122a1b}.ui-notice[data-notice-kind=error]{border-color:#991b1b;background:#321717}.ui-notice[data-notice-kind=progress]{border-color:#1d4ed8;background:#142447}}
</style>
