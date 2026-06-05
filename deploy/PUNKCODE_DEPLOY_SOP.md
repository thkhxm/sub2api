# PunkcodeAI 生产部署 + 运维 SOP

把【二开版 sub2api】部署到腾讯云主机 **<HOST_IP>**，域名 **<DOMAIN>**。

> 📝 **占位符说明**：本文档已脱敏入库，`<HOST_IP>` / `<DOMAIN>` 为占位符。真实生产主机 IP、域名、admin 密码、TCR 凭据等敏感信息见站长私有知识库（思源 `/sub2api/配置与部署`）与主机 `/opt/punkcode-deploy/.env.punkcode.prod`；部署时替换为真实值。

> ⚠️ 该主机**已在跑开源版 sub2api**（project=`sub2api-deploy`，容器 `sub2api`/`sub2api-postgres`/`sub2api-redis`，API 端口 8080，宿主 nginx 宝塔占 80/443）。本部署与之**完全物理隔离**，下表是隔离对照，操作中任何一项都不能撞旧的。

| 维度 | 旧 sub2api（不能撞） | 新 PunkcodeAI |
|---|---|---|
| compose project | `sub2api-deploy` | `punkcode` |
| app 容器 | `sub2api` | `punkcode-sub2api` |
| pg 容器 | `sub2api-postgres` | `punkcode-postgres` |
| redis 容器 | `sub2api-redis` | `punkcode-redis` |
| 网络 | `sub2api-deploy_sub2api-network` | `punkcode_punkcode-net` |
| 卷 | 旧命名卷 | `punkcode_punkcode-pgdata` / `punkcode_punkcode-redisdata` |
| API 宿主端口 | 8080 | `127.0.0.1:38080`（仅本地，nginx 反代） |
| pg / redis 宿主端口 | 旧映射 | 不映射（仅 docker 内网） |
| 部署目录 | 旧目录 | `/opt/punkcode-deploy` |
| 域名 | 旧域名 | `<DOMAIN>` |

---

## ① 前置条件（人工 / 一次性）

1. **DNS**：`<DOMAIN>` A 记录解析到 `<HOST_IP>`，已生效（`dig <DOMAIN>` 返回该 IP）。
2. **sudo / root**：在主机上有 root 或 sudo，能写 `/opt`、`/www/server/panel/vhost/`、reload nginx。
3. **TLS 证书**：已签发 `<DOMAIN>` 证书（本地有 `*_bundle.crt` + `*.key`，或用宝塔面板申请 Let's Encrypt）。
4. **镜像**：二开 sub2api 已构建成镜像（本地 `docker build` 出 `punkcode-sub2api:latest`，或已推到 TCR）。
5. **Docker / compose v2**：主机已装（旧 sub2api 在跑，说明已具备）。

---

## ② 隔离部署步骤

> 全程在主机 `/opt/punkcode-deploy` 下操作。下列命令里 `<...>` 按实际替换。

### 2.1 准备部署目录 + 传文件

本地（开发机）把 deploy 产物 scp 到主机新目录：

```bash
# 主机上先建目录
ssh root@<HOST_IP> 'mkdir -p /opt/punkcode-deploy'

# 从本地仓库 deploy/ 传 4 个产物 + seed 脚本 + dev 基底 env
scp deploy/docker-compose.punkcode.prod.yml \
    deploy/punkcode-prepare.sh \
    deploy/punkcode-seed.sh \
    deploy/.env.punkcode \
    deploy/nginx-punkcodeai.conf \
    root@<HOST_IP>:/opt/punkcode-deploy/
```

### 2.2 传镜像

**方式 A：本地 docker save → 主机 load（无 TCR 时）**

```bash
# 本地
docker save punkcode-sub2api:latest | gzip > punkcode-sub2api.tar.gz
scp punkcode-sub2api.tar.gz root@<HOST_IP>:/opt/punkcode-deploy/
# 主机
ssh root@<HOST_IP> 'gunzip -c /opt/punkcode-deploy/punkcode-sub2api.tar.gz | docker load'
```

**方式 B：TCR（推荐长期方案）**

