# HTTP API 参考

Base URL：`/api/v1`。除健康检查外所有接口返回 JSON。

## 响应约定

成功：

```json
{ "data": { /* 载荷 */ } }
```

失败：

```json
{ "error": { "code": "INVALID_CREDENTIALS", "message": "Invalid username or password" }, "request_id": "…" }
```

- 时间字段为 ISO 8601 UTC（`Z` 结尾），例如 `2026-10-23T03:42:54Z`。
- 管理接口在登录后通过 `radar_admin_session`（HttpOnly Cookie）鉴权；状态变更接口另需 `X-CSRF-Token` 请求头（值等于登录响应中的 `csrf_token`，同时写入 `radar_csrf` Cookie）。
- 登录与注销要求 `Origin` 头与 `PUBLIC_BASE_URL`（或请求 Host）一致。

## 公共接口

### `GET /healthz`

进程存活探针。无需数据库。

```bash
curl -i http://127.0.0.1:8080/healthz
# HTTP/1.1 200 OK
# {"data":{"status":"ok"}}
```

### `GET /readyz`

数据库读写就绪探针（执行一次可回滚的写入事务）。未就绪返回 `503 NOT_READY`。

### `GET /api/v1/stats`

首页统计。

```json
{
  "data": {
    "repositories": 0,
    "star_snapshots": 0,
    "categories": 0,
    "latest_sync_at": null,
    "data_updated_at": null
  }
}
```

`latest_sync_at` / `data_updated_at` 为 null 表示尚无采集数据，前端应显示空态而不是 0。

### `GET /api/v1/repos`

公开仓库列表：本地搜索、固定枚举排序、分页。隐藏仓库（`is_hidden`）永不出现在结果中。

