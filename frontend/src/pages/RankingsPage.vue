<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { apiFetch, ApiError } from '../api/client'
import type { RankedRepo, RankingsMeta } from '../api/types'

const route = useRoute()
const router = useRouter()

const tabs = [
  { value: 'latest', label: '最新项目' },
  { value: '24h', label: '24h 飙升' },
  { value: '7d', label: '7 日增长' },
  { value: 'potential', label: '潜力黑马' },
]

const items = ref<RankedRepo[]>([])
const meta = ref<RankingsMeta | null>(null)
const loading = ref(true)
const error = ref('')
const page = ref(1)

const activeType = computed(() => {
  const raw = route.query.type
  const value = typeof raw === 'string' ? raw : 'latest'
  return tabs.some((tab) => tab.value === value) ? value : 'latest'
})

const totalPages = computed(() => {
  if (!meta.value || meta.value.limit === 0) return 1
  return Math.max(1, Math.ceil(meta.value.total / meta.value.limit))
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const response = await apiFetch<{ items: RankedRepo[]; meta: RankingsMeta }>(
      `/api/v1/rankings?type=${activeType.value}&page=${page.value}`,
    )
    items.value = response.items
    meta.value = response.meta
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '加载排行榜失败'
  } finally {
    loading.value = false
  }
}

function switchType(value: string) {
  if (activeType.value === value) return
  page.value = 1
  void router.push({ query: { type: value } })
}

function goToPage(target: number) {
  if (target < 1 || target > totalPages.value) return
  page.value = target
  void load()
}

watch(
  () => route.query.type,
  () => {
    page.value = 1
    void load()
  },
)

onMounted(load)
</script>


<template>
  <div class="flex flex-col gap-4">
    <section class="rounded-lg border border-line bg-card p-4">
      <h1 class="text-lg font-semibold">排行榜</h1>
      <div class="mt-3 flex flex-wrap gap-2">
        <button
          v-for="tab in tabs"
          :key="tab.value"
          type="button"
          class="rounded-md border px-3 py-1 text-sm"
          :class="activeType === tab.value ? 'border-accent text-accent' : 'border-line text-muted'"
          :aria-pressed="activeType === tab.value"
          @click="switchType(tab.value)"
        >
          {{ tab.label }}
        </button>
      </div>
      <p v-if="meta" class="mt-2 text-xs text-muted">{{ meta.note }}</p>
      <p v-if="meta?.data_updated_at" class="text-xs text-muted">数据更新时间：{{ meta.data_updated_at }}</p>
    </section>

    <p v-if="error" role="alert" class="rounded-lg border border-danger/40 bg-card p-4 text-sm text-danger">
      {{ error }}
      <button type="button" class="ml-2 underline" @click="load">重试</button>
    </p>

    <section
      v-if="meta && !meta.history_available"
      class="rounded-lg border border-warning/50 bg-card p-6"
    >
      <h2 class="font-medium text-warning">数据不足</h2>
      <p class="mt-2 text-sm text-muted">{{ meta.note }}</p>
    </section>

    <div v-if="loading" class="flex flex-col gap-3" aria-busy="true" aria-label="加载中">
      <div v-for="index in 5" :key="index" class="h-16 animate-pulse rounded-lg border border-line bg-card" />
    </div>

    <template v-else-if="meta?.history_available">
      <section v-if="items.length === 0" class="rounded-lg border border-warning/50 bg-card p-6">
        <h2 class="font-medium text-warning">暂无排行数据</h2>
        <p class="mt-2 text-sm text-muted">本站不使用演示数据占位。采集到数据后榜单会自动填充。</p>
      </section>

      <ol v-else class="flex flex-col gap-3">
        <li
          v-for="(item, index) in items"
          :key="item.full_name"
          class="rounded-lg border border-line bg-card p-4"
        >
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <div class="flex items-baseline gap-3">
              <span class="text-sm text-muted">{{ (page - 1) * (meta?.limit ?? 20) + index + 1 }}</span>
              <RouterLink :to="`/repos/${item.owner}/${item.name}`" class="font-medium hover:text-accent">
                {{ item.full_name }}
              </RouterLink>
              <span
                v-if="item.star_delta !== null"
                class="text-sm"
                :class="item.star_delta > 0 ? 'text-success' : 'text-muted'"
              >
                {{ item.star_delta > 0 ? `+${item.star_delta}` : item.star_delta }}
              </span>
              <span v-if="item.score !== null" class="text-xs text-muted">分数 {{ item.score }}</span>
            </div>
            <span class="text-sm text-muted">⭐ {{ item.stars_count }}</span>
          </div>
          <p v-if="item.description" class="mt-1 text-sm text-muted">{{ item.description }}</p>
          <p v-if="item.score_reason" class="mt-1 text-xs text-muted">{{ item.score_reason }}</p>
          <p class="mt-2 flex flex-wrap gap-3 text-xs text-muted">
            <span v-if="item.language">{{ item.language }}</span>
            <span v-if="activeType === 'latest'">创建于 {{ item.github_created_at.slice(0, 10) }}</span>
          </p>
        </li>
      </ol>

      <div v-if="totalPages > 1" class="flex items-center gap-3 text-sm">
        <button type="button" class="underline disabled:opacity-40" :disabled="page <= 1" @click="goToPage(page - 1)">
          上一页
        </button>
        <span class="text-muted">第 {{ page }} / {{ totalPages }} 页</span>
        <button
          type="button"
          class="underline disabled:opacity-40"
          :disabled="page >= totalPages"
          @click="goToPage(page + 1)"
        >
          下一页
        </button>
      </div>
    </template>
  </div>
</template>
