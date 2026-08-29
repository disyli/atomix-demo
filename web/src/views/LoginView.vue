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
    router.push('/dashboard')
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <!-- 顶部导航（Atoms 风格 header） -->
    <header class="nav">
      <div class="brand">
        <span class="mark">A</span>
        <span class="name">Atomix</span>
      </div>
      <div class="nav-right">
        <span class="nav-tag">AI · 构建网站与应用，无需编码</span>
      </div>
    </header>

    <div class="hero">
      <div class="login-card">
        <h1 class="title">把想法变成<br />产品</h1>
        <p class="tagline">AI Agent 团队用于验证想法、构建产品。几分钟内完成，无需编码。</p>

        <div class="tabs">
          <button :class="{ active: mode === 'login' }" @click="mode = 'login'">登录</button>
          <button :class="{ active: mode === 'register' }" @click="mode = 'register'">注册</button>
        </div>

        <form @submit.prevent="submit">
          <input v-model="email" type="email" placeholder="邮箱地址" autocomplete="email" />
          <input v-model="password" type="password" :placeholder="mode === 'register' ? '设置密码（至少 6 位）' : '密码'" autocomplete="current-password" />
          <div v-if="error" class="error">{{ error }}</div>
          <button class="primary" type="submit" :disabled="loading">
            {{ loading ? '请稍候…' : (mode === 'login' ? '开始' : '创建账号并开始') }}
          </button>
        </form>

        <p class="hint">数据持久化于服务端 SQLite · 首次使用请先注册</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  min-height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--beige-100);
}

/* ---- 顶部导航：浅色半透明 + 细分隔线 ---- */
.nav {
  height: 60px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 28px;
  background: rgba(246, 246, 246, .7);
  backdrop-filter: saturate(180%) blur(8px);
  border-bottom: 1px solid var(--line-soft);
}
.brand { display: flex; align-items: center; gap: 10px; }
.mark {
  width: 28px; height: 28px; border-radius: 9px;
  background: var(--blue-500);
  color: #fff; font-weight: 800; font-size: 15px;
  display: flex; align-items: center; justify-content: center;
}
.name { font-weight: 700; font-size: 17px; letter-spacing: -.01em; color: var(--ink-100); }
.nav-tag { font-size: 12.5px; color: var(--ink-55); font-family: var(--font-mono); }

/* ---- 居中大卡片（Atoms 的大圆角 + 米白层次） ---- */
.hero {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 16px;
}
.login-card {
  width: 460px;
  max-width: 100%;
  background: var(--beige-50);
  border: 1px solid var(--line-soft);
  border-radius: 28px;
  padding: 48px 44px 36px;
  box-shadow: 0 1px 0 rgba(12,12,12,.02), 0 12px 40px rgba(12,12,12,.06);
  text-align: center;
}
.title {
  font-size: 40px;
  font-weight: 800;
  letter-spacing: -.03em;
  line-height: 1.08;
  color: var(--ink-100);
}
.tagline {
  color: var(--ink-55);
  font-size: 15px;
  margin: 14px 0 30px;
  line-height: 1.6;
}

/* ---- 分段切换：胶囊容器 + 蓝色实心激活态 ---- */
.tabs {
  display: flex;
  background: var(--beige-150);
  border-radius: var(--r-full);
  padding: 4px;
  margin-bottom: 22px;
}
.tabs button {
  flex: 1;
  padding: 10px;
  border-radius: var(--r-full);
  color: var(--ink-55);
  font-size: 14px;
  font-weight: 600;
  transition: all .18s ease;
}
.tabs button.active {
  background: var(--blue-500);
  color: #fff;
}

/* ---- 表单 ---- */
form { display: flex; flex-direction: column; gap: 12px; }
input {
  background: var(--beige-100);
  border: 1px solid var(--line-soft);
  border-radius: var(--r-m);
  padding: 14px 16px;
  color: var(--ink-100);
  font-size: 15px;
  outline: none;
  transition: border-color .18s ease, background .18s ease;
}
input::placeholder { color: var(--ink-30); }
input:focus {
  background: var(--beige-50);
  border-color: var(--blue-500);
}
.primary {
  background: var(--blue-500);
  color: #fff;
  border-radius: var(--r-full);
  padding: 14px;
  font-size: 16px;
  font-weight: 600;
  letter-spacing: .01em;
  transition: background .18s ease, transform .12s ease;
}
.primary:hover:not(:disabled) { background: var(--blue-600); }
.primary:active:not(:disabled) { transform: scale(.985); }
.primary:disabled { background: var(--beige-300); color: var(--beige-50); cursor: default; }
.error {
  color: var(--red);
  font-size: 13px;
  text-align: left;
  background: rgba(201, 68, 74, .08);
  border: 1px solid rgba(201, 68, 74, .18);
  border-radius: var(--r-s);
  padding: 10px 14px;
}
.hint { margin-top: 22px; color: var(--ink-30); font-size: 12px; }

@media (max-width: 520px) {
  .login-card { padding: 36px 24px 28px; border-radius: 22px; }
  .title { font-size: 32px; }
}
</style>
