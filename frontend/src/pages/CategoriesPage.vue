<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { apiFetch, ApiError } from '../api/client'
import type { PublicCategory } from '../api/types'

const items = ref<PublicCategory[]>([])
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    const response = await apiFetch<{ items: PublicCategory[] }>('/api/v1/categories')
    items.value = response.items
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '加载分类失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>


<template>
  <div class="flex flex-col gap-4">
    <section class="rounded-lg border border-line bg-card p-4">
      <h1 class="text-lg font-semibold">分类专题</h1>
      <p class="mt-1 text-sm text-muted">按领域浏览真实收录的开源项目。</p>
    </section>

    <p v-if="error" role="alert" class="rounded-lg border border-danger/40 bg-card p-4 text-sm text-danger">
      {{ error }}
      <button type="button" class="ml-2 underline" @click="load">重试</button>
    </p>

    <div v-if="loading" class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true" aria-label="加载中">
      <div v-for="index in 6" :key="index" class="h-24 animate-pulse rounded-lg border border-line bg-card" />
    </div>

    <template v-else>
      <section v-if="items.length === 0" class="rounded-lg border border-warning/50 bg-card p-6">
        <h2 class="font-medium text-warning">暂无分类</h2>
        <p class="mt-2 text-sm text-muted">本站不使用演示分类占位。分类规则配置后这里将展示真实分类。</p>
      </section>

      <div v-else class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <RouterLink
          v-for="category in items"
          :key="category.slug"
          :to="`/repos?category=${category.slug}`"
          class="rounded-lg border border-line bg-card p-4 hover:border-accent"
        >
          <div class="flex items-baseline justify-between gap-2">
            <h2 class="font-medium">{{ category.title }}</h2>
            <span class="text-sm text-muted">{{ category.repo_count }}</span>
          </div>
          <p v-if="category.description" class="mt-1 text-sm text-muted">{{ category.description }}</p>
        </RouterLink>
      </div>
    </template>
  </div>
</template>
