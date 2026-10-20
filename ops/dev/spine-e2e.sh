#!/usr/bin/env bash
# The wrapper-only spine, end to end, against a throwaway Debian box.
#
# This branch (wrapper-spine, DECISIONS I-812) is parked and not in the
# release queue, so months can pass between sessions. This script is how
# the next one proves the thing still works without rebuilding the test
# rig by hand.
#
#   ops/dev/spine-e2e.sh up      build a throwaway box and install repose on it
#   ops/dev/spine-e2e.sh run     a dirty checkout, a run, a wreck, a rewind
#   ops/dev/spine-e2e.sh down    remove the box, the image and the scratch
#   ops/dev/spine-e2e.sh all     up, run, down
#
# Against a box you already have, which needs no docker and no disk here:
#   SPINE_E2E_SSH=me@192.168.1.40 SPINE_E2E_KEY=~/.ssh/id_ed25519 \
#     ops/dev/spine-e2e.sh up
# `down` then removes only this script's scratch, never your box.
#
# DISK. The docker path writes the image and the container's layer to
# docker's data root, which on the dev box is /var/lib/docker on /, and / has
# a few gigabytes free. A full root disk breaks every other session on this
# machine, so `up` refuses below MIN_ROOT_MB rather than filling it (it did
# fill it once, 2026-10-20). The box's own big paths -- /opt/repose and
# /var/lib/repose, where the coding agents and the undo store go -- are bound
# to /mnt/nixstore, the data disk, so they are not the problem; the image and
# its apt layer are.
set -euo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
# Free megabytes `up` needs where docker keeps its data, measured from a run
# that needed about 1.5 GB for the image and the apt layer.
MIN_ROOT_MB=${SPINE_E2E_MIN_ROOT_MB:-3000}
BOX=spine-e2e-box          # the image
NAME=spine-box             # the container
PORT=2299
DATA=${SPINE_E2E_DATA:-/mnt/nixstore/spine-e2e}
# Short, because a ControlPath is a unix socket and 108 bytes is the limit.
WORK=${SPINE_E2E_WORK:-/tmp/spine-e2e}
HTTP_PORT=8899

say() { printf '\n== %s\n' "$*" >&2; }
die() { printf 'spine-e2e: %s\n' "$*" >&2; exit 1; }

# SPINE_E2E_SSH names a box you already have; otherwise docker makes one.
EXTERNAL=${SPINE_E2E_SSH:-}
KEY=${SPINE_E2E_KEY:-}

box_target() {
  if [ -n "$EXTERNAL" ]; then
    printf '%s' "$EXTERNAL"
  else
    printf 'me@127.0.0.1'
  fi
}

box_ssh() {
  if [ -n "$EXTERNAL" ]; then
    ssh ${KEY:+-i "$KEY"} -o BatchMode=yes -o LogLevel=ERROR "$EXTERNAL" "$@"
  else
    ssh -i "$WORK/key" -p "$PORT" -o BatchMode=yes -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR me@127.0.0.1 "$@"
  fi
}

