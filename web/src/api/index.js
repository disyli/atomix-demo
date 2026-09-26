const BASE = ''

async function request(path, options = {}) {
  const headers = { 'Content-Type': 'application/json', ...(options.headers || {}) }
  const token = localStorage.getItem('atomix_token')
  if (token) headers.Authorization = 'Bearer ' + token
  const resp = await fetch(BASE + path, { ...options, headers })
  const data = await resp.json().catch(() => ({}))
  if (!resp.ok) {
    if (resp.status === 401) {
      localStorage.removeItem('atomix_token')
      localStorage.removeItem('atomix_user')
      location.href = '/login'
    }
    throw new Error(data.error || '请求失败 (' + resp.status + ')')
  }
  return data
}

export const api = {
  register: (email, password) => request('/api/auth/register', { method: 'POST', body: JSON.stringify({ email, password }) }),
  login: (email, password) => request('/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
  // 游客一键登录：服务端即时创建一次性游客账号，评审无需注册与 API Key
  guestLogin: () => request('/api/auth/guest', { method: 'POST', body: '{}' }),
  me: () => request('/api/me'),
  // 健康检查：返回部署 SHA / 模式 / 游客开关（无需登录）
  health: () => fetch('/api/health').then(r => r.json()),
  listProjects: () => request('/api/projects'),
  getProject: (id) => request('/api/projects/' + id),
  getEvents: (id) => request('/api/projects/' + id + '/events'),
  // 同一 project 的完整对话历史（每轮 user/assistant 消息），刷新/回看时还原对话
  getMessages: (id) => request('/api/projects/' + id + '/messages'),
  // 当前项目生成应用的源码（专门代码展示区数据来源；download=1 走下载链接）
  getSource: (id) => request('/api/projects/' + id + '/source'),
  // 版本管理：成功版本快照列表（version DESC，不含源码正文）
  getSnapshots: (id) => request('/api/projects/' + id + '/snapshots'),
  // 回滚到指定成功版本（服务端事务保证源码/预览/版本号原子一致）
  rollback: (id, version) => request('/api/projects/' + id + '/rollback', { method: 'POST', body: JSON.stringify({ version }) }),
  // 迭代修改：在已有项目上追加自然语言修改指令（后端走 ReAct 循环）
  refine: (id, instruction) => request('/api/projects/' + id + '/refine', { method: 'POST', body: JSON.stringify({ instruction }) }),
  // 签发预览凭据：服务端设置 HttpOnly Cookie（1 小时），iframe/新窗口同源自动携带；
  // 返回的 ticket 仅用于 SSE（EventSource 走 query 参数）
  issueTicket: () => request('/api/ticket', { method: 'POST', body: '{}' }),
  // 预览 URL：无需任何凭据参数（HttpOnly Cookie 自动附带），URL 干净、不过期、不进访问日志
  previewUrl: async (id, payload) => {
    try { await request('/api/ticket', { method: 'POST', body: '{}' }) } catch {}
    let url = BASE + '/api/projects/' + id + '/preview'
    if (payload && Object.keys(payload).length) {
      url += '#atomix-data=' + encodeURIComponent(JSON.stringify(payload))
    }
    return url
  },
  // 源码下载 URL：同样凭 Cookie 鉴权，URL 不含任何凭据
  sourceDownloadUrl: async (id) => {
    try { await request('/api/ticket', { method: 'POST', body: '{}' }) } catch {}
    return BASE + '/api/projects/' + id + '/source?download=1'
  }
}