| 参数 | 说明 |
| --- | --- |
| `q` | 可选；对 `full_name` 与 `description` 做子串匹配（`%`/`_`/`\` 自动转义） |
| `sort` | 默认 `stars_desc`；可选 `stars_desc`、`stars_asc`、`created_desc`、`updated_desc` |
| `page` | 默认 `1`；非正整数返回 `400 INVALID_PAGE` |
| `limit` | 默认 `20`，上限 `100`；越界返回 `400 INVALID_LIMIT` |

```bash
curl -i 'http://127.0.0.1:8080/api/v1/repos?q=agent&sort=stars_desc&page=1&limit=20'
```

```json
{
  "data": {
    "items": [
      {
        "full_name": "acme/agent-framework",
        "owner": "acme",
        "name": "agent-framework",
        "html_url": "https://github.com/acme/agent-framework",
        "homepage_url": null,
        "description": "Build AI agents",
        "language": "Go",
        "license_spdx": "MIT",
        "topics": [],
        "stars_count": 120,
        "forks_count": 8,
        "open_issues_count": 3,
        "is_fork": false,
        "is_archived": false,
        "github_created_at": "2026-10-01T00:00:00Z",
        "github_updated_at": "2026-10-20T00:00:00Z",
        "pushed_at": null,
        "first_seen_at": "2026-10-02T00:00:00Z",
        "last_synced_at": null
      }
    ],
    "meta": {
      "total": 1,
      "page": 1,
      "limit": 20,
      "data_updated_at": null
    }
  }
}
```

`meta.data_updated_at` 为全部可见仓库的最近同步时间，null 表示尚无同步。

### `GET /api/v1/repos/{owner}/{repo}`

仓库详情，字段同列表项。owner/name 不区分大小写（与 GitHub 一致）；未知仓库、隐藏仓库均返回 `404 REPO_NOT_FOUND`。

```bash
curl -i http://127.0.0.1:8080/api/v1/repos/acme/agent-framework
```

### `GET /api/v1/repos/{owner}/{repo}/stars?window=30d`

真实 Star 时间序列。`window` 只接受 `7d`、`30d`、`90d`，缺省 `30d`，其他值返回 `400 INVALID_WINDOW`：

```json
{
  "data": {
    "repo": "acme/agent-framework",
    "window": "30d",
    "points": [{ "captured_at": "2026-10-23T06:00:00Z", "star_count": 120 }],
    "sample_count": 1,
    "history_available": false,
    "method": "每次仓库详情同步写入一条真实 Star 快照（约 6 小时一轮）；此处不做采样、插值和估算，样本少于 2 个时 history_available=false",
    "data_updated_at": "2026-10-23T06:00:00Z"
  }
}
```

- 数据只来自 `star_snapshots` 的真实采样，**不做插值、不估算、不补点**；
- 窗口内样本少于 2 个时 `history_available=false`、`data_updated_at` 可能为 `null`，前端必须显示「数据不足」而不是 0；
- 隐藏仓库与未知仓库一致返回 `404 REPO_NOT_FOUND`。

### `GET /api/v1/repos/{owner}/{repo}/readme`

按需抓取并缓存 README（24 小时 TTL），返回**已净化的 Markdown 原文**，由前端安全渲染：

```json
{
  "data": {
    "repo": "acme/agent-framework",
    "status": "ok",
    "format": "markdown",
    "content": "# Agent Framework\n……",
    "truncated": false,
    "cached": true,
    "stale": false,
    "source": "github",
    "fetched_at": "2026-10-24T10:00:00Z"
  }
}
```

- `status`：`ok` 表示有 README，`missing` 表示 GitHub 返回 404（同时写入 24 小时负缓存，避免反复请求），此时 `content` 为空并带 `note`；
- 净化规则：删除 `<script>/<style>/<iframe>/<object>/<embed>` 块与事件属性，屏蔽 `javascript:`/`data:`/`vbscript:` 链接，长度截断到 60000 字符（`truncated=true`）；
- 上游不可用且本地有旧副本时返回旧副本（`stale=true` 并带 `note`）；无副本时返回 `502 README_UNAVAILABLE`；
- 未配置 README 服务时返回 `503 SERVICE_UNAVAILABLE`；隐藏仓库返回 `404 REPO_NOT_FOUND`。

### `GET /api/v1/categories`

启用中的分类列表（按 `sort_order` 排序），`repo_count` 只统计可见（未隐藏）仓库。

```json
{
  "data": {
    "items": [
      { "slug": "ai", "title": "AI 与大模型", "description": "大模型相关项目", "repo_count": 12 }
    ]
  }
}
```

### `GET /api/v1/categories/{slug}`

单个分类详情；未知或停用的分类返回 `404 CATEGORY_NOT_FOUND`。

> 列表接口支持 `category=<slug>` 参数：按分类过滤仓库；未知或停用分类返回 `400 INVALID_CATEGORY`。

### `GET /api/v1/rankings?type=latest&page=1&limit=20`

榜单数据一律基于已采集的真实数据，`type` 取 `latest` / `24h` / `7d` / `potential`（缺省 `latest`，未知值 `400 INVALID_TYPE`）。可选筛选：`category=<slug>`、`language=<language>`。

| type | 排序口径 | `window_hours` |
| --- | --- | --- |
| `latest` | GitHub `created_at` 降序，同时间 `github_id` 升序 | `0` |
| `24h` | 滚动 24 小时净 Star 变化降序 | `24` |
| `7d` | 滚动 168 小时净 Star 变化降序 | `168` |
| `potential` | 30 天内创建、总 Star ≤ 3000、近 7 天净增 > 0；`score = 净增 × (1 + 内容完整度加分)` 降序 | `168` |

```json
{
  "data": {
    "items": [
      {
        "full_name": "acme/rocket",
        "stars_count": 180,
        "star_delta": 80,
        "score": null,
        "score_reason": null
      }
    ],
    "meta": {
      "type": "24h",
      "window_hours": 24,
      "page": 1,
      "limit": 20,
      "total": 1,
      "data_updated_at": "2026-10-25T10:00:00Z",
      "history_available": true,
      "note": "按滚动 24 小时净 Star 变化降序，样本来自已采集的 Star 快照"
    }
  }
}
```

口径要点：

- 净变化 = 最新快照 − 基线快照；基线必须落在「窗口截止时间前 6 小时内」（一个详情同步周期），**带内无基线的仓库不进入榜单**。不伪造 0 增长，也不用 5 天前的快照冒充 24h 增量。
- `history_available=false` 表示增长窗口无历史样本，前端应展示“数据不足”而不是 0（`note` 携带说明）。
- 同分排序统一为 `stars_count DESC, github_id ASC`，翻页顺序稳定。
- `potential` 的内容完整度加分：描述 +5%、语言 +3%、话题 +2%，`score_reason` 给出可读解释。

## 认证接口

### `POST /api/v1/admin/auth/login`

```bash
curl -i -X POST http://127.0.0.1:8080/api/v1/admin/auth/login \
  -H 'Content-Type: application/json' \
  -H 'Origin: http://127.0.0.1:8080' \
  -d '{"username":"ops","password":"…"}'
