<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api'
import { useRouter } from 'vue-router'

const router = useRouter()
const user = computed(() => {
  try { return JSON.parse(localStorage.getItem('atomix_user') || '{}') } catch { return {} }
})
const firstName = computed(() => {
  const e = user.value.email || ''
  const n = (user.value.name || e.split('@')[0] || '朋友')
  return n.length > 12 ? n.slice(0, 12) : n
})
function logout() {
  localStorage.removeItem('atomix_token')
  localStorage.removeItem('atomix_user')
  router.push('/login')
}

/* 状态 */
const projects = ref([])
const menuOpen = ref(false)
const mode = ref('build')
const attachments = ref([])
const pendingIds = ref([])
const fileInput = ref(null)
const composer = ref('')
const sending = ref(false)
const focused = ref(false)
const menuRef = ref(null)
const serverMode = ref('')

const modes = [
  { id: 'build', label: '构建' },
  { id: 'plan', label: '规划' },
  { id: 'research', label: '深度研究' }
]
const modeLabel = computed(() => modes.find(m => m.id === mode.value)?.label || '构建')
const recentProjects = computed(() => projects.value.slice(0, 3))

onMounted(async () => {
  document.addEventListener('click', onDocClick)
  try {
    const health = await fetch('/api/health').then(r => r.json())
    serverMode.value = health.mode === 'live' ? 'DeepSeek 已接入' : '演示模式'
  } catch { serverMode.value = '' }
  try { projects.value = await api.listProjects() } catch {}
})
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

function onDocClick(e) {
  if (menuRef.value && !menuRef.value.contains(e.target)) menuOpen.value = false
}

/* 发送：先经意图路由，build 才进入工作区流水线；闲聊/澄清在工作区展示回复 */
async function send() {
  const text = composer.value.trim()
  if (!text || sending.value) return
  sending.value = true
  try {
    const resp = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + (localStorage.getItem('atomix_token') || '') },
      body: JSON.stringify({ message: text, attachmentIds: pendingIds.value, mode: mode.value })
    })
    const data = await resp.json().catch(() => ({}))
    sessionStorage.setItem('atomix_pending', JSON.stringify({
      brief: data.brief || text, mode: mode.value,
      attachmentIds: pendingIds.value,
      attachments: attachments.value.filter(a => pendingIds.value.includes(a.id)),
      chat: (data.intent === 'chat' || data.intent === 'clarify') ? (data.reply || '') : ''
    }))
  } catch {
    sessionStorage.setItem('atomix_pending', JSON.stringify({ brief: text, mode: mode.value, attachmentIds: pendingIds.value, attachments: [], chat: '' }))
  } finally {
    sending.value = false
    router.push('/workspace')
  }
}

/* 附件上传 */
function pickFiles() { fileInput.value?.click() }
async function onFiles(e) {
  const files = [...e.target.files || []]
  for (const f of files) {
    const fd = new FormData()
    fd.append('file', f)
    try {
      const resp = await fetch('/api/attachments', {
        method: 'POST',
        headers: { Authorization: 'Bearer ' + (localStorage.getItem('atomix_token') || '') },
        body: fd
      })
      const meta = await resp.json()
      if (resp.ok) { attachments.value.unshift(meta); pendingIds.value.push(meta.id) }
    } catch {}
  }
  e.target.value = ''
}
function removeAttachment(id) {
  pendingIds.value = pendingIds.value.filter(x => x !== id)
}
const pendingAttachments = computed(() => attachments.value.filter(a => pendingIds.value.includes(a.id)))
</script>

