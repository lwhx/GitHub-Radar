# GitHub Radar P1.1 实现计划

> **面向 AI 代理的工作者：** 使用 `subagent-driven-development` 逐任务实现；所有行为变更遵循测试先行，步骤使用复选框跟踪。

**目标：** 交付可原生构建并可通过 Docker Compose 部署的 P1.1 基础版本，包含数据库迁移、管理员创建/登录、健康检查和响应式 Vue 界面。

**架构：** Go 单体服务托管 REST API 与 Vue SPA，使用 SQLite WAL 保存数据。前后端分别构建后打入一个非 root 生产容器，`./data` 是唯一持久化目录。

**技术栈：** Go 1.22、chi、modernc SQLite、Argon2id、Vue 3、TypeScript、Vite、Tailwind CSS、Vitest、Docker Compose。

---

## 文件结构

- `backend/cmd/radar/main.go`：CLI 入口与子命令分派。
- `backend/internal/config/`：运行配置及校验。
- `backend/internal/db/`、`backend/migrations/`：SQLite 连接、迁移和就绪探针。
- `backend/internal/auth/`：密码、管理员与会话。
- `backend/internal/api/`：HTTP 路由、中间件和处理器。
- `frontend/src/{api,components,layouts,pages,router,stores}/`：Vue SPA。
- `Dockerfile`、`docker-compose.yml`：单容器部署。
- `docs/{API,DEPLOYMENT,DECISIONS}.md`：阶段文档。
- `.github/workflows/ci.yml`：后端与前端质量门禁。

### 任务 1：仓库骨架与配置

- [ ] 创建 Go module、前端 package、忽略文件和目录骨架。
- [ ] 先写 `backend/internal/config/config_test.go`，覆盖开发默认值、生产密钥、时区和 URL 校验；运行测试确认失败。
- [ ] 实现 `config.Load()`，再次运行测试确认通过。
- [ ] 记录依赖版本并生成 `go.sum`、`package-lock.json`。

### 任务 2：SQLite 迁移与就绪检查

- [ ] 先写迁移集成测试，断言所有 P1 必需表、WAL、外键和迁移版本存在；运行确认失败。
- [ ] 创建 `backend/migrations/0001_initial.sql`，包含需求文档第 8 节核心表、索引和 FTS5 同步触发器。
- [ ] 实现数据库打开、PRAGMA、事务迁移和 `Ready(ctx)`；运行测试确认通过。
- [ ] 增加数据库目录不可写时的可理解错误测试。

### 任务 3：管理员与会话安全

- [ ] 先写 Argon2id 哈希/错误密码测试、重复用户名测试、会话过期与注销测试；运行确认失败。
- [ ] 实现密码哈希与常数时间校验、管理员创建、随机会话/CSRF Token 哈希存储。
- [ ] 实现 `radar admin create` 交互命令，不接受命令行明文密码，不创建默认账户。
- [ ] 运行认证包及全量 Go 测试。

### 任务 4：HTTP API 与静态托管

- [ ] 先写 `httptest`：`/healthz`、`/readyz`、登录成功/失败、未授权后台、CSRF 注销、真实数据库概览；运行确认失败。
- [ ] 实现 request ID、安全响应头、JSON 错误、请求体上限、登录限速和 Origin/CSRF 校验。
- [ ] 实现登录、注销、会话查询、后台概览以及 SPA fallback；不得暴露密码哈希、会话哈希或密钥。
- [ ] 实现 `serve` 与 `healthcheck` 子命令，运行 Go 测试和 `go vet ./...`。

### 任务 5：Vue 公共与后台界面

- [ ] 先写组件测试：主题切换持久化、登录提交和错误提示；运行确认失败。
- [ ] 实现 API 客户端、认证状态、公共布局、后台布局、首页空数据引导、后台登录与概览。
- [ ] 实现桌面侧栏、移动端抽屉、亮暗主题、可见焦点、加载/错误状态和 GitHub 非官方声明。
- [ ] 运行 `npm test -- --run`、`npm run lint` 和 `npm run build`。

### 任务 6：容器、文档与 CI

- [ ] 创建多阶段 `Dockerfile`，前端和 Go 分别编译，生产镜像以 UID/GID 10001 非 root 运行。
- [ ] 创建与需求端口、卷和健康检查契约一致的 `docker-compose.yml`、`.env.example`。
- [ ] 编写 README、开发说明、API、部署与决策文档，明确 P1.1 已完成和 P1.2+ 未实现。
- [ ] 创建 GitHub Actions，运行 Go 测试/vet 和前端 test/lint/build。
- [ ] 运行完整本机构建；有 Docker 时执行 `docker compose config`、镜像构建与健康烟测，无 Docker 时记录未执行原因。

### 任务 7：阶段审查与验收

- [ ] 对照 P1.1 需求逐项审查，无遗漏、无提前宣称 P1.2 功能。
- [ ] 独立执行规格合规审查与代码质量/安全审查，修复所有真实缺陷。
- [ ] 重新运行全部后端测试、vet、前端测试、lint、构建及二进制启动烟测。
- [ ] 检查 Git 状态、敏感信息和最终变更清单，提交阶段结果。
