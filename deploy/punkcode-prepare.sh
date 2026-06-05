#!/usr/bin/env bash
# =============================================================================
# PunkcodeAI 生产环境变量生成脚本
# =============================================================================
# 作用：基于 .env.punkcode（dev 基底）生成 .env.punkcode.prod，并把所有 dev 默认
#       密钥/密码替换成 openssl 现生成的强随机值，再覆写生产专属的 URL / 端口 / 模式。
#
# 隔离：本脚本只产出 .env.punkcode.prod（PunkcodeAI 专用），与旧 sub2api 的任何
#       .env 文件无关，不读不写旧文件。
#
# 幂等：默认【已存在则不覆盖】，避免重跑把已上线的密钥洗掉（会导致 JWT 全失效、
#       数据库连不上）。需要强制重生成时加 --force（会先备份旧文件为 .bak.<ts>）。
#
# 用法：
#   ./punkcode-prepare.sh            # 首次生成
#   ./punkcode-prepare.sh --force    # 强制重生成（旧文件自动备份）
#
# 依赖：bash、openssl、sed、awk（腾讯云主机默认都有）。
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_ENV="${SCRIPT_DIR}/.env.punkcode"
PROD_ENV="${SCRIPT_DIR}/.env.punkcode.prod"

FORCE=0
for arg in "$@"; do
  case "$arg" in
    --force) FORCE=1 ;;
    *) echo "未知参数: $arg（仅支持 --force）" >&2; exit 2 ;;
  esac
done

# ---- 前置检查 ----
if ! command -v openssl >/dev/null 2>&1; then
  echo "ERROR: 未找到 openssl，无法生成密钥。请先 yum/apt 安装 openssl。" >&2
  exit 1
fi
if [ ! -f "$BASE_ENV" ]; then
  echo "ERROR: 基底文件不存在: $BASE_ENV" >&2
  echo "       请确认在 deploy/ 目录下运行，且 .env.punkcode 已随仓库就位。" >&2
  exit 1
fi

# ---- 幂等保护 ----
if [ -f "$PROD_ENV" ] && [ "$FORCE" -ne 1 ]; then
  echo "已存在 $PROD_ENV，跳过生成（不覆盖已上线密钥）。"
  echo "如确需重新生成，请加 --force（会备份旧文件）。"
  exit 0
fi
if [ -f "$PROD_ENV" ] && [ "$FORCE" -eq 1 ]; then
  BAK="${PROD_ENV}.bak.$(date +%Y%m%d%H%M%S)"
  cp -p "$PROD_ENV" "$BAK"
  echo "已备份旧文件 -> $BAK"
fi

# ---- 以 dev 基底起步 ----
cp "$BASE_ENV" "$PROD_ENV"

# ---- 生成强随机密钥 ----
# JWT_SECRET / TOTP_ENCRYPTION_KEY 用 hex 32 字节 = 64 位 hex 字符（TOTP 强约束 64hex）
JWT_SECRET_VAL="$(openssl rand -hex 32)"
TOTP_KEY_VAL="$(openssl rand -hex 32)"
# 数据库 / Redis / Admin 口令用 base64（含特殊字符，但下面用安全替换避免 sed 元字符问题）
POSTGRES_PASSWORD_VAL="$(openssl rand -base64 24)"
REDIS_PASSWORD_VAL="$(openssl rand -base64 24)"
ADMIN_PASSWORD_VAL="$(openssl rand -base64 18)"

# ---- 安全的 key=value 写入器 ----
# 用 awk 整行替换（按 KEY= 前缀匹配），value 通过环境变量传入，彻底规避 sed 对
# value 中 / & \ 等 base64 特殊字符的转义地狱。若 KEY 不存在则追加到文件末尾。
set_kv() {
  local key="$1"
  local val="$2"
  if grep -qE "^${key}=" "$PROD_ENV"; then
    VAL="$val" awk -v k="$key" '
      $0 ~ "^" k "=" { print k "=" ENVIRON["VAL"]; next }
      { print }
    ' "$PROD_ENV" > "${PROD_ENV}.tmp"
    mv "${PROD_ENV}.tmp" "$PROD_ENV"
  else
    printf '%s=%s\n' "$key" "$val" >> "$PROD_ENV"
  fi
}

