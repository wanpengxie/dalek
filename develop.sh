#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
用法:
  ./develop.sh [--target /path/to/dalek] [--no-restart]

说明:
  1) 编译 dalek 到目标目录中的临时文件
  2) 使用 mv 原子替换目标二进制，避免 Text file busy
  3) 默认在 daemon 运行时执行 restart

参数:
  --target <path>   安装目标（默认: $HOME/bin/dalek）
  --no-restart      安装后不尝试重启 daemon
  -h, --help        显示帮助
EOF
}

target="${HOME}/bin/dalek"
restart_daemon="1"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      shift
      if [[ $# -eq 0 ]]; then
        echo "错误: --target 缺少参数" >&2
        exit 2
      fi
      target="$1"
      ;;
    --no-restart)
      restart_daemon="0"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "错误: 未知参数: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
target_dir="$(dirname "$target")"
target_base="$(basename "$target")"
tmp_bin="${target_dir}/.${target_base}.new.$$"

mkdir -p "$target_dir"

cleanup() {
  rm -f "$tmp_bin"
  rm -f "${target_dir}/.feishu.new.$$"
}
trap cleanup EXIT

echo "[develop] build -> ${tmp_bin}"
build_version="$(
  cd "$repo_root"
  git describe --tags --always --dirty 2>/dev/null || git rev-parse --short HEAD
)"
(
  cd "$repo_root"
  go build -ldflags "-X main.version=${build_version}" -o "$tmp_bin" ./cmd/dalek
)
chmod +x "$tmp_bin"

echo "[develop] install -> ${target}"
mv -f "$tmp_bin" "$target"

# --- feishu CLI ---
feishu_target="${target_dir}/feishu"
feishu_tmp="${target_dir}/.feishu.new.$$"
echo "[develop] build feishu -> ${feishu_tmp}"
(
  cd "$repo_root"
  go build -o "$feishu_tmp" ./cmd/feishu
)
chmod +x "$feishu_tmp"
echo "[develop] install feishu -> ${feishu_target}"
mv -f "$feishu_tmp" "$feishu_target"

if [[ "$restart_daemon" != "1" ]]; then
  echo "[develop] done (skip daemon restart)"
  exit 0
fi

if "$target" daemon status >/dev/null 2>&1; then
  echo "[develop] daemon restart"
  "$target" daemon restart
else
  echo "[develop] daemon not running, skip restart"
fi

echo "[develop] done"
