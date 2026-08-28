import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  { path: '/', redirect: '/workspace' },
  { path: '/login', component: () => import('../views/LoginView.vue') },
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
  if (to.path === '/login' && token) return '/workspace'
  return true
})

export default router
