<script setup>
import { ref, computed, onMounted } from 'vue'
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
const notice = ref(true)
const projects = ref([])
const menuOpen = ref(false)
const modeOpen = ref(false)
const mode = ref('build')
const thinking = ref(false)
const attachments = ref([])
const pendingIds = ref([])
const fileInput = ref(null)
const composer = ref('')
const sending = ref(false)
const menuRef = ref(null)

const modes = [
  { id: 'build', label: '构建' },
  { id: 'plan', label: '规划' },
  { id: 'research', label: '深度研究' }
]
const modeLabel = computed(() => modes.find(m => m.id === mode.value)?.label || '构建')

onMounted(async () => {
  try { projects.value = await api.listProjects() } catch {}
  document.addEventListener('click', onDocClick)
})

function onDocClick(e) {
  if (menuRef.value && !menuRef.value.contains(e.target)) menuOpen.value = false
}

/* 发送：先经意图路由，build 才进入工作区流水线 */
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
      attachmentIds: pendingIds.value, attachments: attachments.value.filter(a => pendingIds.value.includes(a.id)),
      chat: (data.intent === 'chat' || data.intent === 'clarify') ? (data.reply || '') : ''
    }))
    router.push('/workspace')
  } catch {
    sessionStorage.setItem('atomix_pending', JSON.stringify({ brief: text, mode: mode.value, attachmentIds: pendingIds.value, attachments: [], chat: '' }))
    router.push('/workspace')
  } finally { sending.value = false }
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
        <span class="logo-name">Atoms</span>
        <button class="side-collapse" title="收起">▥</button>
      </div>
      <div class="ws-switch">
        <span class="ws-icon">🗂</span>
        <span class="ws-name">{{ firstName }} 的 Atoms</span>
        <span class="chev">⌄</span>
      </div>
      <nav class="nav">
        <button class="nav-item active">🏠 首页</button>
        <button class="nav-item" @click="router.push('/workspace')">🧭 资源</button>
        <button class="nav-item" @click="router.push('/workspace?tab=history')">📁 我的项目</button>
      </nav>
      <div class="side-empty" v-if="!projects.length">还没有项目<br />点击「首页」开始。</div>
      <div class="side-projects" v-else>
        <button v-for="p in projects.slice(0, 6)" :key="p.id" class="side-proj" @click="router.push('/workspace?tab=history')">
          <span class="dot"></span>{{ p.name }}
        </button>
      </div>
      <div class="side-bottom">
        <button class="promo"><b>加入我们的社区</b><span>最多可赢取 25 积分</span><i>›</i></button>
        <button class="promo"><b>获取免费积分</b><span>每人获得10积分</span><i>›</i></button>
      </div>
    </aside>

    <!-- 主区 -->
    <main class="stage">
      <!-- 右上角 -->
      <div class="stage-top">
        <button class="heart">♡</button>
      </div>

      <!-- 居中主区 -->
      <div class="hero-zone">
        <transition name="fade">
          <div v-if="notice" class="notice">
            <span class="n-chip">Notice</span><span class="n-text">· New models are live in Atoms</span>
            <button class="n-close" @click="notice = false">×</button>
          </div>
        </transition>

        <div class="mascots">
          <span class="m m1">🟠</span><span class="m m2">🟡</span><span class="m m3">🟤</span>
          <span class="m m4">🩷</span><span class="m m5">⚪</span><span class="m m6">🔵</span><span class="m m7">🟣</span>
        </div>

        <h1 class="greet">输入想法，产出产品。开始吧，{{ firstName }}。</h1>

        <!-- 输入卡片 -->
        <div class="ask">
          <textarea
            v-model="composer"
            rows="2"
            placeholder="请 Alex 构建一个 Web 应用。"
            @keydown.enter.exact.prevent="send"
          ></textarea>

          <!-- 已传附件 -->
          <div v-if="pendingAttachments.length" class="atts">
            <span v-for="a in pendingAttachments" :key="a.id" class="att">
              <img v-if="a.isImage" src="" alt="" class="att-thumb" />
              <span class="att-name">{{ a.name }}</span>
              <button class="att-x" @click="removeAttachment(a.id)">×</button>
            </span>
          </div>

          <div class="ask-bar">
            <button class="x-btn" @click="composer = ''; pendingIds = []" title="清空">×</button>
            <button class="menu-btn" @click.stop="menuOpen = !menuOpen">
              🏷 主题 <span class="chev">⌄</span>
            </button>
            <span class="flex1"></span>
            <button class="mode-btn" @click.stop="modeOpen = !modeOpen">
              {{ modeLabel }} <span class="chev">⌄</span>
            </button>
            <button class="sound">🔊</button>
            <button class="go" :disabled="sending || !composer.trim()" @click="send">
              <svg viewBox="0 0 16 16" width="15" height="15" fill="none"><path d="M3 8h8M8 4l4 4-4 4" stroke="#fff" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>
            </button>
          </div>

          <!-- 功能菜单（对齐 Atoms：团队模式/附件/连接器/深度研究/竞赛模式） -->
          <div v-if="menuOpen" class="menu" ref="menuRef">
            <button class="menu-item">
              <span class="mi-icon">👥</span> 团队模式
              <span class="switch"><span class="knob"></span></span>
            </button>
            <button class="menu-item" @click="pickFiles">
              <span class="mi-icon">📎</span> 附件
              <span class="chev-r">›</span>
            </button>
            <button class="menu-item">
              <span class="mi-icon">🔌</span> 连接器
              <span class="chev-r">›</span>
            </button>
            <button class="menu-item" :class="{ on: mode === 'research' }" @click="mode = mode === 'research' ? 'build' : 'research'">
              <span class="mi-icon">🎬</span> 视频 <span class="mini-chip">⚡ Seedance 2.0</span>
            </button>
            <button class="menu-item" :class="{ on: mode === 'research' }" @click="mode = mode === 'research' ? 'build' : 'research'">
              <span class="mi-icon">🔭</span> 深度研究
              <span class="switch" :class="{ on: mode === 'research' }"><span class="knob"></span></span>
            </button>
            <button class="menu-item disabled">
              <span class="mi-icon">🏁</span> 竞赛模式
              <span class="chev-r">›</span>
            </button>
          </div>
        </div>

        <!-- 连接器小图标排（截图右下的 app 图标行） -->
        <div class="connectors">
          <span class="c c1">🟩</span><span class="c c2">🎧</span><span class="c c3">🟣</span>
          <span class="c c4">🟠</span><span class="c c5">🔵</span><span class="c c6">🟡</span>
          <button class="c-x">×</button>
        </div>
      </div>

      <!-- 底部标签页 -->
      <div class="stage-bottom">
        <div class="bottom-tabs">
          <button class="b-tab active">发现</button>
          <button class="b-tab" @click="router.push('/workspace?tab=history')">我的项目</button>
          <button class="b-tab">模板</button>
        </div>
        <button class="see-all">查看全部 ›</button>
      </div>
    </main>

    <input ref="fileInput" type="file" multiple hidden @change="onFiles" accept="image/*,.txt,.md,.json,.csv,.js,.html,.css" />
  </div>
