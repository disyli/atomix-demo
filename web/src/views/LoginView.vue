<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api'

const router = useRouter()
const mode = ref('login')
const email = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  error.value = ''
  if (!email.value.trim() || password.value.length < 6) {
    error.value = '请输入邮箱，密码至少 6 位'
    return
  }
  loading.value = true
  try {
    const fn = mode.value === 'login' ? api.login : api.register
    const data = await fn(email.value.trim(), password.value)
    localStorage.setItem('atomix_token', data.token)
    localStorage.setItem('atomix_user', JSON.stringify(data.user))
    router.push('/workspace')
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="glow g1"></div>
    <div class="glow g2"></div>
    <div class="login-card">
      <div class="logo">⚛️</div>
      <h1>Atomix</h1>
      <p class="tagline">AI Agent 驱动的应用生成平台 · Demo</p>
      <div class="tabs">
        <button :class="{ active: mode === 'login' }" @click="mode = 'login'">登录</button>
        <button :class="{ active: mode === 'register' }" @click="mode = 'register'">注册</button>
      </div>
      <form @submit.prevent="submit">
        <input v-model="email" type="email" placeholder="邮箱地址" autocomplete="email" />
        <input v-model="password" type="password" :placeholder="mode === 'register' ? '设置密码（至少 6 位）' : '密码'" autocomplete="current-password" />
        <div v-if="error" class="error">{{ error }}</div>
        <button class="primary" type="submit" :disabled="loading">{{ loading ? '请稍候…' : (mode === 'login' ? '登录工作台' : '创建账号并进入') }}</button>
      </form>
      <p class="hint">数据持久化于服务端 SQLite · 首次使用请先注册</p>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  overflow: hidden;
  background: radial-gradient(1200px 600px at 70% -10%, #1e2a4d 0%, var(--bg) 60%);
}
.glow {
  position: absolute;
  border-radius: 50%;
  filter: blur(90px);
  opacity: .35;
  pointer-events: none;
}
.g1 { width: 420px; height: 420px; background: #6366f1; top: -120px; right: -80px; }
.g2 { width: 360px; height: 360px; background: #38bdf8; bottom: -140px; left: -100px; }
.login-card {
  position: relative;
  width: 400px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 20px;
  padding: 40px 36px;
  box-shadow: 0 30px 80px rgba(0,0,0,.5);
  text-align: center;
}
.logo { font-size: 44px; }
h1 { font-size: 28px; margin: 8px 0 4px; letter-spacing: 1px; }
.tagline { color: var(--muted); font-size: 13px; margin-bottom: 26px; }
.tabs {
  display: flex;
  background: var(--panel-2);
  border-radius: 12px;
  padding: 4px;
  margin-bottom: 20px;
}
.tabs button {
  flex: 1;
  padding: 9px;
  border-radius: 9px;
  background: transparent;
  color: var(--muted);
  font-size: 14px;
  transition: all .2s;
}
.tabs button.active { background: var(--accent); color: #fff; font-weight: 600; }
form { display: flex; flex-direction: column; gap: 12px; }
input {
  background: var(--panel-2);
  border: 1.5px solid var(--border);
  border-radius: 11px;
  padding: 12px 15px;
  color: var(--text);
  font-size: 14px;
  outline: none;
  transition: border-color .2s;
}
input:focus { border-color: var(--accent); }
.primary {
  background: linear-gradient(135deg, var(--accent), #8b5cf6);
  color: #fff;
  border-radius: 11px;
  padding: 13px;
  font-size: 15px;
  font-weight: 600;
  transition: opacity .2s;
}
.primary:disabled { opacity: .6; cursor: default; }
.error {
  color: var(--err);
  font-size: 13px;
  text-align: left;
  background: rgba(248,113,113,.08);
  border: 1px solid rgba(248,113,113,.25);
  border-radius: 8px;
  padding: 8px 12px;
}
.hint { margin-top: 20px; color: var(--muted); font-size: 12px; }
</style>
