# HTTP API

浏览器通过登录接口取得 `HttpOnly` Session Cookie；脚本调用使用在“API 调试”页面创建的
可撤销访问令牌：

```http
Authorization: Bearer <API_ACCESS_TOKEN>
```

除 `/health`、`GET /api/auth/status`、`POST /api/auth/login`、取件接口和前端静态资源外，
`/api/*` 与 `/v1/*` 默认都要求认证。浏览器 Cookie 不应复制到脚本中。

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
| GET | `/health` | 健康检查，不需要登录 |
| GET | `/api/auth/status` | 检查当前浏览器登录状态 |
| POST | `/api/auth/login` | 使用账号密码创建浏览器会话 |
| GET | `/api/auth/me` | 读取当前登录用户 |
| PUT | `/api/auth/password` | 修改密码并撤销当前会话之外的浏览器会话 |
| POST | `/api/auth/logout` | 撤销当前浏览器会话 |
| GET/POST | `/api/auth/tokens` | 查询或创建脚本访问令牌 |
| DELETE | `/api/auth/tokens/{id}` | 撤销脚本访问令牌 |
| GET | `/api/system/version` | 读取当前版本、目标版本和更新状态 |
| POST | `/api/system/version/check` | 仅检查是否存在新构建，不重启服务 |
| POST | `/api/system/update` | 提交宿主机更新请求 |

兼容地址：`/v1/system/version`、`/v1/system/version/check`、`/v1/system/update`。

`GET /api/system/version` 同时返回面向用户的版本号和用于精确比较镜像的构建标识：

```json
{
  "currentVersion": "0.0.42",
  "latestVersion": "0.0.43",
  "currentRevision": "abcdef123456...",
  "latestRevision": "123456abcdef...",
  "updateAvailable": true
}
```

版本号由 GitHub Actions 运行序号生成；是否存在更新始终比较完整 commit SHA，避免版本文案变化
影响更新判断。

### 平台登录

登录请求为 `{"username":"admin","password":"..."}`。成功后服务端设置名为
`running_session` 的 `HttpOnly`、`SameSite=Strict` Cookie；HTTPS 下自动增加 `Secure`。
会话默认有效 168 小时，可通过 `RUNNING_AUTH_SESSION_HOURS` 调整。密码修改请求为：

```json
{"currentPassword":"旧密码","newPassword":"至少 10 个字符的新密码"}
```

修改密码会撤销其他浏览器会话和现有 API 访问令牌。登录按账号和来源地址限制为 15 分钟内最多
5 次失败，Redis 不可用时回退为当前进程内限流；登录成功会清除对应失败计数。
数据库没有用户时固定初始化 `admin / admin123`，该初始账号只能调用改密和退出接口。

创建脚本令牌请求为 `{"name":"自动化脚本","expiresInDays":90}`，有效期范围为 1-365 天。
令牌原文只在创建响应返回一次，列表只返回名称、前缀和使用时间。脚本使用
`Authorization: Bearer rtk_...`，撤销后立即失效。

必须先调用 `POST /api/system/version/check`。检查任务只拉取镜像元数据并比较构建
标识，不会重启服务；只有确认存在新构建后，网页才会显示“立即更新”。

`POST /api/system/update` 返回 HTTP 202。接口只在 `data/system` 中写入更新请求，
真正的镜像拉取和容器重启由宿主机服务完成。

## 邮件接口

邮件模块支持多个 iCloud 账号。除账号管理接口外，标准邮件接口必须携带当前账号：

```http
X-Mail-Account-ID: default
```

