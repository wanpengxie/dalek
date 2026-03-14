#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename "$0")"
POLL_SECONDS=15
AUTO_MERGE=1
TICKETS=(110 111 112 113)
COMMON_ARGS=()

usage() {
  cat <<'EOF'
用法:
  tools/focus_batch_dispatch.sh [--tickets 110,111,112,113] [--poll 15] [--no-merge] [--home PATH] [--project NAME]

行为:
  - 按给定 ticket 列表严格串行调度
  - 前一张 ticket 未真正收口前，绝不启动下一张
  - 默认会在 ticket=done 且 integration_status=needs_merge 时尝试自动 merge
  - 自动 merge 前会检查 repo root 必须干净
  - 遇到 blocked / needs_user / merge conflict / 脏工作区 会立即停下并报错

示例:
  tools/focus_batch_dispatch.sh
  tools/focus_batch_dispatch.sh --tickets 110,111 --poll 10
  tools/focus_batch_dispatch.sh --no-merge
EOF
}

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

json_get() {
  local path="$1"
  python3 - "$path" <<'PY'
import json
import sys

path = [p for p in sys.argv[1].split(".") if p]
obj = json.load(sys.stdin)
value = obj
for part in path:
    if isinstance(value, dict):
        value = value.get(part)
    else:
        value = None
        break

if value is None:
    print("")
elif isinstance(value, bool):
    print("true" if value else "false")
else:
    print(value)
PY
}

strip_heads_ref() {
  local ref="$1"
  ref="${ref#refs/heads/}"
  printf '%s\n' "$ref"
}

repo_dirty() {
  [[ -n "$(git status --porcelain)" ]]
}

ensure_daemon_running() {
  local status_json running
  status_json="$(dalek daemon status -o json)"
  running="$(printf '%s' "$status_json" | json_get "running")"
  if [[ "$running" != "true" ]]; then
    log "daemon 未运行，无法调度。"
    exit 10
  fi
}

ticket_show_json() {
  local ticket_id="$1"
  dalek ticket show "${COMMON_ARGS[@]}" --ticket "$ticket_id" -o json
}

merge_status_json() {
  local ticket_id="$1"
  dalek merge status "${COMMON_ARGS[@]}" --ticket "$ticket_id" -o json
}

start_ticket() {
  local ticket_id="$1"
  log "启动 t${ticket_id}"
  dalek ticket start "${COMMON_ARGS[@]}" --ticket "$ticket_id" -o json >/dev/null
}

rescan_merge_ref() {
  local target_ref="$1"
  dalek merge rescan "${COMMON_ARGS[@]}" --ref "$target_ref" -o json >/dev/null
}

