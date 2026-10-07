#!/usr/bin/env bash
# Bump the agent overlay to upstream's latest releases
# (docs/workstreams/12-nix-config-pipeline.md "Agent overlay").
#
# For each agent in nix/overlay/agents/versions.json: ask upstream for its
# latest version, prefetch the release asset, rewrite the entry, build the
# package and run `<agent> --version` with an empty environment. It leaves
# the rewritten versions.json in the tree and commits nothing:
# scripts/bump-agents-pr.sh checks that file and opens the pull request,
# in a separate CI job that runs nothing this script downloaded
# (DECISIONS I-428). Merging the PR changes no guest until
# `repose-admin base publish`.
#
# Needs: bash, curl, jq, nix (flakes), git.
# Usage: scripts/bump-agents.sh [--only <agent>[,<agent>]] [--check]
#   --check   only report which agents are behind; exit 2 if any
set -euo pipefail

root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
versions="$root/nix/overlay/agents/versions.json"
check=0
only=""
while [ $# -gt 0 ]; do
  case "$1" in
    --pr) echo "$0: --pr moved to scripts/bump-agents-pr.sh (DECISIONS I-428)" >&2; exit 64 ;;
    --check) check=1 ;;
    --only) only="$2"; shift ;;
    *) echo "usage: $0 [--check] [--only a,b]" >&2; exit 64 ;;
  esac
  shift
done

curl_gh() {
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "$@"
  else
    curl -fsSL "$@"
  fi
}

# latest <agent> prints "<version> <url>" for the linux x86_64 asset.
latest() {
  case "$1" in
    claude-code)
      local v; v=$(curl -fsSL https://downloads.claude.ai/claude-code-releases/latest)
      echo "$v https://downloads.claude.ai/claude-code-releases/$v/linux-x64/claude" ;;
    opencode)
      curl_gh https://api.github.com/repos/anomalyco/opencode/releases/latest \
        | jq -r '"\(.tag_name | ltrimstr("v")) \(.assets[] | select(.name == "opencode-linux-x64.tar.gz") | .browser_download_url)"' ;;
    codex)
      curl_gh https://api.github.com/repos/openai/codex/releases/latest \
        | jq -r '"\(.tag_name | ltrimstr("rust-v")) \(.assets[] | select(.name == "codex-x86_64-unknown-linux-musl.tar.gz") | .browser_download_url)"' ;;
    gemini-cli)
      local v; v=$(curl -fsSL https://registry.npmjs.org/@google/gemini-cli/latest | jq -r .version)
      echo "$v https://registry.npmjs.org/@google/gemini-cli/-/gemini-cli-$v.tgz" ;;
    pi-coding-agent)
      curl_gh https://api.github.com/repos/earendil-works/pi/releases/latest \
        | jq -r '"\(.tag_name | ltrimstr("v")) \(.assets[] | select(.name == "pi-linux-x64.tar.gz") | .browser_download_url)"' ;;
    herdr)
      # herdr, the second multiplexer (DECISIONS I-501): not an agent, but
      # pinned and moved the same way. herdr.nix's install check refuses a
      # release with another socket protocol generation.
      curl_gh https://api.github.com/repos/herdrdev/herdr/releases/latest \
        | jq -r '"\(.tag_name | ltrimstr("v")) \(.assets[] | select(.name == "herdr-linux-x86_64") | .browser_download_url)"' ;;
    chrome-devtools-mcp)
      local v; v=$(curl -fsSL https://registry.npmjs.org/chrome-devtools-mcp/latest | jq -r .version)
      echo "$v https://registry.npmjs.org/chrome-devtools-mcp/-/chrome-devtools-mcp-$v.tgz" ;;
    *) echo "unknown agent $1" >&2; return 1 ;;
  esac
}

# The versions.json key that holds url and hash for an agent.
slot() {
  case "$1" in
    gemini-cli) echo npm ;;
    chrome-devtools-mcp) echo "" ;;
    *) echo x86_64-linux ;;
  esac
}

binary() {
  case "$1" in
    claude-code) echo claude ;;
    gemini-cli) echo gemini ;;
    pi-coding-agent) echo pi ;;
    *) echo "$1" ;;
  esac
}

