#!/usr/bin/env bash
# Bump the agent overlay to upstream's latest releases
# (docs/workstreams/12-nix-config-pipeline.md "Agent overlay").
#
# For each agent in nix/overlay/agents/versions.json: ask upstream for its
# latest version, prefetch the release asset for each architecture
# (x86_64-linux and aarch64-linux, DECISIONS I-1374), rewrite the entry,
# build the package for this machine's architecture and run `<agent>
# --version` with an empty environment. The other architecture's asset
# is downloaded and hashed here and built by ci.yml's aarch64 overlay
# job, which runs on an arm64 runner. It leaves
# the rewritten versions.json in the tree and commits nothing:
# scripts/bump-agents-pr.sh checks that file and opens the pull request,
# in a separate CI job that runs nothing this script downloaded
# (DECISIONS I-428). Merging the PR changes no guest until
# `repose-admin base publish`.
#
# images/guest/versions.json is the list (DECISIONS I-1470): the erofs
# guest installs from it (images/guest/scripts/install-agents.sh) and adds
# playwright-mcp, which the NixOS guest takes from nixpkgs. Every rewrite
# here is copied to nix/overlay/agents/versions.json, so the two files are
# always the same bytes while both guests exist.
#
# Without Nix (--no-nix, or no nix on PATH) hashes are computed with curl
# and openssl and nothing is built: images/guest/build.sh checks every hash
# again and the guest boot runs each `--version`.
#
# Needs: bash, curl, jq, git; nix (flakes) for the build and the smoke run,
# openssl without it.
# Usage: scripts/bump-agents.sh [--only <agent>[,<agent>]] [--check] [--no-nix]
#   --check   only report which agents are behind; exit 2 if any
set -euo pipefail

root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
versions="$root/images/guest/versions.json"
nix_versions="$root/nix/overlay/agents/versions.json"
check=0
use_nix=1
command -v nix >/dev/null 2>&1 || use_nix=0
only=""
while [ $# -gt 0 ]; do
  case "$1" in
    --pr) echo "$0: --pr moved to scripts/bump-agents-pr.sh (DECISIONS I-428)" >&2; exit 64 ;;
    --check) check=1 ;;
    --only) only="$2"; shift ;;
    --no-nix) use_nix=0 ;;
    *) echo "usage: $0 [--check] [--only a,b] [--no-nix]" >&2; exit 64 ;;
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

# prefetch <url>: the SRI sha256 of the file at url.
prefetch() {
  if [ "$use_nix" = 1 ]; then
    nix store prefetch-file --json "$1" | jq -r .hash
  else
    local f
    f=$(mktemp)
    curl -fsSL --retry 3 -o "$f" "$1"
    printf 'sha256-%s\n' "$(openssl dgst -sha256 -binary "$f" | base64 -w0)"
    rm -f "$f"
  fi
}

# save: the rewritten list, copied to the Nix overlay's.
save() {
  mv "$1" "$versions"
  cp "$versions" "$nix_versions"
}

# The architectures a binary agent is pinned for (DECISIONS I-1374): one
# versions.json slot each, named by the Nix system.
archs="x86_64-linux aarch64-linux"

# latest <agent> <system> prints "<version> <url>" for that architecture's
# Linux asset; the version is the same for both, upstream publishes them
# together. Each upstream names the architecture its own way.
latest() {
  local a=$1 sys=${2:-x86_64-linux}
  # upstream's name for the architecture: node's (x64, arm64), Rust's
  # target triple, or uname -m.
  local node rust uname
  case "$sys" in
    x86_64-linux) node=x64; rust=x86_64-unknown-linux-musl; uname=x86_64 ;;
    aarch64-linux) node=arm64; rust=aarch64-unknown-linux-musl; uname=aarch64 ;;
    *) echo "unknown system $sys" >&2; return 1 ;;
  esac
  case "$a" in
    claude-code)
      local v; v=$(curl -fsSL https://downloads.claude.ai/claude-code-releases/latest)
      echo "$v https://downloads.claude.ai/claude-code-releases/$v/linux-$node/claude" ;;
    opencode)
      # Version 2 ships on npm, not as a GitHub release asset, and the
      # GitHub "latest" release is still version 1 (DECISIONS I-1050). The
      # channel's metadata names the scope, which has moved once already
      # (@opencode-ai -> @opencode). A guest is glibc, so the plain
      # linux-x64 or linux-arm64 package is the one it wants, not
      # -baseline (no avx2) or -musl.
      local m v scope
      m=$(curl -fsSL https://opencode.ai/update/api/latest/cli/npm)
      v=$(jq -r '.version // empty' <<<"$m")
      scope=$(jq -r '.metadata.package // empty | sub("/cli$"; "")' <<<"$m")
      [ -n "$v" ] && [ -n "$scope" ] || return 0
      echo "$v https://registry.npmjs.org/$scope/cli-linux-$node/-/cli-linux-$node-$v.tgz" ;;
    codex)
      curl_gh https://api.github.com/repos/openai/codex/releases/latest \
        | jq -r --arg n "codex-$rust.tar.gz" '"\(.tag_name | ltrimstr("rust-v")) \(.assets[] | select(.name == $n) | .browser_download_url)"' ;;
    gemini-cli)
      local v; v=$(curl -fsSL https://registry.npmjs.org/@google/gemini-cli/latest | jq -r .version)
      echo "$v https://registry.npmjs.org/@google/gemini-cli/-/gemini-cli-$v.tgz" ;;
    pi-coding-agent)
      curl_gh https://api.github.com/repos/earendil-works/pi/releases/latest \
        | jq -r --arg n "pi-linux-$node.tar.gz" '"\(.tag_name | ltrimstr("v")) \(.assets[] | select(.name == $n) | .browser_download_url)"' ;;
    herdr)
      # herdr, the second multiplexer (DECISIONS I-501): not an agent, but
      # pinned and moved the same way. herdr.nix's install check refuses a
      # release with another socket protocol generation.
      curl_gh https://api.github.com/repos/herdrdev/herdr/releases/latest \
        | jq -r --arg n "herdr-linux-$uname" '"\(.tag_name | ltrimstr("v")) \(.assets[] | select(.name == $n) | .browser_download_url)"' ;;
    chrome-devtools-mcp)
      local v; v=$(curl -fsSL https://registry.npmjs.org/chrome-devtools-mcp/latest | jq -r .version)
      echo "$v https://registry.npmjs.org/chrome-devtools-mcp/-/chrome-devtools-mcp-$v.tgz" ;;
    playwright-mcp)
      # The erofs guest's only (DECISIONS I-1470); the NixOS guest's comes
      # from nixpkgs.
      local v; v=$(curl -fsSL https://registry.npmjs.org/@playwright/mcp/latest | jq -r .version)
      echo "$v https://registry.npmjs.org/@playwright/mcp/-/mcp-$v.tgz" ;;
    *) echo "unknown agent $a" >&2; return 1 ;;
  esac
}