auto_merge_ticket() {
  local ticket_id="$1"
  local worker_branch="$2"
  local target_ref="$3"
  local target_branch

  if [[ -z "$worker_branch" ]]; then
    log "t${ticket_id} 缺少 worker branch，无法自动 merge。"
    exit 21
  fi
  if [[ -z "$target_ref" ]]; then
    log "t${ticket_id} 缺少 target_ref，无法自动 merge。"
    exit 22
  fi
  if repo_dirty; then
    log "repo root 不干净，拒绝自动 merge。请先清理工作区，或用 --no-merge 仅做调度。"
    exit 23
  fi

  target_branch="$(strip_heads_ref "$target_ref")"
  if [[ -z "$target_branch" ]]; then
    log "target_ref=$target_ref 非法，无法自动 merge。"
    exit 24
  fi

  if ! git rev-parse --verify "${worker_branch}^{commit}" >/dev/null 2>&1; then
    log "worker branch 不存在：$worker_branch"
    exit 25
  fi

  log "自动 merge t${ticket_id}: ${worker_branch} -> ${target_branch}"
  git checkout "$target_branch" >/dev/null

  if git merge-base --is-ancestor "$(git rev-parse "$worker_branch")" HEAD >/dev/null 2>&1; then
    log "t${ticket_id} 的 worker branch 已包含在 ${target_branch}，只执行 merge rescan。"
    rescan_merge_ref "$target_ref"
    return
  fi

  if ! git merge --no-edit "$worker_branch"; then
    log "自动 merge t${ticket_id} 发生冲突，执行 git merge --abort 并退出。"
    git merge --abort >/dev/null 2>&1 || true
    exit 26
  fi

  rescan_merge_ref "$target_ref"
  log "自动 merge t${ticket_id} 完成，已触发 merge rescan。"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tickets)
      if [[ $# -lt 2 ]]; then
        echo "--tickets 缺少参数" >&2
        exit 2
      fi
      IFS=',' read -r -a TICKETS <<<"$2"
      shift 2
      ;;
    --poll)
      if [[ $# -lt 2 ]]; then
        echo "--poll 缺少参数" >&2
        exit 2
      fi
      POLL_SECONDS="$2"
      shift 2
      ;;
    --no-merge)
      AUTO_MERGE=0
      shift
      ;;
    --home)
      if [[ $# -lt 2 ]]; then
        echo "--home 缺少参数" >&2
        exit 2
      fi
      COMMON_ARGS+=(--home "$2")
      shift 2
      ;;
    --project|-p)
      if [[ $# -lt 2 ]]; then
        echo "--project 缺少参数" >&2
        exit 2
      fi
      COMMON_ARGS+=(--project "$2")
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "未知参数: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

ensure_daemon_running
log "开始严格串行调度: ${TICKETS[*]}"
if [[ "$AUTO_MERGE" -eq 1 ]]; then
  log "自动 merge: 开启"
else
  log "自动 merge: 关闭"
fi

for ticket_id in "${TICKETS[@]}"; do
  log "进入 t${ticket_id} 调度循环"

  while true; do
    show_json="$(ticket_show_json "$ticket_id")"
    status="$(printf '%s' "$show_json" | json_get "status")"
    title="$(printf '%s' "$show_json" | json_get "title")"
    runtime="$(printf '%s' "$show_json" | json_get "worker.runtime")"
    needs_user="$(printf '%s' "$show_json" | json_get "worker.needs_user")"
    worker_branch="$(printf '%s' "$show_json" | json_get "worker.branch")"

    case "$status" in
      backlog)
        log "t${ticket_id} [$title] status=backlog，准备启动"
        start_ticket "$ticket_id"
        ;;
      queued|active)
        log "t${ticket_id} [$title] status=${status} runtime=${runtime:-unknown}，继续等待"
        ;;
      blocked)
        log "t${ticket_id} [$title] status=blocked runtime=${runtime:-unknown} needs_user=${needs_user:-false}，停止调度"
        exit 30
        ;;
      done)
        merge_json="$(merge_status_json "$ticket_id")"
        integration_status="$(printf '%s' "$merge_json" | json_get "integration_status")"
        target_ref="$(printf '%s' "$merge_json" | json_get "target_ref")"

        case "$integration_status" in
          merged|abandoned)
            log "t${ticket_id} [$title] integration_status=${integration_status}，允许进入下一张"
            break
            ;;
          needs_merge|"")
            if [[ "$AUTO_MERGE" -eq 1 ]]; then
              auto_merge_ticket "$ticket_id" "$worker_branch" "$target_ref"
            else
              log "t${ticket_id} [$title] 已 done 但尚未 merge，等待外部 merge 完成"
            fi
            ;;
          *)
            log "t${ticket_id} [$title] integration_status=${integration_status}，无法判定，停止调度"
            exit 31
            ;;
        esac
        ;;
      archived)
        log "t${ticket_id} [$title] status=archived，视为已完成"
        break
        ;;
      "")
        log "t${ticket_id} 无法读取状态，停止调度"
        exit 32
        ;;
      *)
        log "t${ticket_id} [$title] 遇到未知状态=${status}，停止调度"
        exit 33
        ;;
    esac

    sleep "$POLL_SECONDS"
  done
done

log "全部 ticket 已按严格串行规则完成调度。"
