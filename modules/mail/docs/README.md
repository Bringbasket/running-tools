# 邮件系统模块

## 模块技术栈

邮件模块遵循项目统一规范：Go + Gin + Ent（后端）、Vue 3 + TypeScript + Vite（前端）、
PostgreSQL 15+ + Redis 7+（目标数据层）。当前版本为了兼容原 `hme-manager`，仍使用
Go HTTP 服务和 JSON 文件保存 Session/状态；Gin、Ent、PostgreSQL 和 Redis 暂未启用，
后续接入不得改变现有 API 兼容性和 Cookie 最小暴露原则。

邮件系统使用已经获得授权的 iCloud 网页 Session 管理隐藏邮件地址。前端支持 Apple
SRP 协议登录：Apple ID、密码和两步验证码会经当前服务进程转发给 Apple；密码和验证码
只用于当次请求，不写入文件、日志或响应。部署者必须使用 HTTPS 并限制 API Key 权限。

## 模块目录

- `backend/`：iCloud 客户端、Session 持久化、自动刷新、API 和后台创建 Worker；
- `frontend/`：邮件导航、页面、组件、API 客户端、类型和测试；
- `docs/`：邮件模块 API、数据持久化和运维说明。

## 已实现功能

- 获取隐藏邮箱列表；
- 创建、编辑标签/备注、启用、停用和删除邮箱；
- 搜索、状态筛选和 CSV 导出；
- API 请求调试和 cURL 生成；
- 国际版和中国大陆版 Session 导入；
- iCloud Web / Apple Account 双通道协议登录和原地 2FA 向导；
- Session 状态持久化和立即检查；
- 服务端自动刷新；
- 前端配置、Go 后台 Worker 定时批量创建；
- 可暂停、继续、取消并可重启恢复的持久化批量队列；
- 可撤销、可设置有效期的只读分享链接；
- TLS 只读 IMAP 收件箱、邮件缓存和验证码识别。

## 导入 Session

Session 页面默认使用 Apple 协议登录。`iCloud Web` 是完整主会话，负责邮箱列表、同步、
启停、删除和创建；`Apple Account` 是短时管理态，只作为创建通道。两者不是可互换的
“新旧版本”，也不会合并保存。详细设计见 [`APPLE_LOGIN.md`](APPLE_LOGIN.md)。

手动 cURL/HAR 导入仅作为兼容入口保留。导入接口接受以下 JSON：

```json
{
  "curl_text": "curl ... /v2/hme/list ..."
}
```

`curl_text` 也可以是包含请求 Cookie 的 HAR JSON 字符串。
区域根据请求 URL 的 `.icloud.com` 或 `.icloud.com.cn` 自动识别；旧客户端提交的
`icloud_region` 仍作为兼容覆盖项保留。

解析器要求 Cookie 中包含：

- `X-APPLE-DS-WEB-SESSION-TOKEN`
- `X-APPLE-WEBAUTH-USER`
- `X-APPLE-WEBAUTH-TOKEN`

Cookie 只保存在 `hme-config.json` 中，该文件必须只有所有者可读写。Session 状态
接口不会返回 Cookie。

## 自动刷新

默认启用，默认间隔为 600 秒，最小间隔为 300 秒。可以使用环境变量
`MAIL_AUTO_REFRESH_INTERVAL` 修改首次生成配置时的默认间隔。

检测到 Apple 返回 HTTP 401、403 或 421 后，自动刷新会关闭并记录需要重新导入
Session。重新导入不会把 Cookie 写入日志或返回给前端。

## 自动创建

在“隐藏邮箱”页面的“自动创建计划”中配置并开启即可。任务由 Go 服务端 Worker 执行，不依赖宝塔或浏览器页面。

默认参数为每轮 5 个、每个邮箱间隔 3 秒、每轮间隔 180 秒，标签为 `shopping`，备注为空。配置保存在 `data/mail/state/create-schedule.json`。

单次创建和自动创建都优先使用 Apple Account，失败且尚未进入确认阶段时自动使用 iCloud Web
兜底。Apple Account 首次认证失效会先刷新管理态并安全重试一次；确认请求已发送后不会盲目
重试。通道限额和暂时错误会进入持久冷却，自动创建本轮后续地址直接跳过冷却通道。

## 持久化文件

| 文件 | 内容 |
| --- | --- |
| `data/mail/hme-config.json` | iCloud 主机、请求元数据和 Cookie |
| `data/mail/state/hme-session.json` | 不含秘密的 Session 元数据 |
| `data/mail/state/session-state.json` | 最近一次 Session 检查结果、邮箱数量和转发地址摘要 |
| `data/mail/state/apple-account-state.json` | Apple Account 短时管理态、Cookie、scnt 和动态 API Key，权限 `0600` |
| `data/mail/state/create-channels.json` | 两个创建通道的冷却截止时间、最近创建和错误摘要 |
| `data/mail/state/auto-refresh.json` | 自动刷新设置和执行时间 |
| `data/mail/state/create-schedule.json` | 后台创建计划设置和执行状态 |
| `data/mail/state/alias-queue.json` | 持久化批量队列、候选地址和错误状态 |
| `data/mail/state/share-links.json` | 分享链接和浏览器会话的 SHA-256 摘要 |
| `data/mail/state/mailbox-cache.json` | IMAP UID 游标、白名单邮件的纯文本/安全 HTML 缓存、验证码和本地隐藏记录 |
| `data/mail/state/mailbox-config.json` | 前端保存的 IMAP 连接与同步设置，权限 `0600`，密码不通过 API 回显 |

文件使用临时文件加替换的方式写入，并设置为所有者专用权限。API 响应不会包含
`hme-config.json` 内容或 Cookie 值。

## 标准接口与兼容接口

标准接口前缀为 `/api/mail/v1`。原有 `/v1` 接口继续保留，因此旧版前端、cURL
和 cURL 调用可以在迁移期间继续工作。完整接口见项目根目录的
[`docs/API.md`](../../../docs/API.md)。
