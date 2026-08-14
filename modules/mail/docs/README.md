# 邮件系统模块

## 模块技术栈

邮件模块遵循项目统一规范：Go + Gin + Ent（后端）、Vue 3 + TypeScript + Vite（前端）、
PostgreSQL 15+ + Redis 7+（数据层）。当前版本为了兼容原 `hme-manager`，仍使用
Go 标准库 HTTP 服务；生产数据统一进入 PostgreSQL，Redis 用于多实例同步锁。旧 JSON 只会在
首次启动时导入，不再作为生产读写源。

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
- 批量取件链接：按邮箱创建时间从早到晚生成，支持数量、有效期和 TXT 导出；
- TLS 只读 IMAP 收件箱、邮件缓存和验证码识别。
- 多母号健康摘要与每账号独立 HTTP/HTTPS/SOCKS5 代理；
- 邮件系统独立使用日志、筛选、分页和详情追踪。

## 使用日志

侧边栏“使用日志”只展示邮件系统的用户操作和后台任务，不会混入工具箱或未来模块日志。
日志支持关键词、级别、分类、来源和日期筛选，默认保留 30 天且最多 10,000 条。日志查询、
状态轮询和长轮询不会产生新日志；Cookie、密码、API Key、邮件正文和验证码禁止写入日志。
完整平台规范见 [`../../../docs/LOGGING.md`](../../../docs/LOGGING.md)。

## 导入 Session

Session 页面默认使用 Apple 协议登录。`Apple Account` 是优先使用的短时管理态，负责邮箱列表、
创建、编辑、启停和删除；`iCloud Web` 是长 Session 兼容通道和安全回退。两者不是可互换的
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

Cookie 在生产模式保存在 PostgreSQL `running_state` 中；旧 `hme-config.json` 仅作为首次导入
来源。iCloud Web 每次响应的 `Set-Cookie` 会自动合并续存，即使业务响应失败也不会丢失滚动
Cookie。Session 状态接口不会返回 Cookie。

## 自动刷新

默认启用，默认间隔为 600 秒，最小间隔为 300 秒。Apple Account 还会根据管理态返回的短 TTL
提前刷新，不会机械地等到固定间隔或过期后才请求。可以使用环境变量
`MAIL_AUTO_REFRESH_INTERVAL` 修改首次生成配置时的默认间隔。

检测到 Apple 返回 HTTP 401、403 或 421 后，自动刷新会关闭并记录需要重新导入
Session。重新导入不会把 Cookie 写入日志或返回给前端。

## 自动创建

在“隐藏邮箱”页面的“自动创建计划”中配置并开启即可。任务由 Go 服务端 Worker 执行，不依赖宝塔或浏览器页面。

默认参数为每轮 5 个、每个邮箱间隔 3 秒、每轮间隔 180 秒，标签为 `shopping`，备注为空。
配置按邮件账号保存在 PostgreSQL；每个账号有独立 Worker，可以同时运行互不阻塞。

单次创建和自动创建都优先使用 Apple Account，失败且尚未进入确认阶段时自动使用 iCloud Web
兜底。Apple Account 首次认证失效会先刷新管理态并安全重试一次；确认请求已发送后不会盲目
重试。通道限额和暂时错误会进入持久冷却，自动创建本轮后续地址直接跳过冷却通道。

## PostgreSQL 持久化

| 表 | 内容 |
| --- | --- |
| `mail_accounts` | 邮件账号、显示身份及独立代理配置 |
| `running_state` | 每账号 Session、登录态、配置和任务当前状态 |
| `mailbox_messages` | IMAP 邮件正文、安全 HTML 和验证码索引 |
| `mailbox_hidden_messages` | 每账号的本地隐藏记录 |
| `mailbox_sync_states` | 每账号 IMAP UID 游标与同步状态 |
| `mail_share_links` | 每账号分享链接摘要、有效期和撤销状态 |
| `mail_share_sessions` | 分享浏览器会话摘要和有效期 |
| `activity_logs` | 每账号使用日志，默认 30 天且最多 10,000 条 |

所有邮件 API 通过 `X-Mail-Account-ID` 选择账号。网页切换账号只改变当前视图，其他账号的
自动刷新、自动创建和 IMAP Worker 会继续运行。敏感字段不会通过 API 回显或写入日志。
母号新增与切换统一位于邮件系统的“账号管理”页面；每个母号创建独立运行空间后，再进入
“Session 管理”配置 Apple Account 或 iCloud Web 登录态。平台顶栏不提供重复的账号操作入口。
非默认母号可以在账号管理中永久删除；删除会先停止该账号的全部 Worker，再在一个事务中清理
Session、任务状态、邮件缓存、分享链接和使用日志。默认账号 `default` 用于兼容旧数据，禁止删除。
账号管理页同时展示 Web、Apple Account、IMAP 和后台任务健康摘要。每个账号可以配置独立代理，
代理覆盖 Apple 登录及两个邮件管理通道；API 只返回是否配置，不回显地址和凭据。IMAP Worker
复用账号级认证连接执行同步和 IDLE，并在配置变化、故障或停止时关闭连接。

## 标准接口与兼容接口

### 批量取件

邮箱管理中的“批量取件”只处理启用中的邮箱，后端按创建时间从早到晚选择指定数量，
创建时间缺失的邮箱放在末尾。每个取件地址可单独复制，也可以导出为 `邮箱----取件地址`
格式的 TXT 文件。生成过程是一次事务，数量不足或有效期不合法时不会留下部分链接。

分享页面会直接在页面内展示邮件正文和验证码按钮，不再通过二次详情弹窗展开。

标准接口前缀为 `/api/mail/v1`。原有 `/v1` 接口继续保留，因此旧版前端、cURL
和 cURL 调用可以在迁移期间继续工作。完整接口见项目根目录的
[`docs/API.md`](../../../docs/API.md)。
