#!/usr/bin/env bash
# Installs the five agents, the two browser MCP servers and herdr from the
# URLs and SRI hashes in versions.json, inside the guest image build
# (images/guest/Dockerfile). Each download is checked against its hash
# before anything is unpacked; a mismatch fails the build. The layout and
# the environment each one runs with follow nix/overlay/agents/*.nix, and
# the wrappers follow nix/overlay/agents/wrap.nix (guest-conventions.md
# "Agent wrappers").
#
# usage: install-agents.sh amd64|arm64 versions.json
set -euo pipefail

arch=$1
versions=$2
case "$arch" in
  amd64) sys=x86_64-linux; rust=x86_64-unknown-linux-musl ;;
  arm64) sys=aarch64-linux; rust=aarch64-unknown-linux-musl ;;
  *) echo "install-agents: unknown arch $arch" >&2; exit 64 ;;
esac

opt=/opt/repose
lib=/usr/local/libexec/repose/agents
bin=/usr/local/bin
dl=$(mktemp -d)
mkdir -p "$opt" "$lib"

v() { jq -r "$1" "$versions"; }

# fetch <url> <sri> <dest>: download and check the sha256 SRI hash.
fetch() {
  local url=$1 sri=$2 dest=$3 got
  curl -fsSL --retry 3 -o "$dest" "$url"
  got="sha256-$(openssl dgst -sha256 -binary "$dest" | base64 -w0)"
  if [ "$got" != "$sri" ]; then
    echo "install-agents: $url: hash $got, versions.json says $sri" >&2
    exit 1
  fi
}

# wrap <bin> <real>: the repose wrapper (wrap.nix) at /usr/local/bin/<bin>.
wrap() {
  local name=$1 real=$2 extra=""
  if [ "$name" = claude ]; then
    extra='if command -v repose-remote-control >/dev/null 2>&1; then
  ( repose-remote-control >/dev/null 2>&1 & ) || true
fi'
  fi
  if [ "$name" = codex ]; then
    extra=$(cat "$(dirname "$0")/codex-daemon.sh")
  fi
  cat > "$bin/$name" <<EOF
#!/usr/bin/env bash
# repose wrapper for $name (guest-conventions.md "Agent wrappers").
if [ -n "\${TMUX:-}" ]; then
  export TERM=tmux-256color
fi
export COLORTERM=truecolor
if [ -r /etc/profile.d/repose.sh ]; then
  . /etc/profile.d/repose.sh
fi
export REPOSE_HOOK_AGENT=$name
export HERDR_AGENT=$name
/usr/local/bin/repose-agent-setup $name || true
$extra
. /etc/repose/devshell.sh
_repose_devshell $name
unset -f _repose_devshell _repose_devshell_done _repose_devshell_flake \\
  _repose_devshell_flake_root _repose_devshell_shadow _repose_devshell_prompt
exec $real "\$@"
EOF
  chmod 0755 "$bin/$name"
}

# --- Claude Code: the release binary; no self-update, no install checks,
# the system ripgrep (claude-code.nix).
d=$opt/claude-code; mkdir -p "$d"
fetch "$(v ".\"claude-code\".\"$sys\".url")" "$(v ".\"claude-code\".\"$sys\".hash")" "$d/claude"
chmod 0755 "$d/claude"
cat > "$lib/claude" <<'EOF'
#!/bin/sh
export DISABLE_AUTOUPDATER=1 DISABLE_INSTALLATION_CHECKS=1 USE_BUILTIN_RIPGREP=0
exec /opt/repose/claude-code/claude "$@"
EOF

# --- opencode 2: the per-platform npm package's binary (opencode.nix).
major=$(v '.opencode.version' | cut -d. -f1)
[ "$major" = 2 ] || { echo "install-agents: opencode left version 2's line (DECISIONS I-1050)" >&2; exit 1; }
fetch "$(v ".opencode.\"$sys\".url")" "$(v ".opencode.\"$sys\".hash")" "$dl/opencode.tgz"
mkdir -p "$opt/opencode" && tar -xzf "$dl/opencode.tgz" -C "$dl" package/bin/opencode
install -m 0755 "$dl/package/bin/opencode" "$opt/opencode/opencode"
cat > "$lib/opencode" <<'EOF'
#!/bin/sh
export OPENCODE_DISABLE_AUTOUPDATE=1
exec /opt/repose/opencode/opencode "$@"
EOF

