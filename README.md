# Running Tools

[![Build and publish](https://github.com/Bringbasket/hme-tools/actions/workflows/build.yml/badge.svg)](https://github.com/Bringbasket/hme-tools/actions/workflows/build.yml)
[![Go version](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go&logoColor=white)](go.mod)
[![Vue](https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white)](frontend/package.json)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15%2B-4169E1?logo=postgresql&logoColor=white)](docs/TECH_STACK.md)

Running Tools 是一个模块化、自托管的管理工具平台。项目以邮件系统为当前核心模块，统一提供
多账号 iCloud 隐藏邮箱管理、Session 维护、自动创建任务、IMAP 收件、操作审计和系统更新能力，
并为后续工具模块保留清晰的前后端边界。

> [!IMPORTANT]
> 本项目不是 Apple 官方产品。请仅管理本人拥有或已获得明确授权的账号与数据，并遵守 Apple
> 服务条款及所在地法律。Apple Account 密码、Session Cookie、IMAP 专用密码、代理凭据和 API
> 令牌均属于敏感信息，禁止提交到 Git、Issue 或公开日志。

## 核心能力

### 邮件系统

- **多账号隔离**：每个母号拥有独立的 Session、代理、自动刷新、创建计划、IMAP 配置和使用日志。
- **两种授权方式**：支持 Apple Account 协议登录，以及从 iCloud Web 请求导入 cURL 或 HAR。
- **隐藏邮箱管理**：查询、搜索、分页、创建、启用、停用、删除、批量清理和 CSV 导出。
- **后台自动创建**：由 Go Worker 按账号执行持久化计划，不依赖浏览器页面或宝塔计划任务。
- **收件与验证码**：支持 IMAP 同步、IDLE 等待、邮件详情和验证码提取；每个隐藏邮箱最多保留
  最新 100 封邮件，并受账号级缓存总量限制。
- **独立网络出口**：账号代理同时用于 Apple HTTP 请求和 IMAP 链路，避免同一账号出口不一致。
- **取件接口**：支持单个或批量生成限时取件地址，访问地址直接返回最新邮件的紧凑 JSON。
- **兼容接口**：主要接口位于 `/api/mail/v1`，同时保留原 `hme-manager` 的 `/v1` 兼容入口。

### 平台能力

- PostgreSQL 用户、浏览器会话、API 令牌和登录事件持久化。
- Argon2id 密码哈希、HttpOnly Cookie、来源校验、登录限流和会话撤销。
- 按一级模块隔离的操作日志与数据库清理能力。
- Vue 模块注册、可折叠侧栏、移动端导航和深浅色主题。
- 基于 GHCR 镜像的版本检查、人工确认更新、健康检查、失败回滚和自动重启。
- 独立工具箱模块，当前包含纯前端的保本测算工具。

## 系统架构

```text
Browser
  -> Vue 3 platform shell
     -> Mail module / Tools module
  -> Go HTTP service
     -> Authentication and API tokens
     -> Mail account workers
        -> Apple Account / iCloud Web
        -> IMAP
     -> PostgreSQL 15+
     -> Redis 7+

GitHub Actions -> GHCR image -> Host update service -> Application container
```

| 层级 | 技术与职责 |
| --- | --- |
| 后端 | Go 1.24+、Ent；HTTP 路由当前使用标准库保持兼容，Gin 为统一网关演进基线 |
| 前端 | Vue 3、TypeScript、Vite、Vue Router、Vitest |
| 持久化 | PostgreSQL 15+ 保存业务、认证和日志数据；Redis 7+ 提供协调锁等短期状态 |
| 发布 | 多阶段 Docker 构建；`-tags embed` 将前端静态资源嵌入 Go 二进制 |
| 运维 | Docker Compose、GHCR、宿主机 systemd 更新服务 |

生产模式以 PostgreSQL 为唯一业务数据源。旧 JSON 支持仅用于历史部署迁移；系统更新请求文件
是容器与宿主机之间的受限通信协议，不属于业务数据库。

## 项目结构

