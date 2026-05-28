# PunkcodeAI v1 端到端回归 Checklist

本文档列出 PunkcodeAI v1（M1-M8 全集成版本）发布前必须跑通的端到端测试路径。每个里程碑都有对应的回归条目，覆盖 sub2api 后端 + opencode 桌面端两侧。

## 使用方式

- 每次发布候选构建前，按表格顺序逐项跑
- 「状态」列填 PASS / FAIL / TODO
- FAIL 必须修后重测，不允许带 FAIL 上线
- 每行的「验证手段」给出具体命令或操作，不需要再凭经验猜

## 环境前置

| 项 | 期望 |
|---|---|
| sub2api 代码 | `feat/punkcode-integration` 分支，HEAD 在 b70e2fdf 之后 |
| opencode 代码 | `feat/punkcode-integration` 分支，HEAD 在 c03ab7e 之后 |
| 桌面端 .exe | `packages/desktop/dist/PunkcodeAI-win-x64.exe` 已构建 |
| Docker Desktop | 已起，端口 38080 空闲 |
| Windows | 10/11，PowerShell 5.1+ |
| 测试账号 | u_e2e@punkcode.local，密码 e2e_test_2026 |

## 回归表

| # | 阶段 | 步骤 | 期望结果 | 验证手段 | 状态 |
|---|---|---|---|---|---|
| **--- M1：sub2api 后端启动 ---** |
| 1 | sub2api 启动 | `docker compose -f docker-compose.dev.yml -f docker-compose.punkcode.yml --env-file .env.punkcode up --build` | 所有 service 进入 healthy | `docker compose ps` 看 `STATUS` 全部 `Up (healthy)` | TODO |
| 2 | sub2api health | curl `/health` | 200 `{"status":"ok"}` | `curl -fsS http://localhost:38080/health` | TODO |
| 3 | sub2api admin 登录 | 浏览器打开 `http://localhost:38080`，admin@punkcode.local / Punkcode@dev2026 登录 | 进入后台，左侧菜单全部可见 | 浏览器肉眼 | TODO |
| 4 | seed settings | `punkcode-seed` 容器跑过且写入 3 条 settings | settings 表有 `registration_enabled=true` / `email_verification_required=false` / `invite_code_required=false` | `docker exec sub2api-postgres psql -U punkcode -d punkcode -c "SELECT key,value FROM settings WHERE key IN ('registration_enabled','email_verification_required','invite_code_required');"` | TODO |
| **--- M2：/cli/* API（注册 + 登录 + me）---** |
| 5 | `/cli/register` 正常注册 | curl POST 注册 u_e2e | 200，返 access_token + refresh_token + user.id | `curl -sX POST http://localhost:38080/api/v1/cli/register -H "Content-Type: application/json" -d '{"email":"u_e2e@punkcode.local","password":"e2e_test_2026","nickname":"E2E"}'` | TODO |
| 6 | `/cli/register` 重复邮箱 | 再次注册同邮箱 | 409 `email_already_exists` | 重复跑步骤 5 | TODO |
| 7 | `/cli/login` 正常登录 | curl POST 登录 | 200，返新 access_token | `curl -sX POST http://localhost:38080/api/v1/cli/login -H "Content-Type: application/json" -d '{"email":"u_e2e@punkcode.local","password":"e2e_test_2026"}'` | TODO |
| 8 | `/cli/login` 错密 | 用错密码登录 | 401 `invalid_credentials` | 同步骤 7 改密码 | TODO |
| 9 | `/cli/me` 拿余额 | Authorization Bearer 调 me | 200，user.balance_usd 字段存在（初始 0） | `curl -s http://localhost:38080/api/v1/cli/me -H "Authorization: Bearer $TOKEN"` | TODO |
| 10 | `/cli/me` 无 token | 不带 Authorization 调 me | 401 | curl 去掉 header | TODO |
| **--- M3：/cli/api-key（自动绑 group）---** |
| 11 | `/cli/api-key` 首次拿 key | u_e2e 首次调 api-key | 200，返 `key` 以 `sk-` 开头 | `curl -s http://localhost:38080/api/v1/cli/api-key -H "Authorization: Bearer $TOKEN"` | TODO |
| 12 | u_e2e 自动绑到 default group | 检查 user_groups 关联 | u_e2e 出现在 default user group 的成员里 | admin 后台 → User → 查 u_e2e 详情看 group_id；或 `SELECT * FROM users WHERE email='u_e2e@punkcode.local';` | TODO |
| 13 | `/cli/api-key` 第二次调 | 再次调 api-key | 200，返**同一把** key（幂等） | 重复步骤 11 比对 | TODO |
| **--- M4：桌面端 account/credentials 改造（已锁，回归冒烟）---** |
| 14 | 桌面端首启动 | 双击 `PunkcodeAI-win-x64.exe` | 启动无报错，窗口标题 `PunkcodeAI` | 肉眼 | TODO |
| 15 | SmartScreen 拦截 | 首次启动 Windows SmartScreen 弹窗 | 用户点「更多信息」→「仍要运行」可绕过 | 肉眼 | TODO |
| 16 | 任务管理器进程名 | 任务管理器看进程 | 显示 `PunkcodeAI`（不是 opencode） | 肉眼 | TODO |
| 17 | 程序文件 VersionInfo | 右键 .exe → 属性 → 详细信息 | ProductName / FileDescription / CompanyName 全是 PunkcodeAI | 肉眼 | TODO |
| **--- M5：桌面端注册 / 登录 / 账户页 ---** |
| 18 | 桌面端注册 u_e2e2 | 桌面端点「注册」，填 u_e2e2@punkcode.local + 密码 | 注册成功，自动登录，跳到首页 | 肉眼 | TODO |
| 19 | 桌面端登录 | 退出后用 u_e2e2 登录 | 登录成功 | 肉眼 | TODO |
| 20 | 账户页显示信息 | 右上角头像 → 账户页 | 显示 email / 余额 / 注销按钮 | 肉眼 | TODO |
| 21 | 桌面端注销 | 账户页点注销 | 回到登录页，本地 token 清空 | 肉眼；electron store 看 `punkcode.session` 已删 | TODO |
| **--- M6：模型选择 + 聊天 ---** |
| 22 | admin 配置 channel + group | admin 后台配置至少 1 个上游 channel，挂到 default user group，开启 ModelsListConfig | 模型清单（至少 1 个，如 `claude-3-5-sonnet-20241022`）出现在 group 配置里 | admin 后台 → Group 详情 | TODO |
| 23 | 桌面端模型下拉拉到模型 | 桌面端聊天页点模型下拉 | 至少能看到上一步配的模型 | 肉眼 | TODO |
| 24 | 桌面端真聊天 | 发一句 "Hello, reply with 'pong'" | 200，返「pong」或类似 | 桌面端肉眼；sub2api Ops → 请求日志看到一条 200 记录 | TODO |
| 25 | 余额扣减 | 步骤 24 完成后看余额 widget | 余额降低（金额 = token cost × group 倍率） | 桌面端右上角余额；或 curl `/cli/me` 对比 | TODO |
| **--- M7：余额 widget + 充值申请 ---** |
| 26 | 余额 widget 显示 | 桌面端右上角 | 显示当前美元余额，按汇率展示人民币 | 肉眼 | TODO |
| 27 | 申请充值 | 点余额 → 申请充值 → 填金额 10 USD → 提交 | 弹「申请已提交，待管理员审批」 | 桌面端肉眼 | TODO |
| 28 | admin 看到充值申请 | admin 后台 → 充值审批菜单 | 看到 u_e2e2 的 10 USD 申请，状态 pending | admin 后台 | TODO |
| 29 | admin 审批通过 | admin 点通过 | 申请状态变 approved，u_e2e2 余额 +10 USD | admin 后台；桌面端 5 秒内余额刷新（或手动刷新） | TODO |
| **--- M8：构建 + PE 脱敏 ---** |
| 30 | bun typecheck | `cd opencode && bun run typecheck` | 19/19 PASS | 终端 | TODO |
| 31 | Windows .exe 构建 | 按 M8 文档跑 electron-builder | `dist/PunkcodeAI-win-x64.exe` 存在，约 120 MB | `ls packages/desktop/dist/` | TODO |
| 32 | PE VersionInfo | 步骤 17 已覆盖 | （引用 17） | （引用 17） | TODO |
| 33 | 桌面端 i18n 已脱敏 | grep 用户可见品牌字样 | 命中均为 i18n key 名或 'opencode' 命令名等技术 ID | `grep -rin "opencode\|OpenCode" packages/app/src/i18n packages/desktop/src/renderer/i18n \| grep -v "\.cli\." \| grep -v "opencode://" \| grep -v "opencode-cli" \| grep -v "opencode-debug"` | TODO |
| **--- 跨切面回归 ---** |
| 34 | electron-updater 检查更新 | 桌面端菜单 → Check for Updates | 弹「已是最新版本」（没发新版时） | 肉眼 | TODO |
| 35 | 退出 / 重启保留登录 | 关闭桌面端，重开 | 自动登录，仍在 u_e2e2 | 肉眼 | TODO |
| 36 | SSE / chat stream | 桌面端发一个长回复（让模型生成 300+ 字） | 流式渲染，无断流 | 肉眼 | TODO |
| 37 | 网络断连恢复 | 聊天中断网 5 秒再恢复 | 桌面端报错友好，恢复后能继续 | 肉眼 + Ops 日志 | TODO |
| 38 | sub2api 容器重启 | `docker restart sub2api-app` | 桌面端下一次请求重连成功 | 肉眼 | TODO |
| 39 | 多账号切换 | 退出 u_e2e2，登录 u_e2e；再切回 | 余额 / 聊天历史互不串 | 肉眼 | TODO |
| 40 | 长任务后余额一致 | 跑 5 次聊天后调 `/cli/me` 比对 widget | 余额扣减总和 ≈ Ops 日志里的 5 条 cost 之和（容差 < 0.01） | 算账 | TODO |

## 通过判定

- 全部 40 行状态 PASS（或明确标记「N/A，本次未覆盖」并附理由）
- 若有 FAIL，写明根因 + 修复 commit + 复测时间
- 测试完成后把本表附件提交到 release 总结里

## 回归历史

| 日期 | 版本 | 执行人 | PASS / FAIL / 备注 |
|---|---|---|---|
| YYYY-MM-DD | v1.0.0-rc.1 |  |  |
| YYYY-MM-DD | v1.0.0 |  |  |
