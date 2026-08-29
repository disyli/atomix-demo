<script setup>
import { ref, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
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
    serverMode.value = health.mode === 'live' ? 'DeepSeek 已接入' : '演示模式'
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
      previewUrl.value = api.previewUrl(data.project.id, readAppData(data.project.id))
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

/* ---------- 迭代修改（ReAct 循环） ---------- */
const refineText = ref('')
const refining = ref(false)

async function refineProject() {
  const text = refineText.value.trim()
  if (!text || refining.value || !activeProject.value) return
  refining.value = true
  errorMsg.value = ''
  const pid = activeProject.value.id
  activeEvents.value = []
  try {
    const resp = await fetch('/api/projects/' + pid + '/refine', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + (localStorage.getItem('atomix_token') || '') },
      body: JSON.stringify({ instruction: text })
    })
    if (!resp.ok || !resp.body) throw new Error('修改请求失败 (' + resp.status + ')')
    const es = resp.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    let updatedProject = null
    while (true) {
      const { done, value } = await es.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      const chunks = buf.split('\n\n')
      buf = chunks.pop()
      for (const chunk of chunks) {
        const evLine = chunk.split('\n').find(l => l.startsWith('event:'))
        const dataLine = chunk.split('\n').find(l => l.startsWith('data:'))
        if (!evLine || !dataLine) continue
        const event = evLine.slice(6).trim()
        const data = dataLine.slice(5).trim()
        if (event === 'stage') {
          const [stage, message] = data.split('\x1f')
          timeline.value.push({ stage, message, level: 'stage', ts: Date.now() })
        } else if (event === 'detail') {
          const [stage, message, level] = data.split('\x1f')
          timeline.value.push({ stage, message, level, ts: Date.now() })
        } else if (event === 'done') {
          try { updatedProject = JSON.parse(data).project } catch {}
        } else if (event === 'error') {
          errorMsg.value = data
        }
      }
      nextTick(scrollTimeline)
    }
    if (updatedProject) {
      activeProject.value = updatedProject
      previewUrl.value = api.previewUrl(pid, readAppData(pid))
      // 刷新 iframe 使新产物生效
      previewUrl.value = previewUrl.value
      try { activeEvents.value = await api.getEvents(pid) } catch {}
      loadProjects()
    }
    refineText.value = ''
  } catch (e) {
    errorMsg.value = e.message || '修改失败'
  } finally {
    refining.value = false
    nextTick(scrollTimeline)
  }
}

/* ---------- 历史项目 ---------- */
const APP_DATA_PREFIX = 'atomix_app_'
function readAppData(projectId) {
  try {
    return JSON.parse(localStorage.getItem(APP_DATA_PREFIX + projectId) || '{}')
  } catch { return {} }
}

/* 沙箱存储垫片消息：产物在 iframe 内的写入回传父页面持久化 */
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

async function openProject(p) {
  activeProject.value = p
  previewUrl.value = api.previewUrl(p.id, readAppData(p.id))
  rightTab.value = 'preview'
  try { activeEvents.value = await api.getEvents(p.id) } catch { activeEvents.value = [] }
}

