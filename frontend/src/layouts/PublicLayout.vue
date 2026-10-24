<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink, RouterView } from 'vue-router'

import { authState, ensureSession } from '../stores/auth'
import { isDark, toggleTheme } from '../stores/theme'

interface NavItem {
  label: string
  to?: string
  disabled?: boolean
}

const navItems: NavItem[] = [
  { label: '发现首页', to: '/' },
  { label: '排行榜', to: '/rankings' },
  { label: '探索项目', to: '/repos' },
  { label: '分类专题', to: '/categories' },
  { label: '我的关注', to: '/favorites' },
]

const drawerOpen = ref(false)

function closeDrawer() {
  drawerOpen.value = false
}

onMounted(() => {
  void ensureSession().catch(() => false)
})
</script>

<template>
  <div class="flex min-h-screen flex-col">
    <header class="sticky top-0 z-20 border-b border-line bg-card">
      <div class="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4">
        <button
          type="button"
          class="rounded-md p-2 text-muted hover:bg-bg md:hidden"
          aria-label="打开导航菜单"
          :aria-expanded="drawerOpen"
          @click="drawerOpen = true"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M4 6h16M4 12h16M4 18h16" />
          </svg>
        </button>
        <RouterLink to="/" class="flex items-center gap-2 font-semibold" @click="closeDrawer">
          <span class="text-accent" aria-hidden="true">◉</span>
          GitHub Radar
        </RouterLink>
        <div class="ml-auto flex items-center gap-2">
          <input
            type="search"
            class="hidden w-56 rounded-md border border-line bg-bg px-3 py-1.5 text-sm text-muted sm:block"
            placeholder="搜索项目（P1.3 提供）"
            disabled
            title="本地搜索将在 P1.3 采集闭环后开放"
          />
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
          <nav class="flex items-center gap-2" aria-label="账户导航">
            <span v-if="authState.user" class="hidden text-sm text-muted sm:inline">{{ authState.user.username }}</span>
            <RouterLink
              :to="authState.user ? '/admin' : '/admin/login'"
              class="rounded-md border border-line px-3 py-1.5 text-sm font-medium text-muted hover:bg-bg hover:text-fg"
            >
              {{ authState.user ? '管理后台' : '管理员登录' }}
            </RouterLink>
          </nav>
        </div>
      </div>
    </header>

    <div v-if="drawerOpen" class="fixed inset-0 z-30 md:hidden" @click.self="closeDrawer">
      <div class="absolute inset-y-0 left-0 w-64 border-r border-line bg-card p-4" role="dialog" aria-label="导航菜单">
        <nav class="flex flex-col gap-1">
          <template v-for="item in navItems" :key="item.label">
            <RouterLink
              v-if="item.to"
              :to="item.to"
              class="rounded-md px-3 py-2 text-sm hover:bg-bg"
              active-class="bg-bg font-medium"
              @click="closeDrawer"
            >
              {{ item.label }}
            </RouterLink>
            <span
              v-else
              class="flex items-center justify-between rounded-md px-3 py-2 text-sm text-muted"
              aria-disabled="true"
            >
              {{ item.label }}
              <span class="rounded bg-bg px-1.5 py-0.5 text-xs">后续阶段</span>
            </span>
          </template>
        </nav>
      </div>
    </div>

    <div class="mx-auto flex w-full max-w-6xl flex-1 gap-6 px-4 py-6">
      <aside class="hidden w-48 shrink-0 md:block">
        <nav class="sticky top-20 flex flex-col gap-1" aria-label="主导航">
          <template v-for="item in navItems" :key="item.label">
            <RouterLink
              v-if="item.to"
              :to="item.to"
              class="rounded-md px-3 py-2 text-sm hover:bg-card"
              active-class="bg-card font-medium"
            >
              {{ item.label }}
            </RouterLink>
            <span
              v-else
              class="flex items-center justify-between rounded-md px-3 py-2 text-sm text-muted"
              aria-disabled="true"
            >
              {{ item.label }}
              <span class="rounded bg-card px-1.5 py-0.5 text-xs">后续阶段</span>
            </span>
          </template>
        </nav>
      </aside>

      <main class="min-w-0 flex-1">
        <RouterView />
      </main>
    </div>

    <footer class="border-t border-line py-4 text-center text-xs text-muted">
      <p>GitHub Radar 是独立的第三方开源项目发现平台，与 GitHub, Inc. 无隶属关系，非 GitHub 官方产品。</p>
      <p class="mt-1">
        数据来源于 GitHub 公开 API
      </p>
    </footer>
  </div>
</template>
