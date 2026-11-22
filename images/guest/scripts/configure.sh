#!/usr/bin/env bash
# The last step of the guest image build (images/guest/Dockerfile): stamps
# the base version, renders the machine guide and the agents' system
# config, and enables and masks units. Everything it writes is what
# nix/guest/base writes on a NixOS guest (guest-conventions.md).
#
# usage: configure.sh <base version> <git sha>
set -euo pipefail

version=$1
sha=$2

mkdir -p /etc/repose
printf '%s\n' "$version" > /etc/repose/base-version
printf '%s\n' "$sha" > /etc/repose/git-sha
echo repose-guest > /etc/hostname

# --- the machine guide (agent-guide.nix): HTML comments dropped, a line
# marked `needs: CMD` dropped when the guest has no CMD, the Playwright
# release whose Chromium the guest has in place of @playwrightVersion@.
pwv=$(cat /usr/local/share/repose/playwright-version)
in_comment=0
while IFS= read -r line || [ -n "$line" ]; do
  if [ "$in_comment" = 1 ]; then
    case "$line" in *"-->"*) in_comment=0 ;; esac
    continue
  fi
  case "$line" in
    "<!--"*) case "$line" in *"-->"*) ;; *) in_comment=1; continue ;; esac ;;
  esac
  if [[ "$line" =~ \<!--\ needs:\ ([A-Za-z0-9._-]+)\ --\> ]]; then
    command -v "${BASH_REMATCH[1]}" >/dev/null 2>&1 || continue
  fi
  printf '%s\n' "$line"
done < /usr/local/share/repose/agent-guide.md \
  | sed -E 's/[[:space:]]*<!--([^-]|-[^-])*-->//g; s/[[:space:]]+$//' \
  | sed '/./,$!d' | sed "s/@playwrightVersion@/$pwv/g" > /etc/repose/agent-guide.md
mkdir -p /etc/claude-code /etc/codex /etc/opencode /etc/repose/gemini-extension/hooks /etc/gemini-cli
cp /etc/repose/agent-guide.md /etc/claude-code/CLAUDE.md
{
  printf 'approval_policy = "never"\nsandbox_mode = "danger-full-access"\ncheck_for_update_on_startup = false\n'
  printf 'developer_instructions = '; jq -Rs . /etc/repose/agent-guide.md
  printf '\n[features]\nhooks = true\n'
  printf '\n[[hooks.UserPromptSubmit]]\n[[hooks.UserPromptSubmit.hooks]]\ntype = "command"\ncommand = "repose-hook --agent codex"\n'
} > /etc/codex/config.toml
jq -n '{ "$schema": "https://opencode.ai/config.json", instructions: [ "/etc/repose/agent-guide.md" ] }' > /etc/opencode/opencode.json
jq -n '{ name: "repose-machine-guide", version: "1.0.0", contextFileName: "GEMINI.md" }' > /etc/repose/gemini-extension/gemini-extension.json
cp /etc/repose/agent-guide.md /etc/repose/gemini-extension/GEMINI.md
jq -n '{ hooks: {
  BeforeAgent: [ { hooks: [ { type: "command", command: "repose-hook --agent gemini" } ] } ],
  AfterAgent: [ { hooks: [ { type: "command", command: "repose-hook --agent gemini" } ] } ] } }' \
  > /etc/repose/gemini-extension/hooks/hooks.json

