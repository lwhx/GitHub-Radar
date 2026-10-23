<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { ApiError } from '../api/client'
import { login } from '../stores/auth'

const route = useRoute()
const router = useRouter()

const username = ref('')
const password = ref('')
const submitting = ref(false)
const error = ref('')

async function onSubmit() {
  error.value = ''
  submitting.value = true
  try {
    await login(username.value.trim(), password.value)
    const redirect = route.query.redirect
    await router.push(typeof redirect === 'string' ? redirect : '/admin')
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '登录失败，请稍后重试'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="flex justify-center py-8">
    <form class="w-full max-w-sm rounded-lg border border-line bg-card p-6" @submit.prevent="onSubmit">
      <h1 class="text-lg font-semibold">管理员登录</h1>
      <p class="mt-1 text-sm text-muted">登录后可查看采集健康与系统概览。</p>

      <label class="mt-5 block text-sm" for="admin-username">
        <span class="mb-1 block text-muted">用户名</span>
        <input
          id="admin-username"
          v-model="username"
          type="text"
          required
          autocomplete="username"
          class="w-full rounded-md border border-line bg-bg px-3 py-2"
        />
      </label>

      <label class="mt-4 block text-sm" for="admin-password">
        <span class="mb-1 block text-muted">密码</span>
        <input
          id="admin-password"
          v-model="password"
          type="password"
          required
          autocomplete="current-password"
          class="w-full rounded-md border border-line bg-bg px-3 py-2"
        />
      </label>

      <p v-if="error" role="alert" class="mt-4 rounded-md border border-danger/40 px-3 py-2 text-sm text-danger">
        {{ error }}
      </p>

      <button
        type="submit"
        class="mt-6 w-full rounded-md bg-accent px-3 py-2 text-sm font-medium text-accent-fg hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
        :disabled="submitting"
      >
        {{ submitting ? '登录中…' : '登录' }}
      </button>

      <p class="mt-4 text-xs text-muted">
        连续失败 5 次后登录接口将限速 5 分钟。管理员账户通过部署服务器的
        <code class="rounded bg-bg px-1">radar admin create</code> 命令创建，无默认账户。
      </p>
    </form>
  </div>
</template>
