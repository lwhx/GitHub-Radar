<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'

import { apiFetch, ApiError } from '../api/client'
import type { CredentialsResponse, GitHubCredential, RateLimitSnapshot } from '../api/types'
import { csrfTokenForRequest } from '../stores/auth'

const credentials = ref<GitHubCredential[]>([])
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const form = reactive({ label: '', token: '', priority: 0 })

function messageFrom(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? cause.message : fallback
}

async function loadCredentials() {
  loading.value = true
  error.value = ''
  try {
    const data = await apiFetch<CredentialsResponse>('/api/v1/admin/credentials')
    credentials.value = data.credentials
  } catch (cause) {
    error.value = messageFrom(cause, '加载 GitHub 凭据失败')
  } finally {
    loading.value = false
  }
}

async function createCredential() {
  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    const created = await apiFetch<GitHubCredential>('/api/v1/admin/credentials', {
      method: 'POST',
      body: { label: form.label.trim(), token: form.token, priority: Number(form.priority) },
      csrfToken: csrfTokenForRequest(),
    })
    credentials.value = [{ ...created, rate_limits: [] }, ...credentials.value]
    form.label = ''
    form.token = ''
    form.priority = 0
    notice.value = `凭据“${created.label}”已验证并保存`
  } catch (cause) {
    error.value = messageFrom(cause, '添加 GitHub 凭据失败')
  } finally {
    saving.value = false
  }
}

async function toggleCredential(credential: GitHubCredential) {
  error.value = ''
  notice.value = ''
  const enabled = !credential.enabled
  try {
    await apiFetch<{ id: number; enabled: boolean }>(`/api/v1/admin/credentials/${credential.id}`, {
      method: 'PATCH',
      body: { enabled },
      csrfToken: csrfTokenForRequest(),
    })
    credential.enabled = enabled
    notice.value = `凭据“${credential.label}”已${enabled ? '启用' : '停用'}`
  } catch (cause) {
    error.value = messageFrom(cause, '更新 GitHub 凭据失败')
  }
}

function rateLimit(credential: GitHubCredential, resource: string): RateLimitSnapshot | undefined {
  return credential.rate_limits?.find((item) => item.resource === resource)
}

async function testCredential(credential: GitHubCredential) {
  error.value = ''
  notice.value = ''
  try {
    const result = await apiFetch<{ credential: GitHubCredential }>(`/api/v1/admin/credentials/${credential.id}/test`, {
      method: 'POST',
      csrfToken: csrfTokenForRequest(),
    })
    Object.assign(credential, result.credential)
    notice.value = `凭据“${credential.label}”验证通过：@${result.credential.auth_login}`
  } catch (cause) {
    error.value = messageFrom(cause, '测试 GitHub 凭据失败')
  }
}

async function removeCredential(credential: GitHubCredential) {
  const confirmed = window.confirm(
    `确定删除凭据“${credential.label}”？删除后无法恢复，需要重新添加并验证 Token。`,
  )
  if (!confirmed) return
  error.value = ''
  notice.value = ''
  try {
    await apiFetch<{ id: number; deleted: boolean }>(`/api/v1/admin/credentials/${credential.id}`, {
      method: 'DELETE',
      csrfToken: csrfTokenForRequest(),
    })
    credentials.value = credentials.value.filter((item) => item.id !== credential.id)
    notice.value = `凭据“${credential.label}”已删除`
  } catch (cause) {
    error.value = messageFrom(cause, '删除 GitHub 凭据失败')
  }
}

