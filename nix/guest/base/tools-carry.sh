# repose-tools-install: the guest half of the tools carry (DECISIONS I-221,
# I-222). The CLI writes ~/.repose/tools-wanted.json (guest-conventions.md
# "Tools carry") and runs `repose-tools-install plan` in the carry's ssh;
# the user unit repose-tools-carry runs `repose-tools-install run` in the
# background.
#
#   plan  a few ms: prints `#installing <names>` for what the guest lacks,
#         writes their commands to $XDG_RUNTIME_DIR/repose-installing (one
#         per line, read by command-not-found), prints a `#warn` when the
#         project's node, ruby or java version cannot be made the default,
#         and starts the unit. Nothing is installed here.
#   run   installs each missing tool: a nixpkgs package that has
#         bin/<command> first (nix-locate, else the attribute named like
#         the command), with `nix profile add`; else the laptop's manager,
#         into a directory on the login PATH. What fails is logged in
#         ~/.repose/tools-install.log and said once by the next `repose run`
#         (~/.repose/tools-notices). A failed tool is not retried until the
#         laptop's entry for it changes. The pass ends by writing the carry
#         marker, so the CLI stops sending an unchanged list.
#
# Idempotent: a tool on PATH is skipped, a pass that stopped midway (a
# reboot) is resumed by the unit at the next boot because no marker was
# written, and a list that changed during a pass gets another pass.

wanted="$HOME/.repose/tools-wanted.json"
state="$HOME/.repose/tools"
failed="$state/failed"
log="$HOME/.repose/tools-install.log"
notices="$HOME/.repose/tools-notices"
marker="$HOME/.repose/carry/tools"
rt="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
installing="$rt/repose-installing"
user="${USER:-$(id -un)}"

# The login PATH's directories: this process's PATH (a login shell's, which
# since I-227 starts with every package manager's user bin dir), then the
# directories that must count even when it was not (a plain ssh command on
# an older base). ~/go/bin and ~/.cargo/bin are on it, so a tool the user
# installed with `go install` or `cargo install` is not installed again.
search_path() {
  printf '%s' "${PATH:-}:$HOME/.local/bin:$HOME/.local/share/pnpm/bin:$HOME/.local/share/pnpm:$HOME/.npm-global/bin:$HOME/go/bin:$HOME/.cargo/bin:$HOME/.bun/bin:$HOME/.deno/bin:$HOME/.nix-profile/bin:$HOME/.local/state/nix/profile/bin:/etc/profiles/per-user/$user/bin:/run/current-system/sw/bin"
}

has_cmd() {
  PATH="$(search_path)" command -v "$1" >/dev/null 2>&1
}

# where_cmd prints the directory a command resolves from, "" for none.
where_cmd() {
  local p
  p=$(PATH="$(search_path)" command -v "$1" 2>/dev/null) || return 0
  dirname "$p"
}

# The runtimes a project pins (DECISIONS I-265 added ruby and java to
# node): the key in tools-wanted.json is also the command.
runtimes="node ruby java"

# runtime_attr prints the nixpkgs attribute for a runtime's version.
runtime_attr() {
  case $1 in
    node) printf 'nodejs_%s' "$2" ;;
    ruby) printf 'ruby_%s' "${2//./_}" ;;
    java) printf 'jdk%s_headless' "$2" ;;
  esac
}

# parse_version reads a runtime's version output on stdin and prints the
# part a pin names: node's major, ruby's series ("3.3"), java's major
# ("1.8.0_462" is 8).
parse_version() {
  local v
  case $1 in
    node) v=$(head -n 1); v=${v#v}; printf '%s' "${v%%.*}" ;;
    ruby) sed -n '1s/^ruby \([0-9]*\.[0-9]*\).*/\1/p' ;;
    java)
      v=$(sed -n 's/.*version "\([^"]*\)".*/\1/p' | head -n 1)
      case $v in 1.*) v=${v#1.} ;; esac
      printf '%s' "${v%%[._+-]*}" ;;
  esac
}

version_cmd() {
  case $1 in
    java) printf 'java -version 2>&1' ;;
    *) printf '%s --version' "$1" ;;
  esac
}

# runtime_version: the version of the runtime found on the login PATH now.
runtime_version() {
  { PATH="$(search_path)" bash -c "$(version_cmd "$1")" 2>/dev/null | parse_version "$1"; } || true
}

# login_version: the version a new login shell finds.
login_version() {
  { bash -lc "$(version_cmd "$1")" 2>/dev/null | parse_version "$1"; } || true
}

