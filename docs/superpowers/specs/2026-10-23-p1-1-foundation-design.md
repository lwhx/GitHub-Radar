# GitHub Radar P1.1 设计说明

> 日期：2026-10-23
> 需求基线：`github-radar-spec-v1.0.md` 与 `github-radar-ai-kickoff.md`

## 范围

本轮只实现 P1.1：项目骨架、SQLite 自动迁移、健康检查、管理员 CLI 初始化与会话登录、前台/后台基础布局、主题、响应式导航，以及可构建的单容器 Docker Compose。GitHub 采集、排行榜和完整管理后台属于 P1.2–P1.4，不在本轮伪装为可用功能。

## 架构

采用单仓库、单业务容器架构。`frontend` 使用 Vue 3 + TypeScript + Vite + Tailwind CSS，构建产物由 Go 服务托管；`backend` 使用 Go 1.22、chi 与 SQLite。SQLite 迁移嵌入 Go 可执行文件，服务启动时自动执行；数据库启用 WAL、外键和 busy timeout。

应用只把 `/app/data` 作为持久化目录。管理员通过 `/app/radar admin create` 交互创建，不提供默认密码。密码采用 Argon2id；会话使用服务端保存的随机令牌哈希和 HttpOnly Cookie；状态变更接口使用 CSRF Token 与 Origin 校验。生产环境拒绝示例密钥和不合规配置。

## 组件边界

- `internal/config`：读取并验证端口、数据库路径、时区、公共 URL、密钥和静态目录。
- `internal/db`：打开 SQLite、设置 PRAGMA、执行版本化迁移与就绪探针。
- `internal/auth`：Argon2id 密码、管理员创建、会话签发/校验/注销、登录限速。
- `internal/api`：路由、中间件、健康检查、认证 API、后台概览、SPA 静态托管。
- `cmd/radar`：`serve`、`healthcheck`、`admin create` 子命令。
- `frontend/src`：公共壳、后台壳、主题状态、登录页和基础数据概览。

## 数据与安全

P1 所需核心表全部由初始迁移创建，避免后续阶段反复重塑主键与时间字段；P1.1 只读写管理员、会话、审计及基础统计相关表。敏感令牌字段只建立密文存储契约，不实现 P1.2 的凭据管理。所有数据库时间使用 UTC RFC3339 字符串。

开发环境缺少密钥时可以为单次进程生成临时密钥并输出明确警告；生产环境必须提供至少 32 字符的 `APP_SECRET`、Base64 编码的 32 字节 `APP_ENCRYPTION_KEY` 和合法的绝对 `PUBLIC_BASE_URL`。任何示例占位值在生产模式下都会导致启动失败。

## UI

前台提供响应式顶部栏、桌面侧栏、移动端抽屉、主题切换和空数据初始化引导。后台提供独立侧栏、登录页与真实数据库概览。页面不放置假仓库、假 Star 或假任务。交互控件使用语义化按钮、可见焦点、键盘可操作导航和明暗主题 CSS 变量。

## 测试与验收

后端使用 Go 单元/集成测试覆盖配置、迁移、密码、会话、登录与健康接口；前端使用 Vitest + Vue Test Utils 覆盖主题和登录行为。阶段验证运行：`go test ./...`、`go vet ./...`、`npm test -- --run`、`npm run lint`、`npm run build`，并构建 Go 二进制。若本机没有 Docker，必须明确将 Compose 构建与容器烟测标记为未执行，而不能推断成功。

## 已确认的后续风险

GitHub 官方当前版本支持 `X-GitHub-Api-Version: 2026-03-10`。需求文档第 5.1 节所写 `/repos/{owner}/{repo}/stargazers/history` 不是当前官方 REST 路径；P1.2 应改用官方 `GET /repos/{owner}/{repo}/stargazers` 的时间戳媒体类型或其他经验证的数据源，并保持“不能冒充精确滚动 24h”的产品口径。
