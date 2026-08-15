# Running Tools

[![Build and publish](https://github.com/Bringbasket/running-tools/actions/workflows/build.yml/badge.svg)](https://github.com/Bringbasket/running-tools/actions/workflows/build.yml)

Running Tools 是一个模块化的自托管运维控制台。Go 服务负责鉴权、HTTP 服务、系统更新
和模块注册；每个业务模块独立维护自己的后端、前端、测试和文档。

当前包含两个模块：

- **邮件系统**：管理已授权 iCloud 账号的隐藏邮箱、Session、自动创建计划和 IMAP 收件箱；
- **工具箱**：提供纯前端的保本测算等本地计算工具。

项目面向个人和小型自托管部署，默认使用 PostgreSQL 持久化，不要求把业务源码放在生产
服务器上。当前邮件接口兼容原 `hme-manager` 的 `/v1` API，同时提供新的
`/api/mail/v1` 命名空间。

## 项目状态

项目已经公开仓库，但仍在持续开发中。邮件系统是当前的主要业务模块，后续功能会以
`modules/` 下的独立模块形式加入。接口、数据库迁移和页面结构可能随版本演进，生产升级前
请先备份 PostgreSQL 并阅读 [生产部署](docs/DEPLOYMENT.md)。

## 规范技术栈

后端规范为 **Go + Gin + Ent**，前端为 **Vue 3 + TypeScript + Vite**，数据层为
**PostgreSQL 15+ + Redis 7+**。当前 Ent、PostgreSQL、Redis 和 Vue 已接入；HTTP 网关
目前仍使用 Go 标准库以兼容现有接口，Gin 是后续统一网关规范。生产构建使用
`-tags embed` 将 Vue 前端资源嵌入单个 Go 二进制。

完整组件职责、当前接入状态和迁移顺序见 [技术栈规范](docs/TECH_STACK.md)。

## 项目目录

```text
running-tools/
|-- cmd/                         # 可执行程序入口
|-- internal/platform/           # Go 平台公共基础设施
|-- internal/webui/              # 嵌入 Go 程序的生产前端
|-- modules/
|   |-- mail/
|   |   |-- backend/             # 邮件业务、iCloud 客户端、API 和任务
|   |   |-- frontend/            # 邮件模块专属 Vue/TypeScript 前端
|   |   `-- docs/                # 邮件模块文档
|   `-- tools/
|       |-- frontend/            # 工具箱页面、计算逻辑和测试
|       `-- docs/                # 工具口径和扩展约定
|-- frontend/                    # 平台外壳、路由和公共界面
|-- deploy/                      # Docker 和宿主机集成文件
|-- docs/                        # 架构、API、开发和部署文档
`-- scripts/                     # 构建和数据迁移脚本
```

以后增加 `codex-app`、`cap-converter` 等功能时，应在 `modules/` 下建立新的
同级目录，不能把其他业务代码放进平台外壳或邮件模块。

## 当前功能

- 隐藏邮箱列表、搜索、分页、状态筛选、创建、启用、停用、删除和 CSV 导出；
- 可直接执行请求的 API 调试器，并生成对应 cURL；
- 国际版和中国大陆版 iCloud cURL/HAR Session 导入；
- Session 持久化、状态检查和服务端自动刷新；
- 邮箱创建计划由前端配置、Go 后台 Worker 持久执行；
- 从 GHCR 镜像更新、健康检查、失败回滚和自动重启；GHCR 包可单独设置为公开或私有；
- PostgreSQL 账号密码登录、可撤销浏览器会话、登录限流和独立 API 访问令牌；
- 生产统一使用 PostgreSQL，旧 JSON 仅用于兼容导入；通过 PostgreSQL 模式运行时，逻辑状态
  使用 `running_state` JSONB 保存，不依赖业务 JSON 文件；
- 桌面端可收起侧栏、移动端抽屉菜单和深浅色主题。
- 独立工具箱菜单，以及纯前端的保本测算（保本倍率、额度反推和利润试算）。

与原 `hme-manager` 的逐项迁移状态、已修复问题和仍未完全等价的能力，见
[邮件系统迁移审计](docs/MAIL_MIGRATION.md)。

## 快速开始

### 环境要求

- Go 1.24 或更高版本；
- Node.js 22 和 npm 11 或更高版本；
- PostgreSQL 15 或更高版本；
- Redis 7 或更高版本（多实例 IMAP 协调使用）。

### Docker Compose（推荐）

Compose 会创建本项目独立的应用、PostgreSQL 和 Redis 容器及数据卷，不会操作其他项目
的容器。首次使用：

```bash
cp .env.example .env
# 编辑 .env，至少修改 RUNNING_POSTGRES_PASSWORD、RUNNING_REDIS_PASSWORD
docker compose --env-file .env -f compose.server.yml up -d
```

打开 <http://127.0.0.1:8091>，使用初始账号 `admin`、密码 `admin123` 登录。首次登录必须
设置新密码；修改后密码哈希保存在 PostgreSQL。查看服务状态：

```bash
docker compose --env-file .env -f compose.server.yml ps
docker compose --env-file .env -f compose.server.yml logs --tail=100 app
```

### 本地 Go + Vite

本地直接运行 Go 服务时，需要先准备 PostgreSQL，并在 `.env` 中设置完整的
`RUNNING_DATABASE_URL`；Redis 未配置时，单实例锁会降级为进程内锁。服务会自动读取仓库
根目录的 `.env`，但不会覆盖已经存在的环境变量。

```bash
cp .env.example .env
# 将 RUNNING_DATABASE_URL 改为本地 PostgreSQL 连接串
cd frontend
npm ci
npm run build
cd ..
go run ./cmd/server
```

打开 <http://127.0.0.1:8000>，使用 `admin` 和初始密码登录。

Windows PowerShell 示例：

```powershell
cd D:\All\GPT\running-tools\frontend
npm ci
npm run build

cd ..
$env:RUNNING_ADMIN_USERNAME="admin"
$env:RUNNING_ADDR="127.0.0.1:8000"
$env:RUNNING_DATA_DIR="$PWD\data"
go run ./cmd/server
```

## 验证命令

```bash
go vet ./...
go test ./...

cd frontend
npm run typecheck
npm run test:run
npm run build
npm audit --audit-level=high
```

## 生产部署

生产服务器只需要保存 `.env`、`compose.server.yml` 和 `data/`。GitHub Actions 会在 `main`
分支构建并发布 GHCR 镜像，容器不会挂载 Docker Socket。网页提交更新请求后，由宿主机
systemd path 单元调用更新脚本。

GHCR 镜像的可见性独立于 GitHub 仓库可见性：公开包可以直接拉取，私有包需要具备
`read:packages` 权限的 GitHub PAT。完整安装和更新步骤见 [生产部署](docs/DEPLOYMENT.md)。

相关文档：

- [系统架构](docs/ARCHITECTURE.md)
- [技术栈规范](docs/TECH_STACK.md)
- [界面设计规范](docs/UI_GUIDELINES.md)
- [开发指南](docs/DEVELOPMENT.md)
- [HTTP API](docs/API.md)
- [使用日志规范](docs/LOGGING.md)
- [数据迁移](docs/MIGRATION.md)
- [生产部署](docs/DEPLOYMENT.md)
- [邮件系统模块说明](modules/mail/docs/README.md)
- [工具箱模块说明](modules/tools/docs/README.md)

## 开源使用边界

- 本项目不是 Apple 官方软件，也不代表 Apple；iCloud 接口、账号和邮件数据的使用必须
  遵守适用的服务条款和当地法律。
- 只使用自己拥有或明确获授权的 Apple Account、Session 和邮箱数据。项目不用于绕过
  验证、获取未授权账号或批量滥用服务。
- Session Cookie、登录密码、API 访问令牌、Apple 密码、两步验证码、IMAP App 专用密码和
  代理凭据都是敏感信息，禁止提交到 Git、Issue、日志或截图中。
- 生产环境应使用 HTTPS、受限的反向代理访问和独立数据库账号。

公开仓库不等于公开运行数据。`.env`、`data/`、Session、Cookie 和构建产物已通过
`.gitignore` 排除；提交前仍应使用 `git status` 和代码审查确认没有敏感内容。

## 参与贡献

欢迎通过 Issue 反馈可复现问题，或提交 Pull Request：

1. Fork 仓库并创建功能分支；
2. 修改对应模块的后端、前端、测试和文档，不把业务代码放入平台外壳；
3. 运行 Go 测试、`go vet`、前端类型检查、前端测试和生产构建；
4. 在 Pull Request 中说明行为变化、迁移影响和验证结果。

新增页面必须遵守 [界面设计规范](docs/UI_GUIDELINES.md)，新增日志或持久化行为必须同步
更新 [使用日志规范](docs/LOGGING.md) 和数据库迁移说明。

## 许可证

当前仓库尚未提交 `LICENSE` 文件。公开访问不自动授予复制、修改或再分发权；在明确开源
许可之前，请将本项目视为保留所有权利。维护者应根据项目用途选择并提交合适的许可证。
