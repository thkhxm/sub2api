# PunkcodeAI - Sub2API Dev 启动指南

PunkcodeAI 桌面端依赖 sub2api 作为后端。本目录提供一键 docker compose 启动 dev 环境。

## 前置条件

- Docker Desktop（含 docker compose v2）
- 端口 **38080** 空闲

## 启动

在 `deploy/` 目录下执行：

```bash
docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml \
  --env-file .env.punkcode up --build
```

首次启动会执行：
1. **frontend builder** 编译 Vue 前端，产物嵌入 backend binary
2. **backend builder** 用 Go 1.26 编译 sub2api binary（含 `/api/v1/cli/*` 路由）
3. **postgres / redis** 启动
4. **sub2api** 启动（自动跑 ent migration，自动创建 admin 账号）
5. **punkcode-seed** 在 sub2api 健康后写入 3 个 settings（开启注册、关闭验证码、关闭邀请码）

完成后访问：

| 端点 | URL | 说明 |
|---|---|---|
| sub2api Web 后台 | http://localhost:38080 | 管理员登录配置上游账号 |
| CLI API 健康检查 | http://localhost:38080/health | 应返 200 |
| CLI register（桌面端用） | POST http://localhost:38080/api/v1/cli/register | |

默认 admin 账号（见 `.env.punkcode`）：

```
邮箱：admin@punkcode.local
密码：Punkcode@dev2026
```

## ⏸ 调试阶段：你必须手动操作的事项

桌面端能跑通"注册 → 登录 → 聊天"之前，你需要先在 sub2api Web 后台 (http://localhost:38080) 用 admin 账号登录后配置：

1. **录入上游账号**（Account 菜单）
   - Claude OAuth / OpenAI API Key / Gemini OAuth 等
   - 至少配 1 个能正常工作的上游账号
2. **配置 Channel**（Channel 菜单）
   - 把上游账号挂到 channel
   - 设置可用模型清单（如 `claude-3-5-sonnet-20241022`）
3. **配置 Group**（Group 菜单）
   - 创建 dev group，把 channel 挂上
   - 设置每模型计费倍率
4. **把测试用户绑定到 group**（User 菜单）
   - sub2api 默认 admin 在系统组；
   - 桌面端注册的用户会进入"default user group"，要确认这个 group 也挂了 channel

> 我（Claude）会在 M6 之前停下来，等你完成上面 4 步后再继续 LLM 联调。

## 快捷验证 CLI API

测试 `/api/v1/cli/register`（注册 + 拿 token）：

```bash
curl -sX POST http://localhost:38080/api/v1/cli/register \
  -H "Content-Type: application/json" \
  -d '{"email":"u1@punkcode.local","password":"abc12345","nickname":"User1"}'
```

正常响应：

```json
{
  "code": 0,
  "data": {
    "access_token": "...",
    "refresh_token": "rt_...",
    "expires_in": 86400,
    "token_type": "Bearer",
    "user": { "id": 2, "email": "u1@punkcode.local", "nickname": "User1", "balance_usd": 0 }
  }
}
```

测试 `/api/v1/cli/me`（拿余额）：

```bash
TOKEN=<上面的 access_token>
curl -s http://localhost:38080/api/v1/cli/me -H "Authorization: Bearer $TOKEN"
```

测试 `/api/v1/cli/api-key`（拿桌面端专用 sk- key）：

```bash
curl -s http://localhost:38080/api/v1/cli/api-key -H "Authorization: Bearer $TOKEN"
```

## 停止 / 重置

```bash
# 停止
docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml down

# 完全重置（删除数据库 / Redis 数据）
docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml down
rm -rf data postgres_data redis_data
```

## Prod 部署（M8 后再做）

prod 域名 `<DOMAIN>`，部署时改：

1. `.env.punkcode` 中所有 `change_in_prod` 字段重新生成（特别是 `JWT_SECRET` / `TOTP_ENCRYPTION_KEY` / `POSTGRES_PASSWORD`）
2. `SERVER_MODE=release`
3. 在 sub2api 前面挂 Caddy（参考 deploy/Caddyfile）做 HTTPS termination
4. 桌面端构建时把 `PUNKCODE_API_BASE_URL` 注入为 `https://<DOMAIN>`