# runtime_want: the version tools-wanted.json asks for, "" for none or for
# anything but digits and dots.
runtime_want() {
  local v
  v=$(jq -r --arg k "$1" '.[$k] // empty' "$wanted")
  case $v in "" | *[!0-9.]*) return 0 ;; esac
  printf '%s' "$v"
}

# profile_wins: the runtime in dev's nix profile would be the one of a
# new login shell, because the one found today is not in one of the
# directories env.nix puts before the profile.
profile_wins() {
  local d
  d=$(where_cmd "$1")
  case "$d" in
    "" | "$HOME/.nix-profile/bin" | "$HOME/.local/state/nix/profile/bin" | "/etc/profiles/per-user/$user/bin" | /run/current-system/sw/bin) return 0 ;;
  esac
  return 1
}

items_tsv() {
  jq -r '.items[] | [.name, (.manager // ""), (.pkg // ""), (.version // ""), (.bins | join(" "))] | join("\u001f")' "$wanted"
}

item_key() { printf '%s|%s|%s|%s' "$1" "$2" "$3" "$4"; }

is_failed() { [ -f "$failed" ] && grep -qxF "$1" "$failed"; }

plan() {
  [ -s "$wanted" ] || return 0
  local hash names=() cmds=() name manager pkg version bins b present want cur lang attr
  hash=$(jq -r '.hash' "$wanted")
  if [ -f "$marker" ] && [ "$(cat "$marker")" = "$hash" ]; then
    return 0
  fi
  while IFS=$'\x1f' read -r name manager pkg version bins; do
    [ -n "$name" ] || continue
    present=
    for b in $bins; do
      if has_cmd "$b"; then present=1; break; fi
    done
    [ -z "$present" ] || continue
    is_failed "$(item_key "$name" "$manager" "$pkg" "$version")" && continue
    names+=("$name")
    for b in $bins; do cmds+=("$b"); done
  done < <(items_tsv)
  for lang in $runtimes; do
    want=$(runtime_want "$lang")
    [ -n "$want" ] || continue
    cur=$(runtime_version "$lang")
    [ "$cur" != "$want" ] || continue
    attr=$(runtime_attr "$lang" "$want")
    if profile_wins "$lang"; then
      names+=("$attr")
    else
      echo "#warn This project asks for $lang $want and the guest's $lang is ${cur:-missing}, from $(where_cmd "$lang"), which comes before the nix profile on PATH; run \`repose config add $attr\` to make $lang $want the guest's."
    fi
  done
  if [ "${#cmds[@]}" -gt 0 ]; then
    mkdir -p "$rt" 2>/dev/null || true
    printf '%s\n' "${cmds[@]}" > "$installing.new" && mv -f "$installing.new" "$installing" || true
  fi
  if [ "${#names[@]}" -gt 0 ]; then
    echo "#installing ${names[*]}"
  fi
  XDG_RUNTIME_DIR="$rt" systemctl --user start --no-block repose-tools-carry.service 2>/dev/null || true
}

logline() {
  mkdir -p "$(dirname "$log")"
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >> "$log"
}

# done_installing drops a tool's commands from the installing file.
done_installing() {
  [ -f "$installing" ] || return 0
  local tmp="$installing.tmp" b
  cp "$installing" "$tmp"
  for b in "$@"; do
    grep -vxF "$b" "$tmp" > "$tmp.2" || true
    mv -f "$tmp.2" "$tmp"
  done
  if [ -s "$tmp" ]; then mv -f "$tmp" "$installing"; else rm -f "$tmp" "$installing"; fi
}

add_installing() {
  mkdir -p "$rt" 2>/dev/null || return 0
  local b
  for b in "$@"; do
    grep -qxF "$b" "$installing" 2>/dev/null || printf '%s\n' "$b" >> "$installing"
  done
}

# resolve_attr prints the nixpkgs attribute whose bin/<cmd> it is, "" for
# none: nix-locate's index when the base has it (the call nix-index's own
# command-not-found makes; each line is `<attr>.<output>`), else the
# attribute named like the command, if nixpkgs has one. The attribute
# named like the command wins, then one outside a package set, then the
# shortest, then the first by name.
resolve_attr() {
  local cmd=$1 out best="" a
  if command -v nix-locate >/dev/null 2>&1; then
    out=$(nix-locate --minimal --no-group --type x --type s --whole-name --at-root "/bin/$cmd" 2>/dev/null || true)
    while IFS= read -r a; do
      [ -n "$a" ] || continue
      case $a in *.*) a=${a%.*} ;; esac
      if [ "$a" = "$cmd" ]; then best=$a; break; fi
      if better_attr "$a" "$best"; then best=$a; fi
    done <<< "$out"
    printf '%s' "$best"
    return 0
  fi
  if nix eval --raw "nixpkgs#$cmd.name" >/dev/null 2>&1; then
    printf '%s' "$cmd"
  fi
}