onMounted(() => window.addEventListener('message', onShimMessage))
onBeforeUnmount(() => window.removeEventListener('message', onShimMessage))
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
      <!-- 左栏：对话 + 时间线 -->
      <section class="left">
        <div class="panel input-panel">
          <div class="panel-title">
            <span class="pt-zh">描述你的想法</span>
            <span class="pt-mono">Tell Atomix your idea</span>
          </div>
          <textarea
            v-model="brief"
            rows="3"
            placeholder="几分钟即可上线，不用等几周。例如：做一个番茄钟计时器…"
            :disabled="running"
            @keydown.ctrl.enter="generate"
            @keydown.meta.enter="generate"
          ></textarea>
          <div class="row">
            <div class="examples">
              <button v-for="x in examples" :key="x" class="chip" :disabled="running" @click="brief = x">{{ x }}</button>
            </div>
          </div>
          <div class="row submit-row">
            <span class="kbd-hint">⌘ + Enter 发送</span>
            <button class="go" :disabled="running || !brief.trim()" @click="generate">
              {{ running ? '构建中…' : '开始' }}
              <svg v-if="!running" viewBox="0 0 16 16" width="14" height="14" fill="none"><path d="M2 8h10M8.5 3.5 13 8l-4.5 4.5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>
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
              <span class="icon">{{ stageMeta[s].icon }}</span>
              <span>{{ stageMeta[s].label }}</span>
            </div>
          </div>
          <div class="progress"><div :style="{ width: stageProgress + '%' }"></div></div>
        </div>

        <!-- 时间线 -->
        <div class="panel timeline-panel">
          <div class="panel-title">
            <span class="pt-zh">Agent 执行</span>
            <span class="pt-mono">Agent execution</span>
          </div>
          <div ref="timelineBox" class="timeline">
            <div v-if="!timeline.length && !running" class="empty-tip">
              <div class="empty-mono">agent · team · build</div>
              输入需求后，AI 团队的每一步执行都会实时展示在这里。
            </div>
            <div
              v-for="(t, i) in timeline"
              :key="i"
              class="tl-item"
              :class="t.level"
            >
              <span class="tl-stage">
                <template v-if="t.stage === 'think'">🧠 思考</template>
                <template v-else-if="t.stage === 'act'">⚡ 行动</template>
                <template v-else-if="t.stage === 'observe'">👁 观察</template>
                <template v-else>{{ stageMeta[t.stage] ? stageMeta[t.stage].label : t.stage }}</template>
              </span>
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
            <div class="big-mono">// ready to build</div>
            还没有生成的应用
            <span>告诉 Atomix 你的想法，让 AI 团队为你构建第一个应用</span>
          </div>
          <template v-else>
            <div class="preview-head">
              <b>{{ activeProject.name }}</b>
              <span class="meta">{{ activeProject.template }} · {{ activeProject.status }}</span>
              <a v-if="previewUrl" :href="previewUrl" target="_blank" class="open-link">新窗口打开 ↗</a>
            </div>
            <div class="refine-bar">
              <input
                v-model="refineText"
                :disabled="refining"
                placeholder="继续修改：例如「加上深色模式」「把主色改成绿色」…"
                @keydown.enter="refineProject"
              />
              <button class="refine-go" :disabled="refining || !refineText.trim()" @click="refineProject">
                {{ refining ? '修改中…' : '发送' }}
              </button>
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
  </div>
</template>

<style scoped>
/* ============ 布局骨架 ============ */
.ws { height: 100%; display: flex; flex-direction: column; background: var(--beige-100); }
.main { flex: 1; display: flex; min-height: 0; }

/* ============ 顶栏：浅色半透明 + 细线 ============ */
.topbar {
  height: 58px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 22px;
  background: rgba(246, 246, 246, .78);
  backdrop-filter: saturate(180%) blur(10px);
  border-bottom: 1px solid var(--line-soft);
  flex-shrink: 0;
  z-index: 5;
}
.brand { display: flex; align-items: center; gap: 10px; }
.mark {
  width: 27px; height: 27px; border-radius: 9px;
  background: var(--blue-500);
  color: #fff; font-weight: 800; font-size: 15px;
  display: flex; align-items: center; justify-content: center;
}
.name { font-weight: 700; font-size: 16.5px; letter-spacing: -.01em; }
.badge {
  font-size: 11px;
  font-weight: 600;
  font-family: var(--font-mono);
  padding: 3px 10px;
  border-radius: var(--r-full);
}
.badge.demo { color: var(--ink-55); background: var(--beige-150); border: 1px solid var(--line-soft); }
.badge.live { color: var(--green); background: rgba(45, 187, 92, .1); border: 1px solid rgba(45, 187, 92, .25); }

.user { display: flex; align-items: center; gap: 10px; font-size: 13px; color: var(--ink-55); }
.avatar {
  width: 29px; height: 29px; border-radius: 50%;
  background: var(--blue-500);
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-weight: 700; font-size: 13px;
}
.ghost {
  color: var(--ink-55);
  border: 1px solid var(--line);
  border-radius: var(--r-full);
  padding: 6px 14px; font-size: 12.5px; font-weight: 500;
  transition: all .18s ease;
}
.ghost:hover { color: var(--ink-100); border-color: var(--ink-30); background: var(--beige-50); }

/* ============ 左栏 ============ */
.left {
  width: 44%;
  min-width: 400px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border-right: 1px solid var(--line-soft);
  min-height: 0;
}
.panel {
  background: var(--beige-50);
  border: 1px solid var(--line-soft);
  border-radius: var(--r-l);
  padding: 16px;
}
.panel-title {
  display: flex; align-items: baseline; gap: 10px;
  font-size: 14px; font-weight: 700; color: var(--ink-100);
  margin-bottom: 12px; letter-spacing: -.01em;
}
.pt-mono { font-family: var(--font-mono); font-size: 11px; font-weight: 400; color: var(--ink-30); }

