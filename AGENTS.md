# Running Tools 开发约束

## 前端界面

任何涉及 Vue 页面、平台外壳、组件或样式的工作，开始修改前必须完整阅读并遵守
[`docs/UI_GUIDELINES.md`](docs/UI_GUIDELINES.md)。

前端任务完成前必须检查加载、空数据、错误、禁用、成功和长文本状态，并运行：

```bash
cd frontend
npm run typecheck
npm run test:run
npm run build
```

涉及数据列表时必须实现明确的每页条数、记录范围和翻页状态；桌面端与移动端都不能出现
非预期横向溢出。只更换配色或增加卡片不算完成界面优化。

## 后端和接口

公共 API 行为发生变化时同步更新 `docs/API.md` 和相应测试。持久化文件必须采用原子写入
并保持所有者专用权限；不得把 Cookie、密码、API Key 或真实邮件正文写入日志和仓库。

任何模块新增或修改使用日志时，必须完整阅读并遵守 [`docs/LOGGING.md`](docs/LOGGING.md)。
每个一级模块只能查询和展示自己的日志，不得记录状态轮询、日志查询、认证信息或业务正文。