# --- Codex: a complete package layout, the real binary at bin/codex
# (codex.nix, DECISIONS I-487, I-495).
d=$opt/codex; mkdir -p "$d/bin" "$d/codex-path" "$d/codex-resources"
fetch "$(v ".codex.\"$sys\".url")" "$(v ".codex.\"$sys\".hash")" "$dl/codex.tgz"
fetch "$(v ".codex.\"$sys\".\"code-mode-host\".url")" "$(v ".codex.\"$sys\".\"code-mode-host\".hash")" "$dl/host.tgz"
fetch "$(v ".codex.\"$sys\".bwrap.url")" "$(v ".codex.\"$sys\".bwrap.hash")" "$dl/bwrap.tgz"
tar -xzf "$dl/codex.tgz" -C "$dl" "codex-$rust"
tar -xzf "$dl/host.tgz" -C "$dl" "codex-code-mode-host-$rust"
tar -xzf "$dl/bwrap.tgz" -C "$dl" "bwrap-$rust"
install -m 0755 "$dl/codex-$rust" "$d/bin/codex"
install -m 0755 "$dl/codex-code-mode-host-$rust" "$d/bin/codex-code-mode-host"
install -m 0755 "$dl/bwrap-$rust" "$d/codex-resources/bwrap"
install -m 0755 /usr/bin/rg "$d/codex-path/rg"
jq -n --arg v "$(v .codex.version)" --arg t "$rust" \
  '{layoutVersion: 1, version: $v, target: $t, variant: "codex", entrypoint: "bin/codex", resourcesDir: "codex-resources", pathDir: "codex-path"}' \
  > "$d/codex-package.json"

# --- Gemini CLI: the npm bundle on the system node (gemini-cli.nix).
d=$opt/gemini-cli/lib/node_modules/@google/gemini-cli; mkdir -p "$d"
fetch "$(v '."gemini-cli".npm.url')" "$(v '."gemini-cli".npm.hash')" "$dl/gemini.tgz"
tar -xzf "$dl/gemini.tgz" -C "$d" --strip-components=1
entry=$(jq -r '.bin | if type == "object" then .gemini else . end' "$d/package.json")
entry=${entry#./}
[ -f "$d/$entry" ] || { echo "install-agents: gemini-cli bin names [$entry], which the tarball does not ship" >&2; exit 1; }
cat > "$lib/gemini" <<EOF
#!/bin/sh
exec /usr/bin/node $d/$entry "\$@"
EOF

# --- pi: the whole release tree, bin/pi a wrapper into it (pi-coding-agent.nix).
d=$opt/pi; mkdir -p "$d"
fetch "$(v ".\"pi-coding-agent\".\"$sys\".url")" "$(v ".\"pi-coding-agent\".\"$sys\".hash")" "$dl/pi.tgz"
mkdir -p "$dl/pi" && tar -xzf "$dl/pi.tgz" -C "$dl/pi"
src=$(dirname "$(find "$dl/pi" -name pi -type f -perm -u+x | head -n1)")
cp -a "$src/." "$d/"
chmod 0755 "$d/pi"
cat > "$lib/pi" <<'EOF'
#!/bin/sh
export PI_SKIP_VERSION_CHECK=1
exec /opt/repose/pi/pi "$@"
EOF

