export interface PublicRepo {
  full_name: string
  owner: string
  name: string
  html_url: string
  homepage_url: string | null
  description: string | null
  language: string | null
  license_spdx: string | null
  topics: string[]
  stars_count: number
  forks_count: number
  open_issues_count: number
  is_fork: boolean
  is_archived: boolean
  github_created_at: string
  github_updated_at: string
  pushed_at: string | null
  first_seen_at: string
  last_synced_at: string | null
}

export interface ReposListMeta {
  total: number
  page: number
  limit: number
  data_updated_at: string | null
}

export interface ReposResponse {
  items: PublicRepo[]
  meta: ReposListMeta
}

export interface PublicStats {
  repositories: number
  star_snapshots: number
  categories: number
  latest_sync_at: string | null
  data_updated_at: string | null
}

export interface PublicCategory {
  slug: string
  title: string
  description: string | null
  repo_count: number
}

export interface CategoriesResponse {
  items: PublicCategory[]
}

export interface RankedRepo extends PublicRepo {
  star_delta: number | null
  score: number | null
  score_reason: string | null
}

export interface RankingsMeta {
  type: string
  window_hours: number
  page: number
  limit: number
  total: number
  data_updated_at: string | null
  history_available: boolean
  note: string
}

export interface RankingsResponse {
  items: RankedRepo[]
  meta: RankingsMeta
}

export interface AdminOverview {
  repository_count: number
  snapshot_count: number
  category_count: number
  admin_count: number
  active_session_count: number
  database_ready: boolean
}

export interface AdminUser {
  username: string
}

export interface RateLimitSnapshot {
  resource: 'core' | 'search' | string
  limit: number
  used: number
  remaining: number
  reset_at: string
  cooldown_until: string | null
}

export interface GitHubCredential {
  id: number
  label: string
  auth_identity: string
  auth_login: string
  token_hint: string
  enabled: boolean
  status: string
  priority: number
  last_checked_at: string | null
  created_at: string
  updated_at: string
  rate_limits?: RateLimitSnapshot[]
}

export interface CredentialsResponse {
  credentials: GitHubCredential[]
}

export interface AdminCategory {
  id: number
  slug: string
  title: string
  description: string | null
  sort_order: number
  enabled: boolean
  repo_count: number
}

export interface AdminCategoriesResponse {
  items: AdminCategory[]
}

export interface CollectorJob {
  id: number
  job_type: string
  enabled: boolean
  interval_seconds: number
  next_run_at: string | null
  lease_owner: string | null
  lease_expires_at: string | null
}

export interface JobRun {
  id: number
  job_id: number
  status: string
  trigger: string
  started_at: string
  finished_at: string | null
  request_count: number
  created_count: number
  updated_count: number
  snapshot_count: number
  not_modified_count: number
  retry_count: number
  rate_limited_count: number
  failed_count: number
  coverage_incomplete_count: number
  last_error_code: string | null
  error_summary: string | null
}

export interface JobsResponse {
  jobs: CollectorJob[]
}

export interface JobRunsResponse {
  runs: JobRun[]
}

export interface AdminSettings {
  site_name: string
  site_description: string
  timezone: string
  default_page_size: number
  ai_enabled: boolean
  ai_base_url: string
  ai_model: string
  has_ai_api_key: boolean
  trusted_proxies: string[]
}

export interface BackupRecord {
  name: string
  kind: 'manual' | 'daily' | 'weekly'
  created_at: string
  size_bytes: number
  sha256: string
  verified: boolean
  download_url: string
}

export interface BackupsResponse {
  items: BackupRecord[]
  retention: { daily: number; weekly: number }
}

export interface AuditLog {
  id: number
  admin_id: number | null
  action: string
  target: string
  result: string
  created_at: string
  request_id: string | null
}

export interface AuditLogsResponse {
  items: AuditLog[]
}
