<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'

import { apiFetch, ApiError } from '../api/client'
import type {
  AdminCategoriesResponse,
  AdminCategory,
  AdminRepositoriesResponse,
  AdminRepository,
  AdminRepositoryImportResponse,
  RepositorySyncStatus,
} from '../api/types'
import { csrfTokenForRequest } from '../stores/auth'

const repositories = ref<AdminRepository[]>([])
const categories = ref<AdminCategory[]>([])
const loading = ref(true)
const importing = ref(false)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const query = ref('')
const appliedQuery = ref('')
const page = ref(1)
const pageSize = 20
const total = ref(0)
const importURL = ref('')
const editing = ref<AdminRepository | null>(null)
const editForm = reactive({ hidden: false, category_ids: [] as number[], resync: false })
let repositoryRequestSequence = 0

const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

function messageFrom(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? cause.message : fallback
}

function categoryNames(item: AdminRepository): string {
  const names = item.category_ids
    .map((id) => categories.value.find((category) => category.id === id)?.title)
    .filter((name): name is string => Boolean(name))
  return names.length > 0 ? names.join('、') : '未分类'
}

function syncLabel(status: RepositorySyncStatus): string {
  switch (status) {
    case 'pending':
      return '待同步'
    case 'syncing':
      return '同步中'
    case 'synced':
      return '已同步'
    case 'retrying':
      return '待重试'
    case 'failed':
      return '同步失败'
    case 'inaccessible':
      return '不可访问'
    default:
      return '未知状态'
  }
}

async function loadRepositories() {
  const requestSequence = ++repositoryRequestSequence
  loading.value = true
  error.value = ''
  const params = new URLSearchParams()
  if (appliedQuery.value) params.set('q', appliedQuery.value)
  params.set('page', String(page.value))
  params.set('page_size', String(pageSize))
  try {
    const data = await apiFetch<AdminRepositoriesResponse>(`/api/v1/admin/repos?${params.toString()}`)
    if (requestSequence !== repositoryRequestSequence) return
    repositories.value = data.items
    total.value = data.total
    page.value = data.page
  } catch (cause) {
    if (requestSequence === repositoryRequestSequence) {
      error.value = messageFrom(cause, '加载仓库失败')
    }
  } finally {
    if (requestSequence === repositoryRequestSequence) loading.value = false
  }
}

async function loadCategories() {
  try {
    const data = await apiFetch<AdminCategoriesResponse>('/api/v1/admin/categories')
    categories.value = data.items
  } catch (cause) {
    error.value = messageFrom(cause, '加载分类失败')
  }
}

async function searchRepositories() {
  appliedQuery.value = query.value.trim()
  page.value = 1
  await loadRepositories()
}

async function goToPage(nextPage: number) {
  if (nextPage < 1 || nextPage > pageCount.value || nextPage === page.value) return
  page.value = nextPage
  await loadRepositories()
}

async function importRepository() {
  if (importing.value) return
  importing.value = true
  error.value = ''
  notice.value = ''
  const viewSequence = repositoryRequestSequence
  try {
    const result = await apiFetch<AdminRepositoryImportResponse>('/api/v1/admin/repos/import', {
      method: 'POST',
      body: { url: importURL.value.trim() },
      csrfToken: csrfTokenForRequest(),
    })
    if (viewSequence === repositoryRequestSequence) {
      query.value = ''
      appliedQuery.value = ''
      page.value = 1
    }
    await loadRepositories()
    importURL.value = ''
    notice.value = `${result.created ? '已导入' : '已刷新'} ${result.repository.full_name}`
  } catch (cause) {
    error.value = messageFrom(cause, '导入仓库失败')
  } finally {
    importing.value = false
  }
}

function openEditor(item: AdminRepository) {
  editing.value = item
  editForm.hidden = item.hidden
  editForm.category_ids = [...item.category_ids]
  editForm.resync = false
  error.value = ''
  notice.value = ''
}

function onHiddenChange() {
  if (editForm.hidden) editForm.resync = false
}

function closeEditor() {
  if (!saving.value) editing.value = null
}

async function saveRepository() {
  if (!editing.value || saving.value) return
  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    const updated = await apiFetch<AdminRepository>(`/api/v1/admin/repos/${editing.value.id}`, {
      method: 'PATCH',
      body: {
        hidden: editForm.hidden,
        category_ids: editForm.category_ids,
        resync: editForm.resync,
      },
      csrfToken: csrfTokenForRequest(),
    })
    repositories.value = repositories.value.map((item) => (item.id === updated.id ? updated : item))
    notice.value = `已更新 ${updated.full_name}`
    editing.value = null
  } catch (cause) {
    error.value = messageFrom(cause, '更新仓库失败')
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void loadRepositories()
  void loadCategories()
})
</script>

