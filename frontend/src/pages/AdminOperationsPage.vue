<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'

import { apiFetch, ApiError } from '../api/client'
import type { AdminSettings, AuditLog, AuditLogsResponse, BackupRecord, BackupsResponse } from '../api/types'
import { csrfTokenForRequest } from '../stores/auth'

const settings = reactive<AdminSettings>({
  site_name: '', site_description: '', timezone: 'UTC', default_page_size: 20,
  ai_enabled: false, ai_base_url: '', ai_model: '', has_ai_api_key: false, trusted_proxies: [],
})
const backups = ref<BackupRecord[]>([])
const retention = reactive({ daily: 7, weekly: 4 })
const logs = ref<AuditLog[]>([])
const actionFilter = ref('')
const apiKeyMode = ref<'keep' | 'replace' | 'clear'>('keep')
const apiKey = ref('')
const trustedProxyText = ref('')
const loading = ref(true)
const saving = ref(false)
const creatingBackup = ref(false)
const verifyingName = ref('')
const error = ref('')
const notice = ref('')

const apiKeyHint = computed(() => settings.has_ai_api_key ? '已保存密钥（不会回传）' : '尚未保存密钥')

function messageFrom(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? cause.message : fallback
}

async function loadSettings() {
  const data = await apiFetch<AdminSettings>('/api/v1/admin/settings')
  Object.assign(settings, data)
  trustedProxyText.value = data.trusted_proxies.join('\n')
}

async function loadBackups() {
  const data = await apiFetch<BackupsResponse>('/api/v1/admin/backups')
  backups.value = data.items
  Object.assign(retention, data.retention)
}

async function loadAuditLogs() {
  const query = new URLSearchParams({ limit: '50' })
  const action = actionFilter.value.trim()
  if (action) query.set('action', action)
  const data = await apiFetch<AuditLogsResponse>(`/api/v1/admin/audit-logs?${query.toString()}`)
  logs.value = data.items
}

async function loadAll() {
  loading.value = true
  error.value = ''
  try {
    await Promise.all([loadSettings(), loadBackups(), loadAuditLogs()])
  } catch (cause) {
    error.value = messageFrom(cause, '加载运维数据失败')
  } finally {
    loading.value = false
  }
}

async function saveSettings() {
  if (saving.value) return
  saving.value = true
  error.value = ''
  notice.value = ''
  const body: Record<string, unknown> = {
    site_name: settings.site_name.trim(),
    site_description: settings.site_description.trim(),
    timezone: settings.timezone.trim(),
    default_page_size: Number(settings.default_page_size),
    ai_enabled: settings.ai_enabled,
    ai_base_url: settings.ai_base_url.trim(),
    ai_model: settings.ai_model.trim(),
    trusted_proxies: trustedProxyText.value.split(/[\n,]/).map((value) => value.trim()).filter(Boolean),
  }
  if (apiKeyMode.value === 'replace') body.ai_api_key = apiKey.value
  if (apiKeyMode.value === 'clear') body.ai_api_key = ''
  try {
    Object.assign(settings, await apiFetch<AdminSettings>('/api/v1/admin/settings', {
      method: 'PATCH', body, csrfToken: csrfTokenForRequest(),
    }))
    apiKey.value = ''
    apiKeyMode.value = 'keep'
    notice.value = '设置已保存'
    await loadAuditLogs()
  } catch (cause) {
    error.value = messageFrom(cause, '保存设置失败')
  } finally {
    saving.value = false
  }
}

async function createBackup() {
  if (creatingBackup.value) return
  creatingBackup.value = true
  error.value = ''
  notice.value = ''
  try {
    const item = await apiFetch<BackupRecord>('/api/v1/admin/backups', {
      method: 'POST', csrfToken: csrfTokenForRequest(),
    })
    backups.value = [item, ...backups.value.filter((backup) => backup.name !== item.name)]
    notice.value = `已创建并校验备份 ${item.name}`
    await loadAuditLogs()
  } catch (cause) {
    error.value = messageFrom(cause, '创建备份失败')
  } finally {
    creatingBackup.value = false
  }
}