# ---- 替换密钥 / 密码 ----
set_kv "JWT_SECRET"          "$JWT_SECRET_VAL"
set_kv "TOTP_ENCRYPTION_KEY" "$TOTP_KEY_VAL"
set_kv "POSTGRES_PASSWORD"   "$POSTGRES_PASSWORD_VAL"
set_kv "REDIS_PASSWORD"      "$REDIS_PASSWORD_VAL"
set_kv "ADMIN_PASSWORD"      "$ADMIN_PASSWORD_VAL"

# ---- 生产专属配置（覆盖 dev 默认）----
set_kv "SERVER_MODE"           "release"
set_kv "RUN_MODE"              "standard"
set_kv "SERVER_FRONTEND_URL"   "https://<DOMAIN>"
# CORS 必须是桌面端 Electron renderer 的固定 Origin oc://renderer（不是域名！）。
# 桌面端跨源 fetch 的 Origin 恒为 oc://renderer，填域名会让 OPTIONS 预检返 403、
# 注册/登录全部报 "HTTP request failed"。如需 web 控制台跨域，逗号追加：oc://renderer,https://<DOMAIN>
set_kv "CORS_ALLOWED_ORIGINS"  "oc://renderer"
# 后端只对内监听 8080（compose 内固定），宿主只绑 127.0.0.1:38080，由 nginx 反代。
set_kv "BIND_HOST"             "127.0.0.1"
set_kv "SERVER_PORT"           "38080"
# Admin 邮箱保持企业内部域；如需改用真实邮箱在此调整。
set_kv "ADMIN_EMAIL"           "admin@punkcode.local"
set_kv "TZ"                    "Asia/Shanghai"

# ---- 收紧权限（仅属主可读写）----
chmod 600 "$PROD_ENV"

# ---- 校验 TOTP_ENCRYPTION_KEY 是 64 位 hex 且非 dev 默认值 ----
DEV_TOTP_DEFAULT="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
GEN_TOTP="$(grep -E '^TOTP_ENCRYPTION_KEY=' "$PROD_ENV" | head -n1 | cut -d= -f2-)"
if ! printf '%s' "$GEN_TOTP" | grep -qE '^[0-9a-f]{64}$'; then
  echo "ERROR: TOTP_ENCRYPTION_KEY 不是 64 位 hex，生成失败。" >&2
  exit 1
fi
if [ "$GEN_TOTP" = "$DEV_TOTP_DEFAULT" ]; then
  echo "ERROR: TOTP_ENCRYPTION_KEY 仍是 dev 默认值，未成功替换。" >&2
  exit 1
fi

# ---- 校验 JWT_SECRET 也不是 dev 默认值 ----
DEV_JWT_DEFAULT="a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f99"
GEN_JWT="$(grep -E '^JWT_SECRET=' "$PROD_ENV" | head -n1 | cut -d= -f2-)"
if [ "$GEN_JWT" = "$DEV_JWT_DEFAULT" ] || [ -z "$GEN_JWT" ]; then
  echo "ERROR: JWT_SECRET 仍是 dev 默认值或为空，未成功替换。" >&2
  exit 1
fi

# ---- 完成提示 ----
echo "=============================================================="
echo " 已生成 $PROD_ENV （权限 600）"
echo "--------------------------------------------------------------"
echo " 隔离确认："
echo "   POSTGRES_DB / USER : punkcode（与旧 sub2api 库隔离）"
echo "   宿主绑定           : 127.0.0.1:38080（由 nginx 反代）"
echo "   pg/redis           : 不映射宿主端口（仅 docker 内网）"
echo "--------------------------------------------------------------"
echo " 【请运维妥善记录以下 Admin 登录口令，仅此一次打印】"
echo "   ADMIN_EMAIL    : admin@punkcode.local"
echo "   ADMIN_PASSWORD : ${ADMIN_PASSWORD_VAL}"
echo "--------------------------------------------------------------"
echo " 下一步："
echo "   docker compose -p punkcode -f docker-compose.punkcode.prod.yml \\"
echo "     --env-file .env.punkcode.prod up -d"
echo "=============================================================="