<template>
  <div class="dash">
    <!-- 侧边栏 -->
    <aside class="side">
      <div class="side-brand">
        <span class="logo">A</span>
        <span class="logo-name">Atomix</span>
      </div>
      <div class="ws-switch">
        <span class="ws-icon">🗂</span>
        <span class="ws-name">{{ firstName }} 的 Atomix</span>
      </div>
      <nav class="nav">
        <button class="nav-item active">🏠 首页</button>
        <button class="nav-item" @click="router.push('/workspace?tab=history')">📁 我的项目</button>
      </nav>
      <div class="side-empty" v-if="!projects.length">还没有项目<br />输入一个想法开始创建。</div>
      <div class="side-projects" v-else>
        <div class="side-sec">最近项目</div>
        <button v-for="p in projects.slice(0, 8)" :key="p.id" class="side-proj" @click="router.push('/workspace?tab=history')">
          <span class="dot"></span>{{ p.name }}
        </button>
      </div>
      <!-- 侧边栏底部：真实用户信息 -->
      <div class="side-user">
        <span class="avatar">{{ (user.email || '?')[0].toUpperCase() }}</span>
        <span class="u-email">{{ user.email }}</span>
        <button class="u-logout" @click="logout">退出</button>
      </div>
    </aside>

    <!-- 主区 -->
    <main class="stage">
      <div class="hero-zone">
        <h1 class="greet">输入想法，产出产品。<br />开始吧，{{ firstName }}。</h1>

        <!-- 输入卡片：textarea 在上，工具行在下（标准结构，不再用负margin叠加） -->
        <div class="ask" :class="{ focused: focused }">
          <textarea
            v-model="composer"
            rows="3"
            placeholder="描述你想构建的应用，Enter 发送…"
            @keydown.enter.exact.prevent="send"
            @focus="focused = true"
            @blur="focused = false"
          ></textarea>

          <!-- 已传附件 -->
          <div v-if="pendingAttachments.length" class="atts">
            <span v-for="a in pendingAttachments" :key="a.id" class="att">
              <span class="att-ico">{{ a.isImage ? '🖼' : '📄' }}</span>
              <span class="att-name">{{ a.name }}</span>
              <button class="att-x" @click="removeAttachment(a.id)">×</button>
            </span>
          </div>

          <div class="ask-bar">
            <button class="tool" @click="pickFiles" title="上传附件：图片走多模态识图，文本注入构建上下文">📎</button>
            <button class="tool" @click.stop="menuOpen = !menuOpen" :class="{ active: menuOpen }">⊕</button>
            <span class="flex1"></span>
            <button class="mode-btn" @click.stop="menuOpen = !menuOpen">
              {{ modeLabel }} <span class="chev">⌄</span>
            </button>
            <button class="go" :disabled="sending || !composer.trim()" @click="send" title="发送">
              <svg viewBox="0 0 16 16" width="15" height="15" fill="none"><path d="M3 8h8M8 4l4 4-4 4" stroke="#fff" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>
            </button>
          </div>

          <!-- 模式菜单（真实功能：构建/规划/深度研究 + 附件说明） -->
          <div v-if="menuOpen" class="menu" ref="menuRef" @click.stop>
            <button
              v-for="m in modes" :key="m.id"
              class="menu-item" :class="{ active: mode === m.id }"
              @click="mode = m.id; menuOpen = false"
            >
              <span class="mi-dot" :class="{ on: mode === m.id }"></span>
              {{ m.label }}
              <span class="mi-desc">
                {{ m.id === 'build' ? '标准流程，直接构建' : m.id === 'plan' ? '先出规划清单再实施' : '先拆解需求再构建' }}
              </span>
            </button>
            <div class="menu-sep"></div>
            <button class="menu-item" @click="pickFiles">
              <span class="mi-dot"></span>
              上传附件
              <span class="mi-desc">图片 / 文本 / 代码</span>
            </button>
          </div>
        </div>

        <p class="sub-hint">
          <span class="mode-chip" :class="serverMode === 'DeepSeek 已接入' ? 'live' : 'demo'">{{ serverMode || '连接中…' }}</span>
          Enter 发送 · 附件会作为上下文传给构建 Agent
        </p>
      </div>

      <!-- 底部：最近项目（真实数据） -->
      <div class="stage-bottom" v-if="recentProjects.length">
        <div class="bottom-head">
          <span class="b-title">最近项目</span>
          <button class="see-all" @click="router.push('/workspace?tab=history')">查看全部 ›</button>
        </div>
        <div class="proj-row">
          <button
            v-for="p in recentProjects" :key="p.id"
            class="proj-card" @click="router.push('/workspace?tab=history')"
          >
            <div class="proj-name">{{ p.name }}</div>
            <div class="proj-brief">{{ p.brief }}</div>
            <div class="proj-meta">{{ new Date(p.createdAt).toLocaleDateString() }} · {{ p.template }}</div>
          </button>
        </div>
      </div>
    </main>

    <input ref="fileInput" type="file" multiple hidden @change="onFiles" accept="image/*,.txt,.md,.json,.csv,.js,.html,.css" />
  </div>
