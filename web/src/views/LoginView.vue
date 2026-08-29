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
  <div class="login-page bp-grid">
    <!-- 顶部导航 -->
    <header class="nav">
      <div class="brand">
        <span class="mark serif">A</span>
        <span class="name serif">Atomix</span>
      </div>
      <div class="nav-right">
        <span class="nav-tag">AI · 构建网站与应用，无需编码</span>
      </div>
    </header>

    <div class="hero">
      <div class="login-card">
        <p class="eyebrow">Atomix Studio · v1.0</p>
        <h1 class="title serif">把想法变成<br /><em>产品</em></h1>
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

      <!-- 手稿标注装饰 -->
      <div class="aside-notes" aria-hidden="true">
        <span class="note-line n1">plan → build → verify</span>
        <span class="note-line n2">ReAct loop, sandboxed</span>
        <span class="note-x">×</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  min-height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--paper-100);
  position: relative;
}

/* ---- 顶部导航：暖纸半透明 + 细分隔线 ---- */
.nav {
  height: 60px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 28px;
  background: rgba(250, 248, 243, .72);
  backdrop-filter: saturate(180%) blur(8px);
  border-bottom: 1px solid var(--line-soft);
}
.brand { display: flex; align-items: center; gap: 10px; }
.mark {
  width: 30px; height: 30px; border-radius: 8px;
  background: var(--indigo-500);
  color: var(--paper-50); font-weight: 700; font-size: 16px;
  display: flex; align-items: center; justify-content: center;
  box-shadow: inset 0 0 0 2px rgba(255,255,255,.22), 0 2px 8px rgba(61, 79, 196, .3);
}
.name { font-weight: 700; font-size: 18px; letter-spacing: .01em; color: var(--ink-100); }
.nav-tag { font-size: 12px; color: var(--ink-55); font-family: var(--font-mono); letter-spacing: .02em; }

/* ---- 居中卡片：纸面 + 细描边 + 硬朗投影 ---- */
.hero {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 16px;
  position: relative;
}
.login-card {
  width: 460px;
  max-width: 100%;
  background: var(--paper-50);
  border: 1px solid var(--line);
  border-radius: 18px;
  padding: 46px 44px 34px;
  box-shadow: 0 1px 0 rgba(28,35,51,.03), 0 18px 44px -18px rgba(28,35,51,.22);
  text-align: center;
  position: relative;
}
/* 卡片左上角的手稿角标 */
.login-card::before {
  content: "";
  position: absolute;
  top: 14px; left: 14px;
  width: 26px; height: 26px;
  border-top: 2px solid var(--teal-500);
  border-left: 2px solid var(--teal-500);
  border-top-left-radius: 8px;
  opacity: .7;
}
.login-card .eyebrow { display: block; margin-bottom: 14px; }
.title {
  font-size: 42px;
  font-weight: 600;
  letter-spacing: -.01em;
  line-height: 1.12;
  color: var(--ink-100);
}
.title em {
  font-style: italic;
  color: var(--indigo-500);
  font-weight: 700;
}
.tagline {
  color: var(--ink-55);
  font-size: 14.5px;
  margin: 14px 0 28px;
  line-height: 1.7;
}

/* ---- 分段切换：下划线式标签，告别胶囊 ---- */
.tabs {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--line);
  margin-bottom: 22px;
}
.tabs button {
  flex: 1;
  padding: 10px 0 12px;
  color: var(--ink-55);
  font-size: 14px;
  font-weight: 600;
  position: relative;
  transition: color .18s ease;
}
.tabs button::after {
  content: "";
  position: absolute;
  left: 25%; right: 25%; bottom: -1px;
  height: 2px;
  background: var(--indigo-500);
  transform: scaleX(0);
  transition: transform .22s ease;
}
.tabs button.active { color: var(--ink-100); }
.tabs button.active::after { transform: scaleX(1); }

/* ---- 表单 ---- */
form { display: flex; flex-direction: column; gap: 12px; text-align: left; }
input {
  background: var(--paper-100);
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 13px 15px;
  color: var(--ink-100);
  font-size: 15px;
  outline: none;
  transition: border-color .18s ease, background .18s ease, box-shadow .18s ease;
}
input::placeholder { color: var(--ink-30); }
input:focus {
  background: var(--paper-50);
  border-color: var(--indigo-500);
  box-shadow: 0 0 0 3px var(--indigo-50);
}
.primary {
  background: var(--ink-100);
  color: var(--paper-50);
  border-radius: 10px;
  padding: 14px;
  font-size: 15.5px;
  font-weight: 700;
  letter-spacing: .01em;
  margin-top: 4px;
  transition: background .18s ease, transform .12s ease;
}
.primary:hover:not(:disabled) { background: var(--indigo-600); }
.primary:active:not(:disabled) { transform: scale(.985); }
.primary:disabled { background: var(--paper-300); color: var(--paper-50); cursor: default; }
.error {
  color: var(--red);
  font-size: 13px;
  background: rgba(193, 74, 80, .08);
  border: 1px solid rgba(193, 74, 80, .22);
  border-radius: 8px;
  padding: 10px 14px;
}
.hint { margin-top: 20px; color: var(--ink-30); font-size: 12px; font-family: var(--font-mono); }

/* ---- 手稿标注装饰 ---- */
.aside-notes { position: absolute; right: 6%; top: 22%; display: flex; flex-direction: column; gap: 12px; }
.note-line { font-family: var(--font-mono); font-size: 11.5px; color: var(--ink-30); letter-spacing: .04em; }
.note-line::before { content: "— "; color: var(--teal-500); }
.note-x { color: var(--teal-500); font-size: 18px; opacity: .5; }

@media (max-width: 720px) {
  .aside-notes { display: none; }
}
@media (max-width: 520px) {
  .login-card { padding: 36px 24px 28px; border-radius: 14px; }
  .title { font-size: 34px; }
}
</style>
