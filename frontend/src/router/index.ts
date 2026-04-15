import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import HomeView from '@/views/HomeView.vue'
import LoginView from '@/views/LoginView.vue'
import SetupView from '@/views/SetupView.vue'
import AdminView from '@/views/AdminView.vue'
import SettingsView from '@/views/SettingsView.vue'
import IdeView from '@/views/IdeView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'home',
      component: HomeView,
      meta: { requiresAuth: true },
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsView,
      meta: { requiresAuth: true },
    },
    {
      path: '/login',
      name: 'login',
      component: LoginView,
      meta: { guest: true },
    },
    {
      path: '/setup',
      name: 'setup',
      component: SetupView,
      meta: { guest: true },
    },
    {
      path: '/admin',
      name: 'admin',
      component: AdminView,
      meta: { requiresAuth: true, requiresAdmin: true },
    },
    {
      path: '/workspace/:id',
      name: 'ide',
      component: IdeView,
      meta: { requiresAuth: true, hideChrome: true },
    },
  ],
})

let initialized = false

router.beforeEach(async (to) => {
  const auth = useAuthStore()

  if (!initialized) {
    await auth.checkStatus()
    initialized = true
  }

  // Redirect to setup if no users exist
  if (auth.needsSetup && to.name !== 'setup') {
    return { name: 'setup' }
  }

  // Redirect away from setup if already configured
  if (!auth.needsSetup && to.name === 'setup') {
    return { name: 'login' }
  }

  // Redirect to login if auth required and not logged in
  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return { name: 'login' }
  }

  // Redirect non-admins away from admin pages
  if (to.meta.requiresAdmin && !auth.isAdmin) {
    return { name: 'home' }
  }

  // Redirect to home if logged in and visiting guest page
  if (to.meta.guest && auth.isAuthenticated) {
    return { name: 'home' }
  }
})
