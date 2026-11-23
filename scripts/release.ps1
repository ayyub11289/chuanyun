<#
发布新版本：改版本号、测试、编译、签名，再上传到服务器。客户端会在 3 小时内自动下载并提示「重启更新」。

  .\scripts\release.ps1 -Version 1.4.1 -Notes "修复某某问题"
  .\scripts\release.ps1 -Version 1.4.1 -Notes "…" -Nodes D:\Chuanyun\chuanyun-nodes.json   # 顺带更新节点
  .\scripts\release.ps1 -NodesOnly -Nodes D:\Chuanyun\chuanyun-nodes.json                   # 只推送新节点
  加 -NoUpload 只生成 release\site，不上传；加 -Installer 同时打 NSIS 安装包。

第一次使用前运行：go run ./cmd/chuanyun-release init -github 用户名/仓库名（或 -server root@服务器IP）
已经 init 过、只想换发布位置：go run ./cmd/chuanyun-release target -github 用户名/仓库名
#>
param(
  [string]$Version = "",
  [string]$Notes = "",
  [string]$Nodes = "",
  [switch]$NodesOnly,
  [switch]$NoUpload,
  [switch]$Installer
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root
$utf8 = New-Object Text.UTF8Encoding $false

function Run([string]$what, [scriptblock]$block) {
  Write-Host "==> $what" -ForegroundColor Cyan
  & $block
  if ($LASTEXITCODE -ne 0) { throw "$what 失败（退出码 $LASTEXITCODE）" }
}

function Set-FileText([string]$file, [string]$pattern, [string]$replacement) {
  $p = (Resolve-Path $file).Path
  $t = [IO.File]::ReadAllText($p) -replace $pattern, $replacement
  [IO.File]::WriteAllText($p, $t, $utf8)
}

if (-not (Test-Path release\release.json)) { throw "还没有 release\release.json，先运行 go run ./cmd/chuanyun-release init -github 用户名/仓库名" }
$cfg = Get-Content release\release.json -Raw | ConvertFrom-Json
if ($NodesOnly -and -not $Nodes) { throw "-NodesOnly 需要 -Nodes 节点文件" }

if (-not $NodesOnly) {
  if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "-Version 必须是 x.y.z" }
  Set-FileText internal\version\version.go 'var Version = "[^"]*"' "var Version = `"$Version`""
  Set-FileText cmd\chuanyun\winres\winres.json '"(file_version|product_version)": "[^"]*"' "`"`$1`": `"$Version.0`""
  Set-FileText cmd\chuanyun\winres\winres.json '"ProductVersion": "[^"]*"' "`"ProductVersion`": `"$Version`""
  Set-FileText scripts\chuanyun.nsi '!define VERSION "[^"]*"' "!define VERSION `"$Version`""

  $env:CGO_ENABLED = "1"
  Run "测试" { go test -count=1 ./internal/... }
  Run "生成图标和版本资源" { Push-Location cmd\chuanyun; go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64; Pop-Location }
  Run "编译 Chuanyun.exe" { go build -ldflags "-H windowsgui -s -w" -o dist\Chuanyun.exe ./cmd/chuanyun }
  Run "签名 v$Version" { go run ./cmd/chuanyun-release app -exe dist\Chuanyun.exe -version $Version -notes $Notes }
}

if ($Nodes) {
  Run "加密节点" { go run ./cmd/chuanyun-release nodes -in $Nodes }
}

if ($Installer -and -not $NodesOnly) {
  $nsis = Join-Path $root ".tools\nsis-3.10\makensis.exe"
  if (-not (Test-Path $nsis)) { $nsis = (Get-Command makensis -ErrorAction SilentlyContinue).Source }
  if (-not $nsis) { throw "找不到 makensis" }
  $stage = Join-Path $root "dist\stage"
  Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force "$stage\bin", "$stage\rule-set", "$stage\third_party\sing-box", "$stage\third_party\noto" | Out-Null
  Copy-Item dist\Chuanyun.exe, README.md, LICENSE $stage
  Copy-Item examples\proxy-nodes.json "$stage\示例-proxy-nodes.json"
  $sb = if (Test-Path D:\Chuanyun\bin\sing-box.exe) { "D:\Chuanyun\bin" } else { "dist\Chuanyun\bin" }
  Copy-Item "$sb\sing-box.exe" "$stage\bin"
  Copy-Item (Join-Path (Split-Path $sb -Parent) "rule-set\*.srs") "$stage\rule-set"
  Copy-Item third_party\sing-box\* "$stage\third_party\sing-box"
  Copy-Item third_party\noto\OFL.txt "$stage\third_party\noto"
  Push-Location $stage
  Run "打安装包" { & $nsis -V2 -NOCD -INPUTCHARSET UTF8 "-DVERSION=$Version" (Join-Path $root "scripts\chuanyun.nsi") }
  Pop-Location
  Move-Item -Force "$stage\Chuanyun-Setup-$Version-x64.exe" dist\
  Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
}

if (-not $NoUpload) {
  $files = Get-ChildItem release\site -File | ForEach-Object { $_.FullName }
  if (-not $files) { throw "release\site 是空的" }
  if ($cfg.github) {
    if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { throw "没装 GitHub CLI：winget install --id GitHub.cli，然后 gh auth login" }
    if ($NodesOnly) {
      # 只换节点：覆盖最新 Release 里的 nodes.bin，客户端读的是 releases/latest/download/。
      $tag = gh release view --repo $cfg.github --json tagName -q .tagName
      if ($LASTEXITCODE -ne 0 -or -not $tag) { throw "仓库 $($cfg.github) 还没有 Release，先发一次完整版本" }
      $nodeFiles = $files | Where-Object { (Split-Path $_ -Leaf) -like 'nodes.bin*' }
      Run "更新 $($cfg.github) $tag 的节点" { gh release upload $tag $nodeFiles --repo $cfg.github --clobber }
    } else {
      $title = "穿云 v$Version"
      $body = if ($Notes) { $Notes } else { $title }
      Run "发布到 GitHub $($cfg.github) v$Version" { gh release create "v$Version" $files --repo $cfg.github --title $title --notes $body --latest }
    }
  } else {
    Run "上传到 $($cfg.server):$($cfg.remote_dir)" { scp -q $files "$($cfg.server):$($cfg.remote_dir)/" }
  }
  Write-Host "完成。客户端会在 3 小时内自动检查；想立刻生效可在客户端「设置 → 检查更新」。" -ForegroundColor Green
} else {
  Write-Host "完成。文件在 release\site，未上传。" -ForegroundColor Green
}
