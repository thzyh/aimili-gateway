<script setup lang="ts">
import { RouterLink, useRouter } from 'vue-router'
import { apiFetch } from '../api/client'

const router = useRouter()

async function logout(): Promise<void> {
  await apiFetch('/api/v1/auth/logout', { method: 'POST' })
  await router.push('/login')
}
</script>

<template>
  <div class="app-shell">
    <header class="app-header">
      <div class="brand"><span class="brand-mark">A</span><span>Aimili Gateway</span></div>
      <nav aria-label="主导航">
        <RouterLink to="/">VPN 节点池</RouterLink>
        <RouterLink to="/socks5h">SOCKS5H 代理池</RouterLink>
        <RouterLink to="/freesub">freesub 备用</RouterLink>
        <RouterLink to="/settings">高级设置</RouterLink>
      </nav>
      <button data-logout class="ghost-button" type="button" @click="logout">退出</button>
    </header>
    <main class="app-main"><slot /></main>
  </div>
</template>

<style scoped>
.app-shell{min-height:100vh}.app-header{position:sticky;top:0;z-index:20;height:56px;display:grid;grid-template-columns:1fr auto 1fr;align-items:center;padding:0 24px;border-bottom:1px solid var(--border);background:color-mix(in srgb,var(--panel) 94%,transparent);backdrop-filter:blur(12px)}.brand{display:flex;align-items:center;gap:10px;font-weight:780;letter-spacing:-.02em}.brand-mark{display:grid;place-items:center;width:28px;height:28px;border-radius:8px;color:#fff;background:var(--accent);font-size:14px}nav{display:flex;height:100%;gap:4px}nav a{display:flex;align-items:center;padding:0 14px;border-bottom:2px solid transparent;color:var(--muted-text);font-size:14px;font-weight:650;text-decoration:none}nav a.router-link-exact-active{border-color:var(--accent);color:var(--text)}.ghost-button{justify-self:end}.app-main{width:min(1440px,calc(100% - 40px));margin:0 auto;padding:28px 0 48px}@media(max-width:760px){.app-header{height:auto;min-height:56px;grid-template-columns:1fr auto;padding:10px 16px}.brand{grid-column:1}.ghost-button{grid-column:2;grid-row:1}nav{grid-column:1/-1;overflow-x:auto}nav a{height:42px;white-space:nowrap}.app-main{width:min(100% - 24px,1440px);padding-top:20px}}
</style>
