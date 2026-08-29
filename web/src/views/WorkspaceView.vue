<script setup>
import { ref, reactive, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api'

/* ---------- 用户 ---------- */
const user = computed(() => {
  try { return JSON.parse(localStorage.getItem('atomix_user') || '{}') } catch { return {} }
})
function logout() {
  localStorage.removeItem('atomix_token')
  localStorage.removeItem('atomix_user')
  location.href = '/login'
}

/* ---------- 全局状态 ---------- */
const projects = ref([])
const activeProject = ref(null)
const previewUrl = ref('')
const rightTab = ref('preview')
const serverMode = ref('')
const chatBox = ref(null)
const composer = ref('')
const running = ref(false)
const mode = ref('build')
const pendingAttachments = ref([])
const fileInput = ref(null)
const modeOpen = ref(false)

const modes = [
  { id: 'build', label: '构建' },
  { id: 'plan', label: '规划' },
  { id: 'research', label: '深度研究' }
]
const modeLabel = computed(() => modes.find(m => m.id === mode.value)?.label || '构建')

const stages = ['plan', 'build', 'run', 'verify', 'done']
const stageMeta = {
  plan: { label: '规划', icon: '🧠' },
  build: { label: '构建', icon: '⚙️' },
  run: { label: '运行', icon: '🚀' },
  verify: { label: '校验', icon: '🔍' },
  done: { label: '完成', icon: '🎉' }
}

const examples = [
  '做一个极简待办清单，支持勾选完成和进度统计',
  '做一个彩色便签墙，支持置顶和搜索',
  '做一个项目看板，卡片可以在待办/进行中/已完成之间移动'
]

/* ---------- 会话线程（对话式） ----------
   thread: [{role:'user', text} | {role:'assistant', run}] */
const thread = ref([])
let seq = 0
const procOpen = reactive({})

function newUserMsg(text) {
  return { id: 'u' + (++seq), role: 'user', text, ts: Date.now() }
}
function newRunMsg(brief) {
  return {
    id: 'r' + (++seq), role: 'assistant', brief,
    stageState: Object.fromEntries(stages.map(s => [s, 'pending'])),
    currentStage: '',
    events: [],
    status: 'running',
    errorText: '',
    projectId: null,
    projectName: '',
    summary: ''
  }
}
function stageProgress(run) {
  const done = stages.filter(s => run.stageState[s] === 'done').length
  return Math.round((done / stages.length) * 100)
}
function procIsOpen(run) { return run.status !== 'done' || !!procOpen[run.id] }
function toggleProc(run) { procOpen[run.id] = !procOpen[run.id] }

/* ---------- 滚动（贴底自动跟随） ---------- */
let stick = true
function onChatScroll() {
  const el = chatBox.value
  if (!el) return
  stick = el.scrollHeight - el.scrollTop - el.clientHeight < 60
}
function autoscroll() {
  if (!stick) return
  nextTick(() => { const el = chatBox.value; if (el) el.scrollTop = el.scrollHeight })
}
function scrollToBottom() {
  stick = true
  nextTick(() => { const el = chatBox.value; if (el) el.scrollTop = el.scrollHeight })
}

/* ---------- 初始化 ---------- */
init()
async function init() {
  try {
    const health = await fetch('/api/health').then(r => r.json())
    serverMode.value = health.mode === 'live' ? 'DeepSeek 已接入' : '演示模式'
  } catch { serverMode.value = '' }
  loadProjects()
  // 接收首页 Dashboard 传来的待处理任务
  try {
    const raw = sessionStorage.getItem('atomix_pending')
    if (raw) {
      sessionStorage.removeItem('atomix_pending')
      const p = JSON.parse(raw)
      mode.value = p.mode || 'build'
      pendingAttachments.value = p.attachments || []
      if (p.chat) {
        thread.value.push(newUserMsg(p.brief))
        thread.value.push(reactive({ id: 'r' + (++seq), role: 'assistant', kind: 'chat', text: p.chat, status: 'chat' }))
        autoscroll()
      } else if (p.brief) {
        routeMessageWith(p.brief, p.attachmentIds || [])
      }
    }
  } catch {}
}
async function loadProjects() {
  try { projects.value = await api.listProjects() } catch {}
}

/* ---------- SSE 事件 → 助手消息 ---------- */
function applyStage(run, stage, message) {
  if (run.currentStage) run.stageState[run.currentStage] = 'done'
  run.currentStage = stage
  run.stageState[stage] = 'active'
  run.events.push({ stage, message, level: 'stage', ts: Date.now() })
  autoscroll()
}
function applyDetail(run, stage, message, level) {
  run.events.push({ stage, message, level, ts: Date.now() })
  autoscroll()
}
function finishRun(run) {
  stages.forEach(s => { run.stageState[s] = 'done' })
  run.status = 'done'
  const lastDone = [...run.events].reverse().find(e => e.stage === 'done')
  run.summary = lastDone ? lastDone.message : '构建完成，预览已就绪'
  running.value = false
  rightTab.value = 'preview'
  autoscroll()
}
function failRun(run, text) {
  stages.forEach(s => { if (run.stageState[s] === 'active') run.stageState[s] = 'done' })
  run.status = 'failed'
  run.errorText = text || run.errorText || '构建失败，请重试'
  running.value = false
  autoscroll()
}

/* ---------- 发送：新建 or 迭代修改 ---------- */

function send() {
  const text = composer.value.trim()
  if (!text || running.value) return
  composer.value = ''
  routeMessage(text)
}

async function routeMessage(text) {
  routeMessageWith(text, pendingAttachments.value.map(a => a.id))
}

async function routeMessageWith(text, attachIds) {
  running.value = true
  thread.value.push(newUserMsg(text))
  scrollToBottom()
  let r
  try {
    r = await classify(text, attachIds, mode.value)
  } catch (e) {
    running.value = false
    thread.value.push(reactive({
      id: 'r' + (++seq), role: 'assistant', brief: '', status: 'failed',
      errorText: e.message || '网络错误，请重试', events: [], stageState: {},
      projectId: null, projectName: '', summary: ''
    }))
    autoscroll()
    return
  }
  if (r.intent === 'chat' || r.intent === 'clarify') {
    thread.value.push(reactive({
      id: 'r' + (++seq), role: 'assistant', kind: 'chat', text: r.reply || '我在的，请继续说～',
      status: 'chat'
    }))
    running.value = false
    autoscroll()
    return
  }
  const brief = r.brief || text
  const ids = attachIds || []
  if (activeProject.value) refineSend(brief, true, ids)
  else generateSend(brief, true, ids)
}

async function classify(text, attachIds = [], m = 'build') {
  const resp = await fetch('/api/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + (localStorage.getItem('atomix_token') || '') },
    body: JSON.stringify({ message: text, attachmentIds: attachIds, mode: m })
  })
  if (!resp.ok) throw new Error('意图识别失败 (' + resp.status + ')')
  return resp.json()
}

