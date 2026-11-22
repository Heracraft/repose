# syntax=docker/dockerfile:1.7
#
# The erofs guest (DECISIONS I-1466): Ubuntu 24.04, flattened by build.sh
# into rootfs.erofs, with the kernel and initramfs built from the same
# kernel package. docs/interfaces/guest-conventions.md is the contract;
# "The erofs guest" there has the disk layout.
#
# Targets:
#   rootfs  the root filesystem, exported as a tar and made into erofs
#   bundle  vmlinuz (bzImage on amd64, plain Image on arm64), initramfs.img
#           and kernel-version
#
# Named build contexts build.sh passes:
#   bin     guestd and repose-hook, CGO_ENABLED=0, for TARGETARCH
#   viewer  nix/guest/base/desktop/viewer (the desktop viewer page)

ARG UBUNTU=ubuntu:24.04

FROM ${UBUNTU} AS base
ARG TARGETARCH
ARG BASE_VERSION=dev
ARG GIT_SHA=unknown
ARG GO_VERSION=1.26.11
ENV DEBIAN_FRONTEND=noninteractive LANG=C.UTF-8

# Fixed ids first (proposal §2.3 #3): the stock ubuntu user holds 1000,
# and docker's group must be 999 before any package takes it.
RUN userdel -r ubuntu 2>/dev/null || true; \
    groupadd --system --gid 999 docker && \
    groupadd --gid 1000 dev && \
    groupadd --system wheel && \
    useradd --uid 1000 --gid 1000 --create-home --home-dir /home/dev --shell /bin/bash dev && \
    passwd -l root && passwd -l dev

# Ubuntu's minimal image drops man pages and docs; keep man pages (I-534).
RUN rm -f /etc/dpkg/dpkg.cfg.d/excludes

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-${TARGETARCH} \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=aptlists-${TARGETARCH} \
    rm -f /etc/apt/apt.conf.d/docker-clean && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      systemd systemd-sysv systemd-resolved systemd-timesyncd dbus-broker udev \
      libpam-systemd libnss-systemd dbus-user-session \
      linux-image-virtual \
      openssh-server sudo nftables iproute2 iputils-ping ca-certificates locales \
      util-linux e2fsprogs kmod procps psmisc less man-db file lsof \
      curl wget gnupg jq ripgrep fd-find bat fzf tree unzip zip zstd xz-utils htop \
      git git-lfs just tmux eza zoxide direnv neovim bash-completion \
      build-essential pkg-config cmake \
      dnsutils sqlite3 postgresql-client openssl pinentry-curses \
      python3 python3-venv python3-pip python3-dev rustup \
      libssl-dev zlib1g-dev libsqlite3-dev libffi-dev libyaml-dev libpq-dev \
      libxml2-dev libxslt1-dev libmysqlclient-dev \
      bubblewrap socat libasound2t64 \
      ncurses-term kitty-terminfo command-not-found \
      tigervnc-standalone-server tigervnc-tools openbox novnc \
      fonts-noto-core fonts-noto-mono fonts-noto-cjk fonts-noto-color-emoji fonts-liberation \
      busybox-static cpio && \
    apt-get install -y --no-install-recommends "linux-modules-extra-$(ls /lib/modules)"

# Third-party apt repositories: Docker, NodeSource (Node.js 24), GitHub
# CLI, Cloudflare (cloudflared), mise.
RUN install -d -m 0755 /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc && \
    echo "deb [signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable" > /etc/apt/sources.list.d/docker.list && \
    curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_24.x nodistro main" > /etc/apt/sources.list.d/nodesource.list && \
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/githubcli.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list && \
    curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg -o /etc/apt/keyrings/cloudflare.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/cloudflare.gpg] https://pkg.cloudflare.com/cloudflared noble main" > /etc/apt/sources.list.d/cloudflared.list && \
    curl -fsSL https://mise.jdx.dev/gpg-key.pub | gpg --dearmor -o /etc/apt/keyrings/mise.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/mise.gpg] https://mise.jdx.dev/deb stable main" > /etc/apt/sources.list.d/mise.list

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-${TARGETARCH} \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=aptlists-${TARGETARCH} \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      docker-ce docker-ce-cli containerd.io docker-compose-plugin docker-buildx-plugin \
      nodejs gh cloudflared mise && \
    getent group docker | grep -q ':999:' && \
    usermod -aG sudo,wheel,docker,kvm dev