# check_disk refuses rather than filling a disk other sessions share.
check_disk() {
  root=$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || echo /var/lib/docker)
  free_mb=$(df -Pm "$root" | awk 'NR==2{print $4}')
  if [ "${free_mb:-0}" -lt "$MIN_ROOT_MB" ]; then
    die "docker keeps its data in $root, which has ${free_mb} MB free and needs ${MIN_ROOT_MB}.
  A full disk here breaks every other session on this machine. Either free space
  (\`docker builder prune\`, \`docker image prune\`) or point this at a box you
  already have: SPINE_E2E_SSH=me@host SPINE_E2E_KEY=~/.ssh/id_ed25519 $0 up"
  fi
}

build_binaries() {
  say "building repose and repose-agent"
  mkdir -p "$WORK"
  ( cd "$REPO" && TMPDIR=${TMPDIR:-/tmp/rt} go build -p=2 -o "$WORK/repose" ./cmd/repose )
  ( cd "$REPO" && TMPDIR=${TMPDIR:-/tmp/rt} GOOS=linux GOARCH=amd64 \
      go build -p=2 -o "$WORK/repose-agent" ./cmd/repose-agent )
}

up() {
  mkdir -p "$WORK"
  build_binaries

  if [ -n "$EXTERNAL" ]; then
    say "using the box you named: $EXTERNAL"
    box_ssh true || die "cannot ssh to $EXTERNAL"
  else
    command -v docker >/dev/null || die "docker is not on PATH; or name a box with SPINE_E2E_SSH"
    check_disk
    mkdir -p "$DATA/opt" "$DATA/var"
    make_box
  fi
  install_on_box
}

make_box() {
  if [ ! -f "$WORK/key" ]; then
    ssh-keygen -t ed25519 -N '' -f "$WORK/key" -C spine-e2e -q
  fi

  say "building the box image"
  # systemd as pid 1: the installer refuses a box without it, and it is
  # what mounts the undo store and runs the gc timer.
  cat > "$WORK/Dockerfile" <<'EOF'
FROM debian:12-slim
ENV container=docker
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus openssh-server sudo ca-certificates curl jq git tmux rsync python3 \
      btrfs-progs procps psmisc iproute2 \
    && rm -rf /var/lib/apt/lists/* \
    && systemctl enable ssh
RUN useradd -m -s /bin/bash me && echo 'me ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/me \
    && mkdir -p /home/me/.ssh && chmod 700 /home/me/.ssh
COPY key.pub /home/me/.ssh/authorized_keys
RUN chown -R me:me /home/me/.ssh && chmod 600 /home/me/.ssh/authorized_keys
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
EOF
  docker build -q -t "$BOX" "$WORK" >/dev/null

  say "starting the box on port $PORT"
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker run -d --name "$NAME" --privileged --cgroupns=host \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock \
    -v "$DATA/opt:/opt/repose" -v "$DATA/var:/var/lib/repose" \
    -p "$PORT:22" "$BOX" >/dev/null
  for _ in $(seq 30); do
    box_ssh true 2>/dev/null && break
    sleep 1
  done
  box_ssh true 2>/dev/null || die "the box did not answer ssh on $PORT"
}

install_on_box() {
  # The box pulls repose-agent over http: there is no release asset for it
  # yet, so `machine add` needs --agent-url with its hash (SPINE.md "What
  # is not done").
  say "serving repose-agent to the box"
  mkdir -p "$WORK/serve" && cp "$WORK/repose-agent" "$WORK/serve/"
  ( cd "$WORK/serve" && nohup python3 -m http.server "$HTTP_PORT" --bind 0.0.0.0 >/dev/null 2>&1 & )
  sleep 1
  if [ -n "$EXTERNAL" ]; then
    # The box is elsewhere, so it needs an address of this machine it can
    # reach. On the dev box that is its Tailscale address.
    HOSTIP=${SPINE_E2E_HOSTIP:-$(tailscale ip -4 2>/dev/null | head -1)}
    [ -n "$HOSTIP" ] || die "set SPINE_E2E_HOSTIP to an address of this machine that $EXTERNAL can reach"
  else
    HOSTIP=$(ip -4 addr show docker0 | awk '/inet /{print $2}' | cut -d/ -f1)
  fi
  SHA=$(sha256sum "$WORK/repose-agent" | cut -d' ' -f1)
  box_ssh "curl -fsS -o /dev/null http://$HOSTIP:$HTTP_PORT/repose-agent" ||
    die "the box cannot reach the http server at $HOSTIP:$HTTP_PORT"

  say "repose machine add"
  rm -rf "$WORK/home" && mkdir -p "$WORK/home/.ssh"
  cp "$WORK/key" "$WORK/home/.ssh/spine-key" && chmod 600 "$WORK/home/.ssh/spine-key"
  if [ -n "$EXTERNAL" ]; then
    HOME="$WORK/home" "$WORK/repose" machine add spinebox \
      --ssh "$EXTERNAL" ${KEY:+--key "$KEY"} --undo-gb 4 \
      --agent-url "http://$HOSTIP:$HTTP_PORT/repose-agent" --agent-sha256 "$SHA"
  else
    HOME="$WORK/home" "$WORK/repose" machine add spinebox \
      --ssh "me@127.0.0.1:$PORT" --key "$WORK/home/.ssh/spine-key" --undo-gb 4 \
      --agent-url "http://$HOSTIP:$HTTP_PORT/repose-agent" --agent-sha256 "$SHA"
  fi
  HOME="$WORK/home" "$WORK/repose" machine ls
}

run() {
  [ -x "$WORK/repose" ] || die "run \`$0 up\` first"
  export HOME="$WORK/home"
  # A fresh project each time. The box keeps the last run's checkout, and
  # a `repose run` whose checkout shares no commit with it is refused --
  # correctly, since it would lay one repository over another's machine --
  # so a repeatable harness cannot reuse the name.
  PROJ=e2e-$(date +%H%M%S)
  P=$WORK/$PROJ
  say "a checkout with work that is not committed"
  rm -rf "$P" && mkdir -p "$P/src" && cd "$P"
  git init -q -b main . && git config user.email a@b && git config user.name c
  printf 'export const auth = false;\n' > src/auth.ts
  printf 'node_modules/\n' > .gitignore
  git add -A && git commit -qm "the committed state"
  printf 'export const auth = true; // half-finished\n' > src/auth.ts   # modified
  printf 'console.log("scratch");\n' > src/scratch.ts                   # untracked
  printf '{"records":[1,2,3]}\n' > app.db                               # the dev database
  printf 'DATABASE_URL=postgres://localhost/dev\n' > .env                # gitignored, and the app needs it
  printf 'node_modules/\n.env\n' > .gitignore
  git status --short

  # Stand-ins for the two things a throwaway box and a throwaway HOME do
  # not have, because the run path is what is under test and not Claude's
  # login or GitHub's:
  #  - a claude that behaves like an agent that starts and stays up;
  #  - the credentials file `run` tests for before it types a prompt.
  box_ssh 'printf "#!/bin/sh\necho \"claude: \$*\"\nexec sleep 3600\n" | sudo tee /opt/repose/bin/claude >/dev/null; sudo chmod 0755 /opt/repose/bin/claude'
  box_ssh 'mkdir -p ~/.claude && printf "{\"stand-in\":true}\n" > ~/.claude/.credentials.json'
  # And a gh login on the laptop side, so the carry's GitHub path is
  # exercised: without it the agent cannot push, which the audit rated the
  # gap most likely to lose a user.
  mkdir -p "$HOME/.config/gh"
  printf 'github.com:\n    oauth_token: gho_standin\n    user: e2e\n    git_protocol: https\n' \
    > "$HOME/.config/gh/hosts.yml"
  git config --global user.email e2e@example.com 2>/dev/null || true
  git config --global user.name "End To End" 2>/dev/null || true

  say "repose run --machine spinebox"
  "$WORK/repose" run "$PROJ" --machine spinebox -d -p "say hello"

  say "what the box holds"
  box_ssh "cd ~/$PROJ && find . -path ./.git -prune -o -type f -print | sort && cat app.db"

  say "an agent wrecks it"
  box_ssh "cd ~/$PROJ && rm -rf src && printf 'CORRUPT\n' > app.db && rm -f .gitignore && find . -path ./.git -prune -o -type f -print | wc -l"

  say "repose rewind --list"
  "$WORK/repose" rewind --list

  say "repose rewind --yes"
  "$WORK/repose" rewind --yes

  say "what came back"
  box_ssh "cd ~/$PROJ && find . -path ./.git -prune -o -type f -print | sort && cat app.db && git status --short"

  say "checks"
  n=$(box_ssh "cd ~/$PROJ && find . -path ./.git -prune -o -type f -print | wc -l")
  [ "$n" = 5 ] || die "$n files after the rewind, want 5 (the four plus .env)"
  db=$(box_ssh "cat ~/$PROJ/app.db")
  [ "$db" = '{"records":[1,2,3]}' ] || die "the database came back as $db"

  # The audit's finding: a sync that lands the files but no credentials
  # leaves an agent that cannot commit, cannot push and cannot start the
  # app. These are the checks for that, not for the files.
  say "can the agent actually work?"
  env_line=$(box_ssh "cat ~/$PROJ/.env 2>/dev/null || true")
  [ "$env_line" = 'DATABASE_URL=postgres://localhost/dev' ] ||
    die "the gitignored .env did not arrive: got '$env_line'"
  echo "  .env arrived: $env_line"
  ident=$(box_ssh "cd ~/$PROJ && git config user.email || true")
  [ -n "$ident" ] || die "no git identity on the box: the agent cannot commit"
  echo "  git identity on the box: $ident"
  box_ssh 'test -s ~/.config/gh/hosts.yml' ||
    die "the gh login did not arrive: the agent cannot push or open a PR"
  echo "  gh login arrived, and git is told to use it:"
  box_ssh 'git config --get-regexp "credential|insteadOf" | sed "s/^/    /" || true'
  box_ssh "cd ~/$PROJ && printf 'x\n' >> src/auth.ts && git -c core.pager=cat commit -qam 'the agent commits' && git log --oneline -1" ||
    die "the agent could not commit on the box"

  say "a point is taken by sync too, and the safety point is its own reason"
  "$WORK/repose" sync >/dev/null 2>&1 || die "repose sync failed on an owned machine"
  box_ssh "/opt/repose/bin/repose-agent point list --project $PROJ" |
    grep -q '"reason":"rewind"' || die "the rewind's safety point is not reason=rewind"
  echo "  the safety point is reason=rewind, so a second rewind cannot restore the wreck"

  say "what is running, read from the box"
  "$WORK/repose" ps 2>&1 | tail -3

  say "PASS: the files, the database, the .env, the git identity, sync and ps"
}

down() {
  if [ -n "$EXTERNAL" ]; then
    say "leaving $EXTERNAL alone; removing this script's scratch only"
  else
    say "removing the box"
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    docker rmi "$BOX" >/dev/null 2>&1 || true
    docker builder prune -f >/dev/null 2>&1 || true
  fi
  pid=$(pgrep -f "http[.]server $HTTP_PORT" | head -1 || true)
  [ -n "$pid" ] && kill "$pid" || true
  [ -n "$EXTERNAL" ] || sudo rm -rf "$DATA"
  rm -rf "$WORK"
  say "done; /dev/loop devices and /mnt/nixstore are untouched otherwise"
}

case "${1:-}" in
  up) up ;;
  run) run ;;
  down) down ;;
  all) up; run; down ;;
  *) die "usage: $0 up|run|down|all" ;;
esac