function generateSend(text, alreadyRouted, attachIds = []) {
  if (!alreadyRouted) { running.value = true; thread.value.push(newUserMsg(text)) }
  const run = reactive(newRunMsg(text))
  thread.value.push(run)
  scrollToBottom()

  const params = new URLSearchParams({
    brief: text, mode: mode.value,
    attachmentIds: attachIds.join(','), t: localStorage.getItem('atomix_token') || ''
  })
  const es = new EventSource('/api/generate?' + params.toString())
  es.addEventListener('stage', e => {
    const [stage, message] = e.data.split('\x1f')
    applyStage(run, stage, message)
  })
  es.addEventListener('detail', e => {
    const [stage, message, level] = e.data.split('\x1f')
    applyDetail(run, stage, message, level)
  })
  es.addEventListener('done', e => {
    try {
      const data = JSON.parse(e.data)
      run.projectId = data.project.id
      run.projectName = data.project.name
      setActiveProject(data.project)
    } catch {}
    finishRun(run)
    loadProjects()
    es.close()
  })
  es.addEventListener('error', e => {
    if (e.data) { failRun(run, e.data) }
    else if (run.status === 'running') {
      if (run.projectId) finishRun(run)
      else failRun(run, '连接中断，请重试')
    }
    es.close()
  })
}

async function refineSend(text, alreadyRouted, attachIds = []) {
  if (!alreadyRouted) { running.value = true; thread.value.push(newUserMsg(text)) }
  const pid = activeProject.value.id
  const run = reactive(newRunMsg(text))
  thread.value.push(run)
  scrollToBottom()
  try {
    const resp = await fetch('/api/projects/' + pid + '/refine', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + (localStorage.getItem('atomix_token') || '') },
      body: JSON.stringify({ instruction: text, attachmentIds: attachIds })
    })
    if (!resp.ok || !resp.body) throw new Error('修改请求失败 (' + resp.status + ')')
    const reader = resp.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      const chunks = buf.split('\n\n')
      buf = chunks.pop()
      for (const chunk of chunks) {
        const evLine = chunk.split('\n').find(l => l.startsWith('event:'))
        const dataLine = chunk.split('\n').find(l => l.startsWith('data:'))
        if (!evLine || !dataLine) continue
        const ev = evLine.slice(6).trim()
        const data = dataLine.slice(5).trim()
        if (ev === 'stage') {
          const [stage, message] = data.split('\x1f')
          applyStage(run, stage, message)
        } else if (ev === 'detail') {
          const [stage, message, level] = data.split('\x1f')
          applyDetail(run, stage, message, level)
        } else if (ev === 'done') {
          try {
            const d = JSON.parse(data)
            run.projectId = d.project.id
            run.projectName = d.project.name
            setActiveProject(d.project)
          } catch {}
        } else if (ev === 'error') {
          run.errorText = data
        }
      }
      autoscroll()
    }
    if (run.errorText) failRun(run)
    else finishRun(run)
    loadProjects()
  } catch (e) {
    failRun(run, e.message || '修改失败')
  }
}

