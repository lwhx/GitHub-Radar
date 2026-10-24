# 部署指南

目标环境：自有 VPS（建议 ≥1 vCPU / 1GB RAM）+ Docker Engine（含 Compose 插件）。不依赖 Cloudflare Workers / D1 / Pages。

## 1. 准备目录与环境

```bash
mkdir -p /opt/github-radar/data
cd /opt/github-radar
# 将本仓库检出或复制到该目录
git clone <repo-url> .   # 或解压发布包

cp .env.example .env
chmod 600 .env
```

编辑 `.env`：

| 变量 | 要求 |
| --- | --- |
| `HOST_PORT` | 宿主机监听端口，默认 `18080`，仅绑定 `127.0.0.1` |
| `PUBLIC_BASE_URL` | 对外的完整 `https://` 地址，登录 Origin 校验使用 |
| `APP_SECRET` | ≥32 字符随机串：`openssl rand -base64 32` |
| `APP_ENCRYPTION_KEY` | Base64 编码的 32 字节：`openssl rand -base64 32`；用于加密 GitHub Token，必须备份 |
| `APP_TIMEZONE` | 站点展示时区，默认 `Asia/Shanghai`（数据库始终 UTC） |
| `GITHUB_API_BASE_URL` | 通常留空；只有 GitHub Enterprise 或受控 API 代理才覆盖 |

生产模式拒绝示例占位值，密钥不合规时容器会启动失败并输出错误。

## 2. 构建并启动

```bash
docker compose up -d --build
docker compose ps                 # 期望 (healthy)
docker compose exec app /app/radar admin create   # 交互创建首个管理员
docker compose logs --tail=100 app
```

验证：

```bash
curl -i http://127.0.0.1:18080/healthz
curl -i http://127.0.0.1:18080/readyz
docker compose exec app /app/radar healthcheck
```

浏览器访问 `http://127.0.0.1:18080/` 应看到发现首页；页脚“管理员入口”进入登录页。登录后先在“凭据管理”添加 GitHub Token，服务端会调用 GitHub 验证身份和配额并加密保存；随后在“采集任务”手动运行一次，确认运行记录为“已完成”。

## 3. Nginx / 宝塔反向代理

应用只信任本机回环地址，对外由 Nginx 终结 HTTPS。示例站点配置：

```nginx
server {
    listen 443 ssl http2;
    server_name radar.example.com;

    ssl_certificate     /etc/letsencrypt/live/radar.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/radar.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:18080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

注意：登录限速默认只使用 TCP 对端地址，完全忽略代理头。部署反向代理后，在管理后台“系统运维 → 可信代理”填入应用实际看到的代理 IP/CIDR（单机 Nginx 通常为 `127.0.0.1`）；只有对端命中此列表时应用才从右向左解析 `X-Forwarded-For`，非法链会安全回退到对端地址。不要填写不受控制的公网网段。

## 4. 数据与备份

唯一持久化目录是 `./data`（容器内 `/app/data`），包含 SQLite 数据库、WAL 文件与后续备份：

```bash
ls -la /opt/github-radar/data
```

- 删除容器不删除数据：`docker compose down && docker compose up -d` 即可恢复。
- 应用启动后自动补齐日备/周备，备份目录为 `./data/backups/`；保留最近 **7 个日备 + 4 个周备**，手动备份不自动删除。
- 手动创建在线一致性备份：`docker compose exec app /app/radar backup`。命令输出文件名、字节数与 SHA-256；也可在管理后台创建、校验和下载。
- 每个备份由 SQLite `VACUUM INTO` 生成并立即通过 `PRAGMA quick_check`；无需停服，也不要把实时数据库的单个 `.db` 文件直接当备份。
- 恢复前先在后台点击“校验”或对下载文件运行 `sqlite3 backup.db 'PRAGMA quick_check'`，结果必须为 `ok`。恢复时停止容器，将现有 `radar.db`、`radar.db-wal`、`radar.db-shm` 移出数据目录，再把已校验备份复制为 `data/radar.db`，最后启动并检查 `/readyz`。不要在运行中的服务上覆盖数据库文件。
- `APP_ENCRYPTION_KEY` 必须随数据一起妥善保管，丢失后无法解密已保存的 Token 与 AI Key 密文。

## 5. 升级与回滚

```bash
cd /opt/github-radar
cp -r data "data.bak.$(date +%Y%m%d%H%M)"      # 升级前备份
git pull                                        # 或替换为新版本文件
docker compose up -d --build
docker compose logs --tail=100 app
docker compose ps                               # 确认 (healthy)
```

迁移在启动时自动执行（`schema_migrations` 表记录校验和）。回滚：恢复数据目录备份 + 切回旧代码重新构建。若新版迁移已执行，回滚到旧二进制前必须确认旧版本兼容当前迁移版本。

## 6. 故障排查

| 现象 | 排查 |
| --- | --- |
| 容器反复重启 | `docker compose logs app`；常见原因是 `.env` 密钥不合规（生产模式） |
| `(unhealthy)` | `docker compose exec app /app/radar healthcheck` 手动执行看输出 |
| 登录 403 INVALID_ORIGIN | `PUBLIC_BASE_URL` 与实际访问地址不一致 |
| 登录 429 RATE_LIMITED | 该对端 IP 5 分钟内失败 ≥5 次，等待 5 分钟 |
| `data` 权限错误 | `./data` 需可被容器内 UID 10001 写入：`chown -R 10001:10001 data` |
| 端口冲突 | 修改 `.env` 的 `HOST_PORT` 后 `docker compose up -d` |