未提供时兼容使用 `default`。前端切换账号后会在所有邮件请求中自动设置该请求头；每个账号拥有
独立 Session、邮箱列表、自动刷新、自动创建、批量队列、IMAP 缓存、分享链接和使用日志。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/mail/v1/accounts` | 获取邮件账号列表 |
| POST | `/api/mail/v1/accounts` | 新建独立邮件账号运行空间 |
| POST | `/api/mail/v1/accounts/{id}/proxy/test` | 测试指定母号的候选代理，不保存配置 |
| PUT | `/api/mail/v1/accounts/{id}/proxy` | 设置或清除指定母号的独立代理 |
| DELETE | `/api/mail/v1/accounts/{id}` | 永久删除非默认账号及其数据 |
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
| POST | `/api/mail/v1/aliases/batch-share-links` | 按创建时间从早到晚批量生成取件链接 |
| POST | `/api/mail/v1/share-links/{id}/revoke` | 撤销分享链接 |
| POST | `/api/mail/v1/share-links/clear-inactive` | 永久清理当前账号失效分享记录 |
| GET | `/api/mail/v1/mail/messages?alias=...&limit=...` | 读取指定隐藏邮箱最新邮件，`limit` 最大为 100 |
| GET/POST | `/api/mail/v1/mail/sync/{status,run}` | 查看状态或立即同步 IMAP |
| GET/PUT | `/api/mail/v1/mail/settings` | 读取或保存 IMAP 设置，密码不回显 |
| POST | `/api/mail/v1/mail/settings/test` | 使用当前表单测试只读 IMAP 连接 |
| GET | `/api/mail/v1/mail/recent?limit=...` | 最近 3 天聚合邮件，`limit` 最大为 500 |
| GET | `/api/mail/v1/mail/messages/{uid}?alias=...` | 单封邮件详情与代码识别 |
| POST | `/api/mail/v1/mail/messages/{uid}/hide` | 从本地缓存隐藏邮件 |
| POST | `/api/mail/v1/mail/messages/hide-batch` | 批量从本地缓存隐藏邮件 |
| POST | `/api/mail/v1/mail/messages/clear` | 永久清理当前账号的 SQL 邮件缓存 |
| GET | `/api/mail/v1/mail/sync/wait` | 等待缓存 revision 变化 |

批量取件接口请求体为 `{ "count": 5, "expiresInSeconds": 86400 }`。`count` 范围为 1-750，
只选择启用中的隐藏邮箱，按邮箱创建时间从早到晚排序；创建时间缺失的邮箱排在最后。
可用邮箱不足时返回 `INSUFFICIENT_ALIASES`，不会生成部分结果。`expiresInSeconds` 可为 `null`
表示永久，也可以传 300 至 31536000 秒之间的自定义值。响应 `data.items` 中每项包含
`alias`、`shareUrl` 和有效期；原始 token 只在创建响应返回，数据库只保存摘要和哈希。

分享页面的 `/share/v1/messages?full=1` 会直接返回当前链接可见邮件正文，用于页面内展示；
不带 `full=1` 时仍返回摘要，单封详情接口继续兼容。

生成的取件地址为 `/mail?email=ALIAS&token=TOKEN`，访问时由浏览器直接显示紧凑 JSON 响应；旧的
`/share/#TOKEN` 地址已停用并返回 `410 Gone`。其中 `data.message` 只包含最新邮件的 `uid`、
`from`、`subject`、`date`、压缩后的 `text`、`codes` 和 `partnerCodes`，不会返回重复的 `aliases` 或
`safeHtml`。

删除母号会先停止该账号的 Session 自动刷新、自动创建、批量队列和 IMAP Worker，再永久清理
PostgreSQL 中对应的 Session/任务状态、邮件缓存、分享链接和使用日志。默认账号 `default`
承载旧数据兼容，接口会拒绝删除。该操作不可恢复，且不会影响其他母号的后台任务。

账号列表返回 `status`、`statusMessage`、`icloudWeb`、`appleAccount`、`mailbox`、自动刷新、
自动创建、批量队列和 `aliasCount` 健康摘要。`status` 为 `active`、`warning`、`pending`
或 `error`。这些状态从各账号当前运行态实时汇总，不另存一份会过期的健康数据。

独立代理请求示例：

```json
{"proxy":"socks5://user:password@127.0.0.1:1080"}
```

支持 `http`、`https` 和 `socks5`；提交空字符串清除代理。代理同时用于该母号的 Apple
协议登录、Apple Account 管理接口、iCloud Web HME 接口和 IMAP 收件连接。配置代理后，
连接失败会直接报错，不会回退到服务器公网出口；更换代理会立即关闭旧 IMAP/IDLE 连接并按
新代理重连。列表和更新响应只返回 `hasProxy`，绝不回显代理地址、用户名或密码。

前端保存代理前会先调用 `/accounts/{id}/proxy/test`，由服务端通过候选代理访问 Apple 官方站点。
测试请求最长等待 15 秒，HTTP 200–399 判定为可用；测试不会写入 PostgreSQL，也不会替换当前
账号的 HTTP Client。成功响应仅包含 `reachable`、`statusCode`、`latencyMs` 和目标主机名，
不会回显代理地址或凭据。输入内容发生变化后，前端会立即作废上一次测试结果并重新禁用保存。
这个测试只验证 Apple HTTPS 出口；代理是否允许 CONNECT 到 IMAP `993` 端口，应在“收件箱 →
IMAP 设置”中执行“测试连接”，该测试与后台同步使用同一条母号代理链路。

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
不会只隐藏前端数据。该操作需要登录鉴权且前端必须二次确认。

