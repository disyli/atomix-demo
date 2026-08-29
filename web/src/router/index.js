import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  { path: '/', redirect: '/dashboard' },
  { path: '/login', component: () => import('../views/LoginView.vue') },
  {
    path: '/dashboard',
    component: () => import('../views/DashboardView.vue'),
    meta: { auth: true }
  },
  {
    path: '/workspace',
    component: () => import('../views/WorkspaceView.vue'),
    meta: { auth: true }
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

router.beforeEach((to) => {
  const token = localStorage.getItem('atomix_token')
  if (to.meta.auth && !token) return '/login'
    if (to.path === '/login' && token) return '/dashboard'
  return true
})

export default router
