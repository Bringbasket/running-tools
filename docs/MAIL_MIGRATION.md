# 邮件系统迁移审计

本文件记录 Go 版 `running-tools` 与 `lw20011216-bot/hme-manager` 当前主分支的功能对照。
审计日期为 2026-08-12。这里的“已实现”表示有实际代码路径和测试，不表示两个项目
采用完全相同的存储结构。

## 审计结论

基础的隐藏邮箱管理、Session 持久化、自动保活、后台创建、分享令牌和只读 IMAP
收件箱均已迁入 Go 项目，可以覆盖日常管理流程。本轮审计和完善修复了以下问题：

- 重新启用地址必须调用 Apple `POST /v1/hme/reactivate`。对照项目使用的
  `/v1/hme/activate` 会返回空正文 HTTP 400；正确接口已在实际账号上验证为 HTTP 200。
- 批量隐藏邮件改为先校验整批、按 IMAP UID 去重、一次写入且只增加一次 revision，
  不再发生部分成功。
- 分享页新增单封邮件详情和 revision 等待；列表接口只返回正文预览，完整正文按需读取。
- IMAP Worker 增加持久 UID 游标、IDLE 实时监听和轮询回退，不再每轮覆盖重扫最近邮件。
- 邮件详情新增清理后的安全 HTML；脚本、表单、附件、远程图片、样式和危险链接不会进入结果。

Go 版目前仍不能称为与对照项目完全等价。主要差距集中在旧邮件 HTML 回填、验证码识别
精度和批量创建队列的历史模型。这些差距不会影响隐藏邮箱的
创建、编辑、启停和删除。

## 功能对照

| 能力 | 状态 | Go 版说明 |
| --- | --- | --- |
| 列出、搜索、筛选隐藏邮箱 | 已实现 | Vue 列表支持分页与每页 10/20/50/100 条 |
| 单个创建、标签、备注、CSV | 已实现 | Apple 原生接口；默认标签可设为 `shopping` |
| 停用、重新启用、删除 | 已实现并修复 | 重新启用使用实测有效的 `/v1/hme/reactivate` |
| cURL/HAR Session 导入 | 已实现 | 国际区和中国大陆区；Cookie 仅保存在服务端文件 |
| Session 状态持久化 | 已实现 | 配置、检查状态和自动刷新设置均持久化 |
| 自动保活与失效停用 | 已实现 | Go Worker 定期检查；401/403/421 标记重新导入 |
| 周期自动创建 | Go 扩展 | 前端配置，关闭网页后由 Go Worker 继续执行 |
| 1-99 个持久批量队列 | 主要流程已实现 | 幂等、暂停、继续、取消、重启恢复和结果不明确确认 |
| Apple 限流冷却 | 部分等价 | Go 固定等待 30 分钟；对照项目为账号级 30/60/120 分钟指数退避 |
| 队列逐项历史 | 未完全等价 | Go 保存当前进度和候选；对照项目 SQLite 保存每项尝试与结果历史 |
| 分享令牌摘要与 HttpOnly 会话 | 已实现 | 明文令牌不落盘；停用或删除地址会撤销链接 |
| 分享邮件列表、详情、同步等待 | 已实现 | 分享会话只能读取令牌绑定的单一邮箱 |
| IMAP TLS、只读、白名单归属 | 已实现 | 使用 `BODY.PEEK`，不会把邮件标记为已读 |
| 最近 3 天聚合和详情 | 已实现 | 列表返回 160 字预览，详情按 UID 读取完整缓存正文与安全 HTML |
| 单条和批量本地隐藏 | 已实现并修复 | 不删除 IMAP 原邮件；批量操作原子且按 UID 去重 |
| revision 长轮询 | 已实现 | 使用事件通知唤醒管理和分享 API，不再每 250ms 读取状态文件 |
| UID 增量同步和 IMAP IDLE | 已实现 | UID 游标持久化；支持 IDLE 时实时监听，不支持时按配置轮询 |
| PostgreSQL 数据层 | 已实现 | 账号、状态、邮件、分享数据和使用日志均由 PostgreSQL 持久化 |
| HTML 回填和缓存修剪 | 部分等价 | 新邮件保存安全 HTML并按数量修剪；首次迁移的旧缓存不会主动回填 HTML |
| 安全 HTML 原邮件视图 | 已实现 | 只保留安全排版与 HTTP(S) 链接，在 sandbox iframe 中显示 |
| 验证码识别 | 部分等价 | 常见验证码和合作伙伴代码可识别；对照项目有更严格的误报排除规则 |