# better_attr: is $1 a better pick than $2 ("" is the worst)?
better_attr() {
  local a=$1 b=$2 da=0 db=0
  [ -n "$b" ] || return 0
  case $a in *.*) da=1 ;; esac
  case $b in *.*) db=1 ;; esac
  [ "$da" -eq "$db" ] || { [ "$da" -lt "$db" ]; return; }
  [ "${#a}" -eq "${#b}" ] || { [ "${#a}" -lt "${#b}" ]; return; }
  [[ "$a" < "$b" ]]
}

profile_add() {
  local sub=add
  nix profile add --help >/dev/null 2>&1 || sub=install
  timeout 1200 nix profile "$sub" "nixpkgs#$1"
}

# attempt runs one install command with its output in the log; on failure
# `reason` is its last line.
reason=
attempt() {
  local out rc
  logline "running: $*"
  out=$("$@" 2>&1) && rc=0 || rc=$?
  printf '%s\n' "$out" | tail -n 40 >> "$log"
  if [ "$rc" -ne 0 ]; then
    reason=$(printf '%s\n' "$out" | grep -v '^[[:space:]]*$' | tail -n 1 | cut -c1-160)
    [ -n "$reason" ] || reason="exit status $rc"
    return 1
  fi
  return 0
}

install_one() {
  local name=$1 manager=$2 pkg=$3 version=$4 bins=$5 primary attr b
  primary=${bins%% *}
  reason=
  if command -v nix >/dev/null 2>&1; then
    attr=$(resolve_attr "$primary")
    if [ -n "$attr" ] && attempt profile_add "$attr" && has_cmd "$primary"; then
      logline "$name: installed nixpkgs#$attr"
      return 0
    fi
  fi
  # The laptop's manager, into a directory on the login PATH.
  case "$manager" in
    npm | pnpm | bun)
      attempt timeout 1200 npm install -g --no-fund --no-audit "$pkg@${version:-latest}" || return 1 ;;
    go)
      mkdir -p "$HOME/.local/bin"
      attempt timeout 1200 env GOBIN="$HOME/.local/bin" go install "$pkg@${version:-latest}" || return 1 ;;
    cargo)
      cargo_toolchain || return 1
      if [ -n "$version" ]; then
        attempt timeout 1200 cargo install --locked --root "$HOME/.local" "$pkg" --version "$version" || return 1
      else
        attempt timeout 1200 cargo install --locked --root "$HOME/.local" "$pkg" || return 1
      fi ;;
    uv | pipx)
      attempt timeout 1200 uv tool install "$pkg${version:+==$version}" || return 1 ;;
    *)
      [ -n "$reason" ] || reason="no nixpkgs package has bin/$primary"
      return 1 ;;
  esac
  for b in $bins; do
    if has_cmd "$b"; then
      logline "$name: installed with $manager"
      return 0
    fi
  done
  reason="installed with $manager, but none of its commands is on PATH"
  return 1
}

# cargo_toolchain gives rustup a default toolchain before the first
# cargo install, which fails without one (I-522): the stable toolchain,
# minimal profile. A laptop with cargo-installed crates is a Rust user's,
# who would run `rustup default stable` anyway. A default the user chose
# is kept.
cargo_toolchain() {
  rustup show active-toolchain >/dev/null 2>&1 && return 0
  attempt timeout 1200 rustup toolchain install stable --profile minimal || return 1
  attempt rustup default stable || return 1
  logline "rustup: stable (minimal profile) is the default toolchain, for cargo install"
}