```

成功 `200`，设置两个 Cookie 并返回：

```json
{
  "data": {
    "admin": { "username": "ops" },
    "csrf_token": "…",
    "expires_at": "2026-10-24T03:42:54Z"
  }
}
```

失败：`401 INVALID_CREDENTIALS`（同时按对端 IP 累计失败次数，5 次失败锁定 5 分钟，`429 RATE_LIMITED`）、
`403 INVALID_ORIGIN`、`415 UNSUPPORTED_MEDIA_TYPE`、`413 REQUEST_TOO_LARGE`、`400 INVALID_REQUEST`。

### `GET /api/v1/admin/auth/session`

查询当前会话。未登录返回 `401 UNAUTHENTICATED`。

```json
{ "data": { "admin": { "username": "ops" } } }
```

### `POST /api/v1/admin/auth/logout`

注销当前会话（服务端置 `revoked_at` 并清除 Cookie）。需要会话 Cookie + `X-CSRF-Token` 头 + 合法 `Origin`。
成功返回 `204 No Content`。

## 管理接口

### `GET /api/v1/admin/overview`

数据概览（登录 required）。

```json
{
  "data": {
    "repository_count": 0,
    "snapshot_count": 0,
    "category_count": 0,
    "admin_count": 1,
    "active_session_count": 1,
    "database_ready": true
  }
}
```

未登录返回 `401 UNAUTHENTICATED`；响应头带 `Cache-Control: no-store`。

### `GET /api/v1/admin/credentials`

返回已验证的 GitHub 凭据及 core/search 配额。Token 为只写字段，响应只含 `token_hint`，不会返回明文或密文。

```json
{
  "data": {
    "credentials": [{
      "id": 1,
      "label": "主账号",
      "auth_identity": "583231",
      "auth_login": "octocat",
      "token_hint": "…1234",
      "enabled": true,
      "status": "active",
      "priority": 100,
      "last_checked_at": "2026-10-23T03:42:54Z",
      "created_at": "2026-10-23T03:42:54Z",
      "updated_at": "2026-10-23T03:42:54Z",
      "rate_limits": [{
        "resource": "search",
        "limit": 30,
        "used": 2,
        "remaining": 28,
        "reset_at": "2026-10-23T04:00:00Z",
        "cooldown_until": null
      }]
    }]
  }
}
```

### `POST /api/v1/admin/credentials`

需要合法 Origin、会话和 CSRF。服务端先调用 GitHub `/user` 与 `/rate_limit` 验证身份和配额，再以 AES-256-GCM 加密保存 Token。

```json
{ "label": "主账号", "token": "<github-token>", "priority": 100 }
```

成功返回 `201` 和不含 Token 的凭据摘要。输入不合法返回 `400 INVALID_CREDENTIAL`；GitHub 拒绝或不可达返回 `502 GITHUB_VERIFICATION_FAILED`。

### `PATCH /api/v1/admin/credentials/{id}`

启用或停用凭据，需要合法 Origin、会话和 CSRF：

```json
{ "enabled": false }
```

成功返回 `{ "data": { "id": 1, "enabled": false } }`；不存在返回 `404 NOT_FOUND`。

### `POST /api/v1/admin/credentials/{id}/test`

用已保存的 Token 重新向 GitHub 验证身份与配额（不接收请求体），成功后刷新 `auth_identity`、`auth_login`、`last_checked_at` 与 core/search 配额：

```json
{
  "data": {
    "credential": { "id": 1, "label": "主账号", "auth_login": "octocat", "status": "active" }
  }
}
```

- 不存在返回 `404 NOT_FOUND`；
- Token 已被撤销或 GitHub 无法验证返回 `502 GITHUB_VERIFICATION_FAILED`；
- 需要合法 Origin、会话和 CSRF，操作写入审计日志 `admin.credential.test`。

### `DELETE /api/v1/admin/credentials/{id}`

永久删除凭据，关联配额记录随外键级联删除：

```json
{ "data": { "id": 1, "deleted": true } }
```

不存在返回 `404 NOT_FOUND`；需要合法 Origin、会话和 CSRF，审计日志记录 `admin.credential.delete`。后台界面在删除前必须二次确认。

### `GET /api/v1/admin/categories`

返回全部分类（**包含已停用**）及可见仓库数量，需要管理员会话：

```json
{ "data": { "items": [ { "id": 1, "slug": "ai", "title": "AI 与大模型", "sort_order": 1, "enabled": true, "repo_count": 12 } ] } }
```

公开接口 `GET /api/v1/categories` 只返回启用分类；本接口是运维视图。

### `POST /api/v1/admin/categories`

新建分类，需要合法 Origin、会话和 CSRF：

```json
{ "slug": "ai-agents", "title": "AI Agent", "description": "智能体生态", "sort_order": 5, "enabled": true }
```

- `slug` 为 2–64 位小写字母、数字或连字符，不可首尾连字符，创建后**不可修改**（公开页面以 slug 为路径）；
- `title` 1–80 字、`description` ≤ 500 字，非法返回 `400 INVALID_CATEGORY`；
- slug 已存在返回 `409 CONFLICT`；成功返回 `201` 及完整分类；审计 `admin.category.create`。

### `PATCH /api/v1/admin/categories/{id}`

局部更新 `title` / `description` / `sort_order` / `enabled`（至少一个字段），成功返回更新后的分类；空载荷 `400 INVALID_REQUEST`，不存在 `404 NOT_FOUND`；审计 `admin.category.update`。请求体不接受 `slug`（未知字段直接 `400`）。

### `DELETE /api/v1/admin/categories/{id}`

永久删除分类，`repo_categories` 与 `category_rules` 随外键级联删除（仓库本身不受影响，只是不再归属该分类）。不存在 `404 NOT_FOUND`；需要二次确认后调用；审计 `admin.category.delete`。

### `GET /api/v1/admin/jobs`

返回采集任务的周期、下次运行时间与当前租约。默认包含：

- `github_collection`：每 30 分钟执行增量发现，并在 Search 后同步一批到期仓库详情；
- `github_recovery`：每 24 小时独立补采最近 48 小时的公开仓库创建窗口。

两个任务使用不同的窗口 `source/query_hash`，恢复补采不会推进增量游标。每次占用任务都会获得唯一租约 token，运行中每 3 分 20 秒更新 `heartbeat_at` 并把 10 分钟租约向后延长；如果续租时 token 已失效，旧执行器会停止并记录 `failed/lease_lost`，不会释放或改写新执行器的租约与排期。

### `POST /api/v1/admin/jobs/run`

手动提交一次采集，需要合法 Origin、会话和 CSRF。成功返回 `202`：

```json
{ "data": { "status": "accepted" } }
```

采集在后台执行，结果通过运行记录查询；`202` 只表示任务已接收，不表示采集成功。该接口只触发 `github_collection`，每日 `github_recovery` 由调度器自动执行。

尚未添加、或全部凭据已停用/处于冷却期时返回 `409 CREDENTIAL_REQUIRED`，请求不会创建空运行记录。定时采集同样会跳过并按各自周期顺延，而不是持续写入失败运行。

### `PATCH /api/v1/admin/jobs/{id}`

调整采集任务的启用状态与周期，需要合法 Origin、会话和 CSRF，至少提供一个字段：

```json
{ "enabled": true, "interval_seconds": 1800 }
```

- `interval_seconds` 最小 60，否则返回 `400 INVALID_INTERVAL`；两者都缺失返回 `400 INVALID_REQUEST`；
- 任务不存在返回 `404 NOT_FOUND`；
- 重新启用时会把 `next_run_at` 重排为「当前时间 + 周期」，避免停用期间积压的旧时间点触发立即补跑；租约字段不受影响；
- 审计日志记录 `admin.job.update`。

### `GET /api/v1/admin/settings`

返回脱敏后的站点设置快照（需要管理员会话）：

```json
{
  "data": {
    "site_name": "GitHub Radar",
    "site_description": "",
    "timezone": "UTC",
    "default_page_size": 20,
    "ai_enabled": false,
    "ai_base_url": "",
    "ai_model": "",
    "has_ai_api_key": false,
    "trusted_proxies": []
  }
}
```

`ai_api_key` **永不回传**，只暴露 `has_ai_api_key`；存储使用 `APP_ENCRYPTION_KEY` 加密（AI 配置当前仅做存储，P2 接入时启用）。

### `PATCH /api/v1/admin/settings`

局部更新（至少一个字段，需要合法 Origin、会话和 CSRF）：

```json
{ "site_name": "雷达站", "timezone": "Asia/Shanghai", "default_page_size": 50, "ai_enabled": true, "ai_api_key": "sk-..." }
```

- 校验：时区必须是 IANA 名称、`default_page_size` 10–100、`site_name` 1–80 字、`site_description` ≤ 500 字、`ai_base_url` 必须是 http(s) 绝对地址；`trusted_proxies` 最多 32 个 IP/CIDR（自动规范化与去重）；非法返回 `400 INVALID_SETTINGS`；
- `ai_api_key` 语义：**省略 = 保持不变**，空字符串 = 清除；非法返回 `400 INVALID_SETTINGS`；
- 成功返回更新后的脱敏快照；审计 `admin.settings.update`（成功/失败都记）。

### `GET /api/v1/admin/audit-logs?limit=50&action=`

操作日志（需要管理员会话），按时间倒序：

```json
{ "data": { "items": [ { "id": 9, "admin_id": 1, "action": "admin.settings.update", "target": "site", "result": "success", "created_at": "2026-10-24T12:00:00Z", "request_id": "..." } ] } }
```

- `limit` 1–200，缺省 50，越界返回 `400 INVALID_LIMIT`；
- `action` 可选精确过滤（如 `admin.category.delete`）；
- 日志由所有管理端写操作自动写入，`admin_id` 在管理员被删除后为 `null`（`ON DELETE SET NULL`），历史保留。

### `GET /api/v1/admin/backups`

列出受管理的 SQLite 备份（需要管理员会话），响应包含文件名、类型、UTC 创建时间、字节数、SHA-256 和下载地址；同时返回保留策略 `daily: 7`、`weekly: 4`。应用每小时检查一次窗口，缺少最近 24 小时日备或最近 7 天周备时自动使用 `VACUUM INTO` 创建，手动备份不自动删除。

### `POST /api/v1/admin/backups`

创建在线一致性的手动备份，需要合法 Origin、管理员会话和 CSRF。成功返回 `201`，创建后立即执行 SQLite `PRAGMA quick_check`；失败文件会删除，并写 `admin.backup.create` 审计。

### `POST /api/v1/admin/backups/{name}/verify`

重新执行完整性预检，需要合法 Origin、管理员会话和 CSRF。成功返回 `{ "verified": true }`；损坏返回 `422`。该操作写 `admin.backup.verify` 审计。

### `GET /api/v1/admin/backups/{name}`

下载备份（需要管理员会话）。仅接受服务生成的严格文件名，拒绝目录穿越；响应类型为 `application/vnd.sqlite3` 且强制附件下载。

### `GET /api/v1/admin/job-runs?limit=50`

返回最近运行，包含触发来源，以及请求、新增、更新、详情快照、304、重试、限流、失败和覆盖不完整计数。运行状态包括 `running`、`completed`、`failed` 和 `deferred`；详情同步遇到 GitHub 主/次限流时使用 `deferred`，按 `RetryAt` 重排任务且不增加仓库失败次数。`limit` 有效范围为 1–200，缺省或越界时使用 50。

### `GET /api/v1/admin/job-runs/{id}`

返回单次运行与结构化事件：

```json
{
  "data": {
    "run": { "id": 9, "status": "failed", "trigger": "manual" },
    "events": [{
      "id": 1,
      "run_id": 9,
      "level": "error",
      "event_code": "collection_failed",
      "message": "…",
      "attempt": 0
    }]
  }
}
```
