# 一键发布 PunkcodeAI 桌面端热更新（持久化在仓库 deploy/ 下，不再放 C 盘临时目录）。
#
# 做的事：运行时从思源读取生产 IP + SSH 密码（不落明文、不进 git），再调用同目录的
# publish-desktop-update.ps1，把 ../../opencode/packages/desktop/dist 下的
# latest.yml + PunkcodeAI-win-x64.exe(+.blockmap) 推到生产 /updates。
#
# 前置：先在 opencode 仓库 `bun run build && bun run package:win` 打出 dist；思源客户端在线。
# 用法（在 sub2api 仓库根执行）：
#   pwsh -File deploy/publish-desktop-update-run.ps1
#
# 路径可移植：脚本自身位置($PSScriptRoot)定位 publish-desktop-update.ps1 与 dist（假定
# opencode 与 sub2api 同级置于同一父目录）；siyuan.py 走 $env:USERPROFILE。无明文凭据，
# 仅含公开的 host key 指纹与思源 block id（均非机密），可安全提交。

$ErrorActionPreference = 'Stop'

$root = $PSScriptRoot
$siyuan = Join-Path $env:USERPROFILE '.claude\skills\siyuan-note\siyuan.py'
$distRaw = Join-Path $root '..\..\opencode\packages\desktop\dist'
if (-not (Test-Path $distRaw)) {
  throw "dist 目录不存在: $distRaw（先在 opencode 仓库 bun run build && bun run package:win）"
}
$dist = (Resolve-Path $distRaw).Path

# 思源「/工具/sub2api → 配置与部署 → 部署主机」block：含生产 IP + SSH 密码（运行时读取，不落明文）
$raw = & python $siyuan get-block-kramdown --block-id '20260605164056-4wjrl9f' 2>&1 | Out-String
if (-not $raw -or $raw.Length -lt 20) { throw 'siyuan 返回空（思源客户端未开？）' }
$ip = [regex]::Match($raw, '([0-9]{1,3}(?:\.[0-9]{1,3}){3})').Groups[1].Value
$pw = [regex]::Match($raw, '([A-Za-z0-9][A-Za-z0-9@._!+%-]*@[A-Za-z0-9@._!+%-]+)').Groups[1].Value
if (-not $ip -or -not $pw) { throw '凭据提取失败（检查思源「部署主机」block 格式）' }

# host key 指纹是服务器公钥指纹，公开非机密
$hostkey = 'SHA256:lqoYlgo4QxbFGrksJycjZkc8ELD+yUQFCjuPa+qTByM'

& (Join-Path $root 'publish-desktop-update.ps1') -DistDir $dist -HostName $ip -User 'ubuntu' -Password $pw -HostKey $hostkey