/* 输入面板 */
.input-panel { flex-shrink: 0; }
textarea {
  width: 100%;
  background: var(--beige-100);
  border: 1px solid var(--line-soft);
  border-radius: var(--r-m);
  color: var(--ink-100);
  padding: 13px 14px;
  font-size: 14.5px;
  resize: vertical;
  outline: none;
  transition: border-color .18s ease, background .18s ease;
}
textarea::placeholder { color: var(--ink-30); }
textarea:focus { background: var(--beige-50); border-color: var(--blue-500); }
.row { display: flex; align-items: center; gap: 10px; margin-top: 10px; }
.examples { display: flex; flex-wrap: wrap; gap: 6px; flex: 1; }
.chip {
  background: var(--beige-50);
  border: 1px solid var(--line-soft);
  color: var(--ink-55);
  border-radius: var(--r-full);
  padding: 6px 13px;
  font-size: 12px;
  transition: all .18s ease;
  text-align: left;
}
.chip:hover:not(:disabled) { color: var(--ink-100); border-color: var(--ink-30); }
.chip:disabled { opacity: .5; cursor: default; }
.submit-row { justify-content: space-between; }
.kbd-hint { font-size: 11.5px; color: var(--ink-30); font-family: var(--font-mono); }
.go {
  display: inline-flex; align-items: center; gap: 7px;
  background: var(--blue-500);
  color: #fff;
  border-radius: var(--r-full);
  padding: 11px 20px;
  font-weight: 600;
  font-size: 14.5px;
  white-space: nowrap;
  transition: background .18s ease, transform .12s ease;
}
.go:hover:not(:disabled) { background: var(--blue-600); }
.go:active:not(:disabled) { transform: scale(.97); }
.go:disabled { background: var(--beige-300); color: var(--beige-50); cursor: default; }

/* 阶段进度 */
.stages-panel { flex-shrink: 0; padding: 12px 16px; }
.stages { display: flex; align-items: center; gap: 4px; }
.stage {
  flex: 1;
  display: flex; align-items: center; justify-content: center; gap: 6px;
  font-size: 12.5px; color: var(--ink-30);
  padding: 8px 0; border-radius: var(--r-full);
  transition: all .25s ease;
  font-weight: 500;
}
.stage.active { background: var(--blue-50); color: var(--blue-600); font-weight: 700; }
.stage.done { color: var(--green); }
.progress {
  margin-top: 10px; height: 4px;
  background: var(--beige-150); border-radius: 999px; overflow: hidden;
}
.progress > div { height: 100%; background: var(--blue-500); border-radius: 999px; transition: width .4s ease; }

