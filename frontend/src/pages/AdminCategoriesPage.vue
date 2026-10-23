<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'

import { apiFetch, ApiError } from '../api/client'
import type { AdminCategoriesResponse, AdminCategory } from '../api/types'
import { csrfTokenForRequest } from '../stores/auth'

const categories = ref<AdminCategory[]>([])
const loading = ref(true)
const saving = ref(false)
const busyId = ref<number | null>(null)
const error = ref('')
const notice = ref('')
const form = reactive({ slug: '', title: '', description: '', sort_order: 0 })

function messageFrom(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? cause.message : fallback
}

async function loadCategories() {
  loading.value = true
  error.value = ''
  try {
    const data = await apiFetch<AdminCategoriesResponse>('/api/v1/admin/categories')
    categories.value = data.items
  } catch (cause) {
    error.value = messageFrom(cause, '加载分类失败')
  } finally {
    loading.value = false
  }
}

async function createCategory() {
  if (saving.value) return
  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    const created = await apiFetch<AdminCategory>('/api/v1/admin/categories', {
      method: 'POST',
      body: {
        slug: form.slug.trim(),
        title: form.title.trim(),
        description: form.description.trim(),
        sort_order: Number(form.sort_order),
      },
      csrfToken: csrfTokenForRequest(),
    })
    categories.value = [created, ...categories.value]
    form.slug = ''
    form.title = ''
    form.description = ''
    form.sort_order = 0
    notice.value = `已创建分类“${created.title}”`
  } catch (cause) {
    error.value = messageFrom(cause, '创建分类失败')
  } finally {
    saving.value = false
  }
}

async function toggleCategory(item: AdminCategory) {
  if (busyId.value !== null) return
  busyId.value = item.id
  error.value = ''
  notice.value = ''
  try {
    const updated = await apiFetch<AdminCategory>(`/api/v1/admin/categories/${item.id}`, {
      method: 'PATCH',
      body: { enabled: !item.enabled },
      csrfToken: csrfTokenForRequest(),
    })
    categories.value = categories.value.map((category) => (category.id === updated.id ? updated : category))
    notice.value = `${updated.enabled ? '已启用' : '已停用'}分类“${updated.title}”`
  } catch (cause) {
    error.value = messageFrom(cause, '更新分类失败')
  } finally {
    busyId.value = null
  }
}

async function removeCategory(item: AdminCategory) {
  if (!window.confirm(`确认删除分类“${item.title}”？该分类下的仓库会解除归类，仓库本身不受影响。`)) {
    return
  }
  if (busyId.value !== null) return
  busyId.value = item.id
  error.value = ''
  notice.value = ''
  try {
    await apiFetch<{ id: number; deleted: boolean }>(`/api/v1/admin/categories/${item.id}`, {
      method: 'DELETE',
      csrfToken: csrfTokenForRequest(),
    })
    categories.value = categories.value.filter((category) => category.id !== item.id)
    notice.value = `已删除分类“${item.title}”`
  } catch (cause) {
    error.value = messageFrom(cause, '删除分类失败')
  } finally {
    busyId.value = null
  }
}

onMounted(loadCategories)
</script>

<template>
  <section class="space-y-6">
    <header class="space-y-1">
      <h1 class="text-xl font-semibold">分类管理</h1>
      <p class="text-sm text-muted">
        分类决定前台「分类专题」页面的分组。停用后分类仍保留归属关系，但不再对访客展示；删除会解除归属（仓库不受影响）。
      </p>
    </header>

    <p v-if="error" class="rounded-md border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
    <p v-if="notice" class="rounded-md border border-line bg-bg px-3 py-2 text-sm text-muted">{{ notice }}</p>

    <form
      data-test="category-form"
      class="grid gap-3 rounded-lg border border-line bg-card p-4 md:grid-cols-4"
      @submit.prevent="createCategory"
    >
      <label class="flex flex-col gap-1 text-sm">
        <span class="text-muted">slug（创建后不可改）</span>
        <input
          v-model="form.slug"
          data-test="category-slug"
          class="rounded-md border border-line bg-bg px-2 py-1.5"
          placeholder="ai-agents"
        />
      </label>
      <label class="flex flex-col gap-1 text-sm">
        <span class="text-muted">名称</span>
        <input v-model="form.title" data-test="category-title" class="rounded-md border border-line bg-bg px-2 py-1.5" />
      </label>
      <label class="flex flex-col gap-1 text-sm">
        <span class="text-muted">描述</span>
        <input
          v-model="form.description"
          data-test="category-description"
          class="rounded-md border border-line bg-bg px-2 py-1.5"
        />
      </label>
      <label class="flex flex-col gap-1 text-sm">
        <span class="text-muted">排序（越小越靠前）</span>
        <input
          v-model="form.sort_order"
          data-test="category-sort"
          type="number"
          class="rounded-md border border-line bg-bg px-2 py-1.5"
        />
      </label>
      <div class="md:col-span-4">
        <button
          type="submit"
          data-test="create-category"
          class="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white disabled:opacity-60"
          :disabled="saving"
        >
          {{ saving ? '创建中…' : '新增分类' }}
        </button>
      </div>
    </form>

    <p v-if="loading" class="text-sm text-muted">加载中…</p>

    <div v-else class="overflow-x-auto rounded-lg border border-line">
      <table class="min-w-full divide-y divide-line text-sm">
        <thead class="bg-bg text-left text-xs uppercase tracking-wide text-muted">
          <tr>
            <th class="px-3 py-2">分类</th>
            <th class="px-3 py-2">slug</th>
            <th class="px-3 py-2">排序</th>
            <th class="px-3 py-2">可见仓库</th>
            <th class="px-3 py-2">状态</th>
            <th class="px-3 py-2 text-right">操作</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-line bg-card">
          <tr v-for="item in categories" :key="item.id" :data-test="`category-row-${item.id}`">
            <td class="px-3 py-2">
              <div class="font-medium">{{ item.title }}</div>
              <div v-if="item.description" class="text-xs text-muted">{{ item.description }}</div>
            </td>
            <td class="px-3 py-2 font-mono text-xs text-muted">{{ item.slug }}</td>
            <td class="px-3 py-2">{{ item.sort_order }}</td>
            <td class="px-3 py-2">{{ item.repo_count }}</td>
            <td class="px-3 py-2">
              <span v-if="item.enabled" class="text-emerald-600">启用中</span>
              <span v-else class="text-muted">已停用</span>
            </td>
            <td class="px-3 py-2 text-right">
              <button
                type="button"
                :data-test="`toggle-category-${item.id}`"
                class="rounded-md border border-line px-2 py-1 text-xs hover:bg-bg"
                :disabled="busyId === item.id"
                @click="toggleCategory(item)"
              >
                {{ item.enabled ? '停用' : '启用' }}
              </button>
              <button
                type="button"
                :data-test="`delete-category-${item.id}`"
                class="ml-2 rounded-md border border-red-300 px-2 py-1 text-xs text-red-600 hover:bg-red-50"
                :disabled="busyId === item.id"
                @click="removeCategory(item)"
              >
                删除
              </button>
            </td>
          </tr>
          <tr v-if="categories.length === 0">
            <td colspan="6" class="px-3 py-6 text-center text-sm text-muted">
              尚无分类。上方表单创建后，前台「分类专题」才会出现对应分组。
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
