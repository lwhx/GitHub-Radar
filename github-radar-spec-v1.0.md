# GitHub Radar（开源雷达）开发需求与技术设计文档

> 版本：v1.0 ｜ 日期：2026-10-23 ｜ 状态：可执行开发基线  
> 面向：AI 编程代理、前后端开发者、测试人员、运维人员  
> 部署方式：**自有 VPS + Docker Compose**，不得将 Cloudflare Workers / D1 / Pages 作为必要依赖。

## 0. AI 开发代理执行约定（必须先读）

1. **按本文件实现，而不是只生成 UI 演示图。** 需要真正的 GitHub 数据采集、数据库持久化、真实 API、管理员登录、定时任务与可运行的 Docker 镜像。
2. 优先完成 **MVP（阶段 P1）**，再按 P2/P3 迭代。没有完成并验证当前阶段前，不要擅自堆叠后续功能。
3. 对未列明的细节采用简单、易维护、可替换的实现，记录到 `docs/DECISIONS.md`；不得自行改动本文件明确的边界。
4. 不得填充假项目、假 Star、假排行榜。开发模式可使用明确标注的测试夹具；生产环境只有真实 GitHub 数据，缺失字段显示“暂无数据”。
5. GitHub API Token、管理员密码、AI Key 等**只在服务端**保存和使用；禁止写进源码、前端包、日志、示例配置或 Git 提交。
6. 每个阶段交付：变更清单、测试命令及结果、数据库迁移、使用说明、Docker 构建结果、已知问题；失败必须报告，不能宣称已完成。
7. API 限流遵守 GitHub 规则，不通过多账号轮流请求规避限制；采集器要会主动降速、暂停和恢复。
8. 一切时间在数据库中按 UTC 保存，界面按设置的时区显示；排名窗口要标注是滚动时间还是日历日。
9. 功能实现应具备**可关闭、可降级**的能力：AI 服务、Trending 页面采集、RSS/通知不可用时，不影响基本排行榜。
10. 最终交付必须能执行 `docker compose up -d --build` 启动，并通过本文件的验收测试。

## 1. 产品定位和目标

### 1.1 一句话定位

**GitHub Radar：面向中文开发者和自托管爱好者的开源项目发现、趋势排名与项目解读平台。**

它不是简单复制 GitHub Trending，而是把“发现新建项目”“跟踪 Star 变化”“识别潜力项目”“筛选可部署工具”和“中文理解项目”连在一起。

### 1.2 核心目标

- 无需登录即可在网页上浏览公开仓库。
- 尽早发现刚创建或尚未火起来的项目，而不只是展示历史高 Star 项目。
- 给出可解释的 24 小时、7 天 Star 变化，以及数据采集时间。
- 聚焦 AI Agent、MCP、LLM、Docker、自托管、NAS、Cloudflare、Telegram、效率工具等领域，同时保留“全部项目”。
- 让用户通过中文摘要、项目健康信息、README、部署线索和外部阅读工具快速判断项目价值。
- 管理员可以调整分类、采集频率、凭据、AI 配置，查看任务状态和日志。
- 完整数据保存在服务器项目目录，支持备份、恢复和迁移。

### 1.3 明确不做

- MVP 不提供 GitHub 代码托管、仓库镜像、代码编辑或 CI/CD 平台。
- MVP 不要求公众注册账户；匿名收藏使用浏览器本地存储。
- 不抓取私有仓库；系统只索引公开仓库。
- 不保证覆盖 GitHub 全部新仓库；需要如实展示索引范围和采集质量。
- 不承诺自动生成的部署命令始终安全、正确；未验证时只显示原仓库提供的部署说明。

## 2. 用户、场景与分期范围

### 2.1 用户角色

| 角色 | 可做什么 | 是否登录 |
| --- | --- | --- |
| 游客 | 浏览、搜索、筛选、查看项目、复制链接、浏览器本地收藏 | 否 |
| 管理员 | 管理采集、GitHub 凭据、分类、AI、备份、系统配置 | 是 |

### 2.2 功能范围

**P1 / MVP（必须落地）**

1. 网站首页、排行榜、探索搜索、分类专题、项目详情、本地收藏。
2. 亮色/暗色主题、中文界面、桌面和手机响应式设计。
3. GitHub Search API 仓库发现、仓库信息更新、Star 快照、24 小时和 7 天增长计算。
4. 最新项目榜、24 小时飙升榜、7 日增长榜、潜力黑马榜。
5. 管理员认证、GitHub 多凭据管理、采集任务及日志、分类管理、基础系统设置。
6. SQLite 本地数据库、自动迁移、备份/恢复、Docker Compose 自托管部署。
7. API 健康检查、限流控制、重试、断点续采、基础运行监控。

**P2（P1 通过验收后开发）**

1. 可配置 AI 提供商，对热门仓库生成结构化中文解读；未配置时优雅降级。
2. GitHub Star History 历史补全、30 天/90 天曲线和质量标签。
3. 两到三个项目的横向对比页面。
4. RSS 订阅和 Telegram Bot 推送（独立开关）。
5. 资讯/文章页（先做管理员编辑发布，自动资讯聚合另行设计）。
6. 受支持时可选采集 GitHub Trending，并明确标记来源和采集时间。

**P3（长期扩展）**

1. 自定义榜单与订阅条件，收藏云同步（需要额外用户系统）。
2. 多数据源（如 GH Archive）与历史趋势分析。
3. 高级 AI 相似项目发现、部署难度评估及个性化推荐。
4. 大规模采集时将任务调度器与 Web 服务拆成独立容器，必要时迁移 PostgreSQL。

