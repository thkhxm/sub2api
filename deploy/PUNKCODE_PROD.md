# PunkcodeAI sub2api - Prod 部署指南

PunkcodeAI 桌面端的后端是基于 sub2api 改造的网关服务。本文档描述 prod 环境的部署流程：域名、反代、密钥重置、数据库迁移、监控、备份、升级。

> 适用对象：PunkcodeAI v1 上线时 sub2api 后端的运维负责人
>
> 配套文件：
> - `deploy/.env.punkcode.prod` （你需要自己从 `.env.punkcode` 拷贝并改密）
> - `deploy/docker-compose.punkcode.yml`
> - `deploy/Caddyfile`
>
> 默认假设：
> - prod 域名 `<DOMAIN>`
> - prod 服务器是单机 Linux（Ubuntu 22.04+ / Debian 12+），已装 Docker Engine + docker compose v2
> - 反代用 Caddy（推荐，自动签 Let's Encrypt 证书）或 nginx

---

## 1. 准备 prod env

### 1.1 从 dev 模板拷贝 + 强制改密

```bash
cd deploy
cp .env.punkcode .env.punkcode.prod
```

逐项核对 `.env.punkcode.prod`，**以下字段必须改**（dev 默认值都是占位符，留到 prod 会立刻被人撞库）：

| 字段 | 改成什么 | 生成命令 |
|---|---|---|
| `SERVER_MODE` | `release` | （硬编码） |
| `BIND_HOST` | `0.0.0.0`（要让 Caddy 反代访问）或 `127.0.0.1`（Caddy 同机部署时） | （硬编码） |
| `POSTGRES_PASSWORD` | 强随机 | `openssl rand -base64 24` |
| `REDIS_PASSWORD` | 强随机（可选但强烈建议） | `openssl rand -base64 24` |
| `ADMIN_EMAIL` | 真实管理员邮箱 | （由你决定） |
| `ADMIN_PASSWORD` | 强密码（≥16 字符，建议密码管理器生成） | （由你决定） |
| `JWT_SECRET` | **必须重新生成**（dev 默认值已泄露在仓库里） | `openssl rand -hex 32` |
| `TOTP_ENCRYPTION_KEY` | **必须重新生成**且**严禁后续轮换**（轮换会让所有已绑 2FA 的用户失效） | `openssl rand -hex 32` |
| `SERVER_FRONTEND_URL` | `https://<DOMAIN>` | （硬编码） |
| `CORS_ALLOWED_ORIGINS` | **必须设为 `oc://renderer`** | （硬编码） |
| `TZ` | 按机房定（如 `Asia/Shanghai` / `UTC`） | （由你决定） |

> ⚠ **`CORS_ALLOWED_ORIGINS=oc://renderer` 是桌面端能用的硬前提**。PunkcodeAI 桌面端
> （Electron）renderer 进程的 Origin 固定是 `oc://renderer`，fetch 前会发 OPTIONS 预检；
> sub2api CORS 白名单不含它就返 403，桌面端注册/登录全部报 "HTTP request failed"。
> dev 已在 `docker-compose.punkcode.yml` 默认注入；**prod 的 `.env.punkcode.prod` 必须显式带上**。
> 若将来还要支持 Web 版控制台跨域，用逗号追加：`oc://renderer,https://console.<DOMAIN>`。

补充字段（按需）：

```env
# JWT 过期时长，prod 通常缩到 12 小时
JWT_EXPIRE_HOUR=12

# Postgres 端口仅容器网内可达，不需要暴露宿主机
# Redis 同理
```

### 1.2 密钥归档

把生成的 `JWT_SECRET` / `TOTP_ENCRYPTION_KEY` / `POSTGRES_PASSWORD` 立刻存进**密码管理器或公司 Vault**，不要留在 chat / wiki 里。

`TOTP_ENCRYPTION_KEY` 一旦丢失，所有用户的 2FA 都需重置，**比 JWT_SECRET 更敏感**。

---

## 2. 域名 + 反代配置

### 2.1 DNS

把 `<DOMAIN>` 的 A 记录指向 prod 服务器公网 IP。

如果在 Cloudflare 后面：
- 「Proxy status」推荐**关掉橙云**（防 Caddy 自动签证书踩 Cloudflare 的 challenge 冲突）；或开橙云但用 Cloudflare Origin Certificate + Caddy `tls` 块手动配
- 后端读取真实 IP 优先 `CF-Connecting-IP` → `X-Real-IP`（Caddyfile 已默认传）

### 2.2 Caddy（推荐）

`deploy/Caddyfile` 中第 2 行 `api.sub2api.com` 改成 `<DOMAIN>`：

```caddy
<DOMAIN> {
    # ... 其余配置直接复用 deploy/Caddyfile 模板
    reverse_proxy localhost:38080 {
        health_uri /health
        # ...
    }
}
```

