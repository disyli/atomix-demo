<script setup>
import { ref, computed, nextTick } from 'vue'
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

/* ---------- 状态 ---------- */
const brief = ref('')
const running = ref(false)
const stages = ['plan', 'build', 'run', 'verify', 'done']
const stageMeta = {
  plan: { label: '规划', icon: '🧠' },
  build: { label: '构建', icon: '⚙️' },
  run: { label: '运行', icon: '🚀' },
  verify: { label: '校验', icon: '🔍' },
  done: { label: '完成', icon: '🎉' }
}
const currentStage = ref('')
const stageState = ref({})   // stage -> pending | active | done
const timeline = ref([])     // {stage, message, level, ts}
const projects = ref([])
const activeProject = ref(null)
const activeEvents = ref([])
const previewUrl = ref('')
const rightTab = ref('preview')
const errorMsg = ref('')
const serverMode = ref('')
const timelineBox = ref(null)

const examples = [
  '做一个极简待办清单，支持勾选完成和进度统计',
  '做一个彩色便签墙，支持置顶和搜索',
  '做一个项目看板，卡片可以在待办/进行中/已完成之间移动'
]

const stageProgress = computed(() => {
  const done = stages.filter(s => stageState.value[s] === 'done').length
  return Math.round((done / stages.length) * 100)
})

/* ---------- 初始化 ---------- */
init()
async function init() {
  try {
    const health = await fetch('/api/health').then(r => r.json())
    serverMode.value = health.mode === 'live' ? 'DeepSeek 已接入' : '演示模式（未配置 Key）'
  } catch { serverMode.value = '' }
  await loadProjects()
  await nextTick(); scrollTimeline()
}

async function loadProjects() {
  try { projects.value = await api.listProjects() } catch {}
}

function scrollTimeline() {
  if (timelineBox.value) timelineBox.value.scrollTop = timelineBox.value.scrollHeight
}

/* ---------- 生成流水线（SSE） ---------- */
function generate() {
  const text = brief.value.trim()
  if (!text || running.value) return
  running.value = true
  errorMsg.value = ''
  timeline.value = []
  stageState.value = {}
  currentStage.value = ''
  stages.forEach(s => stageState.value[s] = 'pending')

  const es = new EventSource('/api/generate?brief=' + encodeURIComponent(text) +
    '&t=' + encodeURIComponent(localStorage.getItem('atomix_token') || ''))

  es.addEventListener('stage', e => {
    const [stage, message] = e.data.split('\x1f')
    if (currentStage.value) stageState.value[currentStage.value] = 'done'
    currentStage.value = stage
    stageState.value[stage] = 'active'
    timeline.value.push({ stage, message, level: 'stage', ts: Date.now() })
    nextTick(scrollTimeline)
  })

  es.addEventListener('detail', e => {
    const [stage, message, level] = e.data.split('\x1f')
    timeline.value.push({ stage, message, level, ts: Date.now() })
    nextTick(scrollTimeline)
  })

  es.addEventListener('done', e => {
    try {
      const data = JSON.parse(e.data)
      activeProject.value = data.project
      previewUrl.value = api.previewUrl(data.project.id)
      rightTab.value = 'preview'
    } catch {}
    stageState.value['done'] = 'done'
    running.value = false
    brief.value = ''
    loadProjects()
    es.close()
  })

  es.addEventListener('error', e => {
    if (e.data) errorMsg.value = e.data
    running.value = false
    es.close()
  })
}

/* ---------- 历史项目 ---------- */
async function openProject(p) {
  activeProject.value = p
  previewUrl.value = api.previewUrl(p.id)
  rightTab.value = 'preview'
  try { activeEvents.value = await api.getEvents(p.id) } catch { activeEvents.value = [] }
}
</script>

