# Running Tools

Running Tools 是一个模块化的自托管运维控制台。Go 服务负责身份验证、HTTP
服务、系统更新和模块注册；每个业务模块独立维护自己的后端、前端、测试和文档。

当前第一个模块是**邮件系统**，用于管理 iCloud 隐藏邮件地址，同时兼容原
`hme-manager` 服务的 API。

## 规范技术栈

后端规范为 **Go + Gin + Ent**，前端规范为 **Vue 3 + TypeScript + Vite**，数据层
规范为 **PostgreSQL 15+ + Redis 7+**。当前已使用 Ent + PostgreSQL 持久化邮件正文、
验证码索引、隐藏记录和 IMAP 同步状态，并使用 Redis 协调多实例 IMAP 同步；Session
密钥和 IMAP 密码仍以权限为 `0600` 的文件保存。HTTP 网关目前仍是 Go 标准库，Gin
继续作为后续统一网关规范。生产构建支持 `-tags embed`，将 Vue 前端资源嵌入单个 Go
二进制。

完整组件职责、接入状态和迁移顺序见 [技术栈规范](docs/TECH_STACK.md)。

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
- 从私有 GHCR 镜像更新、健康检查、失败回滚和自动重启；
- 生产统一使用 PostgreSQL，旧 JSON 仅在首次迁移时导入，迁移后不再创建业务 JSON；
- 桌面端可收起侧栏、移动端抽屉菜单和深浅色主题。
- 独立工具箱菜单，以及纯前端的保本测算（保本倍率、额度反推和利润试算）。

与原 `hme-manager` 的逐项迁移状态、已修复问题和仍未完全等价的能力，见
[邮件系统迁移审计](docs/MAIL_MIGRATION.md)。

## 本地开发

环境要求：Go 1.24 或更高版本、Node.js 22 或更高版本、npm 11 或更高版本。直接本地
本地和生产默认均采用 PostgreSQL；本项目 Compose 只管理应用和 Redis，PostgreSQL 通过
`.env` 中的 `RUNNING_DATABASE_URL` 连接已有实例。本地 `go run` 会自动读取仓库根目录的 `.env`。

```bash
cp .env.example .env
cd frontend && npm install && npm run build && cd ..
go run ./cmd/server
```

打开 <http://127.0.0.1:8000>，输入 `RUNNING_API_KEY`。

Windows PowerShell 示例：

```powershell
cd D:\All\GPT\running-tools\frontend
npm ci
npm run build

cd ..
$env:RUNNING_API_KEY="请换成自己的密钥"
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

生产服务器只需要保存 `.env`、`compose.server.yml` 和 `data/`。业务源码由
GitHub Actions 构建为私有 GHCR 镜像，容器不会挂载 Docker Socket。网页提交
更新请求后，由宿主机 systemd path 单元调用更新脚本。

在完成数据迁移和接口对比前，不要停止现有 Python 版本。

相关文档：

- [系统架构](docs/ARCHITECTURE.md)
- [技术栈规范](docs/TECH_STACK.md)
- [界面设计规范](docs/UI_GUIDELINES.md)
- [开发指南](docs/DEVELOPMENT.md)
- [HTTP API](docs/API.md)
- [数据迁移](docs/MIGRATION.md)
- [生产部署](docs/DEPLOYMENT.md)
