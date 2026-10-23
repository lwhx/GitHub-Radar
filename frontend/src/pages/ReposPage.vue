<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { apiFetch, ApiError } from '../api/client'
import type { PublicRepo, ReposListMeta } from '../api/types'

const items = ref<PublicRepo[]>([])
const meta = ref<ReposListMeta | null>(null)
const loading = ref(true)
const error = ref('')
const query = ref('')
const sort = ref('stars_desc')
const page = ref(1)

const sortOptions = [
  { value: 'stars_desc', label: 'Star 最多' },
  { value: 'stars_asc', label: 'Star 最少' },
  { value: 'created_desc', label: '最新创建' },
  { value: 'updated_desc', label: '最近更新' },
]

async function load() {
  loading.value = true
  error.value = ''
  try {
    const params = new URLSearchParams()
    params.set('page', String(page.value))
    if (query.value.trim() !== '') params.set('q', query.value.trim())
    params.set('sort', sort.value)
    const response = await apiFetch<{ items: PublicRepo[]; meta: ReposListMeta }>(`/api/v1/repos?${params.toString()}`)
    items.value = response.items
    meta.value = response.meta
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '加载仓库列表失败'
  } finally {
    loading.value = false
  }
}

function submitSearch() {
  page.value = 1
  void load()
}

function changeSort() {
  page.value = 1
  void load()
}

function goToPage(target: number) {
  page.value = target
  void load()
}

onMounted(load)
</script>


<template>
  <div class="flex flex-col gap-4">
    <section class="rounded-lg border border-line bg-card p-4">
      <h1 class="text-lg font-semibold">探索项目</h1>
      <form class="mt-3 flex flex-wrap items-center gap-2" @submit.prevent="submitSearch">
        <input
          v-model="query"
          type="search"
          class="min-w-0 flex-1 rounded-md border border-line bg-bg px-3 py-1.5 text-sm"
          placeholder="搜索项目名称或描述"
          aria-label="搜索项目"
        />
        <select
          v-model="sort"
          class="rounded-md border border-line bg-bg px-2 py-1.5 text-sm"
          aria-label="排序方式"
          @change="changeSort"
        >
          <option v-for="option in sortOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
        <button type="submit" class="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white">搜索</button>
      </form>
    </section>

    <p v-if="error" role="alert" class="rounded-lg border border-danger/40 bg-card p-4 text-sm text-danger">
      {{ error }}
      <button type="button" class="ml-2 underline" @click="load">重试</button>
    </p>

    <div v-if="loading" class="flex flex-col gap-3" aria-busy="true" aria-label="加载中">
      <div v-for="index in 5" :key="index" class="h-20 animate-pulse rounded-lg border border-line bg-card" />
    </div>

    <template v-else-if="meta">
      <section v-if="items.length === 0" class="rounded-lg border border-warning/50 bg-card p-6">
        <h2 class="font-medium text-warning">暂无收录数据</h2>
        <p class="mt-2 text-sm text-muted">
          本站只展示真实采集的数据，不使用演示项目或假统计占位。采集任务完成回填后，这里将列出真实收录的公开仓库。
        </p>
      </section>

      <template v-else>
        <p class="text-sm text-muted">共 {{ meta.total }} 个项目</p>
        <ul class="flex flex-col gap-3">
          <li v-for="item in items" :key="item.full_name" class="rounded-lg border border-line bg-card p-4">
            <div class="flex flex-wrap items-baseline justify-between gap-2">
              <RouterLink :to="`/repos/${item.owner}/${item.name}`" class="font-medium hover:text-accent">
                {{ item.full_name }}
              </RouterLink>
              <span class="text-sm text-muted">⭐ {{ item.stars_count }}</span>
            </div>
            <p v-if="item.description" class="mt-1 text-sm text-muted">{{ item.description }}</p>
            <p class="mt-2 flex flex-wrap gap-3 text-xs text-muted">
              <span v-if="item.language">{{ item.language }}</span>
              <span v-if="item.license_spdx">{{ item.license_spdx }}</span>
              <span>收录于 {{ item.first_seen_at.slice(0, 10) }}</span>
            </p>
          </li>
        </ul>
        <div class="flex items-center justify-between text-sm">
          <button
            type="button"
            class="rounded-md border border-line px-3 py-1.5 disabled:opacity-40"
            :disabled="meta.page <= 1"
            @click="goToPage(meta.page - 1)"
          >
            上一页
          </button>
          <span class="text-muted">第 {{ meta.page }} 页</span>
          <button
            type="button"
            class="rounded-md border border-line px-3 py-1.5 disabled:opacity-40"
            :disabled="meta.page * meta.limit >= meta.total"
            @click="goToPage(meta.page + 1)"
          >
            下一页
          </button>
        </div>
      </template>
    </template>
  </div>
</template>
