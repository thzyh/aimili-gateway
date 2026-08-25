<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { apiFetch, type AuthOptionsPayload } from '../api/client'

const router = useRouter()
const username = ref('')
const password = ref('')
const totp = ref('')
const errorMessage = ref('')
const submitting = ref(false)
const optionsLoaded = ref(false)
const totpRequired = ref(false)

onMounted(async () => {
  try {
    const options = await apiFetch<AuthOptionsPayload>('/api/v1/auth/options')
    totpRequired.value = options.totpRequired
    optionsLoaded.value = true
  } catch {
    errorMessage.value = '暂时无法获取登录方式，请稍后重试。'
  }
})

async function submit(): Promise<void> {
  if (!optionsLoaded.value) return
  errorMessage.value = ''
  if (totpRequired.value && !/^\d{6}$/.test(totp.value)) {
    errorMessage.value = '请输入 6 位动态验证码。'
    return
  }
  submitting.value = true
  try {
    const credentials: Record<string, string> = {
      username: username.value,
      password: password.value,
    }
    if (totpRequired.value) credentials.totp = totp.value
    await apiFetch('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify(credentials),
    })
    await router.push('/')
  } catch {
    errorMessage.value = totpRequired.value
      ? '登录失败，请检查账户、密码或动态验证码。'
      : '登录失败，请检查账户或密码。'
  } finally {
    password.value = ''
    totp.value = ''
    submitting.value = false
  }
}
</script>

<template>
  <main class="login-shell">
    <section class="login-panel" aria-labelledby="login-title">
      <p class="eyebrow">Aimili Gateway</p>
      <h1 id="login-title">统一控制台</h1>
      <p class="intro">使用 Gateway 管理员账户登录。专家模式仍会使用 3x-ui 自己的账户。</p>
      <form @submit.prevent="submit">
        <label>
          用户名
          <input v-model="username" name="username" autocomplete="username" required />
        </label>
        <label>
          密码
          <input v-model="password" name="password" type="password" autocomplete="current-password" required />
        </label>
        <label v-if="totpRequired">
          动态验证码
          <input v-model="totp" name="totp" inputmode="numeric" autocomplete="one-time-code" maxlength="6" pattern="[0-9]{6}" required />
        </label>
        <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
        <button type="submit" :disabled="submitting || !optionsLoaded">{{ submitting ? '正在登录…' : '登录' }}</button>
      </form>
    </section>
  </main>
</template>

<style scoped>
.login-shell {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 1.5rem;
}

.login-panel {
  width: min(100%, 28rem);
  padding: clamp(1.5rem, 5vw, 2.5rem);
  border: 1px solid var(--border);
  border-radius: 1.25rem;
  background: var(--panel);
  box-shadow: var(--shadow);
}

.eyebrow {
  margin: 0 0 0.5rem;
  color: var(--accent);
  font-size: 0.8rem;
  font-weight: 800;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

h1 { margin: 0; }
.intro { color: var(--muted-text); line-height: 1.6; }
form { display: grid; gap: 1rem; margin-top: 1.5rem; }
label { display: grid; gap: 0.45rem; font-weight: 650; }
input {
  width: 100%;
  box-sizing: border-box;
  padding: 0.75rem 0.85rem;
  border: 1px solid var(--border);
  border-radius: 0.65rem;
  background: var(--input);
  color: inherit;
  font: inherit;
}
input:focus { outline: 3px solid var(--focus); border-color: var(--accent); }
button { width: 100%; }
.form-error { margin: 0; color: var(--unavailable); }
</style>
