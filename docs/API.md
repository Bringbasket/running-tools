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

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/mail/v1/aliases` | 获取隐藏邮箱列表 |
| POST | `/api/mail/v1/aliases` | 创建隐藏邮箱 |
| GET | `/api/mail/v1/aliases/export.csv` | 直接下载 CSV 文件 |
| POST | `/api/mail/v1/aliases/{id}/enable` | 启用邮箱 |
| POST | `/api/mail/v1/aliases/{id}/disable` | 停用邮箱 |
| POST | `/api/mail/v1/aliases/{id}/delete` | 删除邮箱 |
| GET | `/api/mail/v1/session/status` | 读取持久化 Session 状态 |
| POST | `/api/mail/v1/session/refresh` | 立即检查当前 Session |
| POST | `/api/mail/v1/session/import` | 从 cURL 或 HAR 导入 Session |
| GET | `/api/mail/v1/auto-refresh` | 读取自动刷新设置 |
| POST | `/api/mail/v1/auto-refresh` | 修改自动刷新设置 |
| POST | `/api/mail/v1/auto-refresh/run` | 立即执行一次自动刷新 |
| GET | `/api/mail/v1/create-schedule` | 读取自动创建计划 |
| POST | `/api/mail/v1/create-schedule` | 修改自动创建计划 |
| POST | `/api/mail/v1/create-schedule/run` | 立即执行一轮创建 |
| POST | `/api/mail/v1/create-schedule/stop` | 暂停当前计划和执行 |
| POST | `/api/mail/v1/aliases/{id}/update` | 修改标签和备注 |
| GET/POST | `/api/mail/v1/alias-queue` | 查看或创建持久化批量队列 |
| POST | `/api/mail/v1/alias-queue/{pause,resume,cancel}` | 控制批量队列 |
| GET/POST | `/api/mail/v1/aliases/{id}/share-links` | 管理只读分享链接 |
| POST | `/api/mail/v1/share-links/{id}/revoke` | 撤销分享链接 |
| GET | `/api/mail/v1/mail/messages?alias=...` | 读取指定隐藏邮箱的邮件缓存 |
| GET/POST | `/api/mail/v1/mail/sync/{status,run}` | 查看状态或立即同步 IMAP |
| GET | `/api/mail/v1/mail/recent` | 最近 3 天聚合邮件 |
| GET | `/api/mail/v1/mail/messages/{uid}?alias=...` | 单封邮件详情与代码识别 |
| POST | `/api/mail/v1/mail/messages/{uid}/hide` | 从本地缓存隐藏邮件 |
| POST | `/api/mail/v1/mail/messages/hide-batch` | 批量从本地缓存隐藏邮件 |
| GET | `/api/mail/v1/mail/sync/wait` | 等待缓存 revision 变化 |

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

### 导入 Session

`curl_text` 可以是浏览器的 **Copy as cURL (bash)** 内容，也可以是包含请求
Cookie 的 HAR JSON 字符串。

```json
{
  "curl_text": "curl ... /v2/hme/list ...",
  "icloud_region": "international"
}
```

`icloud_region` 可选值：

- `international`：iCloud 国际版；
- `china`：iCloud 中国大陆版。

导入器要求 Cookie 中包含：

- `X-APPLE-DS-WEB-SESSION-TOKEN`
- `X-APPLE-WEBAUTH-USER`
- `X-APPLE-WEBAUTH-TOKEN`

Session 状态和错误响应不会返回持久化的 Cookie 内容。

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

### 持久化批量队列

```json
{"baseLabel":"shopping","count":20,"note":"","requestId":"客户端生成的唯一值"}
```

队列上限为 99 个。`requestId` 用于防止网络重试重复入队。服务重启后继续未完成任务；若在保留邮箱的请求途中重启，会进入 `needs_attention`，先用 iCloud 列表核对候选地址后再继续。Apple 返回 `-41015` 后默认冷却 30 分钟。

### IMAP 收件箱

收件箱使用服务器环境变量中的 IMAP 凭据，通过 TLS 只读连接。首次同步按配置的回看天数读取，之后使用持久 UID 游标增量同步；单批最多 200 封，积压批次会继续推进。Worker 优先使用 IMAP IDLE，服务器不支持时自动按配置轮询。只缓存属于当前启用隐藏邮箱白名单的邮件，响应不会包含 IMAP 密码。

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
| 503 | `MAILBOX_UNAVAILABLE` | IMAP 未配置、认证失败或暂时不可用 |
| 502 | `ICLOUD_ERROR` | iCloud 返回错误或异常响应 |
| 500 | `STORAGE_ERROR` | 状态文件写入失败 |
