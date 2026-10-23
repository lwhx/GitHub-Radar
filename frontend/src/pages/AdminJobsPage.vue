<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { apiFetch, ApiError } from '../api/client'
import type { CollectorJob, JobRun, JobsResponse, JobRunsResponse } from '../api/types'
import { csrfTokenForRequest } from '../stores/auth'

const jobs = ref<CollectorJob[]>([])
const runs = ref<JobRun[]>([])
const loading = ref(true)
const running = ref(false)
const error = ref('')
const notice = ref('')

function messageFrom(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? cause.message : fallback
}

function runErrorMessage(cause: unknown): string {
  if (cause instanceof ApiError && cause.code === 'CREDENTIAL_REQUIRED') {
    return '请先在“凭据管理”添加并验证 GitHub Token，再启动采集'
  }
  return messageFrom(cause, '启动采集任务失败')
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [jobData, runData] = await Promise.all([
      apiFetch<JobsResponse>('/api/v1/admin/jobs'),
      apiFetch<JobRunsResponse>('/api/v1/admin/job-runs?limit=50'),
    ])
    jobs.value = jobData.jobs
    runs.value = runData.runs
  } catch (cause) {
    error.value = messageFrom(cause, '加载采集任务失败')
  } finally {
    loading.value = false
  }
}

async function runNow() {
  running.value = true
  error.value = ''
  notice.value = ''
  try {
    await apiFetch<{ status: string }>('/api/v1/admin/jobs/run', {
      method: 'POST',
      csrfToken: csrfTokenForRequest(),
    })
    notice.value = '采集任务已提交，可稍后刷新运行记录查看结果'
  } catch (cause) {
    error.value = runErrorMessage(cause)
  } finally {
    running.value = false
  }
}

function formatTime(value: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function intervalLabel(seconds: number): string {
  if (seconds % 3600 === 0) return `每 ${seconds / 3600} 小时`
  if (seconds % 60 === 0) return `每 ${seconds / 60} 分钟`
  return `每 ${seconds} 秒`
}

function statusLabel(status: string): string {
  return { running: '运行中', completed: '已完成', failed: '失败', deferred: '已延后' }[status] ?? status
}

function jobLabel(jobType: string): string {
  return { github_collection: 'GitHub 增量采集', github_recovery: '每日覆盖补采' }[jobType] ?? jobType
}

function statusClass(status: string): string {
  if (status === 'completed') return 'bg-success/15 text-success'
  if (status === 'failed') return 'bg-danger/15 text-danger'
  return 'bg-warning/15 text-warning'
}

function triggerLabel(trigger: string): string {
  return { manual: '手动', schedule: '定时', recovery: '恢复' }[trigger] ?? trigger
}

onMounted(load)
</script>

<template>
  <section class="flex flex-col gap-6">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 class="text-lg font-semibold">采集任务</h1>
        <p class="mt-1 text-sm text-muted">查看调度状态、手动触发采集并核对真实运行记录。</p>
      </div>
      <div class="flex gap-2">
        <button type="button" class="rounded-md border border-line px-3 py-1.5 text-sm hover:bg-bg" :disabled="loading" @click="load">刷新</button>
        <button data-test="run-now" type="button" class="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-fg disabled:opacity-60" :disabled="running" @click="runNow">
          {{ running ? '提交中…' : '立即采集' }}
        </button>
      </div>
    </div>

    <p v-if="error" role="alert" class="rounded-lg border border-danger/40 bg-card p-4 text-sm text-danger">{{ error }}</p>
    <p v-if="notice" role="status" class="rounded-lg border border-success/40 bg-card p-4 text-sm text-success">{{ notice }}</p>

    <div v-if="loading" class="h-36 animate-pulse rounded-lg border border-line bg-card" aria-busy="true" />
    <template v-else>
      <div class="grid gap-4 md:grid-cols-2">
        <article v-for="job in jobs" :key="job.id" class="rounded-lg border border-line bg-card p-4">
          <div class="flex items-center justify-between gap-3">
            <h2 class="font-medium">{{ jobLabel(job.job_type) }}</h2>
            <span class="rounded-full px-2 py-1 text-xs font-medium" :class="job.enabled ? 'bg-success/15 text-success' : 'bg-bg text-muted'">{{ job.enabled ? '已启用' : '已停用' }}</span>
          </div>
          <dl class="mt-4 grid grid-cols-2 gap-3 text-sm">
            <div><dt class="text-muted">周期</dt><dd class="mt-1">{{ intervalLabel(job.interval_seconds) }}</dd></div>
            <div><dt class="text-muted">下次运行</dt><dd class="mt-1">{{ formatTime(job.next_run_at) }}</dd></div>
            <div><dt class="text-muted">租约</dt><dd class="mt-1">{{ job.lease_owner ?? '空闲' }}</dd></div>
            <div><dt class="text-muted">租约到期</dt><dd class="mt-1">{{ formatTime(job.lease_expires_at) }}</dd></div>
          </dl>
        </article>
        <div v-if="jobs.length === 0" class="rounded-lg border border-dashed border-line p-6 text-sm text-muted">任务将在服务启动或首次手动采集时初始化。</div>
      </div>

      <div>
        <h2 class="mb-3 font-medium">最近运行</h2>
        <div v-if="runs.length === 0" class="rounded-lg border border-dashed border-line p-8 text-center text-sm text-muted">尚无采集运行记录。</div>
        <div v-else class="overflow-x-auto rounded-lg border border-line bg-card">
          <table class="w-full min-w-[900px] text-left text-sm">
            <thead class="border-b border-line text-muted">
              <tr>
                <th class="px-4 py-3 font-medium">运行</th>
                <th class="px-4 py-3 font-medium">状态</th>
                <th class="px-4 py-3 font-medium">触发</th>
                <th class="px-4 py-3 font-medium">开始</th>
                <th class="px-4 py-3 font-medium">请求</th>
                <th class="px-4 py-3 font-medium">结果</th>
                <th class="px-4 py-3 font-medium">错误</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-line">
              <tr v-for="run in runs" :key="run.id">
                <td class="px-4 py-3 font-medium">#{{ run.id }}</td>
                <td class="px-4 py-3"><span class="rounded-full px-2 py-1 text-xs font-medium" :class="statusClass(run.status)">{{ statusLabel(run.status) }}</span></td>
                <td class="px-4 py-3">{{ triggerLabel(run.trigger) }}</td>
                <td class="px-4 py-3 text-muted">{{ formatTime(run.started_at) }}</td>
                <td class="px-4 py-3">{{ run.request_count }}</td>
                <td class="px-4 py-3 text-xs leading-5">
                  <div>新增 {{ run.created_count }} · 更新 {{ run.updated_count }}</div>
                  <div>快照 {{ run.snapshot_count }} · 未变化 {{ run.not_modified_count }}</div>
                  <div v-if="run.rate_limited_count" class="text-warning">限流 {{ run.rate_limited_count }}</div>
                  <div v-if="run.failed_count" class="text-danger">失败 {{ run.failed_count }}</div>
                  <div v-if="run.coverage_incomplete_count" class="text-warning">覆盖不完整 {{ run.coverage_incomplete_count }}</div>
                </td>
                <td class="max-w-xs px-4 py-3 text-xs text-danger">{{ run.error_summary ?? '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </section>
</template>
