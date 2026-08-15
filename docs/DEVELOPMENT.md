# 开发指南

## 开发工具

- Go 1.24 或更高版本
- Node.js 22 或更高版本
- npm 11 或更高版本
- PostgreSQL 15 或更高版本（本地和生产默认需要）
- Redis 7 或更高版本（生产多实例同步锁需要，单机不可用时允许降级）

后端的目标技术栈为 Go + Gin + Ent。当前代码仍以标准库 HTTP 服务兼容旧接口，Ent
数据模型已用于邮件持久化，Gin 网关尚未切换。前端使用 Vue 3、TypeScript、Vite、Vue
Router 和 Lucide 图标。

所有页面的新建与重构必须遵守 [界面设计规范](UI_GUIDELINES.md)。页面评审先检查信息架构、操作路径和状态完整性，再检查视觉细节；不能仅通过更换配色或增加卡片视为完成优化。

新增一级业务模块或后台任务时，还必须遵守 [使用日志设计规范](LOGGING.md)。模块日志复用平台
日志仓库，但 API 和页面必须固定模块范围；不得由前端传入模块标识。

数据库组件使用 pgx 和 go-redis，版本化 SQL 位于 `internal/platform/persistence/migrations`，
Ent schema 位于 `internal/platform/persistence/ent/schema`。修改 schema 后必须重新生成 Ent
代码并增加迁移文件，不能直接修改生成代码。当前生成命令需要启用项目已有的 upsert 特性：

```bash
go run entgo.io/ent/cmd/ent generate --feature sql/upsert ./internal/platform/persistence/ent/schema
```

## 常用命令

```bash
# 后端格式化、静态检查和测试
go fmt ./...
go vet ./...
go test ./...

# 前端
cd frontend
npm install
npm run dev
npm run typecheck
npm run test:run
npm run build
```

Vite 开发服务器会将 `/api`、`/v1` 和 `/health` 代理到
`http://127.0.0.1:8000`。

Windows 可以直接调用便携版 Go 的 `go.exe`。`scripts/build.ps1` 会执行完整的
前端构建和 Go 检查。

发布构建先生成 Vite 产物，再使用 `embed` 标签构建单个可执行文件：

```bash
cd frontend
npm ci
npm run build
cd ..
go build -tags embed -o running-tools ./cmd/server
```

当前前端嵌入由 `internal/webui/embed.go` 中的 `go:embed` 实现。即使本地调试没有传
`-tags embed`，已有的 `internal/webui/dist` 仍会被编译进程序；发布流水线统一传入该
标签，便于后续区分开发与发布构建。

## 本地联调

仓库根目录的 `.env` 会自动加载，但不会覆盖终端或容器已经设置的环境变量。默认存储模式为
`postgres`，必须先创建 `running_tools` 数据库并填写 `RUNNING_DATABASE_URL`。

终端一：

```powershell
cd D:\All\GPT\running-tools
$env:RUNNING_ADMIN_USERNAME="admin"
$env:RUNNING_ADDR="127.0.0.1:8000"
$env:RUNNING_DATA_DIR="$PWD\data"
go run ./cmd/server
```

终端二：

```powershell
cd D:\All\GPT\running-tools\frontend
npm run dev
```

浏览器打开 <http://127.0.0.1:5173>。

数据库第一次启动会创建 `admin / admin123` 并要求修改初始密码。后续启动读取 PostgreSQL
中的用户和 Argon2id 密码哈希，不通过环境变量配置登录密码。

## 前端模块注册

每个功能模块导出一个 `ModuleManifest`。平台外壳根据该清单渲染一级导航组和
路由。模块拥有自己的页面、API 客户端、类型、组件、样式、测试和文档。只有模块
清单的导入语句可以放在 `frontend/src/modules.ts`。

## 代码边界

- 公共鉴权和响应封装属于 `internal/platform`。
- iCloud 相关实现只属于 `modules/mail/backend`。
- 平台外壳必须根据模块清单渲染页面，不能硬编码邮件页面。
- 一个模块不能写入另一个模块的数据目录。
- 公共 API 行为发生变化时，必须同步更新 API 文档和兼容性测试。
- 生产业务状态必须进入 PostgreSQL；仅宿主机通信文件可以继续使用原子文件写入和严格权限。
- 模块专属前端代码不能放进根平台外壳。
- 纯前端工具的计算公式应提取为独立 TypeScript 函数并添加单元测试，不能直接散落在
  DOM 事件或模板表达式中。

## 完成标准

1. Go 测试、`vet`、前端类型检查、前端测试和生产构建全部通过。
2. 桌面端和 320px 移动端没有内容重叠或横向滚动。
3. 旧 API 兼容性测试通过。
4. 仓库中没有密钥、Session Cookie、运行数据或 `.env` 文件。
5. 模块文档和迁移文档已经同步更新。
