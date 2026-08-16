# 系统架构

## 技术栈基线

项目统一采用 Go + Gin + Ent、Vue 3 + TypeScript + Vite、PostgreSQL 15+ + Redis 7+。
Gin 用于 HTTP 网关和中间件，Ent 用于 PostgreSQL 模型管理，Redis 用于缓存、限流、
分布式锁和短期任务状态。当前已接入 Ent、PostgreSQL 和 Redis；HTTP 服务仍使用标准库
兼容现有路由，Gin 尚未切换。

生产构建使用 `-tags embed` 将 Vite 产物嵌入 Go 二进制。详细约束见
[技术栈规范](TECH_STACK.md)。

## 设计目标

1. 平台公共能力与业务模块相互独立。
2. 每个模块的后端、前端、测试和文档放在同一个模块目录中。
3. 保持旧版公共 API 稳定，同时提供新的命名空间 API。
4. 将生产前端嵌入一个 Go 可执行程序中。
5. 应用容器永远不接触 Docker Socket。
6. 公共日志基础设施复用统一模型，但日志读取和界面按一级业务模块严格隔离。

## 运行层次

```text
浏览器
  -> Vue 平台外壳
       -> 已注册模块的路由和导航
  -> Go HTTP 服务
       -> 平台中间件（请求 ID、会话鉴权、来源校验、异常恢复、安全响应头）
       -> 平台服务（健康检查、版本更新、嵌入式前端）
       -> 邮件模块 API
            -> Session 管理器
            -> iCloud 隐藏邮件地址客户端
            -> 模块专属持久化状态
```

## 后端模块约定

每个后端模块提供一个小型注册入口，并尽量将业务类型保留在模块内部。业务模块
可以依赖 `internal/platform`，但平台包不能反向导入业务模块。可执行程序是组合
入口，负责同时导入平台和业务模块。

邮件模块的标准接口注册在 `/api/mail/v1` 下。旧版 `/v1` 接口作为兼容别名继续
保留，原有脚本无需立即修改。

邮件模块按 `mail_accounts.id` 建立运行空间。每个账号实例化独立 Session 管理器、自动刷新
Worker、自动创建 Worker、批量队列、IMAP 服务和日志仓库；请求通过 `X-Mail-Account-ID`
选择账号。切换前端当前账号不会停止其他账号的后台任务。账号列表实时汇总各运行时的 Web、
Apple Account、IMAP 和后台任务状态，不复制持久化健康状态。

自动创建 Worker 和批量队列使用持久化到期时间配合进程内唤醒信号。配置变化、任务完成和重试
安排会立即重置 Timer；空闲状态只保留五分钟一次的恢复兜底，不允许按账号每秒查询 PostgreSQL。
Session 自动刷新同样按下一次 Web 检测或 Apple Account 保活期限唤醒；账号状态发生外部变化时，
最多 30 秒恢复检查一次，不再为每个账号固定执行 10 秒轮询。

每个账号可以在 `mail_accounts.proxy_url` 保存独立代理。代理客户端归账号 Session 管理器所有，
同时覆盖 Apple SRP 登录、Apple Account 管理态、iCloud Web 请求以及 IMAP/IDLE 收件连接；
配置代理后任何链路失败都不得静默回退到服务器直连，对外只暴露 `hasProxy`。
iCloud Web 客户端会滚动合并响应 Cookie，并在账号内串行写回 `running_state`，避免并发响应覆盖。
IMAP 服务在账号内复用一条已认证连接执行同步和 IDLE，目标、凭据或账号代理变化后主动销毁
旧连接。账号管理的代理测试只验证 Apple HTTPS；IMAP 设置的连接测试才验证完整 `993` 链路。

## 前端模块约定

根目录 `frontend/src` 只包含应用外壳、公共组件、身份验证、系统更新界面和模块
注册表。每个模块通过 `modules/<模块名>/frontend/module.ts` 导出模块清单，清单
包含：

- 模块标识和显示名称；
- 一个可展开、收起的一级导航组；
- 子页面和路由；
- 模块图标和状态信息。

Vue 单文件组件的模板和 TypeScript 逻辑放在对应模块目录内。模块专属 API
客户端、类型、组件、样式和测试也应保留在该模块中。

当前导航注册表为每个模块渲染一个一级折叠菜单。邮件系统下包含账号管理、邮箱管理、收件箱、API
调试、Session 管理和邮件系统使用日志。账号切换与新增只在账号管理页完成，不占用平台顶栏。未来的 Codex APP 或 CAP 转换功能必须建立新的同级模块，
不能作为不相关页面添加到邮件系统中。