> 注意：`reverse_proxy` 目标端口与 `.env.punkcode.prod` 的 `SERVER_PORT` 保持一致（默认 38080）。如果 sub2api 容器内端口是 8080，但宿主机暴露在 38080，那么 Caddy 反代 `localhost:38080`。

启动：

```bash
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Caddy 会自动签 Let's Encrypt 证书 + HTTPS 重定向。

### 2.3 nginx（替代方案）

如果你已经在用 nginx：

```nginx
server {
    listen 80;
    server_name <DOMAIN>;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name <DOMAIN>;

    ssl_certificate     /etc/letsencrypt/live/<DOMAIN>/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/<DOMAIN>/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;

    client_max_body_size 100M;

    location / {
        proxy_pass         http://127.0.0.1:38080;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_set_header   Connection        "";

        # SSE / chat stream 需要长时间保持连接
        proxy_buffering    off;
        proxy_read_timeout 600s;
    }

    location /health {
        proxy_pass http://127.0.0.1:38080/health;
        access_log off;
    }
}
```

证书用 certbot：`sudo certbot certonly --nginx -d <DOMAIN>`

---

## 3. 启动 + 数据库迁移

### 3.1 首次启动

```bash
cd deploy
docker compose \
  -f docker-compose.dev.yml \
  -f docker-compose.punkcode.yml \
  --env-file .env.punkcode.prod \
  up --build -d
```

> dev compose 文件已经覆盖了 prod 需要的所有镜像构建逻辑（前端 → backend builder → runtime）；prod 部署目前不需要单独的 `docker-compose.prod.yml`。`SERVER_MODE=release` 由 `.env.punkcode.prod` 注入即可。

首次启动会**自动**执行：

1. **frontend builder** 编译 Vue 后台
2. **backend builder** 用 Go 编译 sub2api binary
3. **postgres** 启动，sub2api 容器内 ent 自动运行 schema migration（**不需要手动跑** `ent migrate`）
4. **sub2api** 容器健康（`/health` 返 200）后，
5. **punkcode-seed** 容器写入 3 条 settings（`registration_enabled=true` / `email_verification_required=false` / `invite_code_required=false`）然后退出

### 3.2 验证

```bash
# 健康检查
curl -fsS https://<DOMAIN>/health
# 期望：200 {"status":"ok"}

# 检查 settings
docker exec -it sub2api-postgres psql -U $POSTGRES_USER -d $POSTGRES_DB \
  -c "SELECT key, value FROM settings WHERE key IN ('registration_enabled','email_verification_required','invite_code_required');"

# 期望看到 3 行，registration_enabled=true，其余两条 false
```

### 3.3 后续配置

用 admin 账号登录 `https://<DOMAIN>` 完成：

1. **录入上游账号**（Account 菜单）：Claude OAuth / OpenAI API Key / Gemini OAuth
2. **配 Channel**（Channel 菜单）：把上游账号挂到 channel，设置 `restrict_models`（白名单）
3. **配 Group**（Group 菜单）：创建 default group，挂 channel，开启 `ModelsListConfig`（决定桌面端模型下拉拉得到什么），配每模型计费倍率
4. **检查 default user group**：所有新注册用户会落到这个 group；如果忘了把 channel 挂上去，新用户聊天会 500

> 桌面端用户在 `/cli/api-key` 第一次请求时会被**自动**绑到 `default user group`（M3 实现），不需要手动操作。

---

## 4. 监控

### 4.1 内置 ops 模块

sub2api admin 后台 → **Ops** 菜单提供：

- **请求日志**：按时间 / 用户 / 模型 / channel 检索
- **错误日志**：按 5xx / 4xx 检索
- **统计**：QPS / token usage / 上游 channel 成功率
- **健康面板**：postgres / redis / 上游账号 OAuth 状态

prod 建议每天人肉看一次错误日志（特别是 5xx 突增 = 上游 OAuth 过期 / channel 配错）。

### 4.2 进程级监控

容器健康：

```bash
docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml ps
```

每个 service 都应是 `running (healthy)`。如果 sub2api 是 `unhealthy`：

```bash
docker logs --tail 200 sub2api-app
```

常见 `unhealthy` 根因：
- postgres 没启动起来（看 `docker logs sub2api-postgres`）
- `JWT_SECRET` / `TOTP_ENCRYPTION_KEY` 格式错（必须 hex，TOTP 必须 64 字符）
- ent migration 卡住（看是否有 `failed to migrate` 日志）

### 4.3 外部监控（推荐）

接 UptimeRobot / Healthchecks.io：

- Health check URL：`https://<DOMAIN>/health`
- 期望响应：200，5 秒内
- 频率：每分钟一次

---

## 5. 备份

### 5.1 数据范围

需要备份的数据：

| 路径 | 内容 | 关键性 |
|---|---|---|
| `deploy/postgres_data/` | 用户表 / 余额 / 充值记录 / channel 配置 / **加密的 TOTP secret** | 极高 |
| `deploy/data/` | sub2api 的应用数据（日志 / 临时文件） | 中 |
| `deploy/redis_data/` | session / 限流计数 / 验证码 token | 低（重启即丢，不影响业务） |
| `deploy/.env.punkcode.prod` | 所有密钥 | 极高 |

