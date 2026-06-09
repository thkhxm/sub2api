# Publish PunkcodeAI desktop auto-update artifacts to the server /updates endpoint.
#
# What it does: uploads latest.yml + PunkcodeAI-win-x64.exe(+.blockmap) from the local
# packaging dist dir to the host /tmp, then `sudo cp` into /www/wwwroot/punkcodeai-updates/.
# nginx serves that dir at https://<domain>/updates/ ; the desktop app's electron-updater
# (prod channel feed = https://punkcodeai.myverse.site/updates) reads latest.yml and downloads
# the exe when a newer version is available.
#
# IMPORTANT: bump packages/desktop/package.json "version" BEFORE packaging, otherwise clients
# already on the same version number will NOT see an update (electron-updater compares versions,
# not file hashes).
#
# Usage (run on the Windows packaging machine after `bun run build && bun run package:win`):
#   ./publish-desktop-update.ps1 `
#       -DistDir 'D:/project/opencode/packages/desktop/dist' `
#       -HostName '<HOST_IP>' -Password '<SSH_PASSWORD>' `
#       -HostKey 'SHA256:<host-key-fingerprint>'
#
# Credentials are passed as parameters (never hard-coded). Requires PuTTY plink/pscp on PATH.

param(
  [Parameter(Mandatory = $true)] [string]$DistDir,
  [Parameter(Mandatory = $true)] [string]$HostName,
  [string]$User = "ubuntu",
  [Parameter(Mandatory = $true)] [string]$Password,
  [Parameter(Mandatory = $true)] [string]$HostKey,
  [string]$RemoteDir = "/www/wwwroot/punkcodeai-updates"
)

$ErrorActionPreference = "Stop"

# prod channel publishes flat into /updates; dev/beta would use /updates/dev , /updates/beta.
$files = @("latest.yml", "PunkcodeAI-win-x64.exe", "PunkcodeAI-win-x64.exe.blockmap")
foreach ($f in $files) {
  $p = Join-Path $DistDir $f
  if (-not (Test-Path $p)) { throw "Missing artifact '$f' in $DistDir (did packaging succeed?)" }
}

$target = "$User@$HostName"

Write-Output "[1/3] prepare staging dir on host"
& plink -ssh -batch -hostkey $HostKey -pw $Password $target "mkdir -p /tmp/punkcode-updates && rm -f /tmp/punkcode-updates/*"

Write-Output "[2/3] upload artifacts (exe ~118MB, please wait)"
$srcs = $files | ForEach-Object { Join-Path $DistDir $_ }
& pscp -batch -hostkey $HostKey -pw $Password @srcs "${target}:/tmp/punkcode-updates/"
if ($LASTEXITCODE -ne 0) { throw "pscp upload failed (exit $LASTEXITCODE)" }

Write-Output "[3/3] publish into $RemoteDir (sudo) + cleanup"
& plink -ssh -batch -hostkey $HostKey -pw $Password $target "sudo cp /tmp/punkcode-updates/* $RemoteDir/ && sudo chmod 644 $RemoteDir/* && rm -rf /tmp/punkcode-updates && echo PUBLISHED && ls -la $RemoteDir/"

Write-Output "Done. Verify: curl -I https://<domain>/updates/latest.yml  (expect HTTP 200)"
