<script setup lang="ts">
import { RouterLink, RouterView, useRouter } from 'vue-router'

import { authState, logout } from '../stores/auth'
import { isDark, toggleTheme } from '../stores/theme'

const router = useRouter()

async function onLogout() {
  await logout().catch(() => undefined)
  await router.push({ name: 'admin-login' })
}
</script>

<template>
  <div class="flex min-h-screen flex-col">
    <header class="sticky top-0 z-20 border-b border-line bg-card">
      <div class="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4">
        <span class="font-semibold"><span class="text-accent" aria-hidden="true">◉</span> GitHub Radar 管理后台</span>
        <div class="ml-auto flex items-center gap-2">
          <span class="text-sm text-muted">{{ authState.user?.username }}</span>
          <button
            type="button"
            class="rounded-md border border-line p-2 text-muted hover:bg-bg"
            :aria-label="isDark ? '切换到亮色主题' : '切换到暗色主题'"
            @click="toggleTheme"
          >
            <svg v-if="isDark" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="12" cy="12" r="4" />
              <path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
            </svg>
            <svg v-else width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z" />
            </svg>
          </button>
          <button
            type="button"
            class="rounded-md border border-line px-3 py-1.5 text-sm text-muted hover:bg-bg"
            @click="onLogout"
          >
            退出登录
          </button>
        </div>
      </div>
    </header>

    <div class="mx-auto flex w-full max-w-6xl flex-1 flex-col gap-6 px-4 py-6 md:flex-row">
      <aside class="w-full shrink-0 md:w-48">
        <nav class="flex gap-1 overflow-x-auto md:sticky md:top-20 md:flex-col" aria-label="后台导航">
          <RouterLink class="rounded-md px-3 py-2 text-sm font-medium text-muted hover:bg-bg hover:text-fg" active-class="bg-bg text-fg" :to="{ name: 'admin-overview' }">数据概览</RouterLink>
          <RouterLink class="rounded-md px-3 py-2 text-sm font-medium text-muted hover:bg-bg hover:text-fg" active-class="bg-bg text-fg" :to="{ name: 'admin-credentials' }">凭据管理</RouterLink>
          <RouterLink class="rounded-md px-3 py-2 text-sm font-medium text-muted hover:bg-bg hover:text-fg" active-class="bg-bg text-fg" :to="{ name: 'admin-jobs' }">采集任务</RouterLink>
          <span class="flex items-center justify-between gap-2 rounded-md px-3 py-2 text-sm text-muted" aria-disabled="true">
            分类管理<span class="rounded bg-card px-1.5 py-0.5 text-xs">P1.4</span>
          </span>
          <span class="flex items-center justify-between gap-2 rounded-md px-3 py-2 text-sm text-muted" aria-disabled="true">
            系统设置<span class="rounded bg-card px-1.5 py-0.5 text-xs">P1.4</span>
          </span>
          <span class="flex items-center justify-between gap-2 rounded-md px-3 py-2 text-sm text-muted" aria-disabled="true">
            备份恢复<span class="rounded bg-card px-1.5 py-0.5 text-xs">P1.4</span>
          </span>
        </nav>
      </aside>

      <main class="min-w-0 flex-1">
        <RouterView />
      </main>
    </div>
  </div>
</template>
