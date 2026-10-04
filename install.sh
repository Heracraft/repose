#!/bin/sh
# install.sh: installs the repose CLI (docs/workstreams/07-cli.md §5.11).
# Served at https://repose.herakraft.co/install.sh.
#
#   curl -fsSL https://repose.herakraft.co/install.sh | sh
#   curl -fsSL https://repose.herakraft.co/install.sh | sh -s -- --system
#   curl -fsSL https://repose.herakraft.co/install.sh | sh -s -- --version v1.2.3
set -eu

# The GitHub repository the releases live in (DECISIONS I-98, I-286).
REPO="heracraft/repose"

# The release signing key (DECISIONS I-430). release.yml signs each
# release's checksums.txt with its private half; this script refuses a
# release whose checksums.txt.sig does not verify against it, so whoever
# can upload release assets cannot also vouch for them.
RELEASE_PUBKEY='-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEq1BgpU75F46qQ3iMHL1E0VOqye+4
3/u1m873IxGrYRM2AZyYkK6QqN92G2qBuMvUi+qb4OYeF7KRF1MQ5oiIdw==
-----END PUBLIC KEY-----'

# Releases cut before signing carry no checksums.txt.sig. Their
# checksums.txt is pinned here by SHA-256 instead, as published.
pinned_checksums() {
  case "$1" in
    v0.1.0) echo 41c47089f70e713ba5bd5c081bcc7e26597cf0325d93fa0ec1d96f22ceca5d54 ;;
    v0.1.1) echo 5029cc14299230bda38cededa355bff9ea44896c056045c37db0067095acd035 ;;
    v0.1.2) echo dbf75bbe1179763c3537926b32f6a859bec9492aa79290ce10813326e067f8e2 ;;
    v0.1.3) echo 35e64e6977fcd83167e49ae3be387c04271383cb68ac7f2101897d328ca86689 ;;
    v0.1.4) echo 868c79d436044c01c4766b797a36eff7bb2b3b7f3f5d130675106fa8d97b54a1 ;;
    v0.1.5) echo 1a3f8acdbae272ef7bb298b4e2218aa080e652e75e1129d433c7b28d4954198a ;;
    v0.1.6) echo f8e5311dc75d65bb8cd57afc99bec3ea4bd1775cf3441b922b90a7b7bd10890b ;;
    v0.1.7) echo ed66dfc4d6b5ca17a7d33ff8f28bfcc05149f81c733faf2e9d6b320442400ba7 ;;
    v0.1.8) echo 8483b3c6128951a9736120454e1601f7b84574e3c37f7953ae4a0c071638fb97 ;;
    v0.1.9) echo de28a8dc25ee04ce194571e195948e410b1ab48faf579ed4cab930e5e7619279 ;;
    v0.1.10) echo 7ac853a8f95171661b48cd8fc6893eb1c07ae6581b4cf4ea0bfa69d0551ad373 ;;
    v0.1.11) echo ec5a93ab0896c4ed8891c5445417bf5a4c957d264dc9c91c9170da7f88aa6b48 ;;
    v0.1.12) echo fc086c7ca9f7e700b9adcb9830d55988ba0c50f2fa12256c84d0da6bf115045d ;;
    v0.1.13) echo 0c13ed25781ecf42dd88a2f46d8ede74be724b0b83e31ae7d96ce7fad9328e01 ;;
    v0.1.14) echo 5a395a2132f2341c4369dfe0146028091b25af153b6ea57943eb321361db3906 ;;
    v0.1.15) echo cfc3f1e6d3915157d8040407ce2dd40696fbe1ee0d52bb54d6c6e2fc9165ac03 ;;
    v0.1.16) echo 144663ce1325720d1568eb60498114e2475cd277221c4d33ed04af6bad4597cb ;;
    v0.1.17) echo 4451fdf6b9089ad22f6edee1f3b8a3142200a931d3e3846ba6e8df5e274cb09d ;;
    v0.1.18) echo 7aba1d0f5b08c4018607ab22594866e2222b894a2250ff549f23f620e0dbfc6e ;;
    v0.1.19) echo 6f9737c1bb4953de7a58275193571941295397d25dfc6222ce8f44f75e51fdad ;;
    v0.1.20) echo 1af4b6a34d1099af2bbd319132dd1827a264b5eb6dc55ba8acb746347cd90147 ;;
    v0.1.21) echo 8a49a964183580af0b115733dd507204929147f140769c83e888e828087fbc06 ;;
    v0.1.22) echo 314d7d0225029f569e04fc9a84a683d341d9f5c69e2009c21b4d9e7ccf33cc82 ;;
    v0.1.23) echo c614b65c57efb1fa1f6c4fd359e6b6b79f3449f31d1178c5d17eeb8be056b1dc ;;
    v0.1.24) echo 71b84c27fff070fc8cdaeebdd36fda54e74a4db3a070aa0490d843a39e25b859 ;;
    v0.1.25) echo 19bb772746720ba641af08da6eb3a4395904ef3e4935bffbf642f7763d2c4ba9 ;;
    v0.1.26) echo 0f14b76e9b79fbeb917c0124e2d6db3d5866c127bbd4e10efdc3e242717f5339 ;;
    v0.1.27) echo cd53478f92548a44d163206a8e7f0a86c1fe4a739410da5e2f8c392d18600a58 ;;
    *) echo "" ;;
  esac
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    # macOS ships shasum, not sha256sum.
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}
BIN_NAME="repose"
INSTALL_DIR="$HOME/.local/bin"
VERSION="latest"

