import { createRouter, createWebHistory } from 'vue-router'

import { apiFetch, setUnauthorizedHandler, type SessionPayload } from '../api/client'
import LoginView from '../views/LoginView.vue'
import VpnPoolView from '../views/VpnPoolView.vue'
import SocksPoolView from '../views/SocksPoolView.vue'
import SettingsView from '../views/SettingsView.vue'
import AimiliSettingsView from '../views/AimiliSettingsView.vue'
import XUISettingsView from '../views/XUISettingsView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'vpn-pool', component: VpnPoolView },
    { path: '/socks5h', name: 'socks-pool', component: SocksPoolView },
    { path: '/settings', name: 'settings', component: SettingsView },
    { path: '/settings/aimilivpn', name: 'settings-aimilivpn', component: AimiliSettingsView },
    { path: '/settings/3x-ui', name: 'settings-3x-ui', component: XUISettingsView },
    { path: '/login', name: 'login', component: LoginView },
  ],
})

router.beforeEach(async (to) => {
  if (to.name === 'login') {
    return true
  }
  try {
    await apiFetch<SessionPayload>('/api/v1/auth/session')
    return true
  } catch {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
})

setUnauthorizedHandler(() => {
  if (router.currentRoute.value.name !== 'login') {
    void router.replace({ name: 'login' })
  }
})

export default router
