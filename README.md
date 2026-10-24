# GitHub Radar（开源雷达）

面向中文开发者和自托管爱好者的开源项目发现、趋势排名与项目解读平台。它不是简单复制 GitHub Trending，而是把“发现新建项目”“跟踪 Star 变化”“识别潜力项目”和“中文理解项目”连在一起。

> **当前阶段：P1.4 管理与运维闭环完成**。已实现 Go REST 服务、SQLite 版本化迁移与 FTS5、加密凭据和 AI Key、可恢复时间窗采集、Star 快照与四类榜单、仓库/分类/README 公共页面，以及管理员凭据、任务、分类、仓库导入与维护、设置、审计和备份后台。
> 数据始终来自真实采集：数据库为空时展示明确空态，不使用演示仓库或假统计。SQLite 在线备份会自动补齐日备/周备、执行完整性校验并应用 7 日 + 4 周保留策略。

## 功能矩阵

| 功能 | 状态 |
| --- | --- |
| SQLite WAL 数据库 + 版本化迁移 + FTS5 | ✅ P1.1 |
| 健康检查 `/healthz` `/readyz` | ✅ P1.1 |
| 管理员创建（CLI）/ 登录 / 注销 / 会话 | ✅ P1.1 |
| 登录限速、CSRF、Origin 校验、审计日志 | ✅ P1.1 |
| 前端公共布局、明暗主题、移动端抽屉 | ✅ P1.1 |
| 后台登录页与数据概览 | ✅ P1.1 |
| 首页统计与空库初始化引导 | ✅ P1.1 |
| GitHub REST 客户端 / 时间窗口采集 / 初始 Star 快照 | ✅ P1.2 |
| 条件详情同步（ETag/304）/ 持续 Star 快照 / 失败退避 | ✅ P1.2 |
| Search/Core 独立配额 / 限流 deferred / 每日 48 小时恢复补采 | ✅ P1.2 |
| 加密凭据 / 定时与手动任务 / 运行记录与结构化事件 | ✅ P1.2 |
| 后台凭据管理与采集任务页面 | ✅ P1.2 |
| 排行榜 / 搜索 / 公开项目详情页面 | ✅ P1.3 |
| 分类 / 仓库管理 / 手工导入 / 设置 / 审计 / 可信代理 / 备份恢复 | ✅ P1.4 |
| AI 中文解读 / RSS / Telegram 推送 | ⬜ P2 |

## 快速开始（Docker Compose）

前置要求：一台安装了 Docker 与 Docker Compose 插件的 Linux VPS。

```bash
mkdir -p /opt/github-radar/data
cd /opt/github-radar
# 复制本仓库文件到该目录后：
cp .env.example .env
# 编辑 .env：填入随机 APP_SECRET（≥32 字符）、Base64 编码的 32 字节 APP_ENCRYPTION_KEY、
# 真实站点域名 PUBLIC_BASE_URL。禁止使用示例占位值，生产模式会拒绝启动。
docker compose up -d --build

# 创建管理员（交互式输入密码，无默认账户）
docker compose exec app /app/radar admin create

docker compose ps
docker compose logs --tail=100 app
```

默认只监听 `127.0.0.1:18080`，通过 Nginx/宝塔反向代理对外提供 HTTPS。详见 [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)。

## 本地开发

要求：Go 1.22+、Node.js 22+。

```bash
# 后端（终端 1）：默认 8080 端口，数据库写到 ./data/radar.db
cd backend
go run ./cmd/radar serve

# 前端（终端 2）：Vite 开发服务器，/api 与 /healthz 代理到 127.0.0.1:8080
cd frontend
npm ci
npm run dev
```

开发模式下后端允许缺省密钥（启动时会输出警告）。由于登录接口校验 Origin，开发时请给后端设置
`PUBLIC_BASE_URL=http://localhost:5173`（Vite 默认端口），或从 8080 端口直接访问构建产物。

更多说明见 [DEVELOPMENT.md](DEVELOPMENT.md)。

## 常用命令

```bash
# 后端
cd backend && go test ./... && go vet ./...   # 测试与静态检查

# 前端
cd frontend && npm test -- --run && npm run lint && npm run build

# 运行检查
./radar healthcheck                            # 容器内：/app/radar healthcheck
```

## 目录结构

```text
github-radar/
├── Dockerfile / docker-compose.yml / .env.example
├── backend/
│   ├── cmd/radar/            # serve / healthcheck / admin create 子命令
│   ├── internal/{api,auth,collector,config,credentials,db,github,scheduler}/
│   └── migrations/           # 内嵌 SQLite 迁移
├── frontend/src/
│   ├── api/                  # fetch 封装（错误归一化、CSRF）
│   ├── layouts/              # 公共布局 + 后台布局
│   ├── pages/                # 首页 / 登录 / 后台概览 / 404
│   ├── router/ stores/       # vue-router 与轻量状态
├── docs/
│   ├── API.md / DEPLOYMENT.md / DECISIONS.md
└── data/                     # 运行时创建，唯一持久化目录（勿提交）
```

## 安全说明

- 密码使用 Argon2id 哈希；Session/CSRF Token 只存 SHA-256 哈希。
- 生产模式强制校验 `APP_SECRET`、`APP_ENCRYPTION_KEY`、`PUBLIC_BASE_URL`，拒绝示例值。
- GitHub Token、AI Key 只允许经环境变量/后台在服务端使用，禁止进入前端构建产物与日志。
- 登录接口按对端 IP 限速（失败 5 次锁定 5 分钟）；管理 API 要求 CSRF Token 与 Origin 校验。

## 数据来源声明

GitHub Radar 是独立的第三方开源项目发现平台，与 GitHub, Inc. 无隶属关系，非 GitHub 官方产品。仓库数据来源于 GitHub 公开 API，归属各自仓库作者。

完整需求基线见 `github-radar-spec-v1.0.md`，实施决策记录见 [docs/DECISIONS.md](docs/DECISIONS.md)。