async function verifyBackup(item: BackupRecord) {
  if (verifyingName.value) return
  verifyingName.value = item.name
  error.value = ''
  notice.value = ''
  try {
    const result = await apiFetch<{ name: string; verified: boolean }>(`/api/v1/admin/backups/${encodeURIComponent(item.name)}/verify`, {
      method: 'POST', csrfToken: csrfTokenForRequest(),
    })
    notice.value = result.verified ? `备份 ${item.name} 完整性正常` : `备份 ${item.name} 校验失败`
    await loadAuditLogs()
  } catch (cause) {
    error.value = messageFrom(cause, '备份校验失败')
  } finally {
    verifyingName.value = ''
  }
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`
  return `${(value / 1024 / 1024).toFixed(1)} MiB`
}

function formatTime(value: string): string {
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

onMounted(loadAll)
</script>

<template>
  <section class="space-y-8">
    <header class="space-y-1">
      <h1 class="text-xl font-semibold">系统运维</h1>
      <p class="text-sm text-muted">站点设置、SQLite 在线备份和管理员审计日志。所有写操作均受会话、Origin 与 CSRF 校验。</p>
    </header>

    <p v-if="error" class="rounded-md border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
    <p v-if="notice" class="rounded-md border border-line bg-bg px-3 py-2 text-sm text-muted">{{ notice }}</p>
    <p v-if="loading" class="text-sm text-muted">加载中…</p>

    <template v-else>
      <form class="space-y-4 rounded-lg border border-line bg-card p-4" data-test="settings-form" @submit.prevent="saveSettings">
        <div>
          <h2 class="font-semibold">站点设置</h2>
          <p class="mt-1 text-xs text-muted">AI 密钥加密保存，页面和 API 均不会回传原值。</p>
        </div>
        <div class="grid gap-3 md:grid-cols-2">
          <label class="flex flex-col gap-1 text-sm"><span class="text-muted">站点名称</span><input v-model="settings.site_name" data-test="site-name" class="rounded-md border border-line bg-bg px-2 py-1.5" /></label>
          <label class="flex flex-col gap-1 text-sm"><span class="text-muted">IANA 时区</span><input v-model="settings.timezone" class="rounded-md border border-line bg-bg px-2 py-1.5" placeholder="Asia/Shanghai" /></label>
          <label class="flex flex-col gap-1 text-sm md:col-span-2"><span class="text-muted">站点描述</span><textarea v-model="settings.site_description" rows="2" class="rounded-md border border-line bg-bg px-2 py-1.5" /></label>
          <label class="flex flex-col gap-1 text-sm"><span class="text-muted">默认每页数量（10–100）</span><input v-model.number="settings.default_page_size" type="number" min="10" max="100" class="rounded-md border border-line bg-bg px-2 py-1.5" /></label>
          <label class="flex items-center gap-2 self-end py-2 text-sm"><input v-model="settings.ai_enabled" type="checkbox" />启用 AI 配置</label>
          <label class="flex flex-col gap-1 text-sm"><span class="text-muted">AI API 地址</span><input v-model="settings.ai_base_url" class="rounded-md border border-line bg-bg px-2 py-1.5" placeholder="https://api.example.com/v1" /></label>
          <label class="flex flex-col gap-1 text-sm"><span class="text-muted">AI 模型</span><input v-model="settings.ai_model" class="rounded-md border border-line bg-bg px-2 py-1.5" /></label>
          <label class="flex flex-col gap-1 text-sm md:col-span-2"><span class="text-muted">可信代理（每行一个 IP 或 CIDR）</span><textarea v-model="trustedProxyText" data-test="trusted-proxies" rows="2" class="rounded-md border border-line bg-bg px-2 py-1.5" placeholder="127.0.0.1&#10;10.0.0.0/8" /><span class="text-xs text-muted">只有 TCP 对端命中此列表时才信任 X-Forwarded-For；留空时完全忽略代理头。</span></label>
        </div>
        <fieldset class="space-y-2 rounded-md border border-line p-3 text-sm">
          <legend class="px-1 text-muted">AI API Key · {{ apiKeyHint }}</legend>
          <div class="flex flex-wrap gap-4">
            <label><input v-model="apiKeyMode" type="radio" value="keep" /> 保持不变</label>
            <label><input v-model="apiKeyMode" type="radio" value="replace" /> 替换</label>
            <label><input v-model="apiKeyMode" type="radio" value="clear" /> 清除</label>
          </div>
          <input v-if="apiKeyMode === 'replace'" v-model="apiKey" data-test="ai-key" type="password" autocomplete="new-password" class="w-full rounded-md border border-line bg-bg px-2 py-1.5" placeholder="输入新密钥（仅本次发送）" />
        </fieldset>
        <button data-test="save-settings" type="submit" class="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white disabled:opacity-60" :disabled="saving">{{ saving ? '保存中…' : '保存设置' }}</button>
      </form>

      <section class="space-y-3 rounded-lg border border-line bg-card p-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div><h2 class="font-semibold">备份恢复</h2><p class="mt-1 text-xs text-muted">自动保留 {{ retention.daily }} 个日备和 {{ retention.weekly }} 个周备；手动备份不会自动删除。恢复前必须先校验，并在停服后替换数据库。</p></div>
          <button data-test="create-backup" type="button" class="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white disabled:opacity-60" :disabled="creatingBackup" @click="createBackup">{{ creatingBackup ? '创建中…' : '立即备份' }}</button>
        </div>
        <div class="overflow-x-auto rounded-md border border-line">
          <table class="min-w-full divide-y divide-line text-sm">
            <thead class="bg-bg text-left text-xs text-muted"><tr><th class="px-3 py-2">备份</th><th class="px-3 py-2">类型</th><th class="px-3 py-2">大小</th><th class="px-3 py-2">SHA-256</th><th class="px-3 py-2 text-right">操作</th></tr></thead>
            <tbody class="divide-y divide-line">
              <tr v-for="item in backups" :key="item.name" :data-test="`backup-${item.name}`">
                <td class="px-3 py-2"><div class="font-mono text-xs">{{ item.name }}</div><div class="text-xs text-muted">{{ formatTime(item.created_at) }}</div></td>
                <td class="px-3 py-2">{{ item.kind }}</td><td class="px-3 py-2">{{ formatBytes(item.size_bytes) }}</td>
                <td class="max-w-36 truncate px-3 py-2 font-mono text-xs" :title="item.sha256">{{ item.sha256 }}</td>
                <td class="whitespace-nowrap px-3 py-2 text-right"><button type="button" class="rounded-md border border-line px-2 py-1 text-xs" :disabled="Boolean(verifyingName)" @click="verifyBackup(item)">校验</button><a class="ml-2 rounded-md border border-line px-2 py-1 text-xs" :href="item.download_url">下载</a></td>
              </tr>
              <tr v-if="backups.length === 0"><td colspan="5" class="px-3 py-6 text-center text-muted">暂无备份；服务启动后会自动补齐日备和周备。</td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="space-y-3 rounded-lg border border-line bg-card p-4">
        <div class="flex flex-wrap items-end justify-between gap-3"><div><h2 class="font-semibold">审计日志</h2><p class="mt-1 text-xs text-muted">最近 50 条管理操作，支持 action 精确过滤。</p></div><form class="flex gap-2" @submit.prevent="loadAuditLogs"><input v-model="actionFilter" data-test="audit-filter" class="rounded-md border border-line bg-bg px-2 py-1.5 text-sm" placeholder="admin.backup.create" /><button type="submit" class="rounded-md border border-line px-3 py-1.5 text-sm">筛选</button></form></div>
        <div class="overflow-x-auto rounded-md border border-line"><table class="min-w-full divide-y divide-line text-sm"><thead class="bg-bg text-left text-xs text-muted"><tr><th class="px-3 py-2">时间</th><th class="px-3 py-2">动作</th><th class="px-3 py-2">目标</th><th class="px-3 py-2">结果</th><th class="px-3 py-2">Request ID</th></tr></thead><tbody class="divide-y divide-line"><tr v-for="item in logs" :key="item.id"><td class="whitespace-nowrap px-3 py-2 text-xs">{{ formatTime(item.created_at) }}</td><td class="px-3 py-2 font-mono text-xs">{{ item.action }}</td><td class="px-3 py-2">{{ item.target }}</td><td class="px-3 py-2" :class="item.result === 'success' ? 'text-emerald-600' : 'text-red-600'">{{ item.result }}</td><td class="px-3 py-2 font-mono text-xs text-muted">{{ item.request_id ?? '—' }}</td></tr><tr v-if="logs.length === 0"><td colspan="5" class="px-3 py-6 text-center text-muted">暂无匹配日志。</td></tr></tbody></table></div>
      </section>
    </template>
  </section>
</template>
