# 本地开发指南

## 前置要求

- Go 1.22 或更高版本（构建纯 Go SQLite，无需 CGO 工具链）
- Node.js 22 或更高版本（与 Dockerfile 中的构建镜像一致）

## 后端

```bash
cd backend

# 运行全部测试与静态检查
go test ./...
go vet ./...

# 启动开发服务（默认 :8080，数据库写入 ./data/radar.db）
go run ./cmd/radar serve
```

后端环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `APP_ENV` | `development` | `production` 时强制校验密钥与公开地址 |
| `APP_PORT` | `8080` | HTTP 监听端口 |
| `DATABASE_PATH` | `./data/radar.db` | SQLite 数据库文件路径 |
| `PUBLIC_BASE_URL` | 空 | 生产必填；管理写操作与登录/注销的 Origin 校验基准 |
| `GITHUB_API_BASE_URL` | 空 | 可选；留空使用 `https://api.github.com`，仅在 GitHub Enterprise/测试替身时覆盖 |
| `APP_SECRET` | 开发随机生成 | 生产必填，≥32 字符 |
| `APP_ENCRYPTION_KEY` | 开发随机生成 | Base64 编码的 32 字节密钥 |
| `APP_TIMEZONE` | `Asia/Shanghai` | 站点时区（数据库存 UTC） |
| `STATIC_DIR` | `../frontend/dist` | 前端构建产物目录 |

开发模式缺少密钥时进程会生成临时密钥并输出 JSON 警告日志；生产模式直接使用示例占位值会启动失败。

### 创建管理员

```bash
go run ./cmd/radar admin create
```

交互式输入用户名与密码（密码不回显，长度 12–1024 字符）。命令只能创建首个管理员，重复执行会提示已存在。

### 健康检查

```bash
go run ./cmd/radar healthcheck   # 请求 http://127.0.0.1:8080/readyz
```

## 前端

```bash
cd frontend
npm ci
npm test -- --run   # Vitest（jsdom）
npm run lint        # ESLint
npm run build       # vue-tsc 类型检查 + Vite 生产构建
npm run dev         # Vite 开发服务器（默认 :5173）
```

`vite.config.ts` 已将 `/api`、`/healthz`、`/readyz` 代理到 `http://127.0.0.1:8080`。

登录与注销接口校验 `Origin` 头。通过 Vite 代理开发时，请给后端设置：

```bash
PUBLIC_BASE_URL=http://localhost:5173 go run ./cmd/radar serve
```

## 端到端联调（无 Docker）

```bash
cd backend && go build ./cmd/radar
cd ../frontend && npm run build
cd ../backend
STATIC_DIR=../frontend/dist APP_ENV=development ./radar serve
```

访问 `http://127.0.0.1:8080/`。生产形态由 Go 服务同时托管 API 与前端静态资源。创建管理员并登录后，在 `/admin/credentials` 添加 GitHub Token；验证成功后可在 `/admin/jobs` 手动启动采集或查看定时运行记录。

如需执行真实 GitHub 客户端烟测（不读取 Token，只使用未认证公开搜索）：

```bash
cd backend
GITHUB_LIVE=1 go test ./internal/github -run TestLiveSearchContract -count=1 -v
```

## 提交前检查清单

```bash
cd backend && go test ./... && go vet ./... && go build ./cmd/radar
cd ../frontend && npm test -- --run && npm run lint && npm run build
```

两个 `go.mod` / `package-lock.json` 都必须与实际依赖一致（CI 使用 `go mod download` / `npm ci`）。