</template>

<style scoped>
.dash { height: 100%; display: flex; background: var(--beige-100); }

/* ============ 侧边栏 ============ */
.side { width: 200px; flex-shrink: 0; display: flex; flex-direction: column; padding: 14px 10px 12px; border-right: 1px solid var(--line-soft); }
.side-brand { display: flex; align-items: center; gap: 8px; padding: 2px 6px 14px; }
.logo { width: 24px; height: 24px; border-radius: 7px; background: #0c0c0c; color: #fff; font-weight: 800; font-size: 13px; display: flex; align-items: center; justify-content: center; }
.logo-name { font-weight: 800; font-size: 15.5px; letter-spacing: -.01em; }
.side-collapse { margin-left: auto; color: var(--ink-30); font-size: 12px; }
.ws-switch {
  display: flex; align-items: center; gap: 7px; width: 100%;
  background: var(--beige-150); border-radius: var(--r-m); padding: 9px 10px; font-size: 12.5px; font-weight: 600;
}
.ws-icon { font-size: 13px; }
.ws-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chev { color: var(--ink-30); }
.nav { display: flex; flex-direction: column; gap: 2px; margin-top: 10px; }
.nav-item {
  display: flex; align-items: center; gap: 8px; text-align: left;
  padding: 9px 10px; border-radius: var(--r-m); font-size: 13px; font-weight: 550; color: var(--ink-80);
  transition: background .15s ease;
}
.nav-item:hover { background: var(--beige-150); }
.nav-item.active { background: var(--beige-200); font-weight: 650; }
.side-empty { margin-top: 40px; text-align: center; color: var(--ink-30); font-size: 12px; line-height: 1.8; }
.side-projects { margin-top: 18px; display: flex; flex-direction: column; gap: 2px; overflow-y: auto; }
.side-proj { display: flex; align-items: center; gap: 8px; text-align: left; padding: 8px 10px; border-radius: var(--r-s); font-size: 12.5px; color: var(--ink-55); }
.side-proj:hover { background: var(--beige-150); }
.dot { width: 6px; height: 6px; border-radius: 50%; background: var(--beige-300); flex-shrink: 0; }
.side-bottom { margin-top: auto; display: flex; flex-direction: column; gap: 8px; }
.promo {
  display: flex; flex-direction: column; align-items: flex-start; gap: 2px; text-align: left;
  background: var(--beige-50); border: 1px solid var(--line-soft); border-radius: var(--r-m);
  padding: 10px 12px; position: relative;
}
.promo b { font-size: 12px; }
.promo span { font-size: 11px; color: var(--ink-55); }
.promo i { position: absolute; right: 10px; top: 50%; transform: translateY(-50%); color: var(--ink-30); font-style: normal; }

/* ============ 主区 ============ */
.stage { flex: 1; display: flex; flex-direction: column; min-width: 0; position: relative; }
.stage-top { display: flex; justify-content: flex-end; padding: 14px 18px; }
.heart {
  width: 34px; height: 34px; border-radius: 50%; background: var(--beige-50);
  border: 1px solid var(--line-soft); display: flex; align-items: center; justify-content: center;
  color: var(--ink-55); font-size: 15px;
}
.hero-zone { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 0 24px; margin-top: -40px; }
.notice {
  display: flex; align-items: center; gap: 8px;
  background: var(--beige-50); border: 1px solid var(--line-soft);
  border-radius: var(--r-full); padding: 7px 14px; font-size: 12.5px;
  box-shadow: 0 1px 4px rgba(12,12,12,.04);
}
.n-chip { color: var(--ink-55); font-weight: 600; }
.n-text { color: var(--ink-55); }
.n-close { color: var(--ink-30); font-size: 14px; margin-left: 4px; }
.mascots { display: flex; gap: 4px; margin: 18px 0 10px; font-size: 26px; }
.m { filter: saturate(1.1); }
.m1 { transform: rotate(-8deg); } .m3 { transform: rotate(6deg); } .m5 { transform: rotate(-4deg); } .m7 { transform: rotate(8deg); }
.greet { font-size: 30px; font-weight: 800; letter-spacing: -.02em; margin-bottom: 26px; }

/* 输入卡片 */
.ask { width: 560px; max-width: 92%; position: relative; }
.ask textarea {
  width: 100%; border: 1px solid var(--line); background: var(--beige-50);
  border-radius: 18px; padding: 16px 16px 10px; font-size: 14.5px; color: var(--ink-100);
  outline: none; resize: none; box-shadow: 0 4px 24px rgba(12,12,12,.06);
  transition: border-color .18s ease, box-shadow .18s ease;
}
.ask textarea:focus { border-color: var(--blue-500); box-shadow: 0 4px 28px rgba(66,103,255,.12); }
.ask textarea::placeholder { color: var(--ink-30); }
.ask-bar { display: flex; align-items: center; gap: 6px; padding: 4px 10px 10px; margin-top: -36px; }
.x-btn { width: 26px; height: 26px; border-radius: 50%; background: var(--beige-150); color: var(--ink-55); font-size: 14px; }
.menu-btn {
  display: flex; align-items: center; gap: 5px;
  border: 1px solid var(--line); border-radius: var(--r-full);
  background: var(--beige-50); padding: 6px 12px; font-size: 12.5px; font-weight: 600;
}
.mode-btn {
  display: flex; align-items: center; gap: 4px;
  border: 1px solid var(--line); border-radius: var(--r-full);
  background: var(--beige-50); padding: 6px 12px; font-size: 12.5px; font-weight: 600;
}
.sound { width: 30px; height: 30px; border-radius: 50%; color: var(--ink-55); font-size: 13px; }
.go {
  width: 34px; height: 34px; border-radius: 50%; background: var(--orange);
  display: flex; align-items: center; justify-content: center; transition: transform .12s ease, background .18s ease;
}
.go:hover:not(:disabled) { background: #e09e12; }
.go:disabled { background: var(--beige-300); }
.flex1 { flex: 1; }

/* 功能菜单 */
.menu {
  position: absolute; left: 0; top: calc(100% + 8px); z-index: 30;
  width: 230px; background: var(--beige-50); border: 1px solid var(--line-soft);
  border-radius: 14px; box-shadow: 0 12px 40px rgba(12,12,12,.14);
  padding: 6px; display: flex; flex-direction: column; gap: 1px;
}
.menu-item {
  display: flex; align-items: center; gap: 9px; width: 100%; text-align: left;
  padding: 10px 10px; border-radius: 9px; font-size: 13px; font-weight: 550; color: var(--ink-80);
}
.menu-item:hover { background: var(--beige-100); }
.menu-item.disabled { opacity: .45; cursor: default; }
.mi-icon { font-size: 14px; width: 18px; text-align: center; }
.chev-r { margin-left: auto; color: var(--ink-30); }
.mini-chip { font-size: 10.5px; font-weight: 600; color: var(--blue-600); background: var(--blue-50); border-radius: var(--r-full); padding: 2px 8px; margin-left: 6px; }
.switch { margin-left: auto; width: 30px; height: 17px; border-radius: var(--r-full); background: var(--beige-300); position: relative; transition: background .18s ease; }
.switch .knob { position: absolute; top: 2px; left: 2px; width: 13px; height: 13px; border-radius: 50%; background: #fff; transition: left .18s ease; }
.switch.on { background: var(--blue-500); }
.switch.on .knob { left: 15px; }

/* 已传附件 */
.atts { display: flex; flex-wrap: wrap; gap: 6px; padding: 0 14px 4px; }
.att { display: inline-flex; align-items: center; gap: 6px; background: var(--beige-100); border: 1px solid var(--line-soft); border-radius: var(--r-full); padding: 4px 10px; font-size: 12px; }
.att-thumb { width: 16px; height: 16px; border-radius: 4px; background: var(--beige-200); }
.att-name { max-width: 140px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--ink-80); }
.att-x { color: var(--ink-30); font-size: 13px; }

/* 连接器小图标行 */
.connectors { display: flex; align-items: center; gap: 7px; margin-top: 16px; padding: 8px 14px; background: var(--beige-50); border: 1px solid var(--line-soft); border-radius: var(--r-full); }
.c { font-size: 14px; }
.c-x { color: var(--ink-30); margin-left: 4px; font-size: 13px; }

/* 底部标签 */
.stage-bottom {
  display: flex; align-items: center; padding: 14px 26px 18px;
}
.bottom-tabs { display: flex; gap: 18px; flex: 1; }
.b-tab { font-size: 13.5px; font-weight: 600; color: var(--ink-30); padding: 6px 2px; }
.b-tab.active { color: var(--ink-100); }
.see-all { font-size: 12.5px; color: var(--ink-55); font-weight: 550; }

.fade-enter-active, .fade-leave-active { transition: opacity .25s ease; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
