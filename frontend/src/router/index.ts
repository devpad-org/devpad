import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import HomeView from '@/views/HomeView.vue'
import LoginView from '@/views/LoginView.vue'
import SetupView from '@/views/SetupView.vue'
import AdminView from '@/views/AdminView.vue'
import SettingsView from '@/views/SettingsView.vue'
import SecuritySettings from '@/components/settings/SecuritySettings.vue'
import SSHKeySettings from '@/components/settings/SSHKeySettings.vue'
import AIProviderKeys from '@/components/settings/AIProviderKeys.vue'
import AIDefaultModels from '@/components/settings/AIDefaultModels.vue'
import AIBudgets from '@/components/settings/AIBudgets.vue'
import AIModelPricing from '@/components/settings/AIModelPricing.vue'
import InboxView from '@/views/InboxView.vue'
import UsageView from '@/views/UsageView.vue'
const IdeView = () => import('@/views/IdeView.vue')

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'home',
      component: HomeView,
      meta: { requiresAuth: true },
    },
    // Settings is a sidebar of pages, each with its own URL.
    {
      path: '/settings',
      component: SettingsView,
      meta: { requiresAuth: true },
      children: [
        { path: '', name: 'settings', redirect: { name: 'settings-security' } },
        { path: 'security', name: 'settings-security', component: SecuritySettings },
        { path: 'ssh-key', name: 'settings-ssh-key', component: SSHKeySettings },
        { path: 'ai/keys', name: 'settings-ai-keys', component: AIProviderKeys },
        { path: 'ai/models', name: 'settings-ai-models', component: AIDefaultModels },
        { path: 'ai/budgets', name: 'settings-ai-budgets', component: AIBudgets },
        { path: 'ai/pricing', name: 'settings-ai-pricing', component: AIModelPricing },
      ],
    },
    // The launcher at / lists projects.
    {
      path: '/projects',
      name: 'projects',
      redirect: { name: 'home' },
    },
    // A project opens as its IDE: project panels plus the project workspace.
    {
      path: '/projects/:id',
      name: 'project',
      component: IdeView,
      meta: { requiresAuth: true, hideChrome: true },
    },
    // Merge requests open in a modal over their project.
    {
      path: '/projects/:id/merge-requests/:mrId',
      name: 'merge-request',
      redirect: (to) => {
        const mrId = Number(to.params.mrId)
        if (!Number.isInteger(mrId) || mrId < 1) return { name: 'project', params: { id: to.params.id } }
        return { name: 'project', params: { id: to.params.id }, query: { ...to.query, mr: String(mrId) } }
      },
    },
    {
      path: '/inbox',
      name: 'inbox',
      component: InboxView,
      meta: { requiresAuth: true },
    },
    {
      path: '/usage',
      name: 'usage',
      component: UsageView,
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