## 当前接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/mail/v1/aliases/{id}/update` | 更新标签和备注 |
| `POST` | `/api/mail/v1/aliases/{id}/disable` | 停用邮箱 |
| `POST` | `/api/mail/v1/aliases/{id}/enable` | 重新启用邮箱 |
| `GET/POST` | `/api/mail/v1/alias-queue` | 查看或加入批量队列 |
| `POST` | `/api/mail/v1/alias-queue/pause` | 暂停当前队列 |
| `POST` | `/api/mail/v1/alias-queue/resume` | 继续当前队列 |
| `POST` | `/api/mail/v1/alias-queue/cancel` | 取消当前队列 |
| `GET/POST` | `/api/mail/v1/aliases/{id}/share-links` | 查看或生成分享链接 |
| `POST` | `/api/mail/v1/share-links/{id}/revoke` | 撤销分享链接 |
| `GET` | `/api/mail/v1/mail/sync/status` | 查看 IMAP 配置状态 |
| `POST` | `/api/mail/v1/mail/sync/run` | 立即执行一次只读 IMAP 同步 |
| `GET` | `/api/mail/v1/mail/messages?alias=...` | 查询指定地址的邮件摘要 |
| `GET` | `/api/mail/v1/mail/messages/{uid}?alias=...` | 查询完整纯文本和安全 HTML 详情 |
| `GET` | `/share/v1/messages/{uid}` | 分享会话读取单封邮件详情 |
| `GET` | `/share/v1/sync/wait` | 分享会话等待邮件 revision 变化 |

## IMAP 配置

推荐直接进入前端“收件箱”，点击“IMAP 设置”填写账号、应用专用密码、服务器与同步参数，
可以先测试连接再保存。配置会按账号持久化到 PostgreSQL，密码不会通过
读取接口回显。服务器环境变量仍作为尚未保存网页配置时的兼容默认值：

```dotenv
HME_IMAP_USERNAME=your-name@icloud.com
HME_IMAP_PASSWORD_FILE=/run/secrets/hme-imap-password
HME_IMAP_HOST=imap.mail.me.com
HME_IMAP_PORT=993
HME_IMAP_MAILBOX=INBOX
HME_MAIL_SYNC_ENABLED=false
HME_MAIL_SYNC_POLL_SECONDS=120
HME_IMAP_LOOKBACK_DAYS=90
HME_IMAP_CACHE_MAX_MESSAGES=5000
```

iCloud Mail 必须使用 `imap.mail.me.com:993` 和 Apple Account 生成的 App 专用密码；Apple ID
登录密码不能用于 IMAP。Gmail 等其他转发目标应填写对应服务商的 IMAP 主机。

启用 `HME_MAIL_SYNC_ENABLED=true` 后，Go Worker 首次按 `HME_IMAP_LOOKBACK_DAYS` 回看，
之后只读取已保存 UID 游标之后的新邮件。服务器支持 IDLE 时实时等待新邮件通知，否则按
`HME_MAIL_SYNC_POLL_SECONDS` 轮询。单批最多读取 200 封，积压批次会连续推进游标；缓存
最多保留 `HME_IMAP_CACHE_MAX_MESSAGES` 封。收件箱聚合页仍只展示最近 72 小时邮件。
