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
  me: () => request('/api/me'),
  listProjects: () => request('/api/projects'),
  getProject: (id) => request('/api/projects/' + id),
  getEvents: (id) => request('/api/projects/' + id + '/events'),
  // 预览接口由 iframe 直接加载，无法携带 Authorization 头，
  // 与 SSE 一样通过 t 查询参数传递 token（后端中间件支持）。
  // payload 为应用历史数据（键值对），经 location.hash 传给沙箱内存储垫片。
  previewUrl: (id, payload) => {
    let url = BASE + '/api/projects/' + id + '/preview?t=' +
      encodeURIComponent(localStorage.getItem('atomix_token') || '')
    if (payload && Object.keys(payload).length) {
      url += '#atomix-data=' + encodeURIComponent(JSON.stringify(payload))
    }
    return url
  }
}
