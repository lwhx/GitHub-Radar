import { createRouter, createWebHistory } from 'vue-router'

import AdminLayout from '../layouts/AdminLayout.vue'
import PublicLayout from '../layouts/PublicLayout.vue'
import AdminCredentialsPage from '../pages/AdminCredentialsPage.vue'
import AdminCategoriesPage from '../pages/AdminCategoriesPage.vue'
import AdminJobsPage from '../pages/AdminJobsPage.vue'
import AdminLoginPage from '../pages/AdminLoginPage.vue'
import AdminOverviewPage from '../pages/AdminOverviewPage.vue'
import AdminOperationsPage from '../pages/AdminOperationsPage.vue'
import AdminRepositoriesPage from '../pages/AdminRepositoriesPage.vue'
import CategoriesPage from '../pages/CategoriesPage.vue'
import FavoritesPage from '../pages/FavoritesPage.vue'
import HomePage from '../pages/HomePage.vue'
import NotFoundPage from '../pages/NotFoundPage.vue'
import RankingsPage from '../pages/RankingsPage.vue'
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
        { path: 'favorites', name: 'favorites', component: FavoritesPage },
        { path: 'rankings', name: 'rankings', component: RankingsPage },
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
        { path: 'categories', name: 'admin-categories', component: AdminCategoriesPage },
        { path: 'repositories', name: 'admin-repositories', component: AdminRepositoriesPage },
        { path: 'jobs', name: 'admin-jobs', component: AdminJobsPage },
        { path: 'operations', name: 'admin-operations', component: AdminOperationsPage },
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