### 5.2 pg_dump 定时备份

写一个 cron：

```bash
#!/bin/bash
# /opt/punkcode/backup.sh
set -e
BACKUP_DIR=/opt/punkcode/backups
mkdir -p "$BACKUP_DIR"
TS=$(date +%Y%m%d-%H%M%S)

docker exec sub2api-postgres pg_dump \
  -U punkcode \
  -d punkcode \
  --format=custom \
  --no-owner \
  > "$BACKUP_DIR/punkcode-$TS.dump"

# 保留 14 天
find "$BACKUP_DIR" -name 'punkcode-*.dump' -mtime +14 -delete
```

cron：

```cron
0 3 * * *  /opt/punkcode/backup.sh >> /var/log/punkcode-backup.log 2>&1
```

恢复：

```bash
docker exec -i sub2api-postgres pg_restore \
  -U punkcode -d punkcode --clean --if-exists \
  < /opt/punkcode/backups/punkcode-YYYYMMDD-HHMMSS.dump
```

### 5.3 异地副本

强烈建议把 `$BACKUP_DIR` 每天 `rclone sync` 到 S3 / 阿里云 OSS / Backblaze B2，避免单机故障同时丢业务和备份。

### 5.4 .env 备份

`.env.punkcode.prod` 不在 git，要单独存到密码管理器。**强烈建议每次改动后立刻同步**。

---

## 6. 升级

PunkcodeAI 后端升级流程（每次拉新 commit / 改 schema）：

```bash
cd deploy

# 1. 备份（升级前永远先备份）
/opt/punkcode/backup.sh

# 2. 拉新代码
cd ..
git pull origin feat/punkcode-integration  # 或 main

# 3. 停服务
cd deploy
docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml down

# 4. 重新构建 + 启动
docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml \
  --env-file .env.punkcode.prod up --build -d

# 5. 看日志直到 sub2api healthy
docker logs -f sub2api-app
# 期望看到："server started on :8080" 或类似
# Ctrl-C 退出 follow

# 6. 验证
curl -fsS https://<DOMAIN>/health
```

> Ent schema migration 是**自动**的（sub2api 启动时跑），不需要手动 `ent migrate`。

### 6.1 升级失败回滚

```bash
# 停新版
docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml down

# git 回滚到上一个 commit
git checkout <previous-commit-sha>

# 恢复数据库（如果新版做了破坏性 migration，必须从备份恢复）
docker exec -i sub2api-postgres pg_restore -U punkcode -d punkcode --clean --if-exists \
  < /opt/punkcode/backups/punkcode-<升级前的时间戳>.dump

# 重新启动
docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml \
  --env-file .env.punkcode.prod up --build -d
```

---

## 7. 安全清单

上线前最后核一遍：

- [ ] `SERVER_MODE=release`
- [ ] `JWT_SECRET` / `TOTP_ENCRYPTION_KEY` 都是新生成的 hex，不是 dev 默认值
- [ ] `POSTGRES_PASSWORD` 不是 `change_in_prod`
- [ ] `ADMIN_PASSWORD` 不是 `Punkcode@dev2026`
- [ ] HTTPS 已起，HTTP 自动跳转
- [ ] postgres / redis 端口**不**暴露宿主机（`docker compose ps` 看 PORTS 列）
- [ ] sub2api 端口 38080 **不**暴露公网，只让 Caddy 内部访问（`BIND_HOST=127.0.0.1`）
- [ ] 备份脚本已部署，至少跑了一次
- [ ] admin 后台开启了 TOTP（2FA），避免单密码被撞
- [ ] sub2api Channel 上的 `restrict_models` 配了白名单，没有 `*` 通配（防止用户把不该用的模型也调起来）

---

## 8. 排错速查

| 现象 | 可能根因 | 排查 |
|---|---|---|
| 桌面端注册返 403 `registration_disabled` | seed 没跑 / 被人手动改了 | `psql ... SELECT * FROM settings;` 确认 3 条记录 |
| 桌面端登录后模型下拉为空 | 用户没绑 group / group 没挂 channel / channel 没配 `restrict_models` | admin 后台 → Group → 检查目标 group 的 ModelsListConfig |
| 聊天报 `Service temporarily unavailable` | 上游 OAuth 过期 / channel proxy 不通 | admin 后台 → Account → 看 OAuth 状态；Ops → 看错误日志 |
| `/health` 返 502 | sub2api 容器挂了 | `docker logs sub2api-app --tail 200` |
| 用户余额扣了但未充值 | 调用上游成功扣的；正常 | Ops → 用户请求日志查 token cost |
| 桌面端 PE 数字签名警告 | 未签 codesigning 证书 | 上线时可买 EV cert 或让用户走 SmartScreen 确认 |