/* ---------- 项目与预览 ---------- */
const APP_DATA_PREFIX = 'atomix_app_'
function readAppData(projectId) {
  try {
    return JSON.parse(localStorage.getItem(APP_DATA_PREFIX + projectId) || '{}')
  } catch { return {} }
}

function onShimMessage(e) {
  const d = e.data
  if (!d || d.source !== 'atomix-shim' || !activeProject.value) return
  if (d.type === 'storage') {
    const key = APP_DATA_PREFIX + activeProject.value.id
    let data = {}
    try { data = JSON.parse(localStorage.getItem(key) || '{}') } catch {}
    if (d.value === null) delete data[d.key]
    else data[d.key] = d.value
    localStorage.setItem(key, JSON.stringify(data))
  } else if (d.type === 'clear') {
    localStorage.removeItem(APP_DATA_PREFIX + activeProject.value.id)
  }
}

function setActiveProject(p) {
  activeProject.value = p
  previewUrl.value = api.previewUrl(p.id, readAppData(p.id))
  rightTab.value = 'preview'
}

/* 打开历史项目：用 brief + 事件流还原该项目的对话 */
async function openProject(p) {
  if (running.value) return
  setActiveProject(p)
  let events = []
  try { events = await api.getEvents(p.id) } catch {}
  thread.value = [newUserMsg(p.brief)]
  const run = reactive(newRunMsg(p.brief))
  run.status = 'done'
  run.projectId = p.id
  run.projectName = p.name
  let seenStage = ''
  for (const ev of events) {
    if (ev.level === 'stage') {
      if (seenStage) run.stageState[seenStage] = 'done'
      seenStage = ev.stage
      run.stageState[ev.stage] = 'done'
      run.currentStage = ''
    }
    run.events.push({ stage: ev.stage, message: ev.message, level: ev.level, ts: ev.ts })
  }
  if (seenStage) run.stageState[seenStage] = 'done'
  const lastDone = [...events].reverse().find(e => e.stage === 'done')
  run.summary = lastDone ? lastDone.message : '构建完成，预览已就绪'
  thread.value.push(run)
  scrollToBottom()
}

/* 新对话：回到空白状态，输入框恢复"新建应用"模式 */
function newChat() {
  if (running.value) return
  thread.value = []
  activeProject.value = null
  previewUrl.value = ''
  rightTab.value = 'preview'
  composer.value = ''
}

const composerPlaceholder = computed(() =>
  activeProject.value
    ? '继续修改「' + (activeProject.value.name || '当前应用') + '」，例如：加上深色模式…'
    : '描述你想生成的应用，例如：做一个番茄钟计时器…'
)

/* ---------- 附件与模式 ---------- */
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
      if (resp.ok) pendingAttachments.value.push(meta)
    } catch {}
  }
  e.target.value = ''
}
function removeAttachment(idx) { pendingAttachments.value.splice(idx, 1) }
function onDocClick(e) {
  if (modeOpen.value && !e.target.closest('.mode-wrap')) modeOpen.value = false
}
onMounted(() => {
  window.addEventListener('message', onShimMessage)
  document.addEventListener('click', onDocClick)
})
onBeforeUnmount(() => {
  window.removeEventListener('message', onShimMessage)
  document.removeEventListener('click', onDocClick)
})
</script>