<template>
  <section class="space-y-6">
    <header class="space-y-1">
      <h1 class="text-xl font-semibold">仓库管理</h1>
      <p class="text-sm text-muted">手工导入会实时读取 GitHub 公共仓库数据；隐藏、分类与重同步属于本地管理操作。</p>
    </header>

    <p v-if="error" role="alert" class="rounded-md border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
    <p v-if="notice" class="rounded-md border border-line bg-bg px-3 py-2 text-sm text-muted">{{ notice }}</p>

    <form data-test="import-form" class="flex flex-col gap-3 rounded-lg border border-line bg-card p-4 md:flex-row" @submit.prevent="importRepository">
      <label class="min-w-0 flex-1 text-sm">
        <span class="mb-1 block text-muted">标准 GitHub 仓库 URL</span>
        <input v-model="importURL" data-test="import-url" required type="url" placeholder="https://github.com/owner/repo" class="w-full rounded-md border border-line bg-bg px-3 py-2" />
      </label>
      <button type="submit" :disabled="importing" class="self-end rounded-md bg-accent px-4 py-2 text-sm font-medium text-white disabled:opacity-60">
        {{ importing ? '读取 GitHub 中…' : '导入仓库' }}
      </button>
    </form>

    <form data-test="repository-search-form" class="flex gap-2" @submit.prevent="searchRepositories">
      <input v-model="query" data-test="repository-query" class="min-w-0 flex-1 rounded-md border border-line bg-card px-3 py-2 text-sm" placeholder="搜索仓库全名或描述" />
      <button type="submit" class="rounded-md border border-line px-4 py-2 text-sm hover:bg-bg">搜索</button>
    </form>

    <p v-if="loading" class="text-sm text-muted">加载中…</p>
    <div v-else class="overflow-x-auto rounded-lg border border-line">
      <table class="min-w-full divide-y divide-line text-sm">
        <thead class="bg-bg text-left text-xs uppercase tracking-wide text-muted">
          <tr><th class="px-3 py-2">仓库</th><th class="px-3 py-2">数据</th><th class="px-3 py-2">分类</th><th class="px-3 py-2">状态</th><th class="px-3 py-2 text-right">操作</th></tr>
        </thead>
        <tbody class="divide-y divide-line bg-card">
          <tr v-for="item in repositories" :key="item.id" :data-test="`repo-row-${item.id}`">
            <td class="px-3 py-3">
              <a :href="item.html_url" target="_blank" rel="noopener noreferrer" class="font-medium text-accent hover:underline">{{ item.full_name }}</a>
              <p v-if="item.description" class="mt-1 max-w-md text-xs text-muted">{{ item.description }}</p>
            </td>
            <td class="whitespace-nowrap px-3 py-3"><span>{{ item.language || '—' }}</span><span class="ml-3">★ {{ item.stars_count.toLocaleString() }}</span></td>
            <td class="px-3 py-3 text-muted">{{ categoryNames(item) }}</td>
            <td class="whitespace-nowrap px-3 py-3">
              <span :class="item.hidden ? 'text-amber-600' : 'text-emerald-600'">{{ item.hidden ? '已隐藏' : '公开' }}</span>
              <span class="ml-3 text-muted">{{ syncLabel(item.sync_status) }}</span>
            </td>
            <td class="px-3 py-3 text-right">
              <button type="button" :data-test="`manage-repo-${item.id}`" class="rounded-md border border-line px-2 py-1 text-xs hover:bg-bg" @click="openEditor(item)">管理</button>
            </td>
          </tr>
          <tr v-if="repositories.length === 0"><td colspan="5" class="px-3 py-8 text-center text-sm text-muted">没有符合条件的仓库。可使用上方 URL 从 GitHub 实时导入。</td></tr>
        </tbody>
      </table>
    </div>

    <footer v-if="total > pageSize" class="flex items-center justify-between text-sm text-muted">
      <span>共 {{ total }} 个仓库，第 {{ page }} / {{ pageCount }} 页</span>
      <div class="flex gap-2">
        <button type="button" data-test="previous-page" :disabled="page <= 1" class="rounded-md border border-line px-3 py-1 disabled:opacity-50" @click="goToPage(page - 1)">上一页</button>
        <button type="button" data-test="next-page" :disabled="page >= pageCount" class="rounded-md border border-line px-3 py-1 disabled:opacity-50" @click="goToPage(page + 1)">下一页</button>
      </div>
    </footer>

    <div v-if="editing" data-test="repository-edit-modal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" @click.self="closeEditor">
      <form data-test="repository-edit-form" class="w-full max-w-lg space-y-4 rounded-lg border border-line bg-card p-5 shadow-xl" @submit.prevent="saveRepository">
        <div class="flex items-start justify-between gap-4">
          <div><h2 class="font-semibold">管理 {{ editing.full_name }}</h2><p class="text-xs text-muted">公开可见性、分类和详情同步队列</p></div>
          <button type="button" aria-label="关闭" class="text-muted" @click="closeEditor">×</button>
        </div>
        <label class="flex items-center gap-2 text-sm"><input v-model="editForm.hidden" data-test="repo-hidden" type="checkbox" @change="onHiddenChange" />从公开页面隐藏</label>
        <fieldset class="space-y-2">
          <legend class="mb-2 text-sm font-medium">分类（第一项作为主分类）</legend>
          <label v-for="category in categories" :key="category.id" class="flex items-center gap-2 text-sm">
            <input v-model="editForm.category_ids" :data-test="`repo-category-${category.id}`" type="checkbox" :value="category.id" />{{ category.title }}<span v-if="!category.enabled" class="text-xs text-muted">（已停用）</span>
          </label>
          <p v-if="categories.length === 0" class="text-sm text-muted">尚无分类。</p>
        </fieldset>
        <label class="flex items-center gap-2 text-sm"><input v-model="editForm.resync" data-test="repo-resync" type="checkbox" :disabled="editForm.hidden" />立即重新排入详情同步队列</label>
        <p v-if="editForm.hidden" class="text-xs text-muted">隐藏仓库不会进入同步队列；恢复公开后可同时勾选重同步。</p>
        <div class="flex justify-end gap-2">
          <button type="button" class="rounded-md border border-line px-3 py-1.5 text-sm" @click="closeEditor">取消</button>
          <button type="submit" :disabled="saving" class="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white disabled:opacity-60">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </form>
    </div>
  </section>
</template>