```bash
# 本地 tag + push
docker tag punkcode-sub2api:latest ccr.ccs.tencentyun.com/<ns>/punkcode-sub2api:<tag>
docker push ccr.ccs.tencentyun.com/<ns>/punkcode-sub2api:<tag>
# 主机 login + pull；同时把 prod.yml 里 image 改成该 TCR 地址
ssh root@<HOST_IP> 'docker login ccr.ccs.tencentyun.com -u <user> && docker pull ccr.ccs.tencentyun.com/<ns>/punkcode-sub2api:<tag>'
```
> 用方式 B 时，记得把 `docker-compose.punkcode.prod.yml` 里 `image: punkcode-sub2api:latest` 改成完整 TCR 地址。

### 2.3 生成生产环境变量

```bash
ssh root@<HOST_IP>
cd /opt/punkcode-deploy
chmod +x punkcode-prepare.sh punkcode-seed.sh
./punkcode-prepare.sh
```

脚本会：基于 `.env.punkcode` 生成 `.env.punkcode.prod`，用 openssl 现生成并替换 `JWT_SECRET`/`TOTP_ENCRYPTION_KEY`(64hex)/`POSTGRES_PASSWORD`/`REDIS_PASSWORD`/`ADMIN_PASSWORD`，覆写 `SERVER_MODE=release`、URL/CORS、端口，`chmod 600`，最后**打印一次 ADMIN_PASSWORD**——立即记到密码管理器/思源，后面登录后台要用。

> 幂等：已存在 `.env.punkcode.prod` 时默认跳过（保护已上线密钥）。强制重生成用 `./punkcode-prepare.sh --force`（会自动备份旧文件）。

### 2.4 拉起服务

```bash
docker compose -p punkcode -f docker-compose.punkcode.prod.yml \
  --env-file .env.punkcode.prod up -d
```

> `-p punkcode` 显式指定 project（双保险，避免被目录名覆盖）。compose 文件顶部也有 `name: punkcode`。

观察启动（确认 seed 跑完、healthy）：

```bash
docker compose -p punkcode -f docker-compose.punkcode.prod.yml ps
docker logs -f punkcode-sub2api      # Ctrl-C 退出
docker logs punkcode-seed            # 应看到三个 setting 写入成功
```

容器内自测健康：

```bash
curl -s http://127.0.0.1:38080/health      # 期望 200
```

### 2.5 拷证书到宝塔 vhost cert 路径

```bash
mkdir -p /www/server/panel/vhost/cert/<DOMAIN>
# 本地传上来的证书：*_bundle.crt -> fullchain.pem，*.key -> privkey.pem
cp <<DOMAIN>_bundle.crt> /www/server/panel/vhost/cert/<DOMAIN>/fullchain.pem
cp <<DOMAIN>.key>        /www/server/panel/vhost/cert/<DOMAIN>/privkey.pem
chmod 600 /www/server/panel/vhost/cert/<DOMAIN>/privkey.pem
```
> 若用宝塔面板申请/部署证书，可跳过手动拷贝；面板会自动放到该路径。

### 2.6 装 nginx vhost + reload

```bash
# electron-updater feed 目录 + ACME challenge 目录
mkdir -p /www/wwwroot/punkcodeai-updates /www/wwwroot/punkcodeai-acme

# 放 vhost
cp /opt/punkcode-deploy/nginx-punkcodeai.conf \
   /www/server/panel/vhost/nginx/<DOMAIN>.conf

# 校验 + 重载（不重启，零中断，不影响旧 sub2api 站点）
nginx -t && nginx -s reload
```
> 若 `nginx -t` 报 `duplicate ... $connection_upgrade map`，说明宝塔已在 http{} 内置该 map——本 vhost 已改为在 location 里直接处理 Connection 头，正常不会冲突；若仍冲突按文件内注释处理。

### 2.7 端到端验证

```bash
# HTTP 跳转
curl -sI http://<DOMAIN>            # 期望 301 -> https
# HTTPS 健康
curl -s  https://<DOMAIN>/health    # 期望 200
# CLI 注册（桌面端用的端点）
curl -sX POST https://<DOMAIN>/api/v1/cli/register \
  -H "Content-Type: application/json" \
  -d '{"email":"smoke@punkcode.local","password":"abc12345","nickname":"smoke"}'
```

**隔离复核（确认没动旧 sub2api）：**
```bash
docker ps --format '{{.Names}}'         # 应同时看到 sub2api* 和 punkcode* 两套，互不影响
docker compose -p sub2api-deploy ps     # 旧站点仍 running、healthy
curl -s http://127.0.0.1:8080/health    # 旧 sub2api 仍 200（端口没被占）
```