## 3. 信息架构与路由

### 3.1 前台

| 路由 | 页面 | 主要模块 |
| --- | --- | --- |
| `/` | 发现首页 | 搜索、统计、今日精选、飙升项目、最新项目、分类入口 |
| `/rankings` | 排行榜 | 最新/24h/7d/潜力切换、分类语言筛选、排序、分页 |
| `/explore` | 探索项目 | 关键词、语言、分类、Star 区间、创建日期、许可协议等组合筛选 |
| `/topics` | 分类总览 | 分类网格、简介、各分类收录数量 |
| `/topics/:slug` | 分类列表 | 分类介绍、筛选、相应榜单 |
| `/repo/:owner/:repo` | 项目详情 | 中文/原文简介、趋势、README、元数据、部署线索、相关项目 |
| `/bookmarks` | 我的关注 | 浏览器本地收藏、取消收藏、导入/导出 JSON |

后续：`/compare`、`/news`、`/news/:slug`、`/subscribe`。

### 3.2 管理后台

| 路由 | 页面 |
| --- | --- |
| `/admin/login` | 管理员登录 |
| `/admin` | 数据概览、采集健康、失败任务、API 限额 |
| `/admin/credentials` | GitHub Token 添加、测试、启停、状态查看 |
| `/admin/jobs` | 任务列表、启动/暂停、计划、最近运行、失败重试 |
| `/admin/categories` | 分类与 GitHub Topic 映射规则 |
| `/admin/repositories` | 项目查找、手动收录、隐藏/恢复、重新同步 |
| `/admin/settings` | 时区、频率、展示参数、站点信息、AI 配置 |
| `/admin/logs` | 采集日志、操作日志、错误筛选 |
| `/admin/backups` | 备份、下载、恢复前检查、保留策略 |

### 3.3 前端布局规范

- 桌面：顶部全局搜索与主题切换、左侧固定导航或可折叠侧栏、主内容宽度约 1200–1440px。
- 首页上方是“发现值得关注的 GitHub 开源项目”展示区，下面分别放统计概览、快速榜单切换、项目列表与分类入口。
- 列表同时提供**紧凑表格模式**和**卡片模式**；桌面默认表格，移动端默认卡片。
- 每个列表项展示：仓库名、作者、原文/中文摘要、语言、分类标签、总 Star、24h 增长、创建时间、最近同步时间、收藏按钮。
- 项目详情：顶部仓库信息和跳转 GitHub / Zread / DeepWiki；主栏 README、趋势曲线、中文解读；侧栏许可证、语言、最近提交、Stars、Forks、发布时间和部署相关标签。
- 窄屏宽度下表格自动切换卡片，筛选区域可折叠；交互元素可键盘访问，加载/空态/错误态都有明确提示。
- 视觉：现代开源工具站风格，简洁圆角、适当分隔线、统一图标、明暗主题；不照搬任何第三方网站的受保护设计。
- 站点统一显示“最后更新：YYYY-MM-DD HH:mm”，绝不把过时缓存说成实时数据。

### 3.4 关键页面线框图（实施 UI 时参考）

首页桌面版（结构示意，数据必须来自 API）：

```text
┌──────────────────────────────────────────────────────────────────────┐
│ GitHub Radar     [搜索项目 / 关键词 .............]    主题  我的关注  │
├───────────────┬──────────────────────────────────────────────────────┤
│ 发现首页      │ 发现值得关注的开源项目                              │
│ 排行榜        │ [今日收录] [24h 飙升] [本周热门] [分类数]            │
│ 探索项目      │                                                      │
│ 分类专题      │ [最新项目] [24h 飙升] [7日增长] [潜力黑马]          │
│ 我的关注      │ [分类] [语言] [创建日期] [Star 范围] [切换显示]      │
│               │ 仓库名称   中文摘要   语言  总 Star  新增  创建时间   │
│ AI/大模型     │ repo-a    ......     Go    1.2k     +130  昨天      │
│ Docker        │ repo-b    ......     TS      890     +110  2天前     │
│ MCP           │ ...                                                  │
│ NAS           │                                                      │
├───────────────┴──────────────────────────────────────────────────────┤
│ 数据更新时间 | GitHub 数据归属声明 | 关于本站                       │
└──────────────────────────────────────────────────────────────────────┘
```

项目详情桌面版：顶部仓库名称、简介、总 Star、24h/7d 增长及外部阅读入口；正文左侧为“概览 / 趋势 / README / 部署线索”，右侧为语言、许可证、最近同步时间和分类。手机端按“项目概览 → 增长图 → 中文摘要 → README → 元数据”顺序纵向展示。

### 3.5 视觉与交互设计基线

- 主字体优先系统无衬线字体，正文基准 14–16px；小字不可低于可读阈值；长仓库名可换行并保留复制入口。
- 亮色主题：浅灰背景、白色卡片、深色文字、蓝色主操作；暗色主题：深灰背景与分层卡片。
- 推荐将背景、文字、边框、主色、成功、警告统一抽象为 CSS 变量；避免散落魔法色值，保证两种主题对比度。
- 常用卡片具有 Hover、Focus、Loading、Empty、Error 状态；外链明确打开新窗口，通知类操作提供可关闭反馈。
- 真实增长用绿色或中性色展示，负增长按“−N”显示，不把“不知道”渲染成绿色“+0”。
- 管理后台以高信息密度表格为主，配状态标签、搜索、筛选与确认弹窗；删除凭据、恢复数据等危险操作必须二次确认。