工具箱作为 `modules/tools` 独立模块注册，内部可以包含多个小型工具页面。纯本地计算
工具只保留 Vue 页面、计算逻辑、测试和文档，不创建无意义的 Go API；需要服务端存储
或外部请求时，再在工具箱模块内增加独立后端边界。

## 持久化数据

生产邮件模块统一使用 PostgreSQL 主数据源。旧版 `json`/`dual` 仅用于迁移旧部署，
迁移完成后不再写入 JSON。PostgreSQL 保存账号、Session、Apple Account、任务状态、邮件正文、
验证码索引、隐藏记录与 IMAP 同步状态；Redis 当前用于多实例 IMAP 同步锁，故障时回退
本进程锁。Redis 不得作为唯一持久化来源。

会持续增长的数据必须按记录建表：邮件正文和安全 HTML位于 `mailbox_messages`，隐藏记录位于
`mailbox_hidden_messages`，分享链接和会话位于 `mail_share_links`、`mail_share_sessions`，
使用日志位于 `activity_logs`。配置、Session 和任务当前状态按账号存入 `running_state` 的
JSONB 单行，不会生成或追加生产 JSON 文件。

收件箱采用双重保留边界：每个隐藏邮箱最多保留按时间排序的最新 100 封，同时每个母号的全部
邮件仍受 IMAP 设置中的 `cacheMax` 总上限约束；不能把“每邮箱 100 封”理解为绕过账号总上限。
Recent 聚合查询最多返回 500 封。PostgreSQL 列表查询必须在 SQL 层完成账号隔离、隐藏状态过滤、
时间排序和 `LIMIT`，单封详情才读取完整正文。收件箱状态和长轮询只查询
`mailbox_sync_states` 的账号单行，空同步只更新状态和 UID 游标，不得读取或重写整批邮件正文。
首次回填采用“邮件头筛选 → 候选 UID 正文读取”两阶段流程，邮件头按 UID 从新到旧每批 200 封；
候选集合达到每邮箱上限或账号 `cacheMax` 后停止，不允许为无关历史邮件批量下载完整正文。

```text
PostgreSQL
|-- mail_accounts
|-- auth_users / auth_sessions / auth_api_tokens / auth_login_events
|-- running_state
|-- mailbox_messages / mailbox_hidden_messages / mailbox_sync_states
|-- mail_share_links / mail_share_sessions
`-- activity_logs

data/system
|-- check-request.json
|-- update-request.json
`-- update-status.json
```

`data/system` 的文件是 Go 容器与宿主机更新服务的受限通信协议，不是业务数据库。
平台登录密码使用 Argon2id 哈希；浏览器 Session 和 API 访问令牌只在 PostgreSQL 保存 SHA-256
摘要。浏览器 Session 通过 `HttpOnly`、`SameSite=Strict` Cookie 传递，HTTPS 下同时启用
`Secure`，不会由 JSON API 返回，也不会写入日志。除登录状态和登录接口外，`/api/*`、`/v1/*`
统一由平台中间件默认拒绝未认证请求，业务模块不能自行漏配公开路由。数据库没有用户时固定
初始化 `admin / admin123`，该账号在完成强制改密前不能访问业务 API；密码不从环境变量读取。
`mail_accounts.proxy_url` 与 `running_state` 中的认证材料均按敏感配置处理，不得写入 API、日志
或错误详情。

## 系统更新边界

Go 容器只能在 `data/system` 中写入更新请求和读取状态。宿主机上的 systemd
服务负责拉取镜像、重启当前 Compose 项目、执行健康检查和失败回滚。容器不挂载
`/var/run/docker.sock`，因此业务服务无法操作其他 Docker 容器。

发布镜像分别携带 `org.opencontainers.image.version` 和 `org.opencontainers.image.revision`。
前者是 `0.0.<Actions 运行序号>` 形式的用户版本，后者是完整 commit SHA；版本界面展示两者，
更新可用性只比较 revision。

## 增加新模块

1. 将 `modules/_template` 复制为 `modules/<模块名>`。
2. 实现并测试 Go 后端包。
3. 在 `cmd/server/main.go` 注册后端模块。
4. 实现模块前端清单和页面。
5. 在 `frontend/src/modules.ts` 导入模块清单。
6. 记录环境变量、API、状态文件和迁移步骤。