function formatTime(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

onMounted(loadCredentials)
</script>

<template>
  <section class="flex flex-col gap-6">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 class="text-lg font-semibold">GitHub 凭据</h1>
        <p class="mt-1 text-sm text-muted">Token 只在提交时发送，验证后加密保存，页面仅显示末四位。</p>
      </div>
      <button type="button" class="rounded-md border border-line px-3 py-1.5 text-sm hover:bg-bg" :disabled="loading" @click="loadCredentials">
        刷新
      </button>
    </div>

    <form class="grid gap-4 rounded-lg border border-line bg-card p-4 md:grid-cols-[1fr_1.6fr_8rem_auto] md:items-end" @submit.prevent="createCredential">
      <label class="grid gap-1.5 text-sm" for="credential-label">
        <span>名称</span>
        <input id="credential-label" v-model="form.label" class="rounded-md border border-line bg-bg px-3 py-2" maxlength="80" required autocomplete="off" placeholder="例如：主账号" />
      </label>
      <label class="grid gap-1.5 text-sm" for="credential-token">
        <span>GitHub Token</span>
        <input id="credential-token" v-model="form.token" class="rounded-md border border-line bg-bg px-3 py-2" type="password" minlength="8" maxlength="1024" required autocomplete="new-password" placeholder="github_pat_…" />
      </label>
      <label class="grid gap-1.5 text-sm" for="credential-priority">
        <span>优先级</span>
        <input id="credential-priority" v-model.number="form.priority" class="rounded-md border border-line bg-bg px-3 py-2" type="number" />
      </label>
      <button type="submit" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-accent-fg disabled:opacity-60" :disabled="saving">
        {{ saving ? '验证中…' : '验证并添加' }}
      </button>
    </form>

    <p v-if="error" role="alert" class="rounded-lg border border-danger/40 bg-card p-4 text-sm text-danger">{{ error }}</p>
    <p v-if="notice" role="status" class="rounded-lg border border-success/40 bg-card p-4 text-sm text-success">{{ notice }}</p>

    <div v-if="loading" class="h-32 animate-pulse rounded-lg border border-line bg-card" aria-busy="true" />
    <div v-else-if="credentials.length === 0" class="rounded-lg border border-dashed border-line p-8 text-center text-sm text-muted">
      尚无 GitHub 凭据。添加并验证 Token 后才能执行采集。
    </div>
    <div v-else class="overflow-x-auto rounded-lg border border-line bg-card">
      <table class="w-full min-w-[780px] text-left text-sm">
        <thead class="border-b border-line text-muted">
          <tr>
            <th class="px-4 py-3 font-medium">凭据</th>
            <th class="px-4 py-3 font-medium">状态</th>
            <th class="px-4 py-3 font-medium">Search 配额</th>
            <th class="px-4 py-3 font-medium">Core 配额</th>
            <th class="px-4 py-3 font-medium">优先级</th>
            <th class="px-4 py-3 font-medium">最后验证</th>
            <th class="px-4 py-3 font-medium">操作</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-line">
          <tr v-for="credential in credentials" :key="credential.id">
            <td class="px-4 py-3">
              <div class="font-medium">{{ credential.label }}</div>
              <div class="mt-0.5 text-xs text-muted">@{{ credential.auth_login }} · {{ credential.token_hint }}</div>
            </td>
            <td class="px-4 py-3">
              <span class="rounded-full px-2 py-1 text-xs font-medium" :class="credential.enabled ? 'bg-success/15 text-success' : 'bg-bg text-muted'">
                {{ credential.enabled ? '已启用' : '已停用' }}
              </span>
            </td>
            <td class="px-4 py-3">
              <span v-if="rateLimit(credential, 'search')">{{ rateLimit(credential, 'search')?.remaining }} / {{ rateLimit(credential, 'search')?.limit }}</span>
              <span v-else class="text-muted">—</span>
            </td>
            <td class="px-4 py-3">
              <span v-if="rateLimit(credential, 'core')">{{ rateLimit(credential, 'core')?.remaining }} / {{ rateLimit(credential, 'core')?.limit }}</span>
              <span v-else class="text-muted">—</span>
            </td>
            <td class="px-4 py-3">{{ credential.priority }}</td>
            <td class="px-4 py-3 text-muted">{{ formatTime(credential.last_checked_at) }}</td>
            <td class="px-4 py-3">
              <div class="flex flex-wrap gap-2">
                <button
                  type="button"
                  class="rounded-md border border-line px-2.5 py-1 text-xs hover:bg-bg"
                  :data-test="`toggle-credential-${credential.id}`"
                  @click="toggleCredential(credential)"
                >
                  {{ credential.enabled ? '停用' : '启用' }}
                </button>
                <button
                  type="button"
                  class="rounded-md border border-line px-2.5 py-1 text-xs hover:bg-bg"
                  :data-test="`test-credential-${credential.id}`"
                  @click="testCredential(credential)"
                >
                  测试
                </button>
                <button
                  type="button"
                  class="rounded-md border border-danger/40 px-2.5 py-1 text-xs text-danger hover:bg-bg"
                  :data-test="`delete-credential-${credential.id}`"
                  @click="removeCredential(credential)"
                >
                  删除
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