## 4. 主要业务功能详细说明

### 4.1 首页

- 全局搜索框支持仓库名、`owner/repo`、描述、分类关键词的**本地已索引数据**搜索。
- “今日发现”指当日被采集器首次收录的仓库数量；“新增仓库”与 GitHub 的 `created_at` 是不同概念，要分开计数。
- 展示精选卡、最近创建、24h 飙升、潜力黑马和分类快捷入口。
- 所有指标提供口径说明及数据更新时间；数据库为空时展示初始化引导，不用假数字占位。

### 4.2 排行榜

- **最新榜**：按 GitHub `created_at` 降序，同时间按 `github_id` 稳定排序；不能以 `first_seen_at` 冒充创建时间。
- **24h 飙升**：按滚动 24 小时净 Star 变化降序。
- **7d 增长**：按滚动 168 小时净 Star 变化降序。
- **潜力黑马**：30 天内创建、总 Star 不超过 3000、最近 7 天有可信增长的项目，结合增长与内容完整性排序；显示分数和解释。
- 筛选：分类、语言、创建时间、Star 范围、是否 Fork、是否 Archived、是否有许可证、是否支持 Docker（识别结果可标“待验证”）。
- 同分采用 `stars_count DESC, github_id ASC` 稳定排序；避免翻页时项目顺序不稳定。
- 明确告诉用户：增长窗口无有效历史样本时显示“数据不足”，**不显示 0**。

### 4.3 探索/搜索

- P1 只搜索本地已索引仓库，不需要访问 GitHub 做前台实时搜索；页面需明示“搜索已收录项目”。
- 支持关键词、高级筛选、排序、分页；输入防抖 300ms，服务端参数白名单、最大 `limit=100`。
- 关键词搜索支持 SQLite FTS5（若构建环境不支持，需给出替代方案并留测试）。
- 可复制当前筛选 URL 分享给其他用户，筛选状态优先放在 URL query 中。

### 4.4 分类专题

默认分类：全部项目、AI/大模型、AI Agent、MCP、Docker、自托管、NAS、Cloudflare、Telegram、开发工具、效率工具、安全工具、数据/数据库、前端、后端。

- 项目可以匹配多个分类，允许管理员指定主分类。
- 分类由 GitHub Topic、名称和描述规则匹配，并支持人工覆盖。人工覆盖优先级最高。
- 分类规则应该是数据库配置，不应全部硬编码；支持启用、禁用、排序、添加关键词、关联 Topic。
- 分类不匹配的项目保留在“全部项目”，不能静默丢弃。

### 4.5 项目详情

- 基本信息：`full_name`、描述原文及中文摘要、语言、Topic、创建/推送/同步时间、Stars、Forks、Issues、License、仓库主页。
- 统计：可显示 24h、7d、30d 变化；数据不完整时使用空态、采样覆盖说明和方法标识。
- README：按需获取和缓存，使用安全 Markdown 渲染器；过滤 HTML、脚本和不安全链接，限制内容长度。
- “部署支持”只表示检测到 Dockerfile、Compose 文件、容器说明或仓库文档，不代表一定能一键部署。未验证的命令不得标记为“已验证”。
- GitHub 跳转真实 `html_url`；Zread 与 DeepWiki 仅作为外部阅读入口，无法保证已收录，打开失败不影响本站。
- 显示内容来源和时间，不假装 AI 摘要已人工审核。

### 4.6 收藏

- P1 使用 `localStorage` 保存 GitHub 仓库 ID / full_name，不依赖用户登录。
- 支持删除、批量清空、导入导出 JSON；导入时校验结构、数量和长度。
- 明示不同设备不会同步；浏览器清理数据后收藏可能丢失。

## 5. 数据源与采集策略

### 5.1 必要数据源

1. `GET /search/repositories`：发现公开项目，支持 `created:`、`language:`、`topic:` 等过滤。
2. `GET /repos/{owner}/{repo}`：补齐仓库详情、最新 Star 数、Fork、许可证等。
3. `GET /repos/{owner}/{repo}/stargazers/count`：按需取得当前 Star 数（若返回的仓库详情已有可信 `stargazers_count`，不重复调用）。
4. `GET /repos/{owner}/{repo}/stargazers/history`：历史每日新增 Star，P2 用于历史补齐、展示日历趋势；**不是精确滚动 24h 净增长**。
5. README 与文件列表相关官方端点：按需获取，适当缓存。

请求均带 `Accept: application/vnd.github+json` 和 `X-GitHub-Api-Version: 2026-03-10`，版本号集中在配置中以便升级。

### 5.2 初始发现 + 增量采集

- 首次启动默认回填过去 **7 天**的仓库；起始窗口可在管理后台改为 1/3/7/30 天，但大范围回填必须具备进度显示和限流机制。
- 按创建时间切片查询，初始 6 小时窗口；若某查询 `total_count` 过大（默认安全阈值 900）则自动继续拆分窗口。单个切片最多检索前 1000 项，不能因为拿到前 1000 项就认定全量完成。
- 查询返回 `incomplete_results=true` 时标记窗口不完整，并尝试缩小窗口或排队重试。
- 游标记住 `window_from,window_to,page,query_id`；宕机重启能够继续；每个任务写日志。
- 正常每 30 分钟执行一次增量发现；使用 10 分钟重叠窗口防漏。每天再做一次过去 48 小时的补采，缓解 GitHub 搜索索引延迟。
- 查询优先级：基础时间切片覆盖优先，其次主题/语言精细发现；在 API 预算内工作，不做无意义全量扫描。
- `github_id` 是稳定去重键；`full_name` 可随仓库重命名而变化。若 API 返回 301/重命名，更新映射，不生成重复项目。
- 若拆分到合理粒度仍超过结果上限，记录 `coverage_incomplete` 告警；不能声称“全站无遗漏”。

