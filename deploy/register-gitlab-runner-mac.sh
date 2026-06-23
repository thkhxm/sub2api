#!/usr/bin/env bash
# 一键注册 GitLab Runner —— macOS（Apple Silicon），shell executor，供桌面端 mac 打包
# （.gitlab-ci.yml 的 build:mac，tags: [macos]）。
#
# 用法：bash register-gitlab-runner-mac.sh <GitLab地址> <runner认证token>
#   例：bash register-gitlab-runner-mac.sh https://git.myverse.fans glrt-xxxxxxxxxxxx
#
# 获取 token（GitLab 16+ 新流程）：
#   GitLab → opencode 项目 → Settings → CI/CD → Runners → 「New project runner」
#   → 平台选 macOS；【Tags 必须填 macos】；按需勾选 "Run untagged jobs" → Create
#   → 复制生成的认证 token（glrt- 开头）。
#   说明：tag(macos) 在上面 UI 里设定，本脚本不再传 tag（新流程 tag 由 runner 配置决定）。
set -euo pipefail

URL="${1:?用法: bash $0 <GitLab地址> <token glrt-...>}"
TOKEN="${2:?需要 runner 认证 token(glrt-...)，从 GitLab「New project runner」获取}"

echo "[1/4] 安装 gitlab-runner 与工具链（bun / git / xcode 命令行工具）"
if ! command -v brew >/dev/null 2>&1; then
  echo "✗ 未检测到 Homebrew，请先安装：https://brew.sh" >&2
  exit 1
fi
command -v gitlab-runner >/dev/null 2>&1 || brew install gitlab-runner
command -v git >/dev/null 2>&1 || brew install git
if ! command -v bun >/dev/null 2>&1; then
  # 优先用 Homebrew 装 bun（国内比直连 GitHub release 下载稳）；失败再退回官方安装脚本。
  brew install bun || { curl -fsSL https://bun.sh/install | bash; export PATH="$HOME/.bun/bin:$PATH"; }
fi
# codesign（未签名 mac 包要做 ad-hoc 签名才能在 Apple Silicon 启动）需要 Xcode 命令行工具
xcode-select -p >/dev/null 2>&1 || xcode-select --install || true

echo "[2/4] 注册 runner（shell executor）"
gitlab-runner register --non-interactive \
  --url "$URL" --token "$TOKEN" \
  --executor shell --description "punkcode-mac-arm64"

echo "[3/4] 安装为后台服务并启动（开机自启、关终端不停）"
brew services start gitlab-runner 2>/dev/null || { gitlab-runner install; gitlab-runner start; }

echo "[4/4] 完成。验证："
gitlab-runner verify || true
echo "→ 到 GitLab 的 Runners 页应看到该 runner 在线（绿点）。"
echo "→ 若构建 job 报找不到 bun/git：确保 \$HOME/.bun/bin 在 runner 进程的 PATH 里"
echo "  （brew services 以登录用户运行，一般已含；必要时在 ~/.zprofile 加 export PATH=\"\$HOME/.bun/bin:\$PATH\" 后重启服务）。"