chmod 0755 "$lib"/*

wrap claude "$lib/claude"
wrap opencode "$lib/opencode"
wrap codex "$opt/codex/bin/codex"
wrap gemini "$lib/gemini"
wrap pi "$lib/pi"

# --- herdr: the static release binary, refused unless it speaks
# endpoint generation 1 and protocol 22 or later (herdr.nix, I-501).
fetch "$(v ".herdr.\"$sys\".url")" "$(v ".herdr.\"$sys\".hash")" "$bin/herdr"
chmod 0755 "$bin/herdr"
out=$(HOME=$(mktemp -d) "$bin/herdr" status client --json)
gen=$(jq -r '.endpoint_protocol_generation // "missing"' <<<"$out")
proto=$(jq -r '.protocol // 0' <<<"$out")
if [ "$gen" != 1 ] || [ "$proto" -lt 22 ]; then
  echo "install-agents: herdr endpoint_protocol_generation $gen, protocol $proto; repose needs 1 and 22 or later" >&2
  exit 1
fi

# --- playwright-mcp: the verified tarball, installed with its pinned
# Playwright; that Playwright installs the Chromium the agents' browser
# runs (the Ubuntu chromium is a snap).
fetch "$(v '."playwright-mcp".url')" "$(v '."playwright-mcp".hash')" "$dl/playwright-mcp.tgz"
npm install --global --prefix "$opt/playwright-mcp" --no-fund --no-audit "$dl/playwright-mcp.tgz" >/dev/null
pw=$opt/playwright-mcp/lib/node_modules/@playwright/mcp
export PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
node "$pw/node_modules/playwright/cli.js" install --with-deps chromium
chrome=$(find /opt/ms-playwright -path '*chromium-*' -type f -name chrome -perm -u+x | head -n1)
[ -x "$chrome" ] || { echo "install-agents: Playwright installed no chromium" >&2; exit 1; }
ln -sfn "$chrome" /usr/local/libexec/repose/chromium
jq -r .version "$pw/node_modules/playwright/package.json" > /usr/local/share/repose/playwright-version
chmod -R a+rX /opt/ms-playwright
# The machine's chromium on PATH runs in the user's browser slice
# (browser.nix `scoped`).
cat > "$bin/chromium" <<'EOF'
#!/bin/sh
exec /usr/local/bin/repose-browser-scope /usr/local/libexec/repose/chromium "$@"
EOF
cat > "$lib/playwright-mcp" <<EOF
#!/bin/bash
# With --cdp-endpoint the server uses the shared browser's default context
# (I-246); without one it is isolated, as nixpkgs's wrapper does.
case " \$* " in
  *" --cdp-endpoint"*) ;;
  *) if [ -z "\${PLAYWRIGHT_MCP_USER_DATA_DIR:-}" ] && [ -z "\${PLAYWRIGHT_MCP_CDP_ENDPOINT:-}" ]; then export PLAYWRIGHT_MCP_ISOLATED=1; fi ;;
esac
export PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
exec /usr/bin/node $pw/cli.js "\$@"
EOF

# --- chrome-devtools-mcp: the bundled npm tree (chrome-devtools-mcp.nix).
d=$opt/chrome-devtools-mcp/lib/node_modules/chrome-devtools-mcp; mkdir -p "$d"
fetch "$(v '."chrome-devtools-mcp".url')" "$(v '."chrome-devtools-mcp".hash')" "$dl/cdm.tgz"
tar -xzf "$dl/cdm.tgz" -C "$d" --strip-components=1
entry=$(jq -r '.bin | if type == "object" then ."chrome-devtools-mcp" else . end' "$d/package.json")
entry=${entry#./}
[ -f "$d/$entry" ] || { echo "install-agents: chrome-devtools-mcp bin names [$entry], which the tarball does not ship" >&2; exit 1; }
cat > "$lib/chrome-devtools-mcp" <<EOF
#!/bin/bash
export CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS=\${CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS-1}
extra=()
case " \$* " in
  *" --browserUrl"*|*" --browser-url"*|*" -u "*|*" --wsEndpoint"*|*" --ws-endpoint"*|*" -w "*|*" --autoConnect"*|*" --auto-connect"*|*" --executablePath"*|*" --executable-path"*|*" -e "*) ;;
  *) extra+=(--executablePath /usr/local/libexec/repose/chromium) ;;
esac
case " \$* " in
  *"erformanceCrux"*|*"erformance-crux"*) ;;
  *) extra+=(--no-performance-crux) ;;
esac
exec /usr/bin/node $d/$entry "\${extra[@]}" "\$@"
EOF
chmod 0755 "$lib"/*
for m in playwright-mcp chrome-devtools-mcp; do
  printf '#!/bin/sh\nexec /usr/local/bin/repose-browser-scope %s/%s "$@"\n' "$lib" "$m" > "$bin/$m"
  chmod 0755 "$bin/$m"
done

# /etc/repose/agents.json for `repose status --verbose` (agents.nix).
mkdir -p /etc/repose
jq '{
  "claude-code": {binary: "claude", version: ."claude-code".version, hook: "settings.json hooks Notification+Stop+UserPromptSubmit+SessionStart+SessionEnd"},
  "codex": {binary: "codex", version: .codex.version, hook: "config.toml notify, /etc/codex/config.toml hooks UserPromptSubmit"},
  "gemini-cli": {binary: "gemini", version: ."gemini-cli".version, hook: "extension hooks BeforeAgent+AfterAgent"},
  "opencode": {binary: "opencode", version: .opencode.version, hook: "plugin ~/.config/opencode/plugins/repose.js"},
  "pi-coding-agent": {binary: "pi", version: ."pi-coding-agent".version, hook: "extension events input+agent_end"}
}' "$versions" > /etc/repose/agents.json

rm -rf "$dl"
