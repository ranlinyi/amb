#!/usr/bin/env bash
# 构建 amb 自包含单文件二进制（Linux x86_64 / Windows x86_64）
#
# 产物：
#   dist/amb-linux-x86_64          内嵌单文件版（Linux）
#   dist/amb-windows-x86_64.exe    内嵌单文件版（Windows）
#   dist/amb-linux-x86_64-lite     省空间部署版（Linux，按需下载依赖）
#
# 依赖：go(1.22+)、curl、tar、unzip、sha256sum
set -euo pipefail

VERSION="1.4.0"
SCRCPY_VERSION="4.1"

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

PAYLOAD="$ROOT/payload"
BUILD="$ROOT/.build"
DIST="$ROOT/dist"

SCRCPY_TAR="scrcpy-linux-x86_64-v$SCRCPY_VERSION.tar.gz"
SCRCPY_ZIP="scrcpy-win64-v$SCRCPY_VERSION.zip"
SCRCPY_TAR_DIR="scrcpy-linux-x86_64-v$SCRCPY_VERSION"
SCRCPY_ZIP_DIR="scrcpy-win64-v$SCRCPY_VERSION"
BASE="https://github.com/Genymobile/scrcpy/releases/download/v$SCRCPY_VERSION"

mkdir -p "$PAYLOAD" "$BUILD" "$DIST"

fetch() {
  url="$1"
  dest="$2"
  if [ -s "$dest" ]; then
    echo "[cached]   $dest"
    return 0
  fi
  echo "[download] $url"
  curl -fSL -C - --retry 10 --retry-delay 3 --retry-all-errors -o "$dest" "$url"
}

echo "== 1/5 下载官方产物（scrcpy v$SCRCPY_VERSION，内含配套 adb） =="
fetch "$BASE/$SCRCPY_TAR"  "$PAYLOAD/$SCRCPY_TAR"
fetch "$BASE/$SCRCPY_ZIP"  "$PAYLOAD/$SCRCPY_ZIP"
fetch "$BASE/SHA256SUMS.txt" "$PAYLOAD/SHA256SUMS.txt"

echo "== 2/5 校验 scrcpy 官方 SHA256 =="
(
  cd "$PAYLOAD"
  grep -E "($SCRCPY_TAR|$SCRCPY_ZIP)$" SHA256SUMS.txt > .scrcpy-expected
  sha256sum -c .scrcpy-expected
)

echo "== 3/5 组装运行目录 =="
rm -rf "$BUILD/linux" "$BUILD/windows" "$BUILD/linux-raw" "$BUILD/windows-raw"
mkdir -p "$BUILD/linux/bin" "$BUILD/linux/licenses"
mkdir -p "$BUILD/windows/bin" "$BUILD/windows/licenses"
mkdir -p "$BUILD/linux-raw" "$BUILD/windows-raw"

# --- Linux ---
tar -xzf "$PAYLOAD/$SCRCPY_TAR" -C "$BUILD/linux-raw"
cp "$BUILD/linux-raw/$SCRCPY_TAR_DIR/scrcpy"        "$BUILD/linux/bin/scrcpy"
cp "$BUILD/linux-raw/$SCRCPY_TAR_DIR/scrcpy-server" "$BUILD/linux/bin/scrcpy-server"
cp "$BUILD/linux-raw/$SCRCPY_TAR_DIR/adb"           "$BUILD/linux/bin/adb"
cp "$BUILD/linux-raw/$SCRCPY_TAR_DIR/LICENSE"       "$BUILD/linux/licenses/scrcpy-LICENSE"
chmod +x "$BUILD/linux/bin/scrcpy" "$BUILD/linux/bin/adb"

# --- Windows ---
unzip -q -o "$PAYLOAD/$SCRCPY_ZIP" -d "$BUILD/windows-raw"
cp -a "$BUILD/windows-raw/$SCRCPY_ZIP_DIR/." "$BUILD/windows/bin/"
rm -f "$BUILD/windows/bin"/*.bat "$BUILD/windows/bin"/*.vbs 2>/dev/null || true
for lf in LICENSE LICENSE.txt; do
  if [ -f "$BUILD/windows/bin/$lf" ]; then
    mv "$BUILD/windows/bin/$lf" "$BUILD/windows/licenses/scrcpy-LICENSE.txt"
    break
  fi
done

rm -rf "$BUILD/linux-raw" "$BUILD/windows-raw"

echo "== 4/5 生成内嵌 payload zip =="
go run ./tools/mkzip "$BUILD/linux"   "$PAYLOAD/amb-payload-linux-amd64.zip"
go run ./tools/mkzip "$BUILD/windows" "$PAYLOAD/amb-payload-windows-amd64.zip"

echo "== 5/5 交叉编译 =="
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$DIST/amb-linux-x86_64" .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$DIST/amb-windows-x86_64.exe" .
# 省空间部署版（仅 Linux）：不内嵌依赖，运行时扫描并按需下载到用户缓存
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -tags lite -trimpath -ldflags "-s -w" -o "$DIST/amb-linux-x86_64-lite" .

echo
echo "构建完成："
ls -la "$DIST"
(
  cd "$DIST"
  sha256sum amb-linux-x86_64 amb-windows-x86_64.exe amb-linux-x86_64-lite > SHA256SUMS
  cat SHA256SUMS
)