<template>
  <div class="ws">
    <!-- 顶栏 -->
    <header class="topbar">
      <div class="brand">⚛️ <b>Atomix</b> <span class="badge-demo">{{ serverMode }}</span></div>
      <div class="user">
        <span class="avatar">{{ (user.email || '?')[0].toUpperCase() }}</span>
        <span class="email">{{ user.email }}</span>
        <button class="ghost" @click="logout">退出</button>
      </div>
    </header>

    <div class="main">
      <!-- 左栏：对话 + 时间线 -->
      <section class="left">
        <div class="panel input-panel">
          <div class="panel-title">描述你想构建的应用</div>
          <textarea
            v-model="brief"
            rows="3"
            placeholder="例：做一个番茄钟计时器…（内置模板覆盖：待办清单 / 便签墙 / 项目看板）"
            :disabled="running"
            @keydown.ctrl.enter="generate"
            @keydown.meta.enter="generate"
          ></textarea>
          <div class="row">
            <div class="examples">
              <button v-for="x in examples" :key="x" class="chip" :disabled="running" @click="brief = x">{{ x }}</button>
            </div>
            <button class="go" :disabled="running || !brief.trim()" @click="generate">
              {{ running ? '构建中…' : '⚡ 开始生成' }}
            </button>
          </div>
        </div>

        <!-- 阶段进度 -->
        <div class="panel stages-panel">
          <div class="stages">
            <div
              v-for="s in stages"
              :key="s"
              class="stage"
              :class="stageState[s]"
            >
              <span class="dot"></span>
              <span class="icon">{{ stageMeta[s].icon }}</span>
              <span>{{ stageMeta[s].label }}</span>
            </div>
            <div class="progress"><div :style="{ width: stageProgress + '%' }"></div></div>
          </div>
        </div>

        <!-- 时间线 -->
        <div class="panel timeline-panel">
          <div class="panel-title">Agent 执行时间线</div>
          <div ref="timelineBox" class="timeline">
            <div v-if="!timeline.length && !running" class="empty-tip">
              左侧输入需求后，Agent 的每一步执行都会实时展示在这里。
            </div>
            <div
              v-for="(t, i) in timeline"
              :key="i"
              class="tl-item"
              :class="t.level"
            >
              <span class="tl-stage">{{ stageMeta[t.stage] ? stageMeta[t.stage].label : t.stage }}</span>
              <span class="tl-msg">{{ t.message }}</span>
              <span class="tl-time">{{ new Date(t.ts).toLocaleTimeString() }}</span>
            </div>
            <div v-if="errorMsg" class="tl-item err"><span class="tl-msg">{{ errorMsg }}</span></div>
          </div>
        </div>
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
            <div class="big-icon">🛠️</div>
            还没有生成的应用<br /><span>输入需求，让 Agent 为你构建第一个应用</span>
          </div>
          <template v-else>
            <div class="preview-head">
              <b>{{ activeProject.name }}</b>
              <span class="meta">{{ activeProject.template }} · {{ activeProject.status }}</span>
              <a :href="previewUrl" target="_blank" class="ghost">新窗口打开 ↗</a>
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
            <div class="big-icon">📦</div>
            暂无历史项目<br /><span>生成的应用会持久化保存，可随时回看</span>
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
  </div>
</template>

<style scoped>
.ws { height: 100%; display: flex; flex-direction: column; }
.topbar {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  border-bottom: 1px solid var(--border);
  background: var(--panel);
  flex-shrink: 0;
}
.brand { font-size: 17px; display: flex; align-items: center; gap: 8px; }
.badge-demo {
  font-size: 11px;
  color: var(--accent-2);
  background: rgba(56,189,248,.1);
  border: 1px solid rgba(56,189,248,.3);
  padding: 2px 8px;
  border-radius: 999px;
}
.user { display: flex; align-items: center; gap: 10px; font-size: 13px; color: var(--muted); }
.avatar {
  width: 28px; height: 28px; border-radius: 50%;
  background: linear-gradient(135deg, var(--accent), #8b5cf6);
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-weight: 700; font-size: 13px;
}
.ghost {
  background: transparent; color: var(--muted);
  border: 1px solid var(--border); border-radius: 8px;
  padding: 5px 12px; font-size: 12px;
}
.ghost:hover { color: var(--text); border-color: var(--muted); }
.main { flex: 1; display: flex; min-height: 0; }

.left {
  width: 46%;
  min-width: 420px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border-right: 1px solid var(--border);
  min-height: 0;
}
.panel {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 14px;
}
.panel-title { font-size: 13px; color: var(--muted); margin-bottom: 10px; font-weight: 600; }
.input-panel { flex-shrink: 0; }
textarea {
  width: 100%;
  background: var(--panel-2);
  border: 1.5px solid var(--border);
  border-radius: 10px;
  color: var(--text);
  padding: 12px;
  font-size: 14px;
  resize: vertical;
  outline: none;
  transition: border-color .2s;
}
textarea:focus { border-color: var(--accent); }
.row { display: flex; align-items: flex-end; justify-content: space-between; gap: 10px; margin-top: 10px; }
.examples { display: flex; flex-wrap: wrap; gap: 6px; flex: 1; }
.chip {
  background: var(--panel-2);
  border: 1px solid var(--border);
  color: var(--muted);
  border-radius: 999px;
  padding: 5px 11px;
  font-size: 12px;
  transition: all .2s;
  text-align: left;
}
.chip:hover:not(:disabled) { color: var(--text); border-color: var(--accent); }
.go {
  background: linear-gradient(135deg, var(--accent), #8b5cf6);
  color: #fff;
  border-radius: 10px;
  padding: 10px 18px;
  font-weight: 700;
  font-size: 14px;
  white-space: nowrap;
  transition: opacity .2s;
}
.go:disabled { opacity: .5; cursor: default; }

.stages-panel { flex-shrink: 0; }
.stages { display: flex; align-items: center; gap: 4px; position: relative; }
.stage {
  flex: 1;
  display: flex; align-items: center; justify-content: center; gap: 6px;
  font-size: 12.5px; color: var(--muted);
  padding: 8px 0; border-radius: 9px;
  transition: all .25s;
}
.stage .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--border); transition: all .25s; }
.stage.active { background: rgba(99,102,241,.14); color: var(--text); }
.stage.active .dot { background: var(--accent); box-shadow: 0 0 8px var(--accent); animation: pulse 1.2s infinite; }
.stage.done { color: var(--ok); }
.stage.done .dot { background: var(--ok); }
@keyframes pulse { 50% { opacity: .4; } }
.progress {
  position: absolute; bottom: 0; left: 8px; right: 8px; height: 2px;
  background: var(--border); border-radius: 1px; overflow: hidden;
}
.progress > div { height: 100%; background: linear-gradient(90deg, var(--accent), var(--accent-2)); transition: width .4s; }