失败或警告日志的 `detail` 保存经过脱敏和截断的业务错误响应，不记录 Cookie、密码、访问令牌、
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
iCloud Web 响应中的 `Set-Cookie` 会按 Cookie 名合并到当前 Session 并立即写回 PostgreSQL，
包括业务接口返回失败但同时下发新 Cookie 的情况。各母号的 Web 请求串行更新自己的 Cookie，
不会互相覆盖，也不会把 Cookie 写入使用日志。

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

`intervalSeconds` 最小为 300 秒。Apple Account 还会读取管理接口返回的短 TTL，按安全提前量
把下一次检查提前；因此实际 `nextRunAt` 可能早于配置的固定间隔。iCloud Web 长 Session
仍独立检查。自动检查开关开启时，Apple Account 使用独立约 3 分钟、±15% 随机错峰的保活周期，
并以 `/account/manage/forwardemail` 作为真实健康依据；`/v2/jslogs` 仅作补充。网络超时、连接
错误和 HTTP 5xx 只会在读取或候选生成请求上有限重试；编辑、停用、删除和最终确认请求保持
单次执行，避免上游已成功但响应丢失时重复改变数据。

Session 状态中的 Apple Account 通道会返回 `state`：`healthy` 表示可用，`degraded` 表示临时
异常且后台会继续重试，`reauth_required` 表示 Apple 已明确撤销管理态、需要重新登录。只有主
Session 需要重新导入时才会关闭整个自动刷新；Apple Account 单独失效不会反复请求，健康的
iCloud Web 仍可作为备用通道。过期的管理 TTL 在实际业务请求到达时仍会先尝试刷新，不会因为
倒计时过期而永久跳过 Apple Account。保活不会保存密码或验证码，也不能在 Apple 返回
`Invalid global session` 后免验证自动恢复。

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
服务器不支持时自动按配置轮询。同一母号复用已认证的 IMAP 连接完成增量同步和 IDLE，
通过 `NOOP` 验证连接；配置变化、认证失败、连接错误或 Worker 停止时关闭并按需重连。
IMAP、IDLE 和“测试连接”都复用该母号在账号管理中保存的独立代理；代理失败时禁止直连回退。
首次同步或隐藏邮箱集合变化时，服务端先按最新 UID 向前分批读取收件人邮件头，找到受每邮箱
100 封及账号 `cacheMax` 限制的候选 UID 后才读取完整正文，避免为历史无关邮件传输正文。
只缓存属于当前启用隐藏邮箱白名单的邮件。账号、主机、端口
或邮箱目录发生变化时会清空上一连接目标的本地邮件缓存，避免混用 UID 和邮件内容。

每个隐藏邮箱只保留并返回按邮件时间排序的最新 100 封；这不是独立扩容额度，当前母号的所有
隐藏邮箱仍共同受 `cacheMax` 账号级缓存总上限约束。最近邮件接口只聚合最近 3 天的可见邮件，
单次最多返回 500 封。PostgreSQL 会在数据库中先按账号、邮箱、可见状态和时间筛选，再应用
上述上限，不会为了返回 100/500 封而读取当前账号的全部正文。

收件箱状态和长轮询只读取 `mailbox_sync_states` 中当前账号的单行同步状态，不加载邮件正文或
隐藏记录；没有新邮件的同步只更新该状态行。列表接口只返回 160 字纯文本预览。单封详情按需
返回完整纯文本和经过白名单清理的 `safeHtml`；脚本、表单、附件、远程图片、样式和非 HTTP(S)
链接均被移除。前端仅在 sandbox iframe 中显示该内容。

## 旧版兼容行为

上述邮件接口都可以去掉 `/api/mail` 前缀，通过 `/v1` 调用。旧版
`GET /v1/aliases/export.csv` 保持原行为，将 CSV 文本放在 JSON 响应的 `data`
字段中；标准接口 `/api/mail/v1/aliases/export.csv` 直接返回 `text/csv` 文件。

## 常用错误代码

| HTTP 状态 | 错误代码 | 说明 |
| --- | --- | --- |
| 400 | `BAD_REQUEST` | JSON、参数、cURL 或 HAR 内容无效 |
| 401 | `UNAUTHORIZED` | 登录会话或访问令牌缺失、失效或已撤销 |
| 401 | `INVALID_CREDENTIALS` | 账号或密码错误 |
| 403 | `PASSWORD_CHANGE_REQUIRED` | 首次登录后尚未修改初始密码 |
| 429 | `LOGIN_RATE_LIMITED` | 登录失败次数超过限制 |
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
