<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { apiFetch, ApiError } from '../api/client'
import type { PublicStats } from '../api/types'

const stats = ref<PublicStats | null>(null)
const loading = ref(true)
const error = ref('')

function formatDateTime(iso: string | null): string {
  if (!iso) return '暂无数据'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return date.toLocaleString('zh-CN', { hour12: false })
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    stats.value = await apiFetch<PublicStats>('/api/v1/stats')
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '加载统计信息失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <section class="rounded-lg border border-line bg-card p-6">
      <h1 class="text-xl font-semibold">发现值得关注的 GitHub 开源项目</h1>
      <p class="mt-2 text-sm text-muted">
        跟踪新建项目的 Star 增长，按 AI、Docker、自托管等领域分类发现潜力项目。
      </p>
    </section>

    <p v-if="error" role="alert" class="rounded-lg border border-danger/40 bg-card p-4 text-sm text-danger">
      {{ error }}
      <button type="button" class="ml-2 underline" @click="load">重试</button>
    </p>

    <div v-if="loading" class="grid grid-cols-2 gap-4 lg:grid-cols-4" aria-busy="true" aria-label="加载中">
      <div v-for="index in 4" :key="index" class="h-24 animate-pulse rounded-lg border border-line bg-card" />
    </div>

    <template v-else-if="stats">
      <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <div class="rounded-lg border border-line bg-card p-4">
          <p class="text-sm text-muted">收录仓库</p>
          <p class="mt-1 text-2xl font-semibold">{{ stats.repositories }}</p>
        </div>
        <div class="rounded-lg border border-line bg-card p-4">
          <p class="text-sm text-muted">Star 快照</p>
          <p class="mt-1 text-2xl font-semibold">{{ stats.star_snapshots }}</p>
        </div>
        <div class="rounded-lg border border-line bg-card p-4">
          <p class="text-sm text-muted">启用分类</p>
          <p class="mt-1 text-2xl font-semibold">{{ stats.categories }}</p>
        </div>
        <div class="rounded-lg border border-line bg-card p-4">
          <p class="text-sm text-muted">数据更新时间</p>
          <p class="mt-1 text-sm font-medium">{{ formatDateTime(stats.data_updated_at) }}</p>
        </div>
      </div>

      <section
        v-if="stats.repositories === 0"
        class="rounded-lg border border-warning/50 bg-card p-6"
      >
        <h2 class="font-medium text-warning">数据库为空</h2>
        <p class="mt-2 text-sm text-muted">
          本站只展示真实采集的数据，不使用演示项目或假统计占位。采集引擎首次启动后会自动回填过去
          7 天的公开仓库。数据采集完成前，榜单与搜索保持空态。
        </p>
        <ul class="mt-3 list-disc space-y-1 pl-5 text-sm text-muted">
          <li>管理员可在部署后通过 <code class="rounded bg-bg px-1">radar admin create</code> 创建账户并登录后台；</li>
          <li>在后台「凭据管理」添加 GitHub 凭据，采集任务将按计划自动运行；</li>
          <li>采集完成后，首页、排行榜与项目详情将陆续可用。</li>
        </ul>
      </section>
    </template>
  </div>
</template>