### 5.3 仓库更新和采样

- 新发现仓库立刻写入基本信息和初始 Star 快照。
- 24h 热门/收藏优先的仓库每 1 小时更新；普通仓库每 6 小时；长期不活跃仓库每 24 小时更新。频率在后台可配置，调度必须遵守限流。
- 对一次成功的仓库信息更新，使用事务写入仓库字段与 Star 快照；相同仓库同一采样时间不重复写。
- Star 快照记录“请求实际采样时间”，不使用计划执行时间冒充。
- 404/410 项目标记不可访问，保留历史和重新检查机制；不立即物理删除。
- 支持条件 GET（ETag / `If-None-Match`）；304 时更新采集状态但**不新造 Star 数值**。
- GitHub Trending 没有稳定承诺的官方公开 REST 接口，P1 不依赖它。P2 若加入抓取必须评估服务条款及访问规则，支持关闭、失败回退，并标注来源；失败时仍可使用本站自己的增长榜。

## 6. Star 指标与排名算法（不可随意改口径）

### 6.1 基础定义

- `current_stars`：最近一次成功获取的 GitHub 当前 Star 总数。
- `delta_24h`：当前 Star 快照减去目标时间 `now-24h` 之前或附近可信的基准快照。
- `delta_7d`：当前 Star 快照减去目标时间 `now-168h` 之前或附近可信的基准快照。
- `age_days`：当前时间与 GitHub 仓库创建时间的差。
- `data_age_seconds`：当前时间与最新成功同步时间的差。

**采样规则：** 优先选用“最接近目标时刻，且误差不超过 2 小时”的快照作基准；超过容差就返回 `null`，并提供 `insufficient_history` 标记。对于窗口内新创建且能确认创建时 Star 为 0 的仓库，可在规则明确的前提下用 0 作为起点，并在 API 中标明 `baseline_type=created_in_window`。允许负增长（取消 Star），不可强行截成 0。

`/stargazers/history` 的日粒度新增数据可以辅助历史趋势，**不得不经说明直接转换成精确 24h 净 Star 增量**；日界不保证与 UTC 对齐，展示时要区分其方法和本地快照。

### 6.2 潜力项目评分（初始可配置版本）

候选条件：非 Fork、未归档、创建不超过 30 天、当前 Stars 不超过 3000、最近 7 天增长数据可信且 `delta_7d >= 5`。

公式（值归一化到 0–100 分）：

```
score = 100 * (
  0.45 * normalize(log1p(max(delta_24h, 0))) +
  0.30 * normalize(log1p(max(delta_7d, 0))) +
  0.15 * freshness_score +
  0.10 * documentation_score
)
```

- `normalize` 的区间根据同一批候选项目的 P5–P95 分位计算，超出截断到 `[0,1]`；样本过少则使用固定保底上下界。
- `freshness_score`：创建越近分数越高；仓库已归档为 0。
- `documentation_score`：README、描述、License、最近推送等可验证信号；不是安全评级。
- 列表展示“为何上榜”：如“7 天 +36 Star / 最近更新 / 有部署文档”；管理员可配置阈值和权重，记录算法版本。
- 严禁把该分数显示为 GitHub 官方评分。

### 6.3 历史榜单

P1 优先实时查询 / 缓存；P2 增加每日榜单快照，支持查看历史排名。历史榜单不可在后续重新计算时悄悄更改已公布的榜单，需保留计算时间和版本。

## 7. 技术选型及架构

### 7.1 默认技术栈

| 层 | 技术 | 说明 |
| --- | --- | --- |
| 前端 | Vue 3 + TypeScript + Vite | 单页应用；移动适配 |
| UI | Tailwind CSS + 可复用组件 | 风格统一、主题切换 |
| 图表 | Apache ECharts | Star 折线图；按需加载 |
| 后端 | Go + `net/http` / chi | REST API、静态资源托管、采集调度 |
| 数据存储 | SQLite（WAL）+ FTS5 | 单机低运维；迁移脚本版本化 |
| 定时任务 | Go 内部任务队列 + 单实例调度器 | 避免重复任务、支持续跑 |
| 缓存 | 进程内 TTL 缓存 + HTTP 缓存头 | MVP 不引入 Redis |
| 部署 | 多阶段 Dockerfile + Docker Compose | 最终可采用一个业务容器 |
| 入口 | 宝塔 Nginx / Nginx | HTTPS 和反向代理由用户服务器管理 |

依赖版本以实施时稳定版本为准，写入锁定文件；生产镜像尽量使用非 root 用户。前后端在构建阶段分别编译，Go 服务生产环境托管 `frontend/dist` 和 `/api/v1`。

### 7.2 运行链路

