import { createRouter, createWebHistory } from 'vue-router'

import { apiFetch, setUnauthorizedHandler, type SessionPayload } from '../api/client'
import LoginView from '../views/LoginView.vue'
import OverviewView from '../views/OverviewView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'overview', component: OverviewView },
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
