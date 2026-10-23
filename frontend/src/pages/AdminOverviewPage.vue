<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { apiFetch, ApiError } from '../api/client'
import type { AdminOverview } from '../api/types'

const router = useRouter()

const overview = ref<AdminOverview | null>(null)
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    overview.value = await apiFetch<AdminOverview>('/api/v1/admin/overview')
  } catch (cause) {
    if (cause instanceof ApiError && cause.status === 401) {
      await router.replace({ name: 'admin-login', query: { redirect: '/admin' } })
      return
    }
    error.value = cause instanceof ApiError ? cause.message : '加载概览失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold">数据概览</h1>
      <button
        type="button"
        class="rounded-md border border-line px-3 py-1.5 text-sm hover:bg-bg"
        :disabled="loading"
        @click="load"
      >
        刷新
      </button>
    </div>

    <p v-if="error" role="alert" class="rounded-lg border border-danger/40 bg-card p-4 text-sm text-danger">
      {{ error }}
    </p>

    <div v-if="loading" class="grid grid-cols-2 gap-4 lg:grid-cols-3" aria-busy="true">
      <div v-for="index in 6" :key="index" class="h-24 animate-pulse rounded-lg border border-line bg-card" />
    </div>

    <dl v-else-if="overview" class="grid grid-cols-2 gap-4 lg:grid-cols-3">
      <div class="rounded-lg border border-line bg-card p-4">
        <dt class="text-sm text-muted">收录仓库</dt>
        <dd class="mt-1 text-2xl font-semibold">{{ overview.repository_count }}</dd>
      </div>
      <div class="rounded-lg border border-line bg-card p-4">
        <dt class="text-sm text-muted">Star 快照</dt>
        <dd class="mt-1 text-2xl font-semibold">{{ overview.snapshot_count }}</dd>
      </div>
      <div class="rounded-lg border border-line bg-card p-4">
        <dt class="text-sm text-muted">分类</dt>
        <dd class="mt-1 text-2xl font-semibold">{{ overview.category_count }}</dd>
      </div>
      <div class="rounded-lg border border-line bg-card p-4">
        <dt class="text-sm text-muted">管理员</dt>
        <dd class="mt-1 text-2xl font-semibold">{{ overview.admin_count }}</dd>
      </div>
      <div class="rounded-lg border border-line bg-card p-4">
        <dt class="text-sm text-muted">活跃会话</dt>
        <dd class="mt-1 text-2xl font-semibold">{{ overview.active_session_count }}</dd>
      </div>
      <div class="rounded-lg border border-line bg-card p-4">
        <dt class="text-sm text-muted">数据库状态</dt>
        <dd class="mt-1">
          <span
            class="inline-block rounded-full px-2.5 py-1 text-xs font-medium"
            :class="overview.database_ready ? 'bg-success/15 text-success' : 'bg-danger/15 text-danger'"
          >
            {{ overview.database_ready ? '就绪' : '未就绪' }}
          </span>
        </dd>
      </div>
    </dl>

    <p class="text-sm text-muted">
      在“凭据管理”添加并验证 GitHub Token，在“采集任务”查看调度与真实运行记录；分类、设置与备份恢复将在 P1.4 提供。
    </p>
  </div>
</template>