.timeline-panel { flex: 1; display: flex; flex-direction: column; min-height: 0; }
.timeline { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 4px; padding-right: 4px; }
.tl-item {
  display: flex; align-items: baseline; gap: 8px;
  font-size: 12.5px;
  padding: 6px 9px;
  border-radius: 8px;
  background: var(--panel-2);
}
.tl-item.stage { background: rgba(99,102,241,.14); font-weight: 600; }
.tl-item.warn { background: rgba(251,191,36,.1); }
.tl-item.err { background: rgba(248,113,113,.12); color: var(--err); }
.tl-stage { color: var(--accent-2); font-size: 11px; flex-shrink: 0; }
.tl-msg { flex: 1; color: var(--text); }
.tl-item.stage .tl-msg { color: #fff; }
.tl-time { color: var(--muted); font-size: 11px; flex-shrink: 0; }
.empty-tip { color: var(--muted); font-size: 13px; padding: 24px 0; text-align: center; }
.empty-tip.big { padding: 70px 0; line-height: 2; font-size: 14px; }
.big-icon { font-size: 42px; margin-bottom: 6px; }
.empty-tip span { font-size: 12px; opacity: .7; }

.right { flex: 1; display: flex; flex-direction: column; min-width: 0; }
.tabs {
  display: flex; gap: 4px; padding: 12px 16px 0;
}
.tabs button {
  background: var(--panel);
  border: 1px solid var(--border);
  border-bottom: none;
  color: var(--muted);
  padding: 9px 18px;
  border-radius: 10px 10px 0 0;
  font-size: 13px;
}
.tabs button.active { color: var(--text); background: var(--panel-2); }
.tabs .count {
  background: var(--accent); color: #fff; font-size: 11px;
  border-radius: 999px; padding: 1px 7px; margin-left: 4px;
}
.preview-wrap, .history-wrap {
  flex: 1; margin: 0 16px 16px;
  background: var(--panel-2);
  border: 1px solid var(--border);
  border-radius: 0 14px 14px 14px;
  min-height: 0;
  display: flex; flex-direction: column;
  overflow-y: auto;
}
.preview-head {
  display: flex; align-items: center; gap: 10px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  font-size: 14px;
}
.preview-head .meta { color: var(--muted); font-size: 12px; }
.preview-head .ghost { margin-left: auto; }
.preview-frame { flex: 1; width: 100%; border: none; background: #fff; border-radius: 0 0 14px 14px; }
.proj-card {
  padding: 14px 16px;
  border-bottom: 1px solid var(--border);
  cursor: pointer;
  transition: background .15s;
}
.proj-card:hover { background: rgba(99,102,241,.06); }
.proj-card.active { background: rgba(99,102,241,.12); }
.proj-name { font-weight: 600; font-size: 14px; margin-bottom: 4px; }
.proj-brief {
  color: var(--muted); font-size: 12.5px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  margin-bottom: 8px;
}
.proj-meta { display: flex; gap: 10px; align-items: center; font-size: 11.5px; color: var(--muted); }
.tag {
  background: rgba(99,102,241,.15); color: var(--accent-2);
  padding: 1px 9px; border-radius: 999px; font-size: 11px;
}
</style>