</template>

<style scoped>
.dash { height: 100%; display: flex; background: var(--beige-100); }

/* ============ 侧边栏 ============ */
.side { width: 210px; flex-shrink: 0; display: flex; flex-direction: column; padding: 14px 10px 12px; border-right: 1px solid var(--line-soft); }
.side-brand { display: flex; align-items: center; gap: 8px; padding: 2px 6px 14px; }
.logo { width: 24px; height: 24px; border-radius: 7px; background: var(--blue-500); color: #fff; font-weight: 800; font-size: 13px; display: flex; align-items: center; justify-content: center; }
.logo-name { font-weight: 800; font-size: 15.5px; letter-spacing: -.01em; }
.ws-switch {
  display: flex; align-items: center; gap: 7px; width: 100%;
  background: var(--beige-150); border-radius: var(--r-m); padding: 9px 10px; font-size: 12.5px; font-weight: 600;
}
.ws-icon { font-size: 13px; }
.ws-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.nav { display: flex; flex-direction: column; gap: 2px; margin-top: 10px; }
.nav-item {
  display: flex; align-items: center; gap: 8px; text-align: left;
  padding: 9px 10px; border-radius: var(--r-m); font-size: 13px; font-weight: 550; color: var(--ink-80);
  transition: background .15s ease;
}
.nav-item:hover { background: var(--beige-150); }
.nav-item.active { background: var(--beige-200); font-weight: 650; }
.side-empty { margin-top: 40px; text-align: center; color: var(--ink-30); font-size: 12px; line-height: 1.8; }
.side-projects { margin-top: 18px; display: flex; flex-direction: column; gap: 2px; overflow-y: auto; flex: 1; }
.side-sec { font-size: 11px; font-weight: 700; color: var(--ink-30); padding: 0 10px 6px; letter-spacing: .04em; }
.side-proj { display: flex; align-items: center; gap: 8px; text-align: left; padding: 8px 10px; border-radius: var(--r-s); font-size: 12.5px; color: var(--ink-55); overflow: hidden; }
.side-proj span:not(.dot) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.side-proj:hover { background: var(--beige-150); }
.dot { width: 6px; height: 6px; border-radius: 50%; background: var(--beige-300); flex-shrink: 0; }

/* 侧边栏底部用户信息 */
.side-user {
  margin-top: auto; display: flex; align-items: center; gap: 8px;
  padding: 10px; border-top: 1px solid var(--line-soft);
}
.avatar {
  width: 28px; height: 28px; border-radius: 50%; flex-shrink: 0;
  background: var(--blue-500); color: #fff; font-weight: 700; font-size: 12.5px;
  display: flex; align-items: center; justify-content: center;
}
.u-email { flex: 1; font-size: 11.5px; color: var(--ink-55); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.u-logout { font-size: 11.5px; color: var(--ink-55); padding: 4px 8px; border-radius: var(--r-s); }
.u-logout:hover { color: var(--ink-100); background: var(--beige-150); }

/* ============ 主区 ============ */
.stage { flex: 1; display: flex; flex-direction: column; min-width: 0; overflow-y: auto; }
.hero-zone { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 40px 24px 20px; }
.greet { font-size: 30px; font-weight: 800; letter-spacing: -.02em; text-align: center; margin-bottom: 28px; line-height: 1.35; }

/* 输入卡片：整体容器，textarea 与工具行都在内部文档流中 */
.ask {
  width: 580px; max-width: 92%; position: relative;
  background: var(--beige-50); border: 1px solid var(--line);
  border-radius: 20px; padding: 6px 8px 8px;
  box-shadow: 0 4px 24px rgba(12,12,12,.06);
  transition: border-color .18s ease, box-shadow .18s ease;
}
.ask.focused, .ask:focus-within { border-color: var(--blue-500); box-shadow: 0 4px 28px rgba(66,103,255,.13); }
.ask textarea {
  display: block; width: 100%; border: none; background: transparent;
  padding: 12px 12px 6px; font-size: 14.5px; color: var(--ink-100);
  outline: none; resize: none; line-height: 1.6;
}
.ask textarea::placeholder { color: var(--ink-30); }
.ask-bar { display: flex; align-items: center; gap: 6px; padding: 2px 4px 2px 8px; }
.tool {
  width: 30px; height: 30px; border-radius: 50%;
  color: var(--ink-55); font-size: 14px;
  display: flex; align-items: center; justify-content: center;
  transition: all .15s ease;
}
.tool:hover { background: var(--beige-150); color: var(--ink-100); }
.tool.active { background: var(--blue-50); color: var(--blue-600); }
.flex1 { flex: 1; }
.mode-btn {
  display: flex; align-items: center; gap: 4px;
  border: 1px solid var(--line); border-radius: var(--r-full);
  background: transparent; padding: 6px 12px; font-size: 12.5px; font-weight: 600; color: var(--ink-80);
  transition: all .15s ease;
}
.mode-btn:hover { border-color: var(--ink-30); background: var(--beige-100); }
.go {
  width: 34px; height: 34px; border-radius: 50%; background: var(--orange); flex-shrink: 0;
  display: flex; align-items: center; justify-content: center;
  transition: transform .12s ease, background .18s ease;
}
.go:hover:not(:disabled) { background: #e09e12; }
.go:active:not(:disabled) { transform: scale(.94); }
.go:disabled { background: var(--beige-300); cursor: default; }

/* 模式菜单 */
.menu {
  position: absolute; left: 8px; bottom: calc(100% + 8px); z-index: 30;
  width: 280px; background: var(--beige-50); border: 1px solid var(--line-soft);
  border-radius: 14px; box-shadow: 0 12px 40px rgba(12,12,12,.14);
  padding: 6px; display: flex; flex-direction: column; gap: 1px;
}
.menu-item {
  display: flex; align-items: center; gap: 9px; width: 100%; text-align: left;
  padding: 10px 10px; border-radius: 9px; font-size: 13px; font-weight: 550; color: var(--ink-80);
}
.menu-item:hover { background: var(--beige-100); }
.menu-item.active { background: var(--blue-50); color: var(--blue-600); font-weight: 650; }
.mi-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--beige-300); flex-shrink: 0; }
.mi-dot.on { background: var(--blue-500); }
.mi-desc { margin-left: auto; font-size: 11px; font-weight: 400; color: var(--ink-30); }
.menu-sep { height: 1px; background: var(--line-soft); margin: 4px 6px; }

/* 已传附件 */
.atts { display: flex; flex-wrap: wrap; gap: 6px; padding: 4px 12px 2px; }
.att { display: inline-flex; align-items: center; gap: 6px; background: var(--beige-100); border: 1px solid var(--line-soft); border-radius: var(--r-full); padding: 4px 10px; font-size: 12px; }
.att-ico { font-size: 12px; }
.att-name { max-width: 140px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--ink-80); }
.att-x { color: var(--ink-30); font-size: 13px; }