# Go from go.dev (noble's is 1.22), uv and starship from their releases.
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz && \
    ln -s /usr/local/go/bin/go /usr/local/bin/go && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt && \
    curl -fsSL https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin UV_NO_MODIFY_PATH=1 sh && \
    curl -fsSL https://starship.rs/install.sh | sh -s -- --yes --bin-dir /usr/local/bin >/dev/null && \
    npm install -g pnpm && corepack enable yarn && \
    python3 -m venv /opt/repose/websockify && \
    /opt/repose/websockify/bin/pip install --no-cache-dir --quiet websockify==0.13.0 && \
    ln -s /usr/bin/fdfind /usr/local/bin/fd && ln -s /usr/bin/batcat /usr/local/bin/bat && \
    update-alternatives --set editor /usr/bin/nvim && \
    update-alternatives --install /usr/bin/vi vi /usr/bin/nvim 100 && \
    update-alternatives --install /usr/bin/vim vim /usr/bin/nvim 100

# The agents, the two MCP servers and herdr, each verified against its SRI
# hash in versions.json; Playwright's Chromium for the agents' browser.
COPY versions.json /usr/local/share/repose/versions.json
COPY scripts/install-agents.sh scripts/codex-daemon.sh /tmp/
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-${TARGETARCH} \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=aptlists-${TARGETARCH} \
    bash /tmp/install-agents.sh "${TARGETARCH}" /usr/local/share/repose/versions.json && rm /tmp/install-agents.sh /tmp/codex-daemon.sh

COPY --from=bin --chmod=0755 guestd repose-hook /usr/local/bin/
RUN for n in repose-notify repose-ask repose-mcp repose-presence; do ln -s repose-hook "/usr/local/bin/$n"; done

COPY --from=viewer . /usr/local/share/repose/desktop-viewer/
RUN cp -r /usr/share/novnc/core /usr/share/novnc/vendor /usr/local/share/repose/desktop-viewer/

COPY rootfs/ /
COPY agent-guide.md /usr/local/share/repose/agent-guide.md
COPY scripts/configure.sh /tmp/configure.sh
RUN bash /tmp/configure.sh "${BASE_VERSION}" "${GIT_SHA}" && rm /tmp/configure.sh

# --- the kernel and initramfs, from the kernel package installed above
FROM base AS bundle-build
COPY initramfs/init /tmp/init
COPY scripts/make-bundle.sh /tmp/make-bundle.sh
RUN bash /tmp/make-bundle.sh /tmp/init /out

FROM scratch AS bundle
COPY --from=bundle-build /out/ /

# --- the root filesystem: no kernel image in it, an empty machine-id, and
# no build caches
FROM base AS rootfs
RUN rm -f /boot/vmlinuz* /boot/initrd.img* /boot/System.map* && \
    rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/log/apt/* /tmp/* /root/.cache /root/.npm && \
    : > /etc/machine-id && rm -f /var/lib/dbus/machine-id
# The unit-path gate (DECISIONS I-1554): every /usr/local path a unit's
# Exec* line names is in this root, executable. The script is bind-mounted,
# so it leaves nothing in the image.
RUN --mount=type=bind,source=scripts/unit-paths-gate.py,target=/tmp/unit-paths-gate.py \
    python3 /tmp/unit-paths-gate.py /
# Last, as files: during a RUN BuildKit mounts its own over these two, and
# its tar output holds empty placeholders.
COPY rootfs-late/ /
