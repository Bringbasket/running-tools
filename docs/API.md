# HTTP API

除 `/health` 和前端静态资源外，所有接口都需要请求头：

```http
X-API-Key: <RUNNING_API_KEY>
```

JSON 接口统一使用以下响应结构：

```json
{
  "ok": true,
  "data": {},
  "error": null,
  "meta": {
    "service": "running-tools",
    "version": "1",
    "requestId": "..."
  }
}
```

失败响应中的 `ok` 为 `false`，`data` 为 `null`，错误代码和信息位于 `error`
中。`requestId` 可用于对应服务端访问日志。

## 平台接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | 健康检查，不需要 API Key |
| GET | `/api/system/version` | 读取当前版本、目标版本和更新状态 |
| POST | `/api/system/version/check` | 仅检查是否存在新构建，不重启服务 |
| POST | `/api/system/update` | 提交宿主机更新请求 |

兼容地址：`/v1/system/version`、`/v1/system/version/check`、`/v1/system/update`。

必须先调用 `POST /api/system/version/check`。检查任务只拉取镜像元数据并比较构建
标识，不会重启服务；只有确认存在新构建后，网页才会显示“立即更新”。

`POST /api/system/update` 返回 HTTP 202。接口只在 `data/system` 中写入更新请求，
真正的镜像拉取和容器重启由宿主机服务完成。

## 邮件接口

邮件模块支持多个 iCloud 账号。除账号列表和创建账号外，标准邮件接口必须携带当前账号：

```http
X-Mail-Account-ID: default
```

