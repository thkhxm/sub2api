# 一键注册 GitLab Runner —— Windows(x64)，shell executor，供桌面端 win 打包
# （.gitlab-ci.yml 的 build:win，tags: [windows]）。
#
# 用法（以【管理员】身份打开 PowerShell）：
#   .\register-gitlab-runner-windows.ps1 -Url https://git.myverse.fans -Token glrt-xxxxxxxxxxxx
#
# 获取 token（GitLab 16+ 新流程）：
#   GitLab → opencode 项目 → Settings → CI/CD → Runners → 「New project runner」
#   → 平台选 Windows；【Tags 必须填 windows】→ Create → 复制认证 token（glrt- 开头）。
#   说明：tag(windows) 在 UI 设定，脚本不再传 tag。
param(
  [Parameter(Mandatory = $true)][string]$Url,
  [Parameter(Mandatory = $true)][string]$Token,
  [string]$InstallDir = "C:\GitLab-Runner"
)
$ErrorActionPreference = "Stop"

Write-Output "[1/5] 准备目录并下载 gitlab-runner.exe"
New-Item -ItemType Directory -Force $InstallDir | Out-Null
$exe = Join-Path $InstallDir "gitlab-runner.exe"
if (-not (Test-Path $exe)) {
  Invoke-WebRequest -Uri "https://gitlab-runner-downloads.s3.amazonaws.com/latest/binaries/gitlab-runner-windows-amd64.exe" -OutFile $exe
}

Write-Output "[2/5] 检查 / 安装工具链（bun / git）"
if (-not (Get-Command bun -ErrorAction SilentlyContinue)) {
  powershell -NoProfile -Command "irm bun.sh/install.ps1 | iex"
}
if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
  Write-Warning "未检测到 git，请手动安装：https://git-scm.com/download/win"
}

Write-Output "[3/5] 注册 runner（shell executor）"
& $exe register --non-interactive --url $Url --token $Token --executor shell --description "punkcode-win-x64"

Write-Output "[4/5] 安装并启动服务"
& $exe install
& $exe start

Write-Output "[5/5] 完成。验证："
& $exe verify
Write-Output "→ GitLab 的 Runners 页应显示该 runner 在线。"
Write-Output "→ 注意：默认服务以 SYSTEM 账户运行，可能找不到装在用户目录的 bun，导致 job 报 'bun 不是命令'。二选一解决："
Write-Output "   a) 改用当前用户跑服务（有用户 PATH）："
Write-Output "      & '$exe' stop; & '$exe' uninstall; & '$exe' install --user `"$env:COMPUTERNAME\$env:USERNAME`" --password '<你的Windows登录密码>'; & '$exe' start"
Write-Output "   b) 把 bun 目录（默认 %USERPROFILE%\.bun\bin）加进【系统】环境变量 PATH，再重启该服务。"
