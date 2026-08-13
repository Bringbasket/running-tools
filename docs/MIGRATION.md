# 从 hme-manager 迁移

Go 重构版本采用分阶段迁移方式。在 Go 容器通过 API 和数据验证之前，不要删除或
停止现有 Python 容器。

## 数据层迁移规划

当前迁移分为文件目录迁移和数据库迁移。脚本先把旧 JSON 复制到模块化 `data/` 目录；
应用在 `dual` 或 `postgres` 模式首次启动时，再将 `mailbox-cache.json` 导入 PostgreSQL。
导入不会删除或改名旧 JSON，也不会操作其他 Docker 项目。

## 邮件数据库迁移

1. 备份 `data/` 和 PostgreSQL 命名卷。
2. 在 `.env` 设置 `RUNNING_STORAGE_MODE=dual`，启动当前项目。
3. 检查 `/health` 中 `postgres=ok`、`redis=ok`、`storageMode=dual`。
4. 对比网页邮件数量以及 `mailbox_messages`、`mailbox_hidden_messages` 的行数。
5. 至少观察一个完整 IMAP 同步周期，确认 JSON 与数据库都正常更新。
6. 将模式改为 `postgres` 并只重建 `app` 服务；继续保留 JSON 作为回退副本。

如果 PostgreSQL 模式异常，可先切回 `json`；`dual` 模式下 JSON 是读取主源。不要在未
确认备份前删除命名卷或 `mailbox-cache.json`。

## 数据对应关系

```text
旧 /data/hme-config.json          -> 新 /data/mail/hme-config.json
旧 /data/state/hme-session.json   -> 新 /data/mail/state/hme-session.json
旧 /data/state/session-state.json -> 新 /data/mail/state/session-state.json
旧 /data/state/auto-refresh.json  -> 新 /data/mail/state/auto-refresh.json
旧 /data/state/update-*.json      -> 新 /data/system/update-*.json
```

请对已经停止写入的测试数据副本运行 `scripts/migrate-hme-data.sh`。脚本只复制明确
列出的文件，不会删除或修改源文件。

```bash
scripts/migrate-hme-data.sh \
  /www/wwwroot/hme-manager/data \
  /www/wwwroot/running-tools/data
```

## API 兼容性

以下旧接口继续保留：

- `/v1/aliases` 和邮箱操作接口；
- `/v1/session/status`、`/refresh` 和 `/import`；
- `/v1/auto-refresh` 和 `/run`；
- `/v1/system/version` 和 `/update`；
- `/health`。

新客户端应使用 `/api/mail/v1/*` 处理邮件功能，使用 `/api/system/*` 处理平台
功能。

## 切换检查清单

1. 备份当前数据目录。
2. 将旧数据复制到新的模块化目录结构。
3. 在未使用的本机回环端口上启动 Go 容器，例如 `127.0.0.1:8091`。
4. 对比新旧服务的 Session 状态和邮箱列表响应。
5. 执行一次不会修改邮箱数据的 Session 检查。
6. 测试更新请求和状态读取链路。
7. 检查桌面端和移动端界面。
8. 全部通过后再将反向代理切换到 Go 服务。

## 回退方式

在切换期间保留旧容器和数据备份。如果新服务出现问题，只需把反向代理上游恢复
到旧端口；迁移脚本不会更改旧数据，因此不需要执行反向数据转换。
