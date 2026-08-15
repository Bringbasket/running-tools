# 生产部署

该部署方式不会把业务源码放在服务器应用目录中。GitHub Actions 负责构建镜像，
服务器只保存运行配置、Compose 文件和持久化数据。

## 基础设施状态

生产环境使用 PostgreSQL 15+ 和 Redis 7+。`compose.server.yml` 只创建本项目的应用、Redis
和 PostgreSQL 服务，使用独立的容器、网络和数据卷，不会操作其他 Compose 项目的容器。
邮件 Session、密码和业务数据均保存在本项目 PostgreSQL 数据卷中。

首次上线设置 `RUNNING_STORAGE_MODE=postgres` 和 `RUNNING_POSTGRES_PASSWORD`。Compose 会在
应用容器内使用 `postgres` 服务生成数据库连接串。应用启动时
执行内置的版本化迁移，并将现有邮件状态、缓存和分享数据导入 PostgreSQL；迁移完成后不再
写入业务 JSON 文件。迁移 SQL 由 Go 二进制内嵌执行，不需要单独维护或手工运行 SQL 文件。

应用通过 `go:embed` 嵌入前端静态资源，发布构建规范命令为
`go build -tags embed ./cmd/server`。当前数据库和缓存预留不改变现有部署命令。

## 服务器目录

```text
/www/wwwroot/running-tools/
|-- .env
|-- compose.server.yml
`-- data/
    |-- mail/                 # 生产业务状态在 PostgreSQL，此处仅保留目录
    `-- system/               # 更新服务通信文件
```

Redis 和 PostgreSQL 数据保存在当前 Compose 项目的独立命名卷中，不放入应用源码目录，也
不会与其他 Compose 项目的同名逻辑卷共用。

本地直接运行 Go 服务时，`RUNNING_DATABASE_URL` 可以使用 `127.0.0.1:5432`。使用本
Compose 时应用会自动连接内部的 `postgres:5432`，不需要把 PostgreSQL 暴露到宿主机。
若接入外部数据库，请显式设置完整连接串，例如：

```dotenv
RUNNING_DATABASE_URL=postgres://running_tools:password@host.docker.internal:5432/running_tools?sslmode=disable
```

Go 容器不挂载 Docker Socket。网页更新操作只会在 `data/system` 下写入一个小型
请求文件；由 root 管理的宿主机服务监控该文件，并负责拉取镜像和重启服务。

## 初次部署

1. 创建目录：

```bash
mkdir -p \
  /www/wwwroot/running-tools/data/mail/state \
  /www/wwwroot/running-tools/data/system
```

2. 将 `.env.example` 复制为 `.env`。数据库没有用户时会初始化 `admin / admin123`，首次登录
   必须立即修改密码。

3. 将 `compose.server.yml` 放入应用目录。

4. 将数据目录所有权交给容器用户：

```bash
chown -R 10001:10001 /www/wwwroot/running-tools/data
```

5. 私有 GHCR 镜像需要先登录一次。GitHub PAT 需要 `read:packages` 权限：

```bash
echo 'YOUR_GITHUB_PAT' | docker login ghcr.io -u Bringbasket --password-stdin
```

6. 拉取并启动服务：

```bash
cd /www/wwwroot/running-tools
docker compose --env-file .env -f compose.server.yml pull
docker compose --env-file .env -f compose.server.yml up -d
curl http://127.0.0.1:8091/health
```

反向代理必须使用 HTTPS，并转发原始 `Host` 和 `X-Forwarded-Proto`。Compose 默认仅监听
`127.0.0.1`，因此可以设置 `RUNNING_TRUST_PROXY=true` 读取代理传入的客户端地址；不要在应用
端口直接暴露公网时启用该选项。

从旧 Key 版本升级后，`RUNNING_API_KEY` 不再参与登录或 HTTP 鉴权，可以直接从 `.env` 删除。
尚未完成首次改密的管理员会统一恢复为初始密码 `admin123`；已经修改过的数据库密码不会被覆盖。

只有完成新旧接口对比后，才能将反向代理指向 `http://127.0.0.1:8091`。8091
端口用于避开当前运行在 8090 的 Python 服务。

## 网页更新功能

安装宿主机集成文件：

```bash
install -m 0755 deploy/host/update_from_registry.sh /usr/local/sbin/running-tools-update
install -m 0644 deploy/systemd/running-tools-update.service /etc/systemd/system/
install -m 0644 deploy/systemd/running-tools-update.path /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now running-tools-update.path
```

升级已有部署时也必须重新安装脚本和两个 systemd 单元，旧版宿主机脚本不支持
只检查不更新的 `check-request.json`。

版本面板右上角的检查按钮会调用 `POST /api/system/version/check`。宿主机只拉取
`latest` 镜像并比较构建标识，不会重启服务。确认存在新构建后，用户点击“立即
更新”才会调用 `POST /api/system/update`，宿主机随后部署已经构建完成的 `latest`
镜像，只重新创建 `running-tools` Compose 服务，并等待健康检查。如果新容器未能
进入健康状态，脚本会恢复上一个镜像。

该脚本使用 `--no-deps app`，只重建当前 Compose 项目的应用容器；Redis、已有 PostgreSQL
以及服务器上的其他 Docker Compose 项目不会随应用版本更新而重建。

检查宿主机单元：

```bash
systemctl status running-tools-update.path
systemctl status running-tools-update.service
journalctl -u running-tools-update.service -n 100 --no-pager
```

## 数据迁移

先备份，再将旧状态复制到新的模块目录：

```bash
scripts/migrate-hme-data.sh \
  /www/wwwroot/hme-manager/data \
  /www/wwwroot/running-tools/data
```

脚本只复制已知文件，不会删除或编辑旧数据。让 Go 服务先在 8091 运行，对比
`/v1/session/status` 和 `/v1/aliases`，再执行一次 Session 检查，最后才能切换
反向代理。

## 自动创建邮箱

部署后打开“隐藏邮箱”页面，在“自动创建计划”中设置每轮数量、邮箱间隔和执行周期，点击“保存并开启”即可。

任务由 Go 服务的后台 Worker 执行，不依赖宝塔计划任务或浏览器页面。设置按账号持久化在 PostgreSQL，服务重启后会读取并恢复每个账号已开启的周期计划。Apple 返回 `-41015` 时本轮提前结束；Session 失效时会自动暂停对应账号计划。

## 常用运维命令

```bash
cd /www/wwwroot/running-tools

# 查看状态和日志
docker compose --env-file .env -f compose.server.yml ps
docker compose --env-file .env -f compose.server.yml logs --tail=100 app

# 只重启本项目
docker compose --env-file .env -f compose.server.yml restart app

# 停止本项目
docker compose --env-file .env -f compose.server.yml down
```
