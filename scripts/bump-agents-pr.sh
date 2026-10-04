#!/usr/bin/env bash
# Open the agent-bump pull request from a versions.json that
# scripts/bump-agents.sh produced (DECISIONS I-428).
#
# bump-agents.sh runs the downloaded agents, so in CI it holds no write
# token, and its output reaches this script as an artifact. This script
# holds the write token and executes nothing from that artifact: it checks
# that the candidate changes only the values of entries already in
# nix/overlay/agents/versions.json (version strings, https URLs on the
# known upstream hosts, sha256 SRI hashes), copies it into place, commits
# it on a branch and opens or retitles the PR.
#
# Needs: bash, jq, git, gh.
# Usage: scripts/bump-agents-pr.sh [--check-only] <candidate versions.json>
#   --check-only   validate and print the title; commit nothing
set -euo pipefail

root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
versions="$root/nix/overlay/agents/versions.json"
check_only=0
if [ "${1:-}" = "--check-only" ]; then
  check_only=1
  shift
fi
if [ $# -ne 1 ]; then
  echo "usage: $0 [--check-only] <candidate versions.json>" >&2
  exit 64
fi
cand=$1

die() { echo "bump-agents-pr: $*" >&2; exit 1; }

jq -e 'type == "object"' "$cand" >/dev/null 2>&1 || die "candidate is not a JSON object"

# Same shape: every leaf path of the current file, and no other.
shape='[paths(scalars)] | map(map(tostring) | join(".")) | sort'
if [ "$(jq -c "$shape" "$versions")" != "$(jq -c "$shape" "$cand")" ]; then
  die "candidate adds, removes or retypes entries; only values may change"
fi

# Every leaf is a string of the expected form for its key.
bad=$(jq -r '
  [paths(scalars) as $p | {p: ($p | map(tostring) | join(".")), k: $p[-1], v: getpath($p)}]
  | map(select(
      (.v | type) != "string"
      or (.k == "version" and (.v | test("^[0-9][0-9A-Za-z.+-]{0,63}$") | not))
      or (.k == "url" and (.v | test("^https://(downloads\\.claude\\.ai|github\\.com|registry\\.npmjs\\.org)/[A-Za-z0-9._~/@+-]+$") | not))
      or (.k == "hash" and (.v | test("^sha256-[A-Za-z0-9+/]{43}=$") | not))
      or ((.k | IN("version", "url", "hash")) | not)
    ))
  | .[].p' "$cand")
if [ -n "$bad" ]; then
  die "unexpected values at: $(echo "$bad" | tr '\n' ' ')"
fi

changed=$(jq -r --slurpfile old "$versions" '
  to_entries[] | select(.value.version != $old[0][.key].version)
  | "\(.key) \(.value.version)"' "$cand")
if [ -z "$changed" ]; then
  echo "nothing to bump"
  exit 0
fi
title="agents: $(echo "$changed" | paste -sd, - | sed 's/,/, /g')"
echo "$title"
if [ "$check_only" = 1 ]; then
  exit 0
fi

cp "$cand" "$versions"
branch="bump/agents-$(date -u +%Y%m%d)"
git -C "$root" checkout -B "$branch"
git -C "$root" add nix/overlay/agents/versions.json
git -C "$root" commit -m "$title" -m "Automated by scripts/bump-agents.sh and scripts/bump-agents-pr.sh. Merging changes no guest until repose-admin base publish (docs/workstreams/12-nix-config-pipeline.md)."
git -C "$root" push -f origin "$branch"
gh pr create --repo "$(git -C "$root" remote get-url origin | sed -E 's#.*github.com[:/]##; s#\.git$##')" \
  --head "$branch" --base main --title "$title" \
  --body "Automated bump. Each package was built and printed its version in a CI job with no write access; this job only checked and committed versions.json. Merging changes no guest until \`repose-admin base publish\`." \
  || gh pr edit "$branch" --title "$title"
