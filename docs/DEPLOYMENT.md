# 生产部署

该部署方式不会把业务源码放在服务器应用目录中。GitHub Actions 负责构建镜像，
服务器只保存运行配置、Compose 文件和持久化数据。

## 基础设施状态

生产环境使用 PostgreSQL 15+ 和 Redis 7+。`compose.server.yml` 为本项目创建独立的
PostgreSQL、Redis 服务、内部网络和命名卷，数据库与 Redis 均不映射宿主机端口，不会
操作其他 Compose 项目的容器。邮件 Session 与密码仍保存在 `data/` 文件中。

首次上线设置 `RUNNING_STORAGE_MODE=dual`。应用启动时执行版本化迁移，并将现有
`mailbox-cache.json` 导入 PostgreSQL；原 JSON 不会删除。核对一段时间后再切换为
`postgres`。

应用通过 `go:embed` 嵌入前端静态资源，发布构建规范命令为
`go build -tags embed ./cmd/server`。当前数据库和缓存预留不改变现有部署命令。

## 服务器目录

```text
/www/wwwroot/running-tools/
|-- .env
|-- compose.server.yml
`-- data/
    |-- mail/
    |   |-- hme-config.json
    |   `-- state/
    `-- system/
```

PostgreSQL 和 Redis 数据保存在当前 Compose 项目自动命名的独立卷中，不放入应用源码
目录，也不会与其他 Compose 项目的同名逻辑卷共用。

Go 容器不挂载 Docker Socket。网页更新操作只会在 `data/system` 下写入一个小型
请求文件；由 root 管理的宿主机服务监控该文件，并负责拉取镜像和重启服务。

## 初次部署

1. 创建目录：

```bash
mkdir -p \
  /www/wwwroot/running-tools/data/mail/state \
  /www/wwwroot/running-tools/data/system
```

2. 将 `.env.example` 复制为 `.env`，生成新的 `RUNNING_API_KEY`。

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

该脚本使用 `--no-deps app`，只重建当前 Compose 项目的应用容器；PostgreSQL、Redis
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

任务由 Go 服务的后台 Worker 执行，不依赖宝塔计划任务或浏览器页面。设置持久化在 `data/mail/state/create-schedule.json`，服务重启后会读取并恢复已开启的周期计划。Apple 返回 `-41015` 时本轮提前结束；Session 失效时会自动暂停计划。

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
