# Apple 登录与创建通道

## 设计目标

Session 页面只提供一个登录向导，通过分段控件切换 `Apple Account` 和 `iCloud Web`，默认
选择 Apple Account。
需要二次验证时，表单原地切换为 6 位验证码输入，不增加零散的“发送、刷新、信任、保存”
按钮。手动 cURL/HAR 导入放在默认折叠的兼容区域。

页面必须继续遵循 [`docs/UI_GUIDELINES.md`](../../../docs/UI_GUIDELINES.md)：一个视觉主区、
按钮 loading 相互独立、错误靠近操作区域、移动端不横向溢出，不使用嵌套卡片或大段教程文案。

## 两种登录态

| 通道 | Apple 网页接口 | 用途 | 持久化位置 |
| --- | --- | --- | --- |
| Apple Account | `/account/manage/*` | 默认隐藏邮箱列表、创建和管理通道 | PostgreSQL 账号状态 |
| iCloud Web | `/accountLogin`、`/setup/ws/1/validate`、`/v2/hme/*` | 列表、检查、管理、同步和备用创建 | PostgreSQL 账号状态 |

`iCloud Web` 是现有 Session 的完整替代来源。登录使用 SRP，完成 2FA 和 trust 后，通过
validate 响应生成 DSID、maildomainws 主机、构建号和 Cookie。
协议登录会根据 Apple 返回的账号国家自动切换 `icloud.com` 与 `icloud.com.cn`，界面不要求
用户选择区域。兼容导入则根据 cURL/HAR 内的实际请求主机自动识别区域。

`Apple Account` 是 Apple 账号管理网页使用的短时内部接口，需要 Cookie、`scnt`、Session
ID 和动态 API Key。邮箱列表、编辑、启停和删除优先使用该通道；不可用时在仍有有效 iCloud
Web 主会话的情况下回退旧接口。服务会在自动 Session 检查时刷新状态；接口失效后必须重新登录。
管理接口返回的 `timeOutInterval` 会用于计算安全刷新时间，Worker 会在过期前提前续期，
而不是固定等到管理态已经失效后才请求。
它不是 Apple 正式公开、承诺兼容的 API，不能根据第三方项目的测试结果承诺固定配额。

## 创建策略

单次创建和周期创建使用以下顺序：

1. 已保存 Apple Account 登录态且账号匹配时，默认优先调用新接口。
2. 管理态 TTL 已到期时先刷新；首次认证失效且尚未确认创建时，再刷新并重试一次。
3. 在生成候选地址之前失败，可以回退到 iCloud Web。
4. `/account/manage/email/private/add/complete` 一旦开始，失败结果视为不确定，不重试、不回退。
5. 创建成功后最佳努力读取 `.em` 详情，补全转发地址和启用状态；读取失败不影响创建成功。
6. 持久化批量队列固定使用 iCloud Web，保留候选地址和确认阶段的重启恢复语义。

每个通道的冷却截止时间、最近错误和最近成功创建按邮件账号保存在 PostgreSQL。Apple
返回 `retryAfter` 时按其值冷却；未返回时限额默认 2 分钟、其他暂时错误默认 30 秒。冷却期间
自动创建直接跳过该通道，服务重启不会丢失冷却状态。

两个创建通道由互斥锁串行执行。每次 Apple Account 请求返回的新 Cookie、`scnt` 和 API Key
都会原子写回，即使后续创建失败也不会丢失已刷新的状态。

## 安全边界

- Apple 密码只存在于当前 HTTP 请求和 SRP 计算期间，不持久化。
- 2FA 待验证状态仅保存在进程内存中，10 分钟后删除；服务重启后需重新登录。
- Cookie、`scnt`、Session ID 和动态 API Key 使用 `0600` 文件保存，不通过 API 返回。
- 登录和 Apple Account 错误只返回阶段化信息与 HTTP 状态，不透传可能包含敏感数据的原始正文。
- 活动批量队列期间不允许切换到不同 DSID 的 iCloud Web 登录。
- 已有 iCloud Web 与新登录 Apple Account 能识别到 Apple ID 时，账号必须一致。
- 生产环境必须使用 HTTPS；否则 Apple ID、密码和验证码在浏览器到服务端这一段无法得到传输保护。

## 后续维护

Apple 网页内部接口可能调整路径、请求头、Hashcash 或浏览器指纹格式。修改协议时必须补充
模拟上游测试，至少覆盖 2FA、状态持久化、敏感字段不回显、安全回退和确认后结果不确定分支。