# The versions.json keys that hold url and hash for an agent: one per
# architecture for a binary, `npm` for an architecture-neutral bundle,
# none (url and hash at the top) for chrome-devtools-mcp.
slots() {
  case "$1" in
    gemini-cli) echo npm ;;
    chrome-devtools-mcp|playwright-mcp) echo "" ;;
    *) echo "$archs" ;;
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
  tmp=$(mktemp)
  jq --arg a "$a" --arg v "$new" '.[$a].version = $v' "$versions" > "$tmp"
  save "$tmp"
  if [ -z "$(slots "$a")" ]; then
    hash=$(prefetch "$url")
    tmp=$(mktemp)
    jq --arg a "$a" --arg u "$url" --arg h "$hash" \
      '.[$a].url = $u | .[$a].hash = $h' "$versions" > "$tmp"
    save "$tmp"
  fi
  for s in $(slots "$a"); do
    if [ "$s" = npm ]; then
      surl=$url
    else
      read -r _ surl < <(latest "$a" "$s")
      if [ -z "$surl" ] || [ "$surl" = null ]; then
        echo "$a: upstream has no $s asset for $new" >&2
        exit 1
      fi
    fi
    hash=$(prefetch "$surl")
    tmp=$(mktemp)
    jq --arg a "$a" --arg s "$s" --arg u "$surl" --arg h "$hash" \
      '.[$a][$s].url = $u | .[$a][$s].hash = $h' "$versions" > "$tmp"
    save "$tmp"
    # Codex runs shell commands through a second release asset, pinned
    # beside the binary at the same version and under the same
    # architecture's slot (DECISIONS I-426, I-1374).
    if [ "$a" = codex ]; then
      hurl=${surl/\/codex-/\/codex-code-mode-host-}
      hhash=$(prefetch "$hurl")
      tmp=$(mktemp)
      jq --arg s "$s" --arg u "$hurl" --arg h "$hhash" '.codex[$s]["code-mode-host"] = {url: $u, hash: $h}' "$versions" > "$tmp"
      save "$tmp"
      # And its bundled bwrap, which it checks against a digest built into
      # the binary, so it moves with the version too (DECISIONS I-495).
      burl=${surl/\/codex-/\/bwrap-}
      bhash=$(prefetch "$burl")
      tmp=$(mktemp)
      jq --arg s "$s" --arg u "$burl" --arg h "$bhash" '.codex[$s].bwrap = {url: $u, hash: $h}' "$versions" > "$tmp"
      save "$tmp"
    fi
  done
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

if [ "$use_nix" = 0 ]; then
  echo "agents (hashes only, no Nix build): $(printf '%s, ' "${changed[@]}" | sed 's/, $//')"
  exit 0
fi

# Build what changed for this machine's architecture and prove each
# binary starts. The other architecture's package is built by ci.yml's
# aarch64 overlay job (or the x86 one, on an arm64 machine) once the
# bump is merged; the download above already checked its hash.
for c in "${changed[@]}"; do
  a=${c%% *}
  case "$a" in
    chrome-devtools-mcp) attr="chrome-devtools-mcp"; b="chrome-devtools-mcp" ;;
    herdr) attr="herdr"; b="herdr" ;;
    playwright-mcp) continue ;; # the erofs guest's; images/guest/build.sh checks it
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