<template>
  <div class="ws">
    <!-- 顶栏 -->
    <header class="topbar">
      <div class="brand">
        <span class="mark">A</span>
        <span class="name">Atomix</span>
        <span class="badge" :class="serverMode === 'DeepSeek 已接入' ? 'live' : 'demo'">{{ serverMode }}</span>
      </div>
      <div class="user">
        <span class="avatar">{{ (user.email || '?')[0].toUpperCase() }}</span>
        <span class="email">{{ user.email }}</span>
        <button class="ghost" @click="logout">退出</button>
      </div>
    </header>

    <div class="main">
      <!-- 左栏：对话流 -->
      <section class="left">
        <div ref="chatBox" class="chat" @scroll="onChatScroll">
          <!-- 空状态 -->
          <div v-if="!thread.length" class="hero">
            <div class="hero-mark">A</div>
            <h1>今天想构建什么？</h1>
            <p>描述你的想法，Atomix 通过 ReAct 循环完成规划、编码、自检，几分钟交付一个可用的网页应用。</p>
            <div class="examples">
              <button v-for="x in examples" :key="x" class="chip" :disabled="running" @click="composer = x">{{ x }}</button>
            </div>
          </div>

          <!-- 消息流 -->
          <template v-for="m in thread" :key="m.id">
            <!-- 用户消息 -->
            <div v-if="m.role === 'user'" class="msg user">
              <div class="bubble">{{ m.text }}</div>
              <div class="ava u">{{ (user.email || '?')[0].toUpperCase() }}</div>
            </div>

            <!-- 助手消息（聊天回复） -->
            <div v-else-if="m.kind === 'chat'" class="msg assistant">
              <div class="ava">A</div>
              <div class="bubble chat-bubble">{{ m.text }}</div>
            </div>

            <!-- 助手消息（构建回合） -->
            <div v-else class="msg assistant">
              <div class="ava">A</div>
              <div class="bubble run">
                <div class="run-head">
                  <b>{{ m.projectName || 'Atomix' }}</b>
                  <span class="run-status" :class="m.status">
                    {{ m.status === 'running' ? '构建中…' : m.status === 'failed' ? '构建失败' : '已完成' }}
                  </span>
                </div>

                <!-- 运行中的阶段进度 -->
                <div v-if="m.status === 'running'" class="stage-strip">
                  <div class="stages">
                    <div v-for="s in stages" :key="s" class="stage" :class="m.stageState[s]">
                      <span class="icon">{{ stageMeta[s].icon }}</span>
                      <span>{{ stageMeta[s].label }}</span>
                    </div>
                  </div>
                  <div class="progress"><div :style="{ width: stageProgress(m) + '%' }"></div></div>
                </div>

                <!-- 构建过程（可折叠） -->
                <div class="process" :class="{ open: procIsOpen(m) }">
                  <button class="proc-toggle" @click="toggleProc(m)">
                    <span class="chev">▾</span> 构建过程 · {{ m.events.length }} 步
                  </button>
                  <div v-if="procIsOpen(m)" class="proc-list">
                    <div v-for="(t, i) in m.events" :key="i" class="tl-item" :class="t.level">
                      <span class="tl-stage">
                        <template v-if="t.stage === 'think'">🧠 思考</template>
                        <template v-else-if="t.stage === 'act'">⚡ 行动</template>
                        <template v-else-if="t.stage === 'observe'">👁 观察</template>
                        <template v-else>{{ stageMeta[t.stage] ? stageMeta[t.stage].label : t.stage }}</template>
                      </span>
                      <span class="tl-msg">{{ t.message }}</span>
                      <span class="tl-time">{{ new Date(t.ts).toLocaleTimeString() }}</span>
                    </div>
                  </div>
                </div>

                <!-- 结果 / 错误 -->
                <div v-if="m.status === 'done'" class="run-result">
                  <span>{{ m.summary }}</span>
                  <button v-if="m.projectId" class="view-btn" @click="rightTab = 'preview'">查看应用 →</button>
                </div>
                <div v-if="m.status === 'failed'" class="run-error">{{ m.errorText }}</div>
              </div>
            </div>
          </template>
        </div>

        <!-- 底部输入区（固定） -->
        <footer class="composer">
          <div v-if="pendingAttachments.length" class="atts-row">
            <span v-for="(a, i) in pendingAttachments" :key="a.id" class="att">
              <span class="att-ico">{{ a.isImage ? '🖼' : '📄' }}</span>
              <span class="att-name">{{ a.name }}</span>
              <button class="att-x" @click="removeAttachment(i)">×</button>
            </span>
          </div>
          <div class="composer-box">
            <textarea
              v-model="composer"
              rows="2"
              :placeholder="composerPlaceholder"
              :disabled="running"
              @keydown.enter.exact.prevent="send"
              @keydown.ctrl.enter.prevent="send"
              @keydown.meta.enter.prevent="send"
            ></textarea>
            <div class="composer-tools">
              <button class="tool-btn" @click="pickFiles" title="上传附件（图片走多模态识图，文本注入上下文）">📎 附件</button>
              <span class="flex1"></span>
              <div class="mode-wrap">
                <button class="tool-btn mode" @click.stop="modeOpen = !modeOpen">
                  {{ modeLabel }} <span class="chev">⌄</span>
                </button>
                <div v-if="modeOpen" class="mode-menu">
                  <button
                    v-for="m in modes" :key="m.id"
                    class="mode-item" :class="{ active: mode === m.id }"
                    @click="mode = m.id; modeOpen = false"
                  >
                    {{ m.label }}
                    <span v-if="m.id === 'plan'" class="mode-desc">先出规划再实施</span>
                    <span v-else-if="m.id === 'research'" class="mode-desc">先拆解需求再构建</span>
                  </button>
                </div>
              </div>
              <button class="go" :disabled="running || !composer.trim()" @click="send" :title="running ? '构建中…' : '发送'">
                <svg v-if="!running" viewBox="0 0 16 16" width="15" height="15" fill="none"><path d="M2 8h10M8.5 3.5 13 8l-4.5 4.5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>
                <span v-else class="spin">◐</span>
              </button>
            </div>
          </div>
          <div class="composer-hint">
            <span>Enter 发送 · Shift+Enter 换行</span>
            <span v-if="activeProject" class="ctx">基于「{{ activeProject.name }}」迭代修改</span>
            <button v-if="activeProject && !running" class="new-chat" @click="newChat">＋ 新对话</button>
          </div>
        </footer>
      </section>

      <!-- 右栏：预览 / 历史 -->
      <section class="right">
        <div class="tabs">
          <button :class="{ active: rightTab === 'preview' }" @click="rightTab = 'preview'">应用预览</button>
          <button :class="{ active: rightTab === 'history' }" @click="rightTab = 'history'">
            历史项目 <span class="count">{{ projects.length }}</span>
          </button>
        </div>

        <div v-show="rightTab === 'preview'" class="preview-wrap">
          <div v-if="!activeProject" class="empty-tip big">
            <div class="big-mono">// ready to build</div>
            还没有生成的应用
            <span>告诉 Atomix 你的想法，让 AI 为你构建第一个应用</span>
          </div>
          <template v-else>
            <div class="preview-head">
              <b>{{ activeProject.name }}</b>
              <span class="meta">{{ activeProject.template }} · {{ activeProject.status }}</span>
              <a v-if="previewUrl" :href="previewUrl" target="_blank" class="open-link">新窗口打开 ↗</a>
            </div>
            <iframe
              v-if="previewUrl"
              :src="previewUrl"
              sandbox="allow-scripts allow-forms allow-modals"
              class="preview-frame"
            ></iframe>
          </template>
        </div>

        <div v-show="rightTab === 'history'" class="history-wrap">
          <div v-if="!projects.length" class="empty-tip big">
            <div class="big-mono">// empty</div>
            暂无历史项目
            <span>生成的应用会持久化保存，可随时回看</span>
          </div>
          <div
            v-for="p in projects"
            :key="p.id"
            class="proj-card"
            :class="{ active: activeProject && activeProject.id === p.id }"
            @click="openProject(p)"
          >
            <div class="proj-name">{{ p.name }}</div>
            <div class="proj-brief">{{ p.brief }}</div>
            <div class="proj-meta">
              <span class="tag">{{ p.template }}</span>
              <span>{{ new Date(p.createdAt).toLocaleString() }}</span>
            </div>
          </div>
        </div>
      </section>
    </div>
    <input ref="fileInput" type="file" multiple hidden @change="onFiles" accept="image/*,.txt,.md,.json,.csv,.js,.html,.css" />
  </div>
