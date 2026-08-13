# 从 hme-manager 迁移

Go 重构版本的生产数据源固定为 PostgreSQL。旧 JSON 只作为一次性导入来源；应用确认导入成功后
只读写数据库，不会继续双写，也不会删除或改名旧文件。

## 迁移范围

- `hme-config.json`、Session、Apple Account、自动刷新、自动创建、批量队列和 IMAP 配置：
  首次读取时导入 `running_state` JSONB 行。
- `mailbox-cache.json`：首次启动导入 `mailbox_messages`、`mailbox_hidden_messages` 和
  `mailbox_sync_states` 的独立记录。
- `share-links.json`：首次启动导入 `mail_share_links` 和 `mail_share_sessions`，后续不再整包写入。
- 使用日志：生产直接写入 `activity_logs`，不使用会持续增长的 JSON 文件。
- `data/system/update-*.json`：保留，供 Go 容器与宿主机更新服务通信，不属于业务持久化。

## 迁移步骤

1. 备份旧 `data/` 和 PostgreSQL 数据库或命名卷。
2. 使用 `scripts/migrate-hme-data.sh` 把旧文件复制到新的模块目录。
3. 在 `.env` 设置 `RUNNING_STORAGE_MODE=postgres` 和有效的 `RUNNING_DATABASE_URL`。
4. 启动应用，确认 `/health` 返回 `postgres=ok`、`storageMode=postgres`。
5. 在前端分别检查 Session、邮箱列表、自动任务、收件箱、分享链接和使用日志。
6. 查询 PostgreSQL 确认默认账号的数据均带有 `account_id=default`。
7. 新建第二个邮件账号，确认其 Session、邮箱列表、Worker、IMAP 缓存和日志独立。

迁移脚本只复制明确列出的文件，不修改源文件：

```bash
scripts/migrate-hme-data.sh \
  /www/wwwroot/hme-manager/data \
  /www/wwwroot/running-tools/data
```

## 清理验证

- “使用日志 → 清理”执行 `activity_logs` 的账号级 `DELETE`。
- “收件箱 → 清理缓存”删除当前账号的邮件、隐藏记录和同步状态。
- 分享弹窗“清理失效”删除当前账号已撤销或过期的链接以及过期会话。

这些操作不可撤销，执行前必须确认数据库备份。前端清空数组不算数据清理。

## API 兼容

旧 `/v1/*` 接口继续使用 `default` 账号。新客户端使用 `/api/mail/v1/*`，并在账号相关请求中
发送 `X-Mail-Account-ID`。平台更新接口仍使用 `/api/system/*`。

## 回退

如果切换后出现问题，将反向代理恢复到旧服务并使用迁移前备份。不要把运行过的新 PostgreSQL
反向覆盖旧 JSON；两种数据结构没有可靠的自动反向转换。
