#!/usr/bin/env bash
# 在 Linux 上交叉编译穿云 Windows x64 便携包（以及可选的 NSIS 安装器）。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CACHE="${CHUANYUN_CACHE:-/tmp/chuanyun-tools}"
VER="1.14.4"
DIST="$ROOT/dist/Chuanyun"
ART="${ARTIFACT_DIR:-/opt/cursor/artifacts}"
SB_LINUX="$CACHE/sing-box-${VER}-linux-amd64/sing-box"
SB_WIN_DIR="$CACHE/sing-box-${VER}-windows-amd64"

mkdir -p "$CACHE" "$ART" "$DIST/bin" "$DIST/rule-set" "$DIST/third_party/sing-box" "$DIST/third_party/noto"

if ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
  echo "需要 mingw-w64（x86_64-w64-mingw32-gcc）。Debian/Ubuntu: sudo apt install gcc-mingw-w64-x86-64" >&2
  exit 1
fi

if [[ ! -x "$SB_LINUX" ]]; then
  echo "下载 sing-box ${VER} linux-amd64（仅用于 sing-box check）…"
  curl -fL --retry 3 -o "$CACHE/sb-linux.tar.gz" \
    "https://github.com/SagerNet/sing-box/releases/download/v${VER}/sing-box-${VER}-linux-amd64.tar.gz"
  tar -xzf "$CACHE/sb-linux.tar.gz" -C "$CACHE"
fi
if [[ ! -f "$SB_WIN_DIR/sing-box.exe" ]]; then
  echo "下载 sing-box ${VER} windows-amd64…"
  curl -fL --retry 3 -o "$CACHE/sb-win.zip" \
    "https://github.com/SagerNet/sing-box/releases/download/v${VER}/sing-box-${VER}-windows-amd64.zip"
  unzip -qo "$CACHE/sb-win.zip" -d "$CACHE"
fi
if [[ ! -f "$CACHE/geosite-cn.srs" || ! -f "$CACHE/geoip-cn.srs" ]]; then
  curl -fL --retry 3 -o "$CACHE/geosite-cn.srs" \
    "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs"
  curl -fL --retry 3 -o "$CACHE/geoip-cn.srs" \
    "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"
fi

export SINGBOX_BIN="$SB_LINUX"
export CHUANYUN_RULESET_DIR="$CACHE"
cd "$ROOT"
go test ./...

(
  cd "$ROOT/cmd/chuanyun"
  go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64
)

CGO_ENABLED=1 \
  CC=x86_64-w64-mingw32-gcc \
  GOOS=windows \
  GOARCH=amd64 \
  go build -ldflags "-H windowsgui -s -w" \
  -o "$DIST/Chuanyun.exe" ./cmd/chuanyun

cp "$SB_WIN_DIR/sing-box.exe" "$DIST/bin/sing-box.exe"
cp "$CACHE/geosite-cn.srs" "$CACHE/geoip-cn.srs" "$DIST/rule-set/"
cp "$ROOT/README.md" "$ROOT/LICENSE" "$ROOT/examples/proxy-nodes.json" "$DIST/"
mv "$DIST/proxy-nodes.json" "$DIST/示例-proxy-nodes.json"
cp "$ROOT/third_party/sing-box/LICENSE" "$ROOT/third_party/sing-box/GPL-3.0.txt" "$DIST/third_party/sing-box/"
cp "$ROOT/third_party/noto/OFL.txt" "$DIST/third_party/noto/"
printf '%s\n' '便携模式：设置、日志和生成的 sing-box.json 保存在本目录的 data 文件夹。' > "$DIST/portable.txt"

TMPZIP="$(mktemp /tmp/chuanyun-XXXX.zip)"
rm -f "$TMPZIP" "$ART/Chuanyun-windows-amd64.zip"
(
  cd "$ROOT/dist"
  zip -r -9 -q "$TMPZIP" Chuanyun
)
cp "$TMPZIP" "$ART/Chuanyun-windows-amd64.zip"
rm -f "$TMPZIP"

if command -v makensis >/dev/null 2>&1; then
  APPVER="$(sed -n 's/^var Version = "\(.*\)"/\1/p' "$ROOT/internal/version/version.go")"
  (
    cd "$DIST"
    makensis -NOCD -INPUTCHARSET UTF8 -DVERSION="$APPVER" "$ROOT/scripts/chuanyun.nsi"
  )
  mv "$DIST/Chuanyun-Setup-${APPVER}-x64.exe" "$ART/"
else
  echo "未安装 makensis，跳过安装器。仅生成便携 zip。"
fi

echo "完成:"
ls -lh "$ART"/Chuanyun-*
sha256sum "$ART"/Chuanyun-*