```text
running-tools/
|-- cmd/server/                 # 服务入口与模块组装
|-- internal/platform/          # 鉴权、持久化、日志、HTTP 与更新基础设施
|-- internal/webui/             # 前端嵌入与静态资源处理
|-- modules/
|   |-- mail/
|   |   |-- backend/            # iCloud、IMAP、任务、API 与测试
|   |   |-- frontend/           # 邮件模块页面、组件与类型
|   |   `-- docs/               # 邮件模块文档
|   |-- tools/
|   |   |-- frontend/           # 工具箱页面、计算逻辑与测试
|   |   `-- docs/               # 工具箱说明
|   `-- _template/              # 新业务模块模板
|-- frontend/                   # 平台外壳、路由、鉴权和公共组件
|-- deploy/                     # 宿主机更新脚本与 systemd 单元
|-- docs/                       # 架构、API、开发、迁移和部署文档
`-- scripts/                    # 构建与旧数据迁移脚本
```

新增业务应在 `modules/<module-name>` 建立独立目录，避免把模块业务写入平台外壳或其他模块。

## 快速开始

### Docker Compose

推荐使用 Docker Compose。该配置只创建本项目的应用、PostgreSQL、Redis、内部网络和命名卷，
不会操作其他 Compose 项目或容器。

```bash
git clone https://github.com/Bringbasket/hme-tools.git
cd hme-tools
cp .env.example .env
```

至少修改 `.env` 中以下两个值：

```dotenv
RUNNING_POSTGRES_PASSWORD=replace-with-a-long-random-password
RUNNING_REDIS_PASSWORD=replace-with-another-long-random-password
```

启动服务：

```bash
docker compose --env-file .env -f compose.server.yml up -d
docker compose --env-file .env -f compose.server.yml ps
curl http://127.0.0.1:8091/health
```

浏览器打开 <http://127.0.0.1:8091>。数据库首次初始化时使用：

- 用户名：`admin`
- 初始密码：`admin123`

首次登录必须立即设置新密码。生产环境应通过 HTTPS 反向代理访问，不应直接向公网暴露应用端口。

### 本地开发

本地开发需要 Go 1.24+、Node.js 22+、npm 和 PostgreSQL 15+。复制 `.env.example` 后设置本地
数据库连接；Redis 未配置时，单实例协调锁会降级为进程内锁。

```dotenv
RUNNING_STORAGE_MODE=postgres
RUNNING_DATABASE_URL=postgres://running_tools:password@127.0.0.1:5432/running_tools?sslmode=disable
RUNNING_ADDR=127.0.0.1:8000
RUNNING_DATA_DIR=./data
```

构建前端并启动 Go 服务：

```bash
cd frontend
npm ci
npm run build
cd ..
go run ./cmd/server
```

需要前端热更新时分别运行：

```bash
# Terminal 1
go run ./cmd/server

# Terminal 2
cd frontend
npm run dev
```

Vite 默认监听 <http://127.0.0.1:5173>，并将 `/api`、`/v1` 和 `/health` 转发到
`http://127.0.0.1:8000`。可使用 `RUNNING_DEV_PROXY` 修改后端地址。

## 关键配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `RUNNING_ADDR` | `:8000` | Go 服务监听地址 |
| `RUNNING_DATABASE_URL` | 无 | 完整 PostgreSQL 连接串；非 Compose 部署必须配置 |
| `RUNNING_POSTGRES_PASSWORD` | 无安全默认值 | Compose PostgreSQL 密码，部署前必须修改 |
| `RUNNING_REDIS_PASSWORD` | 无安全默认值 | Compose Redis 密码，部署前必须修改 |
| `RUNNING_AUTH_SESSION_HOURS` | `168` | 浏览器会话有效期，单位为小时 |
| `RUNNING_TRUST_PROXY` | `false` | 仅在可信 HTTPS 反向代理后启用 |
| `RUNNING_IMAGE` | `ghcr.io/bringbasket/running-tools:latest` | 生产部署和在线更新使用的镜像 |
| `MAIL_AUTO_REFRESH_INTERVAL` | `600` | 邮件 Session 自动检查间隔，单位为秒 |
| `HME_MAIL_SYNC_ENABLED` | `false` | 是否默认启用后台 IMAP 同步 |
| `HME_IMAP_CACHE_MAX_MESSAGES` | `5000` | 单个母号的邮件缓存总上限 |

完整配置及注释见 [.env.example](.env.example)。IMAP 密码、账号代理和 Apple 授权材料可以在
前端按账号配置，敏感原文不会通过查询接口回传。

## 构建与验证

```bash
# Go
go vet ./...
go test ./...

# Vue
cd frontend
npm ci
npm run typecheck
npm run test:run
npm run build
npm audit --audit-level=high
```

