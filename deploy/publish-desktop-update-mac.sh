#!/usr/bin/env bash
# 在 macOS 上把桌面端 mac 更新产物发布到自建 /updates 服务器（Windows 版见 publish-desktop-update-run.ps1）。
#
# 做的事：把 dist 里的 latest-mac.yml + *.dmg + *.zip(+.blockmap) scp 到主机 /tmp，再 sudo cp 进
# /www/wwwroot/punkcodeai-updates/。nginx 在 https://<域名>/updates/ 提供，桌面端 electron-updater
# 读 latest-mac.yml 自动更新（mac 走 .zip 做差量/全量更新，.dmg 仅供手动安装）。
#
# 前置：先在 packages/desktop 跑 `PUNKCODE_CHANNEL=prod bun run build && bun run package:mac`。
# 凭据：主机 IP / SSH 密码见思源「/工具/sub2api → 配置与部署 → 部署主机」。密码交互输入、不落盘。
#
# 用法（在 macOS 上、仓库根或任意目录）：
#   bash deploy/publish-desktop-update-mac.sh <HostIP> [User] [DistDir]
#   例：bash deploy/publish-desktop-update-mac.sh 1.12.x.x ubuntu ../opencode/packages/desktop/dist
set -euo pipefail

HOST="${1:?用法: bash $0 <HostIP> [User] [DistDir]}"
USER_="${2:-ubuntu}"
# 默认 dist：相对本脚本所在 deploy/，即 ../../opencode/packages/desktop/dist（sub2api 与 opencode 同级）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST="${3:-$SCRIPT_DIR/../../opencode/packages/desktop/dist}"
REMOTE="/www/wwwroot/punkcodeai-updates"

[ -d "$DIST" ] || { echo "dist 目录不存在: $DIST（先 bun run build && bun run package:mac）"; exit 1; }
cd "$DIST"

shopt -s nullglob
files=( latest-mac.yml PunkcodeAI-*.dmg PunkcodeAI-*.zip PunkcodeAI-*.zip.blockmap )
[ ${#files[@]} -gt 0 ] || { echo "dist 里没有 mac 产物(latest-mac.yml / *.dmg / *.zip)"; exit 1; }
echo "[0/3] 将发布: ${files[*]}"

target="$USER_@$HOST"
# 用 ControlMaster 复用一条连接 → 全程只需输一次 SSH 密码
ctl="/tmp/pk-publish-%r@%h:%p"
SSH=( ssh -o StrictHostKeyChecking=accept-new -o ControlMaster=auto -o "ControlPath=$ctl" -o ControlPersist=120 )
SCP=( scp -o StrictHostKeyChecking=accept-new -o ControlMaster=auto -o "ControlPath=$ctl" -o ControlPersist=120 )

echo "[1/3] 准备远端暂存目录（首次会提示输入 SSH 密码）"
"${SSH[@]}" "$target" "mkdir -p /tmp/punkcode-updates && rm -f /tmp/punkcode-updates/*"

echo "[2/3] 上传产物（zip/dmg 各上百 MB，请稍候）"
"${SCP[@]}" "${files[@]}" "$target:/tmp/punkcode-updates/"

echo "[3/3] 发布进 $REMOTE（sudo）+ 清理"
"${SSH[@]}" "$target" "sudo cp /tmp/punkcode-updates/* $REMOTE/ && sudo chmod 644 $REMOTE/* && rm -rf /tmp/punkcode-updates && echo PUBLISHED && ls -la $REMOTE/"

# 关闭复用连接
"${SSH[@]}" -O exit -o "ControlPath=$ctl" "$target" 2>/dev/null || true
echo "完成。验证: curl -I https://punkcodeai.myverse.site/updates/latest-mac.yml  (期望 200)"