/* 时间线 */
.timeline-panel { flex: 1; display: flex; flex-direction: column; min-height: 0; }
.timeline { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 5px; padding-right: 4px; }
.tl-item {
  display: flex; align-items: baseline; gap: 9px;
  font-size: 12.5px;
  padding: 8px 12px;
  border-radius: var(--r-s);
  background: var(--beige-100);
}
.tl-item.stage { background: var(--blue-50); font-weight: 600; color: var(--blue-600); }
.tl-item.warn { background: rgba(239, 174, 34, .1); color: #a87707; }
.tl-item.err { background: rgba(201, 68, 74, .08); color: var(--red); }
/* ReAct 三类事件 */
.tl-item.think { background: var(--beige-50); border: 1px dashed var(--line); }
.tl-item.think .tl-msg { color: var(--ink-55); }
.tl-item.act { background: rgba(66, 103, 255, .06); }
.tl-item.act .tl-msg { color: var(--blue-600); font-family: var(--font-mono); font-size: 12px; }
.tl-item.observe { background: rgba(15, 139, 141, .06); }
.tl-item.observe .tl-msg { color: #0b5f60; }

/* 继续修改输入条 */
.refine-bar {
  display: flex; gap: 8px; padding: 10px 18px;
  border-bottom: 1px solid var(--line-soft);
  background: var(--beige-50);
}
.refine-bar input {
  flex: 1;
  border: 1px solid var(--line-soft);
  border-radius: var(--r-full);
  background: var(--beige-100);
  color: var(--ink-100);
  padding: 9px 15px;
  font-size: 13px;
  outline: none;
  transition: border-color .18s ease;
}
.refine-bar input:focus { border-color: var(--blue-500); background: #fff; }
.refine-go {
  background: var(--blue-500); color: #fff;
  border-radius: var(--r-full);
  padding: 0 18px; font-size: 13px; font-weight: 600;
  transition: background .18s ease;
}
.refine-go:hover:not(:disabled) { background: var(--blue-600); }
.refine-go:disabled { background: var(--beige-300); color: var(--beige-50); cursor: default; }
.tl-stage { color: var(--ink-30); font-size: 11px; flex-shrink: 0; font-family: var(--font-mono); }
.tl-item.stage .tl-stage { color: var(--blue-500); }
.tl-msg { flex: 1; color: var(--ink-80); }
.tl-item.stage .tl-msg { color: var(--blue-600); }
.tl-time { color: var(--ink-30); font-size: 11px; flex-shrink: 0; font-family: var(--font-mono); }
.empty-tip { color: var(--ink-55); font-size: 13px; padding: 30px 0; text-align: center; }
.empty-mono { font-family: var(--font-mono); font-size: 12px; color: var(--ink-30); margin-bottom: 8px; }

/* ============ 右栏 ============ */
.right { flex: 1; display: flex; flex-direction: column; min-width: 0; padding: 16px 16px 16px 0; }
.tabs {
  display: flex; gap: 6px;
  background: var(--beige-150);
  border-radius: var(--r-full);
  padding: 4px;
  align-self: flex-start;
  margin-bottom: 12px;
}
.tabs button {
  color: var(--ink-55);
  padding: 8px 18px;
  border-radius: var(--r-full);
  font-size: 13px;
  font-weight: 600;
  transition: all .18s ease;
}
.tabs button.active { background: var(--beige-50); color: var(--ink-100); box-shadow: 0 1px 3px rgba(12,12,12,.08); }
.tabs .count {
  background: var(--beige-200); color: var(--ink-80); font-size: 11px; font-weight: 700;
  border-radius: var(--r-full); padding: 1px 8px; margin-left: 5px;
}
.tabs button.active .count { background: var(--blue-500); color: #fff; }

.preview-wrap, .history-wrap {
  flex: 1;
  background: var(--beige-50);
  border: 1px solid var(--line-soft);
  border-radius: var(--r-l);
  min-height: 0;
  display: flex; flex-direction: column;
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(12,12,12,.03);
}
.history-wrap { overflow-y: auto; }
.empty-tip.big {
  padding: 80px 20px; line-height: 2; font-size: 15px; font-weight: 600; color: var(--ink-80);
}
.empty-tip.big span { display: block; font-size: 13px; font-weight: 400; color: var(--ink-55); }
.big-mono { font-family: var(--font-mono); font-size: 13px; color: var(--ink-30); margin-bottom: 10px; }
.preview-head {
  display: flex; align-items: center; gap: 10px;
  padding: 13px 18px;
  border-bottom: 1px solid var(--line-soft);
  font-size: 14.5px;
}
.preview-head .meta { color: var(--ink-30); font-size: 12px; font-family: var(--font-mono); }
.open-link {
  margin-left: auto;
  color: var(--blue-500); font-size: 12.5px; font-weight: 600;
  border: 1px solid rgba(66, 103, 255, .3);
  border-radius: var(--r-full);
  padding: 6px 14px;
  transition: all .18s ease;
}
.open-link:hover { background: var(--blue-50); border-color: var(--blue-500); }
.preview-frame { flex: 1; width: 100%; border: none; background: #fff; }

/* 历史卡片 */
.proj-card {
  padding: 15px 18px;
  border-bottom: 1px solid var(--line-soft);
  cursor: pointer;
  transition: background .15s ease;
}
.proj-card:hover { background: var(--beige-100); }
.proj-card.active { background: var(--blue-50); }
.proj-name { font-weight: 700; font-size: 14.5px; margin-bottom: 4px; color: var(--ink-100); }
.proj-brief {
  color: var(--ink-55); font-size: 12.5px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  margin-bottom: 9px;
}
.proj-meta { display: flex; gap: 10px; align-items: center; font-size: 11.5px; color: var(--ink-30); font-family: var(--font-mono); }
.tag {
  background: var(--blue-50); color: var(--blue-600);
  padding: 2px 10px; border-radius: var(--r-full); font-size: 11px; font-weight: 600;
  font-family: var(--font-sans);
}
</style>