# --- the platform MCP servers (browser.nix): Claude Code's shape in
# /etc/repose/mcp.json, Gemini CLI's system defaults beside it.
cdp=http://127.0.0.1:9224
jq -n --arg cdp "$cdp" '{
  mcpServers: {
    "chrome-devtools": {type: "stdio", command: "chrome-devtools-mcp", args: ["--browserUrl", $cdp]},
    playwright: {type: "stdio", command: "playwright-mcp", args: ["--cdp-endpoint", $cdp]}
  },
  repose_retired: {
    playwright: [{type: "stdio", command: "playwright-mcp", args: ["--headless"]}],
    "chrome-devtools": [{type: "stdio", command: "chrome-devtools-mcp", args: ["--headless"]}]
  }
}' > /etc/repose/mcp.json
jq '{
  mcpServers: (.mcpServers | map_values({command, args})),
  security: {folderTrust: {enabled: false}},
  general: {enableAutoUpdate: false, enableAutoUpdateNotification: false}
}' /etc/repose/mcp.json > /etc/gemini-cli/system-defaults.json
chmod 0644 /etc/gemini-cli/system-defaults.json /etc/repose/*.json

# --- environment, shell and sshd.
grep -v '^#' /etc/repose/session.env > /etc/environment
grep -q /etc/repose/bashrc.sh /etc/bash.bashrc || \
  printf '\n# repose (images/guest)\n[ -r /etc/repose/bashrc.sh ] && . /etc/repose/bashrc.sh\n' >> /etc/bash.bashrc
# No host keys in the image: every guest would share them. The real key
# and certificate arrive from hostd (ssh.nix, DECISIONS I-35).
rm -f /etc/ssh/ssh_host_*
ln -s /run/repose/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key
ln -s /run/repose/ssh_host_ed25519_key-cert.pub /etc/ssh/ssh_host_ed25519_key-cert.pub
ln -s /run/repose/user_ca.pub /etc/ssh/user_ca.pub
chmod 0755 /usr/local/libexec/repose/* /usr/local/bin/*

# --- units. ssh.socket is Ubuntu's default; the guest runs sshd as a
# service that guestd reloads (ssh.nix).
systemctl disable ssh.socket 2>/dev/null || true
systemctl mask ssh.socket
systemctl enable ssh.service
systemctl enable \
  guestd.service \
  repose-claude-auth.service \
  repose-zram-swap.service \
  repose-trim-on-stop.service \
  repose-tmp-rotate.service \
  repose-tmp-purge.timer \
  repose-tmux-save.timer \
  repose-browser.socket \
  repose-novnc.socket \
  repose-desktop-idle-check.timer \
  repose-playwright-seed.service \
  fstrim.timer \
  repose-docker-start.service docker.socket \
  systemd-networkd.service systemd-resolved.service systemd-network-generator.service
systemctl disable docker.service containerd.service
systemctl --global enable repose-agent-hooks.service repose-npm-registry.service repose-tools-carry.service
# Nothing a guest needs at boot, or anything that waits for a network the
# guest has from its first instruction (network.nix, boot.nix I-231).
systemctl mask \
  systemd-networkd-wait-online.service \
  apt-daily.timer apt-daily-upgrade.timer motd-news.timer dpkg-db-backup.timer \
  e2scrub_all.timer e2scrub_reap.service \
  sys-kernel-debug.mount sys-kernel-tracing.mount sys-kernel-config.mount \
  sys-fs-fuse-connections.mount dev-hugepages.mount \
  systemd-timesyncd.service \
  getty@tty1.service \
  systemd-machine-id-commit.service \
  ldconfig.service systemd-update-done.service 2>/dev/null || true
systemctl set-default multi-user.target
# The serial console's login prompt stays (serial-getty@ttyS0/ttyAMA0
# come from the console= argument).

# /run/current-system/sw/bin (the system profile a NixOS guest's tools,
# guestd included, name): every command in /usr/local/bin, /usr/bin and
# /usr/sbin, as links (tmpfiles.d/repose.conf links the directory).
sw=/usr/local/lib/repose/sw
mkdir -p "$sw/bin"
for d in /usr/sbin /usr/bin /usr/local/bin; do
  for f in "$d"/*; do
    [ -e "$sw/bin/${f##*/}" ] || ln -s "$f" "$sw/bin/${f##*/}"
  done
done
ln -sfn /usr/share "$sw/share"

# The packages the base installed by name; repose-apt-manual lists the
# ones installed since, for guestd to replay after a base change.
apt-mark showmanual | sort > /usr/local/share/repose/apt-manual.base

# dev's user manager runs from boot (linger).
mkdir -p /var/lib/systemd/linger
touch /var/lib/systemd/linger/dev

# Predictable interface names off: the one NIC is eth0 (network.nix).
ln -sf /dev/null /etc/systemd/network/99-default.link

# /home/dev in the image is the mount point; its content comes from
# /etc/skel on the volume's first boot (initramfs/init).
rm -rf /home/dev && mkdir -p /home/dev && chown 1000:1000 /home/dev && chmod 0750 /home/dev
mkdir -p /var/lib/repose/services /var/lib/docker
chmod 0440 /etc/sudoers.d/repose
visudo -cf /etc/sudoers.d/repose >/dev/null