```
GitHub REST API / 可选 Trending
             │
      GithubClient（带限流）
             │
      Job Scheduler + Queue
             │
       Normalize / Dedupe
             │
      SQLite（WAL + FTS）
             │
    RankingService / SearchService
             │
        Go REST API
             │
       Vue 3 SPA
             │
      VPS + Docker Compose
             │
       Nginx / HTTPS
```

P1 不需要 PostgreSQL、Redis、消息队列或微服务；只有明确遇到容量瓶颈才引入。

### 7.3 单实例和并发要求

- Web API 与定时采集可位于同一 Go 进程，但需要互相隔离的超时和资源限制。
- 数据库设置 `PRAGMA journal_mode=WAL`、`foreign_keys=ON`、合理 `busy_timeout`；写入事务短小且批量执行。
- 在容器内用作业租约（DB 锁或等价机制）避免相同采集任务并发执行。
- API 请求默认低并发、令牌桶控制；次级限流优先遵守 GitHub 的退避要求。

## 8. 数据库设计（P1 必要表）

> 字段是最低契约；开发时可以添加内部字段，但不得删除核心主键、时间与状态字段。迁移文件必须进入版本库。

### 8.1 `repositories`

- `id`：本地 INTEGER PRIMARY KEY。
- `github_id`：GitHub 全局 ID，UNIQUE NOT NULL。
- `owner`, `name`, `full_name`, `html_url`, `homepage_url`。
- `description`, `description_zh`（可为空）。
- `language`, `license_spdx`, `default_branch`, `topics_json`。
- `stars_count`, `forks_count`, `open_issues_count`。
- `is_fork`, `is_archived`, `is_disabled`, `is_hidden`。
- `github_created_at`, `github_updated_at`, `pushed_at`。
- `first_seen_at`, `last_synced_at`, `last_successful_fetch_at`, `sync_status`。
- 索引：`github_id UNIQUE`、`full_name UNIQUE`、`github_created_at DESC`、`stars_count DESC`、`last_synced_at`、`language`、`is_hidden`。

### 8.2 `star_snapshots`

- `id`, `repo_id`（FK）, `star_count`, `captured_at`, `source`, `sample_status`。
- 关键索引 `(repo_id, captured_at DESC)`；重复采样用稳定去重键或时间粒度约束处理。
- 保留原始采样用于滚动增长，禁止由图表层随意补零。

### 8.3 其他必需表

| 表 | 关键字段和作用 |
| --- | --- |
| `categories` | id、slug UNIQUE、title、description、sort_order、enabled |
| `category_rules` | category_id、match_type(topic/keyword/language)、pattern、priority、enabled |
| `repo_categories` | repo_id、category_id、is_primary、source(auto/manual)；手动覆盖优先 |
| `collection_windows` | id、source、query_hash、time_from、time_to、page、cursor_json、coverage_status、last_error |
| `collector_jobs` | id、job_type、enabled、interval_seconds、next_run_at、priority、config_json |
| `job_runs` | id、job_id、started_at、finished_at、status、request_count、created_count、updated_count、error_summary |
| `github_credentials` | id、label、auth_identity、token_ciphertext、token_hint、enabled、status、last_checked_at |
| `api_rate_limits` | credential_id、resource、limit_value、remaining、reset_at、cooldown_until、updated_at |
| `admin_users` | id、username UNIQUE、password_hash、created_at、last_login_at |
| `admin_sessions` | id、admin_id、session_hash、expires_at、created_at、revoked_at |
| `site_settings` | key UNIQUE、json_value、updated_at；敏感值不可明文保存 |
| `audit_logs` | id、admin_id、action、target、result、created_at、request_id |

P2 新增：`star_history_daily`、`rank_snapshots`、`ai_summaries`、`subscriptions`、`notification_deliveries`、`articles` 等。

FTS 索引必须与仓库增删改同步，避免更新后搜索不到。删除仓库与隐藏项目是不同操作：默认支持隐藏，不做强制物理删除。

## 9. HTTP API 规范

统一返回 JSON；公共 API 不要求登录，管理 API 强制认证。字段名采用 `snake_case`，ISO 8601 时间戳包含时区（建议 `Z`）。

成功示例：

```json
{
  "data": [{"github_id": 123, "full_name": "owner/repo", "stars_count": 120, "delta_24h": null}],
  "meta": {"page": 1, "limit": 20, "total": 317, "data_updated_at": "2026-10-23T00:00:00Z"}
}
```

失败示例：

```json
{
  "error": {"code": "VALIDATION_ERROR", "message": "Unsupported sort field"},
  "request_id": "req-example"
}
```