for arg in "$@"; do
  case "$arg" in
    --system) INSTALL_DIR="/usr/local/bin" ;;
    --version=*) VERSION="${arg#--version=}" ;;
    --version) NEED_VERSION_ARG=1 ;;
    *)
      if [ "${NEED_VERSION_ARG:-0}" = "1" ]; then
        VERSION="$arg"
        NEED_VERSION_ARG=0
      fi
      ;;
  esac
done

os_raw=$(uname -s)
arch_raw=$(uname -m)

case "$os_raw" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *)
    echo "repose: unsupported OS $os_raw (supports darwin, linux)" >&2
    exit 1
    ;;
esac

case "$arch_raw" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    echo "repose: unsupported architecture $arch_raw (supports amd64, arm64)" >&2
    exit 1
    ;;
esac

if [ "$VERSION" = "latest" ] && [ -z "${REPOSE_INSTALL_BASE_URL:-}" ]; then
  # The archive name carries the version, so "latest" is resolved to a
  # real tag first, by following releases/latest's redirect.
  VERSION=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
    "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/##')
  if [ -z "$VERSION" ]; then
    echo "repose: could not resolve the latest release" >&2
    exit 1
  fi
fi

if [ -n "${REPOSE_INSTALL_BASE_URL:-}" ]; then
  # Overridable for CI and local testing; production installs never set it.
  release_url="$REPOSE_INSTALL_BASE_URL"
else
  release_url="https://github.com/$REPO/releases/download/$VERSION"
fi

archive="${BIN_NAME}_${VERSION}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $archive ($VERSION)..." >&2
curl -fsSL "$release_url/$archive" -o "$tmp/$archive"
curl -fsSL "$release_url/checksums.txt" -o "$tmp/checksums.txt"

pin=$(pinned_checksums "$VERSION")
if [ -n "$pin" ]; then
  if [ "$(sha256_of "$tmp/checksums.txt")" != "$pin" ]; then
    echo "repose: checksums.txt of $VERSION is not the one published; refusing to install" >&2
    exit 1
  fi
else
  pubkey="$tmp/release.pub.pem"
  if [ -n "${REPOSE_INSTALL_BASE_URL:-}" ] && [ -n "${REPOSE_INSTALL_PUBKEY:-}" ]; then
    # Tests sign their fake release with their own key.
    cp "$REPOSE_INSTALL_PUBKEY" "$pubkey"
  else
    printf '%s\n' "$RELEASE_PUBKEY" > "$pubkey"
  fi
  if ! command -v openssl >/dev/null 2>&1; then
    echo "repose: install.sh needs openssl to check the release signature; install openssl and run it again" >&2
    exit 1
  fi
  if ! curl -fsSL "$release_url/checksums.txt.sig" -o "$tmp/checksums.txt.sig"; then
    echo "repose: release $VERSION has no signature (checksums.txt.sig); refusing to install" >&2
    exit 1
  fi
  if ! openssl dgst -sha256 -verify "$pubkey" -signature "$tmp/checksums.txt.sig" \
      "$tmp/checksums.txt" >/dev/null 2>&1; then
    echo "repose: the signature on checksums.txt of $VERSION does not verify; refusing to install" >&2
    exit 1
  fi
fi

want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1 || true)
if [ -z "$want" ] || [ "$(sha256_of "$tmp/$archive")" != "$want" ]; then
  echo "repose: checksum verification failed" >&2
  exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp"

if [ "$INSTALL_DIR" = "/usr/local/bin" ] && [ ! -w "$INSTALL_DIR" ]; then
  sudo mkdir -p "$INSTALL_DIR"
  sudo install -m 755 "$tmp/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
else
  mkdir -p "$INSTALL_DIR"
  install -m 755 "$tmp/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
fi

echo "Installed $INSTALL_DIR/$BIN_NAME"

# DECISIONS I-15: Arch Linux ships an unrelated `repose` binary. Put ours
# first on PATH, and say so rather than silently shadowing or losing to it.
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    shell_rc=""
    case "${SHELL:-}" in
      */zsh) shell_rc="$HOME/.zshrc" ;;
      */bash) shell_rc="$HOME/.bashrc" ;;
      *) shell_rc="$HOME/.profile" ;;
    esac
    printf '\nexport PATH="%s:$PATH"\n' "$INSTALL_DIR" >> "$shell_rc"
    echo "Added $INSTALL_DIR to PATH in $shell_rc; restart your shell or run: export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

resolved=$(command -v "$BIN_NAME" 2>/dev/null || true)
if [ -n "$resolved" ] && [ "$resolved" != "$INSTALL_DIR/$BIN_NAME" ]; then
  echo "another repose is on your PATH at $resolved; ours is at $INSTALL_DIR/$BIN_NAME"
fi

exit 0