未提供时兼容使用 `default`。前端切换账号后会在所有邮件请求中自动设置该请求头；每个账号拥有
独立 Session、邮箱列表、自动刷新、自动创建、批量队列、IMAP 缓存、分享链接和使用日志。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/mail/v1/accounts` | 获取邮件账号列表 |
| POST | `/api/mail/v1/accounts` | 新建独立邮件账号运行空间 |
| GET | `/api/mail/v1/aliases` | 获取隐藏邮箱列表 |
| POST | `/api/mail/v1/aliases` | 创建隐藏邮箱 |
| GET | `/api/mail/v1/aliases/export.csv` | 直接下载 CSV 文件 |
| POST | `/api/mail/v1/aliases/{id}/enable` | 启用邮箱 |
| POST | `/api/mail/v1/aliases/{id}/disable` | 停用邮箱 |
| POST | `/api/mail/v1/aliases/{id}/delete` | 删除邮箱 |
| GET | `/api/mail/v1/session/status` | 读取持久化 Session 状态 |
| POST | `/api/mail/v1/session/refresh` | 立即检查当前 Session |
| POST | `/api/mail/v1/session/import` | 从 cURL 或 HAR 导入 Session |
| POST | `/api/mail/v1/session/apple-login/start` | 使用 Apple ID 开始 SRP 协议登录 |
| POST | `/api/mail/v1/session/apple-login/verify` | 提交 6 位验证码并完成登录 |
| GET | `/api/mail/v1/auto-refresh` | 读取自动刷新设置 |
| POST | `/api/mail/v1/auto-refresh` | 修改自动刷新设置 |
| POST | `/api/mail/v1/auto-refresh/run` | 立即执行一次自动刷新 |
| GET | `/api/mail/v1/create-schedule` | 读取自动创建计划 |
| POST | `/api/mail/v1/create-schedule` | 修改自动创建计划 |
| POST | `/api/mail/v1/create-schedule/run` | 立即执行一轮创建 |
| POST | `/api/mail/v1/create-schedule/stop` | 暂停当前计划和执行 |
| GET | `/api/mail/v1/activity-logs` | 分页查询邮件系统使用日志 |
| POST | `/api/mail/v1/aliases/{id}/update` | 修改标签和备注 |
| GET/POST | `/api/mail/v1/alias-queue` | 查看或创建持久化批量队列 |
| POST | `/api/mail/v1/alias-queue/{pause,resume,cancel}` | 控制批量队列 |
| GET/POST | `/api/mail/v1/aliases/{id}/share-links` | 管理只读分享链接 |
| POST | `/api/mail/v1/share-links/{id}/revoke` | 撤销分享链接 |
| POST | `/api/mail/v1/share-links/clear-inactive` | 永久清理当前账号失效分享记录 |
| GET | `/api/mail/v1/mail/messages?alias=...` | 读取指定隐藏邮箱的邮件缓存 |
| GET/POST | `/api/mail/v1/mail/sync/{status,run}` | 查看状态或立即同步 IMAP |
| GET/PUT | `/api/mail/v1/mail/settings` | 读取或保存 IMAP 设置，密码不回显 |
| POST | `/api/mail/v1/mail/settings/test` | 使用当前表单测试只读 IMAP 连接 |
| GET | `/api/mail/v1/mail/recent` | 最近 3 天聚合邮件 |
| GET | `/api/mail/v1/mail/messages/{uid}?alias=...` | 单封邮件详情与代码识别 |
| POST | `/api/mail/v1/mail/messages/{uid}/hide` | 从本地缓存隐藏邮件 |
| POST | `/api/mail/v1/mail/messages/hide-batch` | 批量从本地缓存隐藏邮件 |
| POST | `/api/mail/v1/mail/messages/clear` | 永久清理当前账号的 SQL 邮件缓存 |
| GET | `/api/mail/v1/mail/sync/wait` | 等待缓存 revision 变化 |

### 邮件系统使用日志

```http
GET /api/mail/v1/activity-logs?page=1&pageSize=10&search=&level=&category=&source=&start=&end=
```

`pageSize` 可选 `10`、`20`、`50` 或 `100`，默认 `10`；`level` 可选 `info`、`warning`、`error`；
`category` 可选 `alias`、`session`、`mailbox`、`automation`；`source` 可选 `user`、
`background`、`system`。`start` 和 `end` 使用 RFC3339。接口由服务端固定查询 `mail` 模块，
不接受模块参数。返回 `items`、`total`、分页信息以及今日、24 小时失败、24 小时后台任务统计。

日志查询本身、状态轮询和长轮询不会写入日志。字段与脱敏要求见 [`LOGGING.md`](LOGGING.md)。

`POST /api/mail/v1/activity-logs/clear` 会永久删除邮件系统日志表中当前账号的记录，
不会只隐藏前端数据。该操作需要 API Key 鉴权且前端必须二次确认。

失败或警告日志的 `detail` 保存经过脱敏和截断的业务错误响应，不记录 Cookie、密码、API Key、
邮件正文或验证码。`mail/messages/clear`、`activity-logs/clear` 和 `share-links/clear-inactive`
均直接执行 PostgreSQL `DELETE`，不是前端过滤。

### 创建邮箱

请求：

```http
POST /api/mail/v1/aliases
Content-Type: application/json
```

```json
{"label":"shopping","note":""}
```

`label` 不能为空，`note` 可以为空字符串。

成功结果会额外返回本次创建路由：

```json
{
  "usedChannel": "apple_account",
  "attemptedChannels": ["apple_account"],
  "fallbackUsed": false,
  "detailConfirmed": true,
  "nextRetryAt": null
}
```

`usedChannel` 表示实际成功通道；`attemptedChannels` 是按顺序尝试的通道；
`detailConfirmed` 表示 Apple Account 创建后是否成功读取了完整详情。详情查询失败不会把
已经成功的创建标记为失败。

### 导入 Session

推荐优先使用协议登录。开始登录请求：

```json
{
  "appleId": "owner@example.com",
  "password": "本次登录使用的密码",
  "channel": "apple_account",
  "twoFactorMethod": "trusted_device"
}
```

`channel` 可选 `apple_account` 或 `icloud_web`，前端默认选择 `apple_account`。Apple Account
管理态用于隐藏邮箱列表、创建、编辑、启停和删除；iCloud Web 主会话用于兼容旧接口并作为
备用通道。`twoFactorMethod` 可选 `trusted_device` 或 `phone`。`iCloud Web` 协议登录会根据
Apple 返回的账号国家或目标域名自动选择 `icloud.com` 或 `icloud.com.cn`，无需指定区域；
`region` 仅作为旧客户端的可选兼容字段保留。

需要二次验证时，开始接口返回内存态 `pendingId`，有效期为 10 分钟：

```json
{"pendingId":"...","code":"123456"}
```

密码和验证码只参与当次 Apple 协议请求，不会写入状态文件、日志或 API 响应。接口也不
返回 Cookie、`scnt`、Session ID 或动态 API Key。

手动导入是协议登录不可用时的兼容方案。`curl_text` 可以是浏览器的
**Copy as cURL (bash)** 内容，也可以是包含请求 Cookie 的 HAR JSON 字符串。

```json
{
  "curl_text": "curl ... /v2/hme/list ..."
}
```

导入器根据请求 URL 中的 `maildomainws.icloud.com` 或 `maildomainws.icloud.com.cn`
自动识别区域。旧客户端仍可通过可选的 `icloud_region` 显式覆盖识别结果。

导入器要求 Cookie 中包含：

- `X-APPLE-DS-WEB-SESSION-TOKEN`
- `X-APPLE-WEBAUTH-USER`
- `X-APPLE-WEBAUTH-TOKEN`

Session 状态和错误响应不会返回持久化的 Cookie 内容。

### 创建通道

单次创建和自动创建计划优先使用健康且与当前 iCloud Web 属于同一 Apple ID 的
Apple Account 登录态。新接口在生成候选地址前失败时可以回退到 iCloud Web；确认创建
请求一旦开始，结果可能已经生效，此时不会自动回退，避免重复创建。

Apple Account 管理态过期或首次返回认证失效时，服务会在确认创建之前自动刷新并安全重试
一次。两个通道的失败和冷却状态按账号保存在 PostgreSQL；某通道冷却
期间自动跳过该通道。Apple 返回 `retryAfter` 时按其指定时间冷却，否则限额使用 2 分钟、
其他暂时错误使用 30 秒。

持久化批量队列继续使用 iCloud Web 的“生成候选 + 确认占用”流程。队列会保存中间候选
状态以支持重启恢复，因此不会在任务中途切换创建协议。

### 自动刷新

```json
{"enabled":true,"intervalSeconds":600}
```

`intervalSeconds` 最小为 300 秒。检测到 HTTP 401、403 或 421 等认证失效结果
时，自动刷新会自行关闭，并在状态中标记需要重新导入 Session。

### 自动创建计划

```json
{"enabled":true,"batchSize":5,"aliasIntervalSeconds":3,"intervalSeconds":180,"label":"shopping","note":""}
```

`enabled` 控制后台周期执行，`run` 可以在计划关闭时手动执行一轮。同一时间只允许一轮创建任务。
状态中的 `lastUsedChannel`、`lastFallbackUsed` 和 `lastAttemptedChannels` 用于显示最近一次成功创建的实际通道和自动兜底情况。

### 持久化批量队列

```json
{"baseLabel":"shopping","count":20,"note":"","requestId":"客户端生成的唯一值"}
```

队列上限为 99 个。`requestId` 用于防止网络重试重复入队。服务重启后继续未完成任务；若在保留邮箱的请求途中重启，会进入 `needs_attention`，先用 iCloud 列表核对候选地址后再继续。Apple 返回 `-41015` 后默认冷却 30 分钟。

### IMAP 收件箱

收件箱可以在前端“IMAP 设置”中配置，也兼容服务器环境变量作为首次默认值。设置按账号保存到
PostgreSQL；密码只接受写入，读取接口仅返回
`passwordConfigured`，不会返回密码正文。

```json
{
  "username": "your-name@icloud.com",
  "password": "应用专用密码；留空表示保留原密码",
  "host": "imap.mail.me.com",
  "port": 993,
  "mailbox": "INBOX",
  "enabled": true,
  "pollSeconds": 120,
  "lookbackDays": 90,
  "cacheMax": 5000
}
```

iCloud Mail 固定使用 `imap.mail.me.com:993`，密码必须是在 Apple Account 安全设置中生成的
App 专用密码，不能使用 Apple ID 登录密码。前端会按常见邮箱域名推荐服务器，但仍允许填写
其他合法的自定义 IMAP 主机。

所有连接固定使用 TLS，并以只读模式选择邮箱。首次同步按配置的回看天数读取，之后使用持久
UID 游标增量同步；单批最多 200 封，积压批次会继续推进。Worker 优先使用 IMAP IDLE，
服务器不支持时自动按配置轮询。只缓存属于当前启用隐藏邮箱白名单的邮件。账号、主机、端口
或邮箱目录发生变化时会清空上一连接目标的本地邮件缓存，避免混用 UID 和邮件内容。

列表接口只返回 160 字纯文本预览。单封详情按需返回完整纯文本和经过白名单清理的 `safeHtml`；脚本、表单、附件、远程图片、样式和非 HTTP(S) 链接均被移除。前端仅在 sandbox iframe 中显示该内容。

## 旧版兼容行为

上述邮件接口都可以去掉 `/api/mail` 前缀，通过 `/v1` 调用。旧版
`GET /v1/aliases/export.csv` 保持原行为，将 CSV 文本放在 JSON 响应的 `data`
字段中；标准接口 `/api/mail/v1/aliases/export.csv` 直接返回 `text/csv` 文件。

## 常用错误代码

| HTTP 状态 | 错误代码 | 说明 |
| --- | --- | --- |
| 400 | `BAD_REQUEST` | JSON、参数、cURL 或 HAR 内容无效 |
| 401 | `UNAUTHORIZED` | API Key 缺失或错误 |
| 409 | `SESSION_MISSING` | 尚未导入 iCloud Session |
| 409 | `SESSION_EXPIRED` | Apple 已拒绝当前 Session |
| 409 | `UPDATE_IN_PROGRESS` | 已有更新任务正在执行 |
| 409 | `QUEUE_ACTIVE` | 已有批量队列正在执行 |
| 409 | `RESULT_CONFIRMATION_REQUIRED` | 上次保留结果不明确，需要确认 |
| 400 | `APPLE_ACCOUNT_MISMATCH` | Apple Account 与当前 iCloud Web 不是同一账号 |
| 400 | `QUEUE_ACCOUNT_MISMATCH` | 活动队列绑定了另一个 iCloud 账号 |
| 502 | `APPLE_PROTOCOL_ERROR` | Apple 登录协议临时失败或被拒绝 |
| 502 | `APPLE_ACCOUNT_EXPIRED` | Apple Account 短时管理态失效 |
| 503 | `MAILBOX_UNAVAILABLE` | IMAP 未配置、认证失败或暂时不可用 |
| 502 | `ICLOUD_ERROR` | iCloud 返回错误或异常响应 |
| 500 | `STORAGE_ERROR` | 状态文件写入失败 |
