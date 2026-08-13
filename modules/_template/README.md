# 模块模板

增加新业务时，将该目录复制为 `modules/<模块名>`，并保持以下结构：

```text
modules/<模块名>/
|-- backend/     # Go 后端、API、业务逻辑和测试
|-- frontend/    # Vue 页面、组件、类型、API 客户端和测试
`-- docs/        # 模块专属文档
```

后端和前端清单只能在各自的组合入口中注册。不要把业务逻辑移入
`internal/platform`，也不要将模块专属页面放进根目录 `frontend/src`。

每个模块至少应记录：

- 模块职责和不负责的范围；
- 环境变量；
- HTTP API；
- 持久化文件；
- 本地开发和测试命令；
- 部署与迁移步骤。

模块后端遵循 Go + Gin + Ent 规范，前端遵循 Vue 3 + TypeScript + Vite 规范。需要
PostgreSQL 15+ 或 Redis 7+ 时，必须在模块文档中注明用途、连接配置、迁移/TTL 策略和
未配置基础设施时的降级行为。当前项目尚未默认启用数据库和 Redis，不能假设它们已经
可用。