# ruby_bundler installs Bundler 2.7 into GEM_HOME for a ruby whose
# RubyGems is 3.7 or newer and whose own Bundler is older than 2.7
# (I-524): nixpkgs's ruby_3_4 pairs RubyGems 3.7 with Bundler 2.6, which
# redefines RubyGems constants and prints a screen of warnings on every
# `bundle`. A Ruby with Bundler 2.7 or newer (ruby_4_0 has 4.0) gets
# nothing. --env-shebang, so the `bundle` in GEM_HOME runs whichever ruby
# is first on PATH after a later pin.
ruby_bundler() {
  local gv bv
  gv=$(bash -lc 'gem --version' 2>/dev/null) || return 0
  version_at_least "$gv" 3.7 || return 0
  bv=$(bash -lc 'bundle -v' 2>/dev/null | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -n 1)
  [ -z "$bv" ] || ! version_at_least "$bv" 2.7 || return 0
  reason=
  if ! attempt timeout 600 bash -lc "gem install --no-document --env-shebang bundler -v '~> 2.7'"; then
    notice "Could not install Bundler 2.7 for Ruby: $reason"
    return 0
  fi
  bv=$(bash -lc 'bundle -v' 2>/dev/null | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -n 1)
  logline "ruby: bundler ${bv:-?} in GEM_HOME"
}

# version_at_least A B: dotted version A is B or newer.
version_at_least() {
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n 1)" = "$2" ]
}

notice() {
  printf '%s\n' "$*" >> "$notices"
  logline "$*"
}

# runtime_pass makes the pinned version of one runtime the one of new
# login shells: node's major, ruby's series, java's major (I-265). The
# attribute it added is recorded in ~/.repose/tools/<runtime> and replaced
# when the project asks for another version.
runtime_pass() {
  local lang=$1 want cur prev attr
  want=$(runtime_want "$lang")
  [ -n "$want" ] || return 0
  cur=$(runtime_version "$lang")
  if [ "$cur" = "$want" ]; then
    # A Ruby an earlier pass pinned, before I-524: it gets its Bundler now.
    if [ "$lang" = ruby ] && [ -s "$state/ruby" ]; then ruby_bundler; fi
    return 0
  fi
  profile_wins "$lang" || return 0 # plan said so already
  attr=$(runtime_attr "$lang" "$want")
  prev=$(cat "$state/$lang" 2>/dev/null || true)
  if [ -n "$prev" ] && [ "$prev" != "$attr" ]; then
    nix profile remove "$prev" >/dev/null 2>&1 || true
  fi
  reason=
  if attempt profile_add "$attr" && [ "$(login_version "$lang")" = "$want" ]; then
    printf '%s\n' "$attr" > "$state/$lang"
    logline "$lang: $attr is the $lang of new shells"
    [ "$lang" != ruby ] || ruby_bundler
    return 0
  fi
  nix profile remove "$attr" >/dev/null 2>&1 || true
  rm -f "$state/$lang"
  notice "Could not make $lang $want the guest's $lang: ${reason:-another $lang comes first on PATH}. Run \`repose config add $attr\`."
}

pass() {
  local name manager pkg version bins b present key lang
  mkdir -p "$state"
  for lang in $runtimes; do
    runtime_pass "$lang"
  done
  while IFS=$'\x1f' read -r name manager pkg version bins; do
    [ -n "$name" ] || continue
    present=
    for b in $bins; do
      if has_cmd "$b"; then present=1; break; fi
    done
    key=$(item_key "$name" "$manager" "$pkg" "$version")
    if [ -n "$present" ] || is_failed "$key"; then
      # shellcheck disable=SC2086
      done_installing $bins
      continue
    fi
    # shellcheck disable=SC2086
    add_installing $bins
    if install_one "$name" "$manager" "$pkg" "$version" "$bins"; then
      :
    else
      printf '%s\n' "$key" >> "$failed"
      notice "Could not install $name: $reason"
    fi
    # shellcheck disable=SC2086
    done_installing $bins
  done < <(items_tsv)
}

run() {
  sleep "${REPOSE_TOOLS_DELAY:-2}"
  local hash
  while [ -s "$wanted" ]; do
    hash=$(jq -r '.hash' "$wanted")
    if [ -f "$marker" ] && [ "$(cat "$marker")" = "$hash" ]; then
      break
    fi
    pass
    if [ "$(jq -r '.hash' "$wanted")" = "$hash" ]; then
      mkdir -p "$(dirname "$marker")"
      printf '%s\n' "$hash" > "$marker"
      break
    fi
  done
  rm -f "$installing"
  # keep the log small
  if [ -f "$log" ] && [ "$(wc -c < "$log")" -gt 1048576 ]; then
    tail -c 524288 "$log" > "$log.tmp" && mv -f "$log.tmp" "$log"
  fi
}

case "${1:-}" in
  plan) plan ;;
  run) run ;;
  *)
    echo "usage: repose-tools-install plan|run" >&2
    exit 64 ;;
esac