</template>

<style scoped>
/* ============ 布局骨架 ============ */
.ws { height: 100%; display: flex; flex-direction: column; background: var(--beige-100); }
.main { flex: 1; display: flex; min-height: 0; }

/* ============ 顶栏 ============ */
.topbar {
  height: 58px; display: flex; align-items: center; justify-content: space-between;
  padding: 0 22px;
  background: rgba(246, 246, 246, .78);
  backdrop-filter: saturate(180%) blur(10px);
  border-bottom: 1px solid var(--line-soft);
  flex-shrink: 0; z-index: 5;
}
.brand { display: flex; align-items: center; gap: 10px; }
.mark {
  width: 27px; height: 27px; border-radius: 9px;
  background: var(--blue-500); color: #fff; font-weight: 800; font-size: 15px;
  display: flex; align-items: center; justify-content: center;
}
.name { font-weight: 700; font-size: 16.5px; letter-spacing: -.01em; }
.badge { font-size: 11px; font-weight: 600; font-family: var(--font-mono); padding: 3px 10px; border-radius: var(--r-full); }
.badge.demo { color: var(--ink-55); background: var(--beige-150); border: 1px solid var(--line-soft); }
.badge.live { color: var(--green); background: rgba(45, 187, 92, .1); border: 1px solid rgba(45, 187, 92, .25); }
.user { display: flex; align-items: center; gap: 10px; font-size: 13px; color: var(--ink-55); }
.avatar {
  width: 29px; height: 29px; border-radius: 50%;
  background: var(--blue-500); color: #fff; font-weight: 700; font-size: 13px;
  display: flex; align-items: center; justify-content: center;
}
.ghost {
  color: var(--ink-55); border: 1px solid var(--line); border-radius: var(--r-full);
  padding: 6px 14px; font-size: 12.5px; font-weight: 500; transition: all .18s ease;
}
.ghost:hover { color: var(--ink-100); border-color: var(--ink-30); background: var(--beige-50); }

