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

## 运行层次

```text
浏览器
  -> Vue 平台外壳
       -> 已注册模块的路由和导航
  -> Go HTTP 服务
       -> 平台中间件（请求 ID、鉴权、异常恢复、安全响应头）
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

当前导航注册表为每个模块渲染一个一级折叠菜单。邮件系统下包含邮箱管理、API
调试和 Session 管理。未来的 Codex APP 或 CAP 转换功能必须建立新的同级模块，
不能作为不相关页面添加到邮件系统中。

工具箱作为 `modules/tools` 独立模块注册，内部可以包含多个小型工具页面。纯本地计算
工具只保留 Vue 页面、计算逻辑、测试和文档，不创建无意义的 Go API；需要服务端存储
或外部请求时，再在工具箱模块内增加独立后端边界。

## 持久化数据

当前邮件缓存支持三种数据路径：`json` 用于无数据库本地开发，`dual` 以 JSON 为主读并
同步写 PostgreSQL，`postgres` 以 PostgreSQL 为主数据源。PostgreSQL 保存邮件正文、
验证码索引、隐藏记录与 IMAP 同步状态；Redis 当前用于多实例 IMAP 同步锁，故障时回退
本进程锁。Redis 不得作为唯一持久化来源。

会持续快速增长的数据按优先级为：邮件正文和安全 HTML、分享链接与分享会话、创建任务
执行历史、审计日志。第一类已经迁移；其余类型应直接建 PostgreSQL 模型，不能继续设计
为无限增长的整份 JSON 文件。

```text
data/
|-- system/
|   |-- check-request.json
|   |-- update-request.json
|   `-- update-status.json
`-- mail/
    |-- hme-config.json
    `-- state/
        |-- hme-session.json
        |-- session-state.json
        `-- auto-refresh.json
```

敏感文件使用临时文件加替换的方式写入，并设置严格权限。Session Cookie 不会由
API 返回，也不会写入日志。

## 系统更新边界

Go 容器只能在 `data/system` 中写入更新请求和读取状态。宿主机上的 systemd
服务负责拉取镜像、重启当前 Compose 项目、执行健康检查和失败回滚。容器不挂载
`/var/run/docker.sock`，因此业务服务无法操作其他 Docker 容器。

## 增加新模块

1. 将 `modules/_template` 复制为 `modules/<模块名>`。
2. 实现并测试 Go 后端包。
3. 在 `cmd/server/main.go` 注册后端模块。
4. 实现模块前端清单和页面。
5. 在 `frontend/src/modules.ts` 导入模块清单。
6. 记录环境变量、API、状态文件和迁移步骤。
