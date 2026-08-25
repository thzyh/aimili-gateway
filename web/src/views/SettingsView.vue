<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { apiFetch, type NavigationPayload } from '../api/client'
import AppShell from '../components/AppShell.vue'

const password = ref('')
const totp = ref('')
const cidrs = ref('')
const notice = ref('')
const busy = ref(false)
const expertModeUrl = ref('')

onMounted(async () => {
  try { expertModeUrl.value = (await apiFetch<NavigationPayload>('/api/v1/navigation')).expertModeUrl }
  catch { expertModeUrl.value = '' }
})

async function reauthenticate(): Promise<void> {
  busy.value = true
  try {
    await apiFetch('/api/v1/auth/reauth', { method: 'POST', body: JSON.stringify({ password: password.value, ...(totp.value ? { totp: totp.value } : {}) }) })
    password.value = ''; totp.value = ''; notice.value = '安全确认已生效，五分钟内可复制或导出连接地址。'
  } catch { notice.value = '安全确认失败，请检查 Gateway 密码或 TOTP。' }
  finally { busy.value = false }
}

async function saveCIDRs(): Promise<void> {
  busy.value = true
  const values = cidrs.value.split(/[\s,]+/).map(value => value.trim()).filter(Boolean)
  try { await apiFetch('/api/v1/settings/mixed-cidrs', { method: 'PUT', body: JSON.stringify({ cidrs: values }) }); notice.value = 'SOCKS5H 来源范围已保存。' }
  catch { notice.value = '保存失败；请使用具体的 IPv4 或 IPv6 CIDR，不能开放整个互联网。' }
  finally { busy.value = false }
}
</script>

<template>
  <AppShell>
    <section class="page-heading"><p class="eyebrow">SETTINGS</p><h1>高级设置</h1><p>AimiliVPN 与 3x-ui 保持独立运行；常用安全设置集中在这里。</p></section>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <div class="settings-grid">
      <section class="setting-card"><div><h2>安全确认</h2><p>显示、复制或导出连接秘密前确认 Gateway 管理员身份。</p></div><form @submit.prevent="reauthenticate"><label>Gateway 密码<input v-model="password" required type="password" autocomplete="current-password"></label><label>TOTP（如已启用）<input v-model="totp" inputmode="numeric" autocomplete="one-time-code"></label><button :disabled="busy">确认身份</button></form></section>
      <section class="setting-card"><div><h2>SOCKS5H 来源限制</h2><p>SOCKS5H 本身不加密，只允许你的固定公网来源访问。</p></div><form @submit.prevent="saveCIDRs"><label>允许的 CIDR<textarea v-model="cidrs" rows="4" placeholder="每行一个，例如 203.0.113.24/32" /></label><button :disabled="busy">保存来源范围</button></form></section>
      <section class="setting-card expert"><div><h2>3x-ui 专家模式</h2><p>打开原版 3x-ui 后仍使用 3x-ui 自己的账户登录；这不是覆盖全部页面的真正 SSO。</p></div><a v-if="expertModeUrl" data-expert-mode :href="expertModeUrl" target="_blank" rel="noopener noreferrer">打开专家模式</a><span v-else>当前未配置专家模式入口</span></section>
    </div>
  </AppShell>
</template>

<style scoped>
.page-heading{margin-bottom:20px}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:11px;font-weight:800;letter-spacing:.14em}.page-heading h1{margin:0;font-size:28px}.page-heading>p:last-child{margin:8px 0 0;color:var(--muted-text)}.notice{padding:10px 13px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--muted-text)}.settings-grid{display:grid;grid-template-columns:1fr 1fr;gap:14px}.setting-card{display:grid;align-content:start;gap:20px;padding:20px;border:1px solid var(--border);border-radius:12px;background:var(--panel);box-shadow:var(--shadow-soft)}.setting-card h2{margin:0;font-size:17px}.setting-card p{margin:7px 0 0;color:var(--muted-text);font-size:13px;line-height:1.6}.setting-card form{display:grid;gap:12px}.setting-card label{display:grid;gap:6px;font-size:12px;font-weight:700}.setting-card input,.setting-card textarea{width:100%;padding:10px 11px;border:1px solid var(--border);border-radius:8px;background:var(--input);color:var(--text);font:inherit}.expert{grid-column:1/-1;grid-template-columns:1fr auto;align-items:center}.expert a{padding:9px 13px;border-radius:8px;background:var(--accent);color:#fff;font-size:13px;font-weight:700;text-decoration:none}.expert span{color:var(--muted-text);font-size:13px}@media(max-width:760px){.settings-grid{grid-template-columns:1fr}.expert{grid-column:auto;grid-template-columns:1fr}}
</style>