/* 输入卡下方提示 */
.sub-hint { margin-top: 14px; font-size: 12px; color: var(--ink-30); display: flex; align-items: center; gap: 8px; }
.mode-chip { font-size: 11px; font-weight: 600; font-family: var(--font-mono); padding: 2px 10px; border-radius: var(--r-full); }
.mode-chip.demo { color: var(--ink-55); background: var(--beige-150); border: 1px solid var(--line-soft); }
.mode-chip.live { color: var(--green); background: rgba(45, 187, 92, .1); border: 1px solid rgba(45, 187, 92, .25); }

/* 底部最近项目 */
.stage-bottom { padding: 0 32px 28px; }
.bottom-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.b-title { font-size: 14px; font-weight: 700; color: var(--ink-80); }
.see-all { font-size: 12.5px; color: var(--ink-55); font-weight: 550; }
.see-all:hover { color: var(--ink-100); }
.proj-row { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; }
.proj-card {
  display: block; text-align: left; width: 100%;
  background: var(--beige-50); border: 1px solid var(--line-soft); border-radius: var(--r-l);
  padding: 14px 16px; transition: all .18s ease;
}
.proj-card:hover { border-color: var(--ink-30); box-shadow: 0 4px 16px rgba(12,12,12,.07); transform: translateY(-1px); }
.proj-name { font-weight: 700; font-size: 13.5px; margin-bottom: 5px; color: var(--ink-100); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.proj-brief { color: var(--ink-55); font-size: 12px; line-height: 1.5; height: 36px; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; margin-bottom: 8px; }
.proj-meta { color: var(--ink-30); font-size: 11px; font-family: var(--font-mono); }
</style>
