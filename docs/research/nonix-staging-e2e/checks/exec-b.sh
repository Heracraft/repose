#!/usr/bin/env bash
# Staging EXEC_B: `repose-admin exec` on the staging edge (stg-admin).
#   exec-b.sh <unused> <unused> <project id> <script>
set -uo pipefail
pid=$3 script=$4
out=$(ssh -F /home/azureuser/repose-ws/staging-ssh-config stg-edge "/var/lib/repose-staging/admin exec $pid -- sh -c $(printf '%q' "$script")" 2>/dev/null)
rc=$?
printf '%s\n' "$out" | grep -vE '^op [0-9a-f-]{36} enqueued$'
exit $rc