/* ============ 左栏：对话 ============ */
.left {
  width: 46%; min-width: 420px;
  display: flex; flex-direction: column;
  border-right: 1px solid var(--line-soft);
  min-height: 0;
}
.chat { flex: 1; overflow-y: auto; padding: 20px 22px 10px; display: flex; flex-direction: column; gap: 16px; }

/* 空状态 Hero */
.hero { margin: auto; text-align: center; max-width: 460px; padding: 40px 0; }
.hero-mark {
  width: 44px; height: 44px; border-radius: 14px; margin: 0 auto 16px;
  background: var(--blue-500); color: #fff; font-weight: 800; font-size: 22px;
  display: flex; align-items: center; justify-content: center;
  box-shadow: 0 8px 24px rgba(66, 103, 255, .25);
}
.hero h1 { font-size: 22px; font-weight: 800; letter-spacing: -.01em; margin-bottom: 8px; }
.hero p { color: var(--ink-55); font-size: 13.5px; line-height: 1.7; margin-bottom: 20px; }
.hero .examples { display: flex; flex-direction: column; gap: 8px; align-items: stretch; }
.chip {
  background: var(--beige-50); border: 1px solid var(--line-soft); color: var(--ink-55);
  border-radius: var(--r-m); padding: 10px 14px; font-size: 13px; text-align: left;
  transition: all .18s ease;
}
.chip:hover:not(:disabled) { color: var(--ink-100); border-color: var(--ink-30); background: #fff; }
.chip:disabled { opacity: .5; cursor: default; }

/* 消息 */
.msg { display: flex; gap: 10px; align-items: flex-start; }
.msg.user { justify-content: flex-end; }
.ava {
  width: 28px; height: 28px; border-radius: 9px; flex-shrink: 0;
  background: var(--blue-500); color: #fff; font-weight: 800; font-size: 13px;
  display: flex; align-items: center; justify-content: center; margin-top: 2px;
}
.ava.u { border-radius: 50%; background: var(--beige-300); color: var(--ink-80); }
.bubble {
  background: var(--beige-50); border: 1px solid var(--line-soft);
  border-radius: var(--r-l); padding: 12px 16px;
  font-size: 14px; line-height: 1.65; color: var(--ink-100);
  max-width: 82%;
  box-shadow: 0 1px 3px rgba(12,12,12,.03);
}
.msg.user .bubble {
  background: var(--blue-500); color: #fff; border: none;
  border-bottom-right-radius: 6px; white-space: pre-wrap; word-break: break-word;
}
.msg.assistant .bubble { border-bottom-left-radius: 6px; min-width: 0; flex: 1; max-width: none; }
.chat-bubble { max-width: 100% !important; white-space: pre-wrap; }

/* 助手运行块 */
.run-head { display: flex; align-items: center; gap: 10px; margin-bottom: 8px; font-size: 14px; }
.run-status { font-size: 11.5px; font-weight: 600; font-family: var(--font-mono); padding: 2px 10px; border-radius: var(--r-full); }
.run-status.running { color: var(--blue-600); background: var(--blue-50); }
.run-status.done { color: var(--green); background: rgba(45, 187, 92, .1); }
.run-status.failed { color: var(--red); background: rgba(201, 68, 74, .08); }

.stage-strip { margin: 4px 0 10px; padding: 10px 12px; background: var(--beige-100); border-radius: var(--r-m); }
.stages { display: flex; align-items: center; gap: 4px; }
.stage {
  flex: 1; display: flex; align-items: center; justify-content: center; gap: 5px;
  font-size: 12px; color: var(--ink-30); padding: 6px 0; border-radius: var(--r-full);
  transition: all .25s ease; font-weight: 500;
}
.stage.active { background: var(--blue-50); color: var(--blue-600); font-weight: 700; }
.stage.done { color: var(--green); }
.progress { margin-top: 8px; height: 4px; background: var(--beige-150); border-radius: 999px; overflow: hidden; }
.progress > div { height: 100%; background: var(--blue-500); border-radius: 999px; transition: width .4s ease; }

/* 构建过程（折叠块） */
.process { border: 1px solid var(--line-soft); border-radius: var(--r-m); overflow: hidden; background: var(--beige-100); }
.proc-toggle {
  width: 100%; display: flex; align-items: center; gap: 7px;
  padding: 9px 12px; font-size: 12.5px; font-weight: 600; color: var(--ink-55);
  transition: color .18s ease;
}
.proc-toggle:hover { color: var(--ink-100); }
.chev { display: inline-block; transition: transform .2s ease; color: var(--ink-30); }
.process.open .chev { transform: rotate(0deg); }
.process:not(.open) .chev { transform: rotate(-90deg); }
.proc-list { max-height: 280px; overflow-y: auto; display: flex; flex-direction: column; gap: 4px; padding: 0 10px 10px; }
.tl-item {
  display: flex; align-items: baseline; gap: 8px; font-size: 12.5px;
  padding: 7px 10px; border-radius: var(--r-s); background: var(--beige-50);
}
.tl-item.stage { background: var(--blue-50); font-weight: 600; color: var(--blue-600); }
.tl-item.warn { background: rgba(239, 174, 34, .1); color: #a87707; }
.tl-item.err { background: rgba(201, 68, 74, .08); color: var(--red); }
.tl-item.think { background: var(--beige-50); border: 1px dashed var(--line); }
.tl-item.think .tl-msg { color: var(--ink-55); }
.tl-item.act { background: rgba(66, 103, 255, .06); }
.tl-item.act .tl-msg { color: var(--blue-600); font-family: var(--font-mono); font-size: 12px; }
.tl-item.observe { background: rgba(15, 139, 141, .06); }
.tl-item.observe .tl-msg { color: #0b5f60; }
.tl-stage { color: var(--ink-30); font-size: 11px; flex-shrink: 0; font-family: var(--font-mono); }
.tl-item.stage .tl-stage { color: var(--blue-500); }
.tl-msg { flex: 1; color: var(--ink-80); min-width: 0; }
.tl-item.stage .tl-msg { color: var(--blue-600); }
.tl-time { color: var(--ink-30); font-size: 11px; flex-shrink: 0; font-family: var(--font-mono); }

/* 结果与错误 */
.run-result { display: flex; align-items: center; gap: 12px; margin-top: 10px; font-size: 13.5px; color: var(--ink-80); }
.view-btn {
  color: var(--blue-500); font-weight: 600; font-size: 12.5px;
  border: 1px solid rgba(66, 103, 255, .3); border-radius: var(--r-full);
  padding: 5px 13px; transition: all .18s ease; flex-shrink: 0;
}
.view-btn:hover { background: var(--blue-50); border-color: var(--blue-500); }
.run-error {
  margin-top: 10px; font-size: 13px; color: var(--red);
  background: rgba(201, 68, 74, .08); border-radius: var(--r-s); padding: 9px 12px;
}

/* ============ 底部输入区 ============ */
.composer { flex-shrink: 0; padding: 10px 22px 16px; }
.atts-row { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 8px; }
.att { display: inline-flex; align-items: center; gap: 6px; background: var(--beige-50); border: 1px solid var(--line-soft); border-radius: var(--r-full); padding: 5px 12px; font-size: 12px; }
.att-ico { font-size: 13px; }
.att-name { max-width: 160px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--ink-80); }
.att-x { color: var(--ink-30); font-size: 13px; }
.composer-box {
  display: flex; flex-direction: column;
  background: var(--beige-50); border: 1px solid var(--line);
  border-radius: 18px; padding: 12px 12px 8px 16px;
  box-shadow: 0 2px 12px rgba(12,12,12,.06);
  transition: border-color .18s ease, box-shadow .18s ease;
}
.composer-box:focus-within { border-color: var(--blue-500); box-shadow: 0 2px 16px rgba(66,103,255,.14); }
.composer-box textarea {
  border: none; outline: none; background: transparent; resize: none;
  color: var(--ink-100); font-size: 14px; line-height: 1.6;
  max-height: 120px; padding: 0; width: 100%;
}
.composer-tools { display: flex; align-items: center; gap: 6px; padding: 6px 0 2px; }
.flex1 { flex: 1; }
.tool-btn {
  display: inline-flex; align-items: center; gap: 5px;
  border: 1px solid var(--line); border-radius: var(--r-full);
  background: transparent; padding: 5px 12px; font-size: 12px; font-weight: 600; color: var(--ink-55);
  transition: all .18s ease;
}
.tool-btn:hover { color: var(--ink-100); border-color: var(--ink-30); background: var(--beige-100); }
.mode-wrap { position: relative; }
.mode-menu {
  position: absolute; bottom: calc(100% + 8px); right: 0; z-index: 30;
  width: 200px; background: var(--beige-50); border: 1px solid var(--line-soft);
  border-radius: 12px; box-shadow: 0 10px 36px rgba(12,12,12,.14); padding: 5px;
  display: flex; flex-direction: column; gap: 1px;
}
.mode-item {
  display: flex; align-items: center; justify-content: space-between; gap: 8px;
  padding: 9px 11px; border-radius: 8px; font-size: 13px; font-weight: 550; text-align: left;
}
.mode-item:hover { background: var(--beige-100); }
.mode-item.active { background: var(--blue-50); color: var(--blue-600); font-weight: 650; }
.mode-desc { font-size: 11px; color: var(--ink-30); font-weight: 400; }
.composer-box textarea::placeholder { color: var(--ink-30); }
.composer-box textarea:disabled { opacity: .6; }
.go {
  width: 36px; height: 36px; border-radius: 12px; flex-shrink: 0;
  background: var(--orange); color: #fff;
  display: flex; align-items: center; justify-content: center;
  transition: background .18s ease, transform .12s ease;
}
.go:hover:not(:disabled) { background: #e09e12; }
.go:active:not(:disabled) { transform: scale(.95); }
.go:disabled { background: var(--beige-300); color: var(--beige-50); cursor: default; }
.spin { display: inline-block; animation: rot 1s linear infinite; font-size: 16px; }
@keyframes rot { to { transform: rotate(360deg); } }
.composer-hint {
  display: flex; align-items: center; gap: 12px;
  margin-top: 8px; padding: 0 6px;
  font-size: 11.5px; color: var(--ink-30); font-family: var(--font-mono);
}
.ctx { color: var(--blue-600); }
.new-chat {
  margin-left: auto; color: var(--ink-55); font-size: 12px; font-weight: 600;
  border: 1px solid var(--line); border-radius: var(--r-full); padding: 4px 12px;
  font-family: var(--font-sans); transition: all .18s ease;
}
.new-chat:hover { color: var(--ink-100); border-color: var(--ink-30); background: var(--beige-50); }

/* ============ 右栏 ============ */
.right { flex: 1; display: flex; flex-direction: column; min-width: 0; padding: 16px 16px 16px 0; }
.tabs {
  display: flex; gap: 6px; background: var(--beige-150);
  border-radius: var(--r-full); padding: 4px; align-self: flex-start; margin-bottom: 12px;
}
.tabs button {
  color: var(--ink-55); padding: 8px 18px; border-radius: var(--r-full);
  font-size: 13px; font-weight: 600; transition: all .18s ease;
}
.tabs button.active { background: var(--beige-50); color: var(--ink-100); box-shadow: 0 1px 3px rgba(12,12,12,.08); }
.tabs .count {
  background: var(--beige-200); color: var(--ink-80); font-size: 11px; font-weight: 700;
  border-radius: var(--r-full); padding: 1px 8px; margin-left: 5px;
}
.tabs button.active .count { background: var(--blue-500); color: #fff; }

.preview-wrap, .history-wrap {
  flex: 1; background: var(--beige-50); border: 1px solid var(--line-soft);
  border-radius: var(--r-l); min-height: 0; display: flex; flex-direction: column;
  overflow: hidden; box-shadow: 0 1px 3px rgba(12,12,12,.03);
}
.history-wrap { overflow-y: auto; }
.empty-tip { color: var(--ink-55); font-size: 13px; text-align: center; }
.empty-tip.big { margin: auto; padding: 60px 20px; line-height: 2; font-size: 15px; font-weight: 600; color: var(--ink-80); }
.empty-tip.big span { display: block; font-size: 13px; font-weight: 400; color: var(--ink-55); }
.big-mono { font-family: var(--font-mono); font-size: 13px; color: var(--ink-30); margin-bottom: 10px; }
.preview-head {
  display: flex; align-items: center; gap: 10px; padding: 13px 18px;
  border-bottom: 1px solid var(--line-soft); font-size: 14.5px;
}
.preview-head .meta { color: var(--ink-30); font-size: 12px; font-family: var(--font-mono); }
.open-link {
  margin-left: auto; color: var(--blue-500); font-size: 12.5px; font-weight: 600;
  border: 1px solid rgba(66, 103, 255, .3); border-radius: var(--r-full);
  padding: 6px 14px; transition: all .18s ease;
}
.open-link:hover { background: var(--blue-50); border-color: var(--blue-500); }
.preview-frame { flex: 1; width: 100%; border: none; background: #fff; }

/* 历史卡片 */
.proj-card { padding: 15px 18px; border-bottom: 1px solid var(--line-soft); cursor: pointer; transition: background .15s ease; }
.proj-card:hover { background: var(--beige-100); }
.proj-card.active { background: var(--blue-50); }
.proj-name { font-weight: 700; font-size: 14.5px; margin-bottom: 4px; color: var(--ink-100); }
.proj-brief {
  color: var(--ink-55); font-size: 12.5px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-bottom: 9px;
}
.proj-meta { display: flex; gap: 10px; align-items: center; font-size: 11.5px; color: var(--ink-30); font-family: var(--font-mono); }
.tag {
  background: var(--blue-50); color: var(--blue-600);
  padding: 2px 10px; border-radius: var(--r-full); font-size: 11px; font-weight: 600;
  font-family: var(--font-sans);
}
</style>
