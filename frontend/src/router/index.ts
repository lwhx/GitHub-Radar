import { createRouter, createWebHistory } from 'vue-router'

import AdminLayout from '../layouts/AdminLayout.vue'
import PublicLayout from '../layouts/PublicLayout.vue'
import AdminCredentialsPage from '../pages/AdminCredentialsPage.vue'
import AdminJobsPage from '../pages/AdminJobsPage.vue'
import AdminLoginPage from '../pages/AdminLoginPage.vue'
import AdminOverviewPage from '../pages/AdminOverviewPage.vue'
import CategoriesPage from '../pages/CategoriesPage.vue'
import HomePage from '../pages/HomePage.vue'
import NotFoundPage from '../pages/NotFoundPage.vue'
import RepoDetailPage from '../pages/RepoDetailPage.vue'
import ReposPage from '../pages/ReposPage.vue'
import { authState, ensureSession } from '../stores/auth'

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
  }
}

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: PublicLayout,
      children: [
        { path: '', name: 'home', component: HomePage },
        { path: 'repos', name: 'repos', component: ReposPage },
        { path: 'repos/:owner/:repo', name: 'repo-detail', component: RepoDetailPage },
        { path: 'categories', name: 'categories', component: CategoriesPage },
        { path: 'admin/login', name: 'admin-login', component: AdminLoginPage },
      ],
    },
    {
      path: '/admin',
      component: AdminLayout,
      meta: { requiresAuth: true },
      children: [
        { path: '', name: 'admin-overview', component: AdminOverviewPage },
        { path: 'credentials', name: 'admin-credentials', component: AdminCredentialsPage },
        { path: 'jobs', name: 'admin-jobs', component: AdminJobsPage },
      ],
    },
    { path: '/:pathMatch(.*)*', name: 'not-found', component: NotFoundPage },
  ],
})

router.beforeEach(async (to) => {
  if (to.meta.requiresAuth) {
    const authenticated = await ensureSession().catch(() => false)
    if (!authenticated) {
      return { name: 'admin-login', query: { redirect: to.fullPath } }
    }
  }
  if (to.name === 'admin-login' && authState.user) {
    return { name: 'admin-overview' }
  }
  return true
})

export default router