---

## ③ 运行期配置清单（admin 后台，人工）

用 `admin@punkcode.local` + 2.3 打印的密码登录 `https://<DOMAIN>` 后台，依次配置：

1. **系统设置 → `api_base_url`** 设为 `https://<DOMAIN>`
   （后端把它返给桌面端作为 LLM 网关 base_url；务必是 https 公网域名，不能是 localhost/38080）。
2. **配置 SMTP**（系统设置 → 邮件）：填企业 SMTP，用于找回密码/通知。
   （注册邮箱验证已被 seed 关闭 `email_verify_enabled=false`，SMTP 仅用于其它通知场景。）
3. **录入上游账号**（Account 菜单），按需要的能力配置：
   - 文本：Claude OAuth / OpenAI API Key / Gemini OAuth 等，至少 1 个可用。
   - **图片生成（codex 账号）**：该账号 `extra` 设 `codex_image_generation_bridge=false`
     （对应后端 `codex_image_generation_bridge_enabled`，保持纯文本 Codex 请求不被改写；
     客户端显式带 image_generation tool 时仍按分组放行转发）。
   - 该 account 的 `model_mapping` 需包含 **`gpt-image-2`**（让图片模型路由到该上游）。
4. **Group 配置**（Group 菜单）：目标分组 `allow_image_generation=true`（开启该组图片生成权限），
   并把上游 channel 挂到分组、设好每模型计费倍率。
5. **用户分组绑定**（User 菜单）：确认桌面端注册用户落入的「default user group」已挂 channel，
   否则新用户注册后无可用模型。

---

## ④ 桌面端连生产

PunkcodeAI 桌面端构建时注入生产 API 地址：

```bash
PUNKCODE_API_BASE_URL=https://<DOMAIN>
```

- 打包后 Electron renderer origin 是 `oc://renderer`，已被后端 `CORS_ALLOWED_ORIGINS=https://<DOMAIN>` 限定；
  桌面端用 Bearer token（不带 cookie credentials），跨 origin fetch 不受影响。
- 自动更新：electron-updater feed 指向 `https://<DOMAIN>/updates/`，
  把 `latest.yml` + 安装包传到主机 `/www/wwwroot/punkcodeai-updates/` 即可。

---

## ⑤ 回滚预案

> 回滚只影响 PunkcodeAI（project=punkcode），对旧 sub2api 零影响。

```bash
cd /opt/punkcode-deploy

# 仅停服务（保留数据卷）
docker compose -p punkcode -f docker-compose.punkcode.prod.yml down

# 回退到上一镜像 tag（方式 B）：改 prod.yml 的 image tag 后重新 up
docker compose -p punkcode -f docker-compose.punkcode.prod.yml --env-file .env.punkcode.prod up -d

# nginx 回滚：删 vhost 后 reload（站点 503，旧 sub2api 不受影响）
rm /www/server/panel/vhost/nginx/<DOMAIN>.conf
nginx -t && nginx -s reload
```

**彻底拆除（含数据，谨慎）：**
```bash
docker compose -p punkcode -f docker-compose.punkcode.prod.yml down -v   # -v 会删 punkcode-pgdata/redisdata，数据不可恢复
```

**数据备份（升级/回滚前建议先做）：**
```bash
docker exec punkcode-postgres pg_dump -U punkcode punkcode | gzip > /opt/punkcode-deploy/backup_$(date +%F).sql.gz
```

---

## ⑥ 升级方式（换镜像 tag 重新 up）

```bash
cd /opt/punkcode-deploy
# 0) 先备份数据库（见 ⑤）
# 1) 拿到新镜像
#    方式 A：docker load 新的 punkcode-sub2api.tar.gz
#    方式 B：docker pull ccr.ccs.tencentyun.com/<ns>/punkcode-sub2api:<新tag>，并改 prod.yml image
# 2) 重新拉起（ent migration 容器启动时自动跑；seed 幂等重跑安全）
docker compose -p punkcode -f docker-compose.punkcode.prod.yml --env-file .env.punkcode.prod up -d
# 3) 验证
docker compose -p punkcode -f docker-compose.punkcode.prod.yml ps
curl -s https://<DOMAIN>/health
```

> `.env.punkcode.prod` 升级时**不要重生成**（prepare 脚本默认幂等跳过）；重生成会洗掉密钥导致 JWT 失效、数据库密码对不上连不上。