agents=$(jq -r 'keys[]' "$versions")
if [ -n "$only" ]; then
  agents=$(tr ',' '\n' <<<"$only")
fi

changed=()
behind=()
for a in $agents; do
  cur=$(jq -r --arg a "$a" '.[$a].version' "$versions")
  read -r new url < <(latest "$a")
  if [ -z "$new" ] || [ -z "$url" ] || [ "$url" = "null" ]; then
    echo "$a: could not resolve upstream" >&2
    continue
  fi
  if [ "$new" = "$cur" ]; then
    echo "$a: $cur is current"
    continue
  fi
  behind+=("$a $cur -> $new")
  if [ "$check" = 1 ]; then
    continue
  fi
  echo "$a: $cur -> $new ($url)"
  hash=$(nix store prefetch-file --json "$url" | jq -r .hash)
  s=$(slot "$a")
  tmp=$(mktemp)
  if [ -n "$s" ]; then
    jq --arg a "$a" --arg v "$new" --arg s "$s" --arg u "$url" --arg h "$hash" \
      '.[$a].version = $v | .[$a][$s] = {url: $u, hash: $h}' "$versions" > "$tmp"
  else
    jq --arg a "$a" --arg v "$new" --arg u "$url" --arg h "$hash" \
      '.[$a].version = $v | .[$a].url = $u | .[$a].hash = $h' "$versions" > "$tmp"
  fi
  mv "$tmp" "$versions"
  # Codex runs shell commands through a second release asset, pinned
  # beside the binary at the same version (DECISIONS I-426).
  if [ "$a" = codex ]; then
    hurl=${url/codex-x86_64/codex-code-mode-host-x86_64}
    hhash=$(nix store prefetch-file --json "$hurl" | jq -r .hash)
    tmp=$(mktemp)
    jq --arg u "$hurl" --arg h "$hhash" '.codex["code-mode-host"] = {url: $u, hash: $h}' "$versions" > "$tmp"
    mv "$tmp" "$versions"
    # And its bundled bwrap, which it checks against a digest built into
    # the binary, so it moves with the version too (DECISIONS I-495).
    burl=${url/codex-x86_64/bwrap-x86_64}
    bhash=$(nix store prefetch-file --json "$burl" | jq -r .hash)
    tmp=$(mktemp)
    jq --arg u "$burl" --arg h "$bhash" '.codex.bwrap = {url: $u, hash: $h}' "$versions" > "$tmp"
    mv "$tmp" "$versions"
  fi
  changed+=("$a $new")
done

if [ "$check" = 1 ]; then
  if [ ${#behind[@]} -gt 0 ]; then
    printf '%s\n' "${behind[@]}"
    exit 2
  fi
  exit 0
fi

if [ ${#changed[@]} -eq 0 ]; then
  echo "nothing to bump"
  exit 0
fi

# Build what changed and prove each binary starts.
for c in "${changed[@]}"; do
  a=${c%% *}
  case "$a" in
    chrome-devtools-mcp) attr="chrome-devtools-mcp"; b="chrome-devtools-mcp" ;;
    herdr) attr="herdr"; b="herdr" ;;
    *) attr="$a.unwrapped"; b=$(binary "$a") ;;
  esac
  # git+file, like ci.yml's builds: a bare absolute path is a path flake,
  # and CI's Nix refuses a path flake whose lock has the relative, unlocked
  # fragment-placeholder input ("lock file contains unlocked input"); the
  # daily job failed on it from 2026-09-21. Dirty tracked files (the bump
  # just edited) are still included.
  out=$(nix build --no-link --print-out-paths "git+file://$root?dir=nix#$attr")
  echo "$a: built $out"
  # The one place a downloaded binary runs. env -i: it sees no token or
  # other variable of the caller, only a scratch HOME and PATH.
  env -i HOME="$(mktemp -d)" PATH="$PATH" "$out/bin/$b" --version
  # Codex's own install check already started its daemon from the built
  # package, which needs the whole package layout (DECISIONS I-487).
  if [ "$a" = codex ]; then
    test -x "$out/bin/codex-code-mode-host"
    test -f "$out/codex-package.json"
  fi
done

echo "agents: $(printf '%s, ' "${changed[@]}" | sed 's/, $//')"
