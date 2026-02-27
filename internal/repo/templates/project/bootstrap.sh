#!/usr/bin/env bash
set -euo pipefail

# Worktree bootstrap hook.
# 由 project-init 根据项目实际情况填充。
# 约束：幂等、快速、静默成功。
#
# 可用环境变量：
#   DALEK_WORKTREE_PATH  DALEK_TICKET_ID  DALEK_WORKER_ID
#   DALEK_PROJECT_NAME   DALEK_PROJECT_KEY DALEK_REPO_ROOT

echo 'bootstrap: ok'
exit 0
