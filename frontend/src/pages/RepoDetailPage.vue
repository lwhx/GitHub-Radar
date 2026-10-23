<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { apiFetch, ApiError } from '../api/client'
import type { PublicRepo } from '../api/types'
import { isFavorited, toggleFavorite } from '../stores/favorites'

const route = useRoute()
const repo = ref<PublicRepo | null>(null)
const loading = ref(true)
const notFound = ref(false)
const error = ref('')

async function load() {
  const owner = String(route.params.owner ?? '')
  const name = String(route.params.repo ?? '')
  if (owner === '' || name === '') return
  loading.value = true
  notFound.value = false
  error.value = ''
  try {
    repo.value = await apiFetch<PublicRepo>(
      `/api/v1/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`,
    )
  } catch (cause) {
    if (cause instanceof ApiError && cause.status === 404) {
      notFound.value = true
    } else {
      error.value = cause instanceof ApiError ? cause.message : '加载仓库详情失败'
    }
  } finally {
    loading.value = false
  }
}

watch(() => [route.params.owner, route.params.repo], load, { immediate: true })

function favoritePayload(current: PublicRepo) {
  return {
    full_name: current.full_name,
    owner: current.owner,
    name: current.name,
    html_url: current.html_url,
    description: current.description,
    language: current.language,
    stars_count: current.stars_count,
  }
}
</script>


<template>
  <div class="flex flex-col gap-4">
    <p v-if="error" role="alert" class="rounded-lg border border-danger/40 bg-card p-4 text-sm text-danger">
      {{ error }}
      <button type="button" class="ml-2 underline" @click="load">重试</button>
    </p>

    <div v-if="loading" class="h-40 animate-pulse rounded-lg border border-line bg-card" aria-busy="true" aria-label="加载中" />

    <section v-else-if="notFound" class="rounded-lg border border-warning/50 bg-card p-6">
      <h1 class="font-medium text-warning">仓库不存在或已被隐藏</h1>
      <p class="mt-2 text-sm text-muted">它可能尚未被采集，或已被管理员隐藏。</p>
      <RouterLink to="/repos" class="mt-3 inline-block text-sm underline hover:text-accent">返回列表</RouterLink>
    </section>

    <template v-else-if="repo">
      <section class="rounded-lg border border-line bg-card p-6">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <h1 class="text-xl font-semibold">{{ repo.full_name }}</h1>
          <div class="flex items-center gap-3 text-sm">
            <button
              type="button"
              class="underline hover:text-accent"
              :aria-pressed="isFavorited(repo.full_name)"
              @click="toggleFavorite(favoritePayload(repo))"
            >
              {{ isFavorited(repo.full_name) ? '★ 已收藏' : '☆ 收藏' }}
            </button>
            <a :href="repo.html_url" class="underline hover:text-accent" rel="noopener noreferrer">在 GitHub 打开</a>
          </div>
        </div>
        <p v-if="repo.description" class="mt-2 text-sm text-muted">{{ repo.description }}</p>
        <div class="mt-4 grid grid-cols-3 gap-4">
          <div class="rounded-lg border border-line bg-bg p-3">
            <p class="text-xs text-muted">Star</p>
            <p class="mt-1 text-xl font-semibold">{{ repo.stars_count }}</p>
          </div>
          <div class="rounded-lg border border-line bg-bg p-3">
            <p class="text-xs text-muted">Fork</p>
            <p class="mt-1 text-xl font-semibold">{{ repo.forks_count }}</p>
          </div>
          <div class="rounded-lg border border-line bg-bg p-3">
            <p class="text-xs text-muted">Issue</p>
            <p class="mt-1 text-xl font-semibold">{{ repo.open_issues_count }}</p>
          </div>
        </div>
        <div v-if="repo.topics.length > 0" class="mt-4 flex flex-wrap gap-2">
          <span v-for="topic in repo.topics" :key="topic" class="rounded bg-bg px-2 py-0.5 text-xs text-muted">{{ topic }}</span>
        </div>
      </section>

      <section class="rounded-lg border border-line bg-card p-6">
        <h2 class="text-sm font-medium">项目信息</h2>
        <dl class="mt-3 grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
          <div v-if="repo.language"><dt class="text-muted">语言</dt><dd>{{ repo.language }}</dd></div>
          <div v-if="repo.license_spdx"><dt class="text-muted">许可证</dt><dd>{{ repo.license_spdx }}</dd></div>
          <div v-if="repo.homepage_url">
            <dt class="text-muted">主页</dt>
            <dd><a :href="repo.homepage_url" class="underline hover:text-accent" rel="noopener noreferrer">{{ repo.homepage_url }}</a></dd>
          </div>
          <div><dt class="text-muted">创建时间</dt><dd>{{ repo.github_created_at.slice(0, 10) }}</dd></div>
          <div><dt class="text-muted">收录时间</dt><dd>{{ repo.first_seen_at.slice(0, 10) }}</dd></div>
          <div>
            <dt class="text-muted">最近同步</dt>
            <dd>{{ repo.last_synced_at ? repo.last_synced_at.slice(0, 10) : '暂无' }}</dd>
          </div>
        </dl>
        <RouterLink to="/repos" class="mt-4 inline-block text-sm underline hover:text-accent">返回列表</RouterLink>
      </section>
    </template>
  </div>
</template>