### 9.1 公共 API

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/v1/stats` | 首页统计与最新采集时间 |
| GET | `/api/v1/rankings?type=latest&category=ai&language=Go&page=1&limit=20` | 榜单；`type=latest,24h,7d,potential` |
| GET | `/api/v1/repos?q=agent&sort=stars_desc&page=1&limit=20` | 本地搜索、排序、筛选 |
| GET | `/api/v1/repos/:owner/:repo` | 仓库详情 |
| GET | `/api/v1/repos/:owner/:repo/stars?window=30d` | 时间序列与数据方法说明 |
| GET | `/api/v1/repos/:owner/:repo/readme` | 缓存 README（经过净化处理） |
| GET | `/api/v1/categories` | 分类列表及数量 |
| GET | `/api/v1/categories/:slug` | 分类信息 |
| GET | `/healthz` | 进程健康检查 |
| GET | `/readyz` | 服务和数据库就绪检查 |

### 9.2 管理 API

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| POST | `/api/v1/admin/auth/login` | 管理员登录，设置安全 Session Cookie |
| POST | `/api/v1/admin/auth/logout` | 退出并注销 Session |
| GET | `/api/v1/admin/overview` | 采集健康与概览 |
| GET/POST | `/api/v1/admin/credentials` | 列表（脱敏）/添加凭据 |
| POST | `/api/v1/admin/credentials/:id/test` | 测试 Token 与身份 |
| PATCH/DELETE | `/api/v1/admin/credentials/:id` | 启用、禁用、删除 |
| GET | `/api/v1/admin/jobs` | 列出计划和运行状态 |
| POST | `/api/v1/admin/jobs/:id/run` | 手动触发（排队而非无限并发） |
| PATCH | `/api/v1/admin/jobs/:id` | 修改频率、开关 |
| GET | `/api/v1/admin/job-runs` | 运行明细及失败原因 |
| GET/POST/PATCH/DELETE | `/api/v1/admin/categories` | 分类管理 |
| POST | `/api/v1/admin/repos/import` | 手动录入 GitHub 仓库 URL |
| PATCH | `/api/v1/admin/repos/:id` | 隐藏/恢复、修改分类 |
| GET/PATCH | `/api/v1/admin/settings` | 站点设置（敏感字段脱敏） |
| GET | `/api/v1/admin/audit-logs` | 操作日志 |
| POST | `/api/v1/admin/backups` | 创建备份 |
| GET | `/api/v1/admin/backups` | 列出备份 |

管理员接口务必做服务端权限校验、参数验证、CSRF 防护与审计记录。管理请求不能使用仅在前端隐藏按钮的方式防护。

### 9.3 筛选参数标准

- `page` 默认 1；`limit` 默认 20，最大 100。
- `sort` 必须是固定枚举，不能直接拼入 SQL。
- 所有搜索关键词和分类字段长度限制，采用参数化查询；返回 `meta.total` 和 `meta.data_updated_at`。
- 公开列表加服务端缓存，缓存 TTL 依数据实时性配置（建议 60–180 秒）。
- 后续如采用游标分页，必须兼容已有 URL 与排序语义。

## 10. GitHub 多凭据与限流调度

### 10.1 需求

管理员可以添加多个**有合法使用权限**的 GitHub 凭据，包括个人访问令牌（PAT）以及将来扩展的 GitHub App Token。系统展示凭据名称、认证身份、脱敏 Token 末尾、验证状态、`core/search/graphql` 资源的限额与恢复时间。

- 可以标记主凭据与备用凭据；备用用于凭据被撤销、权限不符等合法故障转移，不能设计为“消耗完一个账号额度立刻切换下一个账号继续跑”。
- 同一 GitHub 用户的多个 Token 可能共享额度；不得把多个 Token 自动当作彼此独立的配额池。
- GitHub API 请求统一经过 `GitHubClient`，业务代码不得绕过限流器直接调用。
- 优先从每次响应头读取 `x-ratelimit-limit`、`x-ratelimit-remaining`、`x-ratelimit-reset`、`x-ratelimit-resource`，必要时调用 `/rate_limit` 核实。

### 10.2 错误处理

| 情形 | 动作 |
| --- | --- |
| 401 | 凭据标记 `invalid`；提醒管理员；允许切换合法备用凭据 |
| 403 权限不足 | 标记 `forbidden`，停止相应受限接口并报警 |
| 403/429 主要限流 | 按 `x-ratelimit-reset` 等待；不继续请求 |
| 403/429 次级限流 | 遵守 `retry-after`，否则按官方建议至少等待 1 分钟并指数退避 |
| 网络超时 / 5xx | 有上限的指数退避 + 抖动；重试队列记录次数 |
| 304 | 使用已有数据，不虚构变化；适用时保留 ETag |
| 422 查询错误或结果截断 | 校验参数/拆分时间窗口，记录覆盖不完整 |

默认并发保守（例如 2 个并发请求），限制每类任务请求预算；禁止无限重试和请求风暴。所有 Token 加密存储：密钥从容器环境变量读取，数据库只保留密文；前端和日志只显示脱敏信息。

## 11. AI 中文解读（P2）

- 管理员在后台配置兼容 OpenAI 风格的 API 地址、模型、密钥和每日成本/请求限额；未配置时系统照常运行。
- AI 输出结构化字段：一句话总结、核心功能、典型用途、适合人群、部署方式线索、项目局限、内容生成时间、模型版本。
- 先从仓库 description 和 README 的**有限摘要**进行分析；README 属于不可信输入，不得执行其中的提示、脚本或命令。
- 对同一仓库相同内容 hash + 模型 + prompt 版本复用缓存，避免重复花费。
- 生成内容明确标记“AI 生成，仅供参考”，部署和安全相关论断必须能对应原始文件证据；不编造 Docker 支持、许可证或功能。
- 所有 AI 请求都必须具备超时、重试上限、队列节流、敏感信息过滤；外部自定义 API 地址要防止 SSRF 访问本机、内网和云元数据地址。

## 12. 安全、隐私与运维

### 12.1 管理员安全

- 首次部署后用 CLI 交互创建管理员：`docker compose exec app /app/radar admin create`；**无默认管理员密码**。
- 密码使用 Argon2id（或经过审查的同级 KDF）哈希；登录限速、失败记录、会话有效期和登出注销。
- Session Cookie 使用 `HttpOnly`、`SameSite=Lax`；生产 HTTPS 下启用 `Secure`；敏感管理操作校验 CSRF Token 和 Origin。
- `/admin` 路由和管理 API 不允许被搜索引擎索引；后台角色最小权限，保留登录日志。
- 采用安全响应头、内容安全策略（CSP）、Markdown 净化、SQL 参数化和服务端校验；对外链使用 `rel="noopener noreferrer"`。

### 12.2 数据保护与备份

- `/app/data` 是唯一核心持久化目录，里面包含 SQLite 数据库、WAL、备份和必要的本地缓存。
- SQLite 备份采用在线备份或 `VACUUM INTO` 等一致性机制；不得在数据库写入时简单复制单个 `.db` 当作完整备份。
- 默认每天备份一次：保留最近 7 个每日备份和 4 个每周备份；提供手动创建、下载和恢复前校验。
- 管理员通过 CLI 完成恢复操作，恢复前自动创建当前备份；明确告知需要一起保留 `APP_ENCRYPTION_KEY` 才能解密旧 Token。
- 日志做大小轮转与保留期（默认 14–30 天）；不输出明文 Token、密码、AI Key。
- 不收集 GitHub stargazer 用户的个人身份列表；榜单只需聚合数值。

### 12.3 运行健康

- `/healthz` 返回进程正常；`/readyz` 包含数据库读写就绪检查（只进行轻量查询）。
- 管理后台展示：最后一次成功采集、待处理任务数、失败任务数、搜索覆盖缺口、凭据状态、API 请求次数与 429 次数、SQLite 文件体积。
- 定时任务重启后从断点继续；启动时不会并发补跑大量过期任务导致拥塞。
- 生产日志输出结构化 JSON，字段包括 `timestamp,level,component,job_id,request_id,error_code,duration_ms`。
- 前台页脚加“独立第三方开源发现平台，非 GitHub 官方产品”声明；尊重第三方品牌与数据使用约束。

## 13. Docker Compose 部署契约

### 13.1 项目目录

```text
github-radar/
├── README.md
├── DEVELOPMENT.md
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── .gitignore
├── backend/
│   ├── cmd/radar/
│   ├── internal/{api,auth,config,db,github,jobs,ranks,search,service}/
│   └── migrations/
├── frontend/
│   ├── src/{pages,components,stores,router,api}/
│   └── package.json
├── docs/{API.md,DEPLOYMENT.md,DECISIONS.md}/
├── tests/
└── data/                  # 运行时创建；不得提交 Git
    ├── radar.db
    ├── backups/
    └── cache/