Windows 也可在仓库根目录运行完整构建脚本：

```powershell
.\scripts\build.ps1
```

推送到 `main` 后，GitHub Actions 会执行前端类型检查与测试、Go 测试，并发布两个镜像标签：

```text
ghcr.io/bringbasket/running-tools:latest
ghcr.io/bringbasket/running-tools:<commit-sha>
```

发布版本采用 `0.0.<GitHub Actions 运行序号>`，commit SHA 作为独立构建标识。纯文档修改不会
触发应用构建；连续推送时只保留最新构建任务。在线更新后，侧栏会从后端读取新版本，无需手工
修改前端常量或服务器环境变量。

## 生产部署与更新

生产服务器只需要保留 `.env`、`compose.server.yml` 和 `data/`，无需存放业务源码。应用容器
不会挂载 Docker Socket；网页只负责提交检查或更新请求，真正的镜像拉取、重启、健康检查和
失败回滚由宿主机 systemd 服务完成。

GHCR 包的可见性与 GitHub 仓库可见性相互独立。私有包需要先使用具备 `read:packages` 权限的
GitHub PAT 登录 GHCR。完整部署与在线更新步骤见 [生产部署文档](docs/DEPLOYMENT.md)。

升级前必须备份 PostgreSQL 数据卷。不要把 `.env`、`data/` 或数据库备份提交到仓库。

## API 与认证

- 浏览器使用服务端签发的 `HttpOnly`、`SameSite=Strict` Session Cookie。
- 自动化脚本应在系统中创建可撤销 API 令牌，并发送 `Authorization: Bearer rtk_...`。
- 除健康检查、登录状态、登录接口和公开取件接口外，`/api/*` 与 `/v1/*` 默认要求认证。
- API 响应使用统一的 `ok`、`data`、`error`、`meta` 结构，并包含可追踪的 `requestId`。

接口清单、请求示例和兼容路径见 [HTTP API 文档](docs/API.md)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [系统架构](docs/ARCHITECTURE.md) | 模块边界、数据流、持久化和更新架构 |
| [技术栈规范](docs/TECH_STACK.md) | 技术基线、组件职责和演进约束 |
| [开发指南](docs/DEVELOPMENT.md) | 本地环境、构建、测试和模块开发流程 |
| [界面设计规范](docs/UI_GUIDELINES.md) | 布局、组件、响应式和交互约束 |
| [HTTP API](docs/API.md) | 认证、平台接口和邮件接口 |
| [使用日志规范](docs/LOGGING.md) | 模块日志边界、字段与清理行为 |
| [生产部署](docs/DEPLOYMENT.md) | 无源码部署、GHCR 和在线更新 |
| [数据迁移](docs/MIGRATION.md) | 旧版 HME 数据迁移流程 |
| [邮件模块](modules/mail/docs/README.md) | 邮件能力、限制和模块扩展说明 |
| [Apple 登录](modules/mail/docs/APPLE_LOGIN.md) | Apple Account 与 iCloud Web 授权流程 |

## 安全说明

- 初始管理员密码只用于首次初始化，部署后必须立即修改。
- 使用独立数据库用户和强随机密码，并限制 PostgreSQL、Redis 只在可信网络访问。
- 只有在受信任的 HTTPS 反向代理后才能启用 `RUNNING_TRUST_PROXY`。
- 不记录密码、Cookie、验证码、API 令牌、代理凭据或真实邮件正文。
- 邮件取件地址本质上是临时访问凭证，应设置合理有效期并通过 HTTPS 传输。
- 公开仓库不等于公开运行数据，提交前应检查 `git status` 和差异内容。

若发现安全问题，请避免在公开 Issue 中附带账号、Session、邮件内容或服务器凭据。

## 参与贡献

1. Fork 仓库并从 `main` 创建功能分支。
2. 将业务代码、页面、测试和文档放在对应模块中。
3. 运行 Go 与前端的完整验证命令。
4. 在 Pull Request 中说明行为变化、数据迁移影响和验证结果。

新增页面应遵守 [界面设计规范](docs/UI_GUIDELINES.md)；新增日志或持久化行为时，应同步更新
[使用日志规范](docs/LOGGING.md) 和相关迁移文档。

## 许可证

当前仓库尚未包含 `LICENSE` 文件。公开可见不代表自动授予复制、修改或再分发权；在维护者
选择并提交明确许可证前，本项目保留所有权利。