```

### 13.2 `docker-compose.yml` 参考（必须与代码实现一致）

```yaml
name: github-radar

services:
  app:
    build: .
    image: github-radar:local
    container_name: github-radar
    restart: unless-stopped
    ports:
      - "127.0.0.1:${HOST_PORT:-18080}:8080"
    env_file:
      - .env
    environment:
      APP_PORT: "8080"
      DATABASE_PATH: "/app/data/radar.db"
    volumes:
      - ./data:/app/data
    healthcheck:
      test: ["CMD", "/app/radar", "healthcheck"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 30s
    security_opt:
      - no-new-privileges:true
```

实现 Dockerfile 时，将 Go 可执行文件放在 `/app/radar`，实现 `serve`、`healthcheck`、`admin create`、`backup` 等子命令；数据库目录由容器中非 root 用户持有，`./data` 权限不足时给出可理解错误提示。

### 13.3 `.env.example` 参考

```dotenv
HOST_PORT=18080
PUBLIC_BASE_URL=https://radar.example.com
APP_ENV=production
APP_TIMEZONE=Asia/Shanghai
APP_SECRET=replace-with-long-random-secret
APP_ENCRYPTION_KEY=replace-with-base64-encoded-32-byte-key
# 可选：首次运行时从环境变量导入 GitHub Token；推荐安装后在后台安全添加
GITHUB_TOKEN=
# 可选：P2 AI 服务（未设置时自动禁用）
AI_ENABLED=false
AI_BASE_URL=
AI_MODEL=
AI_API_KEY=
```

实现时必须验证密钥格式、长度、时区、PUBLIC_BASE_URL；禁止使用示例密钥启动生产服务。`.env` 加入 `.gitignore`，推荐文件权限 `0600`。

### 13.4 部署命令与代理

```bash
mkdir -p /opt/github-radar/data
cd /opt/github-radar
cp .env.example .env
# 编辑 .env，写入真正的随机密钥和站点域名
docker compose up -d --build
docker compose exec app /app/radar admin create
docker compose ps
docker compose logs --tail=100 app
```

- 默认只在 VPS 本地监听 `127.0.0.1:18080`。
- 在宝塔或 Nginx 中新建网站，以 HTTPS 反向代理到 `http://127.0.0.1:18080`；同时透传真实协议和客户端 IP 相关头，应用只信任配置的代理来源。
- 若宝塔和 Docker 不在同一网络命名空间，应使用服务器可达地址并通过防火墙/专网限制，而不是盲目开放管理端口。
- 更新：拉取新代码或新镜像 → 备份 → `docker compose up -d --build` → 检查迁移与健康；回滚需兼顾数据库迁移兼容性。
- 数据始终在 `./data` 中；删除容器不删除数据，卸载数据需明确二次确认。

## 14. 非功能性验收要求

1. **真实性**：生产页面不包含演示项目或假统计；所有数值能追溯到仓库和采集时间。
2. **正确性**：无历史样本时 `delta_24h`/`delta_7d` 为 `null`；时间、负增长、仓库更名、429、301、304 都有测试。
3. **可运行性**：干净的 Linux VPS 能以 Docker Compose 启动；重启后数据保留，首次管理员创建流程可用。
4. **响应性**：375px 手机、768px 平板和 1440px 桌面视口下无横向溢出；排行榜、搜索、详情可操作。
5. **性能目标**：在 2 vCPU/2GB RAM、约 10,000 仓库和 100,000 快照的基准数据下，已缓存公开列表 API 的 p95 响应时间目标 < 500ms；写出测试方法，不作为任何服务器上的无条件保证。
6. **稳定性**：GitHub API 中断 1 小时时，网站仍能访问已有缓存，页面明确显示数据陈旧；采集恢复后能续跑。
7. **安全性**：Token 不出现在前端构建产物、网页接口响应和日志；匿名用户无法调用后台 API；管理登录具备暴力破解防护。
8. **测试**：Go 单元/集成测试，Vue 组件测试，关键路径 E2E，采集器假 GitHub API 夹具测试，Docker 启动烟测。
9. **可观测性**：任务失败可定位到任务、时间窗口和错误码；具备管理日志和手动重试能力。
10. **可迁移性**：备份恢复后仓库、榜单、分类、管理员可用，敏感 Token 解密需要独立保管的加密密钥。

## 15. 分阶段开发任务和完成定义

### P1.1 项目骨架与部署

- [ ] 建立前后端项目、锁定依赖、基础目录、Dockerfile、Compose、环境示例。
- [ ] 实现 SQLite 迁移、健康检查、管理员初始化和会话登录。
- [ ] 实现前端公共布局、管理员布局、主题和响应式导航。
- **验收**：新机器可运行，管理员登录成功，重启持久化有效。

### P1.2 GitHub 采集引擎

- [ ] GitHubClient、认证凭据加密与测试、响应头额度跟踪。
- [ ] 时间切片发现、分页、重名去重、断点续采、每日补采。
- [ ] 仓库详情更新、Star 快照、限流退避、作业状态与日志。
- **验收**：用模拟 GitHub API 稳定复现 200/304/401/403/429/5xx/422 情形；真实公开仓库收录成功。

### P1.3 前台完整闭环

- [ ] 首页、排行榜、探索、分类、项目详情、README、本地收藏。
- [ ] 排名算法、分页筛选、搜索、数据不足状态、同步时间。
- [ ] 移动端与明暗主题；无数据/错误/加载状态。
- **验收**：可从首页进入真实项目详情；榜单数据正确；筛选组合和浏览器收藏正常。

### P1.4 后台与质量加固

- [ ] 凭据、任务、日志、分类、项目隐藏和设置。
- [ ] 备份恢复、审计日志、管理员安全、限流保护。
- [ ] README、部署说明、API 文档、测试、Docker 烟测。
- **验收**：执行第 14 节全部关键验收测试，P1 可在生产 VPS 长期运行。

### P2 与 P3

仅在 P1 完成后进入。每增加一个功能，都要同时提供 API、界面、日志、权限、测试和关闭/降级路径。不要把 P2/P3 伪装成已完成的 P1 功能。

## 16. 开发完成时必须交付的文件

- 可构建完整源码及可复现的依赖锁定文件。
- `README.md`：项目特色、截图、功能矩阵、快速开始。
- `docs/DEPLOYMENT.md`：完整 Docker Compose 部署、宝塔/Nginx 反代、备份恢复、升级回滚。
- `docs/API.md`：公共接口、后台接口、示例请求、错误码。
- `docs/DECISIONS.md`：实施时补充的设计决策及原因。
- `.env.example`：不含真实秘钥。
- `Dockerfile`、`docker-compose.yml`、数据库迁移文件。
- 测试套件、GitHub 模拟响应夹具、基础 CI 工作流。
- 若提供界面截图，截图应取自真正运行的前端，而不是生成的概念图。

## 17. 官方参考资料与实现前核对项

1. GitHub 仓库搜索语法：<https://docs.github.com/en/search-github/searching-on-github/searching-for-repositories>
2. GitHub REST API 速率限制及次级限流：<https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api?apiVersion=2026-03-10>
3. GitHub REST API 最佳实践（条件请求、退避）：<https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api>
4. GitHub Star 数和日粒度历史 API：<https://docs.github.com/en/rest/activity/starring?apiVersion=2026-03-10>
5. GitHub REST API 版本说明：<https://docs.github.com/en/rest/about-the-rest-api/api-versions>
6. GitHub REST API 分页：<https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api>
7. GitHub Trending 页面（非稳定官方 REST 接口）：<https://github.com/trending>

**开发前必须再次验证**：当前 GitHub API 版本、各端点的实际权限、Search API 返回范围/上限、Star History 可用性与响应结构、GitHub 使用条款。使用模拟响应做单元测试，但上线前必须对真实公开仓库执行集成测试。对接口不可用情况提供降级，不允许用伪造数据掩盖。

---

## 最终给 AI 的目标确认

请把本项目开发为真实可用的 **GitHub Radar 自托管网站**，以 `docker compose up -d --build` 能运行、GitHub 自动采集正常、前台能查真实项目、后台可管理、多凭据与限流合规、数据可备份恢复为“完成”标准。首先只执行 P1.1，验收通过后继续 P1.2 → P1.3 → P1.4；每阶段提供测试结果及对应变更，不要只输出计划或 UI 原型。
