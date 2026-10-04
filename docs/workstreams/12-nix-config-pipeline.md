# 12: Nix config pipeline

Milestone: M1. Owns the fragment contract, the build limits, the menu
catalog format, the agent overlay packages and their bump job. Consumes
`interfaces/grpc-hostd.md` (`Build`, `ApplyConfig`, `BuildLog`) and
`interfaces/vsock-guestd.md` (`Switch`). Consumed by 03-hostd (runs the
build), 05-api (validates and stores revisions, renders the menu), 08-
dashboard (the menu), 07-cli (`config` commands and error display).

## 1. Goal

Turn a user's home-manager fragment, or a menu selection, into a guest
system closure in the host's store, safely, within known limits, with
errors a person can act on. Keep the platform base moving underneath every
guest without breaking the ones that asked to be left alone.

## 2. Scope: builds

- `nix/guest/fragment.nix`: the module that wraps a user fragment, and
  `nix/guest/compose.nix`: the function `composeGuest { baseRef, fragment,
  menuSnippet, guestParams }` that 03 calls (through `mkGuestRunner` from
  02) to produce the runner and the toplevel.
- `internal/nixbuild/`: the Go package hostd uses: writes the fragment to
  `/var/lib/repose/builds/<revision_id>/fragment.nix`, runs `nix eval` then
  `nix build` with the flags below, enforces the limits with a cgroup and
  timers, parses errors into `{code, message, fragment_line}`, streams
  `BuildLog` lines, registers the GC root, measures closure size.
- `internal/menu/`: the catalog (`internal/menu/catalog.yaml`), the
  validator, and the renderer that turns a `MenuSelection` into a fragment
  plus a system snippet. The api calls it; the dashboard reads the catalog
  via `GET /catalog`.
- `nix/overlay/agents/`: package definitions for `claude-code`, `opencode`,
  `codex`, `gemini-cli`, `pi-coding-agent` that fetch upstream release
  binaries by version and hash (`versions.json`), plus the npm MCP
  servers; a `scripts/bump-agents.sh` that reads upstream release feeds,
  updates `versions.json` with new hashes and builds; a
  `scripts/bump-agents-pr.sh` that checks the result and opens a PR; a
  GitHub Actions workflow running them daily in two jobs, the first with no
  write access (DECISIONS I-428). The overlay is also pushed to a
  binary cache (Cachix or an S3-compatible bucket served by `nix-serve` on
  the Coolify VM; decision recorded at implementation as I-n) so hosts do
  not rebuild it.
- `internal/basebump/`: the api-side job that, when a new `base_versions`
  row is published (`repose-admin base publish <git rev> --changelog`),
  schedules `Build` + `ApplyConfig` for every project not on hold, at most
  N concurrent per host, over a 24 h window; records per-project outcome;
  never touches a project whose last build failed until the user
  re-applies.
- `nix/flake.nix` outputs: `lib.composeGuest`, `nixosModules.guestBase`
  (02), `overlays.agents`, `packages.<agent>`, and a `checks.fragment-
  examples` that composes every example fragment in `docs/features/config-
  examples/` and asserts they build.

## 3. Scope: does not build

- The base module contents (02). This workstream composes it.
- hostd's process supervision, gRPC plumbing (03). This workstream is a Go
  package hostd calls and the Nix functions it invokes.
- The dashboard UI for the menu (08). This workstream defines the catalog
  format and renderer.
- The api endpoints (05). This workstream defines validation rules the api
  enforces.

## 4. Interfaces owned / consumed

Owned: the fragment contract (below), the menu catalog schema, the
`versions.json` schema for the overlay, the `Build` limits. Consumed:
`grpc-hostd.md` `Build`/`ApplyConfig`/`BuildLog` shapes; `vsock-guestd.md`
`Switch`.

## 5. Design detail

### The fragment contract

A fragment is a single Nix file whose value is a home-manager module:
either an attribute set or a function `{ config, pkgs, lib, ... }:
attrset`. It is applied to user `dev`. It may:

- add `home.packages` from `pkgs` (nixpkgs at the platform's pinned rev,
  with the agents overlay applied, `allowUnfree = true` for a fixed
  allowlist: `claude-code`, `codex`, `gemini-cli`, `vscode`, `cursor`,
  `terraform`, `ngrok`, `google-chrome`);
- configure any `programs.*` and `services.*` home-manager module;
- write dotfiles via `home.file` and `xdg.configFile`;
- set `home.sessionVariables` and `home.sessionPath`;
- define overlays via `nixpkgs.overlays` **only** when `useGlobalPkgs` is
  off, which it is not, so overlays are instead accepted through a
  dedicated option `repose.overlays = [ (self: super: {...}) ]` that the
  composer applies to the guest's `pkgs` before evaluating anything;
- fetch sources with `pkgs.fetchFromGitHub`, `pkgs.fetchurl` and friends
  (fixed-output derivations with a hash: these run in the build sandbox
  with network, which is Nix's normal model, and on a host reach the
  public internet only, DECISIONS I-439);
- call `pkgs.writeShellScriptBin`, `pkgs.buildNpmPackage`, `pkgs.
  buildGoModule`, `pkgs.rustPlatform.buildRustPackage`, any nixpkgs builder.

It may not:

- use `builtins.fetchurl`, `builtins.fetchTarball`, `builtins.fetchGit`
  or flake inputs of its own (evaluation-time fetches are disabled by
  `--pure-eval` and the absence of `--impure`; the error is `access to
  absolute path` or `cannot fetch in pure evaluation mode`, which the
  parser maps to `eval_failed` with a hint);
- import-from-derivation (`allow-import-from-derivation = false`);
- read any path outside the build directory (`restrict-eval = true`, with
  `allowed-uris` empty and the nixpkgs and home-manager sources passed as
  path arguments);
- touch NixOS options (it is a home-manager module; a NixOS option name
  yields `The option 'services.postgresql' does not exist` from home-
  manager, which is the intended error, with a hint pointing at the menu).

The composer wraps it:

```nix
# nix/guest/compose.nix (sketch)
{ nixpkgs, home-manager, microvm, self }:
{ baseRef, fragmentPath, menuSnippet, guestParams }:
let
  base = import "${self}/nix/guest/base";        # pinned by baseRef through the flake
  fragment = import fragmentPath;
in nixpkgs.lib.nixosSystem {
  system = "x86_64-linux";
  modules = [
    microvm.nixosModules.microvm
    home-manager.nixosModules.home-manager
    base
    menuSnippet                                   # platform-rendered NixOS snippet, allowlisted services
    { home-manager.users.dev = fragment;
      home-manager.useGlobalPkgs = true;
      home-manager.useUserPackages = true;
      nixpkgs.overlays = [ self.overlays.agents ] ++ (fragment.repose.overlays or []); }
    (guestParamsModule guestParams)               # ip, cid, tap, volume, vcpu, mem
  ];
}
```

`baseRef` is a git revision of this repository's `nix/` directory; hostd
keeps a checkout per `base_versions` row under `/var/lib/repose/base/<rev>`
(fetched over HTTPS from the repository with a read token, or delivered by
the api as a tarball in `Build`; implementation choice recorded as I-n).
The build command is `nix build <checkout>#lib.guestRunner --arg ...` with
the fragment path passed as a `--arg fragmentPath /var/lib/repose/builds/
<rev>/fragment.nix` under `restrict-eval` with that directory in
`allowed-paths`.

### Flags and limits

Evaluation and build run as user `nixbuild` (not root) in a transient
systemd scope: `systemd-run --scope --property CPUQuota=800% --property
MemoryMax=16G --property RuntimeMaxSec=1800 -- nix build ...` with these
`nix.conf`-level options passed as `--option`:

```
pure-eval true
restrict-eval true
allow-import-from-derivation false
allowed-uris ""               # nothing at eval time
sandbox true
max-jobs 2
cores 8
timeout 1800                  # per derivation build, seconds
max-silent-time 600
substituters https://cache.nixos.org https://<overlay cache>
trusted-public-keys <both>
keep-going false
```

Evaluation is a separate first step, `nix eval --raw <...>.config.system.
build.toplevel.drvPath` with `RuntimeMaxSec=60`, so a pathological
expression fails fast with `eval_timeout` before any build starts.

After the build: `nix path-info -S` on the toplevel; if closure size minus
the base closure size exceeds 20 GB, the build result is deleted (no GC
root) and `closure_too_large` is returned with the ten largest paths listed
in `message`. Otherwise `nix-store --add-root /nix/var/nix/gcroots/repose/
<guest_id>-<revision_id> -r <toplevel>`; the previous revision's root is
kept until the new one is applied plus 14 days, so rollback is instant.

`kernel_changed` is `readlink <new>/kernel != readlink <current>/kernel ||
readlink <new>/initrd != readlink <current>/initrd`; hostd reports it in
the `Build` result and the api tells the CLI before `ApplyConfig`.

### Error extraction

`internal/nixbuild/errors.go` parses Nix's stderr:

| Pattern | Code | fragment_line |
|---|---|---|
| `error: syntax error, unexpected ... at /var/lib/repose/builds/<rev>/fragment.nix:L:C` | `eval_failed` | L |
| `error: attribute 'X' missing` with a trace line in `fragment.nix:L` | `eval_failed` | L |
| `The option 'X' does not exist` with a definition location in `fragment.nix` | `eval_failed` | L, hint "system services come from the menu or `repose config menu`" |
| `cannot fetch ... in pure evaluation mode` / `access to absolute path` | `eval_failed` | L, hint "use pkgs.fetchurl with a hash instead of builtins.fetch*" |
| `RuntimeMaxSec` kill during eval | `eval_timeout` | none, message "evaluation exceeded 60 s" |
| `error: builder for '/nix/store/...-X.drv' failed` | `build_failed` | none, message = last 200 lines of that builder's log via `nix log`, prefixed with the derivation name |
| `RuntimeMaxSec` kill during build, or `timed out after 1800 seconds` | `build_timeout` | none, message names the derivation that was running |
| closure check | `closure_too_large` | none, top ten paths |
| anything else | `build_failed` | none, raw tail 32 KB |

The `message` always begins with a one-line summary, then a blank line,
then the verbatim Nix output. The CLI prints the summary in red, the
fragment line with two lines of context from the local copy, then the
verbatim block. The dashboard shows the same.

### The five canonical error cases (from CHECKLIST.md)

| Case | Fragment | Exact CLI first line |
|---|---|---|
| (a) syntax error | `{ home.packages = [ pkgs.ripgrep ; }` | `config error: syntax error at fragment.nix:1:32, unexpected ';'` |
| (b) missing attribute | `{ home.packages = [ pkgs.ripgrepp ]; }` | `config error: attribute 'ripgrepp' missing at fragment.nix:1:22 (did you mean ripgrep?)` |
| (c) 31-minute build | a derivation running `sleep 1860` | `build timed out after 30 minutes while building sleep-forever-1.0` |
| (d) closure over cap | `home.packages = [ pkgs.cudaPackages.cudatoolkit ... ]` (30 GB) | `config too large: closure is 31.2 GB, limit is 20 GB; largest paths: ...` |
| (e) eval-time fetch | `{ home.file.x.source = builtins.fetchurl "https://example.com/x"; }` | `config error: eval-time fetch not allowed at fragment.nix:1:26; use pkgs.fetchurl { url = ...; hash = ...; }` |

Each case has a fixture in `internal/nixbuild/testdata/` and a test that
runs it against real `nix` (in CI on a Linux runner with Nix installed,
cases (a), (b), (e) fully; (c) with the timeout lowered to 5 s by a test
flag; (d) with the cap lowered to 100 MB).

### The menu

`internal/menu/catalog.yaml`:

```yaml
- id: postgres
  label: PostgreSQL 16
  group: databases
  kind: service          # service | package | agent | runtime
  description: A local PostgreSQL on 5432, data in /home/dev/.local/share/postgres
  nixos: |               # rendered into the platform snippet, not the fragment
    services.postgresql = { enable = true; package = pkgs.postgresql_16; };
  options:
    - id: version
      type: enum
      values: ["15", "16", "17"]
      default: "16"
- id: bun
  label: Bun
  group: runtimes
  kind: package
  hm: |                  # rendered into the fragment
    home.packages = [ pkgs.bun ];
```

`MenuSelection` is `[{id, options: {name: value}}]`. The renderer
validates ids and option values against the catalog (unknown id → api
`invalid` with the id), renders `hm` snippets into one generated fragment
(with a header comment `# generated by repose from your menu selection;
edit with repose config edit to take over`) and `nixos` snippets into the
`menuSnippet` module. Services in the catalog are the only way a fragment
gets a system service, and every `nixos` snippet in the catalog is reviewed
under `SECURITY.md`'s allowlist (no `networking`, no `users`, no
`boot`, no `virtualisation` beyond Docker, no `services.openssh`).

Taking over: `repose config edit` on a menu-managed project writes the
generated fragment to the user's editor; saving it switches the project to
fragment mode and the menu selection is kept as history only. `PUT
/config` with `menu` on a fragment-mode project is refused with
`conflict: project uses a custom fragment; use fragment mode or reset`.

### Base bumps

`repose-admin base publish <rev> --changelog "..." [--security]` inserts a
`base_versions` row. `<rev>` must be a full 40-hex sha that GitHub's
compare API places on `main` of the platform repository (`--repo`,
`$REPOSE_BASE_REPO`, default the repository hosts clone); anything else
is refused before the row is written (DECISIONS I-173). `internal/basebump` then, for each project with
`hold_base_updates = false` and `state in (running, stopped)`, enqueues
`Build` with the new `baseRef` and the project's current fragment, then
`ApplyConfig` on success (for stopped projects, the closure is just rooted
and used at the next start). Concurrency: 2 builds per host (the host's
`max-jobs`), spread over 24 h (`--security` sets 2 h). A project whose
bump build fails keeps its old closure, gets an event `base_update_failed`
with the error, and is skipped by later bumps until the user's next
successful apply. `kernel_changed = true` for a bump means the guest is
switched with `needs_reboot` and the user sees `base update ready; reboot
when convenient: repose config apply --reboot` in `status` and an event;
the api does not reboot unattended guests.

`repose status` shows `base 2026.09.3 (2026.09.4 available, held)` or
`(applied 2026-09-20)`.

### Agent overlay

`nix/overlay/agents/versions.json`:

```json
{ "claude-code": { "version": "2.1.273", "x86_64-linux": { "url": "...", "hash": "sha256-..." } },
  "codex": { ... }, "opencode": { ... }, "gemini-cli": { ... }, "pi-coding-agent": { ... },
  "playwright-mcp": { "version": "...", "npmDepsHash": "..." }, "chrome-devtools-mcp": { ... } }
```

Each package is a `stdenv.mkDerivation` (or `buildNpmPackage`) reading
that file, with `autoPatchelfHook` where binaries are dynamically linked
(Claude Code is; Codex's Rust binary is static; pi is a bun-compiled
binary; opencode is a Go/bun binary). `scripts/bump-agents.sh` queries each
upstream (npm registry for Claude Code's manifest, GitHub releases for the
rest), prefetches, rewrites `versions.json`, runs `nix build .#<agent>` and
`<agent> --version` with an empty environment. In a second CI job, one
that runs none of the downloaded code, `scripts/bump-agents-pr.sh` checks
that only values changed and opens a PR titled `agents: claude-code 2.1.273,
codex 0.155.0` (DECISIONS I-428). Merging the PR does nothing to guests until `base publish`.

## 6. Failure modes

| Failure | Outcome |
|---|---|
| Any of the five canonical errors | The exact messages above, revision `failed`, guest untouched. |
| Overlay cache unreachable | Builds fall back to building the agents from the release binaries (they are fixed-output fetches, so this is a download, not a compile); slower, `host_warning{kind: "cache_unreachable"}`. |
| nixpkgs rev in `baseRef` cannot be fetched | `Build` fails `internal: base <rev> unavailable`; alert; no project changes. |
| A base bump breaks a fragment that used to build | That project gets `base_update_failed` with the error, stays on the old base, is listed in `repose-admin base status <version>`; the operator can `base rollback` if it is widespread. |
| Build cgroup OOM (over 16 GB) | Nix reports the builder killed; mapped to `build_failed` with hint "build exceeded 16 GB RAM". |
| GC root missing (host bug) and the closure is collected while a guest runs | The guest keeps running from page cache until it touches a missing path; guestd's `Warning{store_path_missing}` (added here to 04's warning list) fires; hostd re-builds the revision (deterministic) and re-roots; the runbook entry says how to restart the guest if it wedged. |
| Two applies race | The api serialises per project (`ops` with state `running` blocks a second `PUT /config` with `conflict: a build is in progress`). |

## 7. Testing

- `internal/nixbuild` tests against real Nix in CI (Linux runner, `nix`
  installed by the workflow, sandbox on): the five canonical cases, a
  successful trivial fragment, the closure-size check, the GC root
  registration, `kernel_changed` detection with two fixture toplevels.
- `internal/menu` unit tests: every catalog entry renders, validates,
  round-trips; an unknown id is rejected; a `nixos` snippet mentioning a
  forbidden option prefix fails the catalog lint (`go test` includes a
  lint over `catalog.yaml` against the allowlist in `SECURITY.md`).
- `nix flake check` builds every fragment in `docs/features/config-
  examples/`.
- On a real host with 03: apply a fragment adding `bun`, see `bun
  --version` in the guest without reboot; apply a catalog `postgres`, see
  `psql` connect; publish a base bump, see it applied to a non-held project
  and skipped for a held one.

## 8. Rollback

Per project: `POST /config/revisions/:rev/apply` re-applies any successful
revision still rooted (14 days). Per base: `repose-admin base rollback
<version>`. The overlay: revert the `versions.json` PR and `base publish`
again.

## 9. Checklist

Ticked 2026-09-20 by the M3 integration session from the evidence in
`STATUS.md` (12 done-local line, 2026-09-20), the test names in the tree
and the M1 and M2 host sessions; `[~]` is a row whose local half is closed
and whose host half is one of the `ops/checks/menu.sh` or
`ops/checks/resilience.sh` runs on host-01 (`ops/checks/README.md`).

- [x] The fragment contract section above is reproduced in
      `docs/features/config.md` for users, with three example fragments in
      `docs/features/config-examples/` that `nix flake check` builds.
      Evidence: `docs/features/config.md` "Writing a fragment";
      `nix/flake.nix` `checks.fragment-examples` and
      `checks.fragment-contract` (`nix flake check ./nix`, CI `nix` job).
- [x] Each of the five canonical cases produces exactly the documented
      first line. Evidence: `internal/hostd/nixbuild/nixbuild_test.go`
      `TestMapEvalErrorFixtures` (the summary line and `fragment_line` of
      every `testdata/*.stderr`, real Nix output) and
      `realnix_test.go` `TestRealNixCanonicalCases`,
      `TestRealNixBuildTimeout`, `TestRealNixClosureCap`
      (`REPOSE_NIX_TESTS=1`, timeout 5 s and cap 100 MB). Through the CLI
      on host-01 (`ops/checks/menu.sh`, m3-check, 2026-09-21 00:47Z; the
      CLI prints the local file's name in place of `fragment.nix`): (b)
      `config error: attribute 'ripgrepp' missing at missing.nix:1:36 (did
      you mean one of ripgrep, ipgrep or repgrep?)`, exit 10; (e) `config
      error: eval-time fetch not allowed at fetch.nix:1:33; use
      pkgs.fetchurl { url = ...; hash = ...; }`, exit 10; (a) reads
      `syntax error, unexpected ';' at …` from the api's parse-time check on
      the deployed api, the contract's order on the next api deploy
      (I-126): closed by the M5 session on the api image `74d45e3`,
      project repose-m5-frag, 2026-09-21 02:29Z, `config error: syntax
      error at syntax.nix:1:34, unexpected ';'` with the caret under the
      `;`, exit 10; (d) `--with-closure-cap` closed below; (c) closed by the M5
      session on host-01, project repose-m5-frag, 2026-09-21: `repose
      config apply build-timeout.nix` (the `ops/checks/fragments` sleep
      1860 derivation) at 03:03:10Z ran as `nixbuild` in
      `repose-build-<rev>.scope` under `timeout -k 5 1800`; hostd logged
      `build_fail` with `code=build_timeout`, `duration_ms=1805018`
      (03:33:15Z); the api's op error is `{code: build_timeout, message:
      "build timed out after 30 minutes while building sleep-forever-1.0"
      + the verbatim block}`, the revision `failed`, and the guest was
      untouched (its journal for the window: `build_start`,
      `build_fail`, the 03:00 nightly snapshot; no switch). The CLI
      process on the dev box was killed by that box's memory pressure at
      03:04Z, so the CLI's rendering of `build_timeout` (no prefix, the
      summary line, then the block) rests on its unit tests
      (`TestRenderBuildErrorPrintsTheVerbatimBlock`, I-114, I-128) rather
      than a transcript. — closed: all five first lines recorded on host-01 in
      this row ((a) M5 02:29Z on api 74d45e3, (b)/(e) m3-check 00:47Z, (c) M5
      03:33Z `build_timeout`, (d) the closure-cap row; commits 954c21f,
      2f90b2d). Only the CLI's rendering of (c) rests on
      `TestRenderBuildErrorPrintsTheVerbatimBlock` rather than a transcript.
- [x] `nix eval` of a fragment containing `builtins.readFile "/etc/passwd"`
      fails with `access to absolute path` (restrict-eval works). Evidence:
      `testdata/abspath.stderr` pinned by `TestMapEvalErrorFixtures`; on
      host-01 (00:47Z): `config error: access to absolute path
      '/etc/passwd' is forbidden in pure evaluation mode (use '--impure'
      to override) at abspath.nix:1:31; a fragment may only read files it
      carries`, exit 10. — closed: the host-01 run quoted in this row
      (m3-check, 2026-09-21 00:47Z, commit 954c21f).
- [x] A fragment containing `import <nixpkgs>` fails (no `NIX_PATH`,
      pure eval). Evidence: `testdata/nixpath.stderr` pinned by
      `TestMapEvalErrorFixtures`; on host-01 (00:47Z): `config error:
      <nixpkgs> is not available at nixpath.nix:1:38; use the pkgs
      argument, which is the platform's pinned nixpkgs`, exit 10. — closed:
      the host-01 run quoted in this row (m3-check, 2026-09-21 00:47Z, commit
      954c21f).
- [x] A fragment with `pkgs.fetchurl { url; hash }` for a file not in any
      cache builds (sandbox network for fixed-output works). Evidence:
      `realnix_test.go` `TestRealNixFixedOutputFetch`
      (`REPOSE_NIX_NETWORK=1`); on host-01 (00:47Z) `fragments/fetchurl-ok.nix`
      (nixpkgs' COPYING by url and sha256) built and applied, and
      `~/.m3-copying` in the guest begins "Copyright (c) 2003-2026 Eelco
      Dolstra and the Nixpkgs/NixOS". — closed: the host-01 run quoted in this
      row (`fetchurl-ok.nix` built and applied, 2026-09-21 00:47Z, commit
      954c21f).
- [x] The build runs as `nixbuild`, not root, inside a scope with the
      documented `CPUQuota`, `MemoryMax`, `RuntimeMaxSec`. Evidence
      (host-01 during the bun menu build, 2026-09-20 23:55Z,
      `ops/checks/menu.sh`): `repose-build-<revision>.scope` with
      `CPUQuotaPerSecUSec=8s`, `MemoryMax=17179869184`,
      `RuntimeMaxUSec=30min 30s`, `ControlGroup=/system.slice/repose-build-
      <revision>.scope`, and `ps -eo user,pid,comm` showing `nixbuild 95717
      nix`. Locally `TestWrapArgv`.
- [x] Closure cap test with the cap lowered passes; a real 20 GB+ fragment
      on a host returns `closure_too_large` with ten paths. Evidence:
      `TestRealNixClosureCap` (cap 100 MB, ten paths); on host-01
      (`ops/checks/menu.sh --only-closure-cap`, m3-check, 2026-09-21
      00:52Z, `fragments/closure-cap.nix`, a 21 GB sparse output): `config
      too large: closure is 26.6 GB, limit is 20 GB; largest paths:` then
      ten paths, the 21 GB one first (`21 GB
      /nix/store/…-m3-twenty-one-gb`, then chromium 701.7 MB, the
      playwright browsers, codex, claude-code, go, …), exit 10; no
      `gcroots/repose` entry for it and the path gone after
      `nix-collect-garbage`. The CLI printed no path until DECISIONS I-128.
- [x] GC root exists after a build and is removed after destroy; `nix-
      collect-garbage` on the host does not remove a running guest's
      closure. Evidence: `TestRealNixSuccessRootAndKernelChanged` (the
      root under `gcroots/repose`); `nix-collect-garbage` on host-01 with
      two guests running kept both rooted closures (M1 session,
      `docs/RESEARCH.md` §11: 1.1 GiB freed, guests unaffected). On host-01
      after three builds (00:47Z): `gcroots/repose/<guest id>` plus the
      newest three `rev-<project>-<revision>` roots, and the applied
      closure absent from `nix-store --gc --print-dead` (count 0). After a
      destroy: the guest's root went, the project's `rev-*` roots stayed
      (m3-check, 23:46Z), fixed as DECISIONS I-115 and on host-01 since the
      00:16Z switch; the three synthetic projects destroyed after it left
      no roots. — closed: RESEARCH §11 (collect-garbage with two guests
      running) and the host-01 roots after build and destroy quoted in this
      row (DECISIONS I-115).
- [x] `kernel_changed` is true when the base kernel is bumped and false for
      a package-only change. Evidence: `TestKernelChanged` and
      `TestRealNixSuccessRootAndKernelChanged`; on host-01 through the api
      (2026-09-21, `ops/checks/resilience.sh bump`, evidence
      `ops/checks/out/resilience-20260921T022007Z.txt`): the security
      publish of test base 2026.09.21-m3-0220 (commit 38d1cbf, linux
      7.2.6 instead of the LTS 6.18.52) swept nuru-playground,
      age-calculator, m3-check and m3-iso-c at 02:29:22Z; every `Build`
      reported `kernel_changed` true, every revision ended `built` with
      `reboot_required`, hostd logged "apply needs reboot" and no guest
      rebooted (unit start times unchanged); m3-held (held) got no op.
      `repose stop && repose start` of m3-check booted 7.2.6 (`uname -r`).
      The LTS republish 2026.09.21.3 (main 9d4cb40) at 02:59:18Z gave the
      second result: `kernel_changed` true for m3-check and repose-m5-frag
      (7.2.6 back to 6.18.52, `built` with `reboot_required`) and false
      for age-calculator, m3-iso-c and m3-held (package-only against their
      running kernel, switched in place). The first attempt (02:12Z) found
      every bump built against the base it was leaving (DECISIONS I-134),
      and the LTS sweep found the activation stranding guestd (I-143), the
      clone race (I-144), the wording (I-145) and the skip rule (I-146).

- [x] Menu: every catalog entry has a test; the allowlist lint runs in CI
      and rejects a test entry with `networking.firewall`. Evidence:
      `internal/menu/menu_test.go` `TestEveryEntryRendersAndRoundTrips`,
      `TestRealNixAllEntriesEvaluate`, `TestLintRejectsForbiddenPrefix`
      (`networking.firewall`), `TestAllowlistMatchesNix`; CI `go` job.
- [x] Menu → fragment → edit → takeover flow works end to end through the
      api. Evidence (`ops/checks/menu.sh`, host-01, 2026-09-21 00:47Z, api
      at main with I-119): `PUT /projects/:id/config {menu:[{id:bun}]}` →
      `{revision_id, op_id}`, 40 SSE lines, op done in 5 s, `bun` 1.4.2 on
      PATH in a new login shell with the same boot_id and tmux session;
      `GET /config` → `menu: [{"id":"bun"}]`, fragment beginning `#
      generated by repose from your menu selection…` then `# repose-menu:
      [{"id":"bun"}]`; the fragment edited (a comment before the header)
      and applied with `repose config apply`; then `PUT {menu}` → 409
      `project uses a custom fragment; use fragment mode or reset`;
      applying the api's default fragment puts the project back in menu
      mode. Before I-119 the deployed api served its own 23-entry stand-in
      catalog with no selection line.
- [x] Base bump: publishing a version applies to a non-held project and
      not a held one; a project whose bump fails shows
      `base_update_failed` and keeps working. Evidence:
      `internal/basebump/basebump_test.go` `TestThreeProjects`,
      `TestApplyFailureAndContextCancel`; `internal/api/basebump`
      `TestSweepBuildsUnheldSkipsHeld`. `repose status` for real projects:
      `ops/checks/resilience.sh bump`. — closed: real sweeps on host-01
      (STATUS 2026-09-21 m3-integration lines: the 02:12Z sweep built the four
      unheld projects and skipped m3-held; commit 777cabd); the failed bumps
      of the 02:59Z sweep sent `base_update_failed` and the guests kept
      running on their old system (DECISIONS I-144, I-145, I-146).
- [ ] `scripts/bump-agents.sh` produces a PR on a schedule and the built
      agents print their versions. Evidence: a merged PR link and CI log.
      The workflow exists (`.github/workflows/bump-agents.yml`) and every
      overlay package prints its version locally (STATUS 12); no run has
      happened on GitHub yet (owner). — open, half closed (2026-09-26): the
      workflow's flake update was fixed in 01b6243; scheduled runs pass
      (35984425847, 36122532223) and open PRs; #1 and #3 are merged
      (https://github.com/Heracraft/factory/pull/3). Still missing: a CI log
      of the PR building the agents and printing their versions. The ci
      workflow on bump PRs waits for approval (`action_required`) and was
      never run before merging; approve it on the next bump PR (#4 is open)
      and paste that log.
- [ ] Overlay cache is populated and a fresh host substitutes the agents
      instead of fetching upstream (`nix build --print-build-logs` shows
      `copying path ... from <cache>`). Evidence: pasted. Waits on the
      owner's Cachix cache and `CACHIX_AUTH_TOKEN` (`ops/AZURE-SETUP.md`
      step 16, DECISIONS I-46). — waits on: owner (the Cachix cache `repose`
      and `CACHIX_AUTH_TOKEN` in the repository secrets, AZURE-SETUP step 16).
- [x] `RESEARCH.md` records eval and build timings for the base plus a
      typical fragment on a host (so 05 can set user expectations).
      Evidence: `docs/RESEARCH.md` §15 (dev box), §12 (host-01: the first
      `Build`, 35.0 s, eval 13.0 s, build 21.9 s) and §13 (host-01 through
      the api: four consecutive fragment builds at 5.1 s each, eval 4.7 s,
      build 0.33 s, closure 6.0 GB; the bun menu apply in 5 s).
- [x] `ops/RUNBOOK.md` entries: build stuck, cache unreachable, base bump
      failures, closure collected. Evidence: "Build stuck (no BuildLog line
      for 10 minutes)", "Build: cache unreachable", "Base bump failures",
      "Closure collected under a running guest".
- [x] `rg 'TODO|FIXME|not implemented' internal/nixbuild internal/menu
      internal/basebump nix/guest/compose.nix nix/guest/fragment.nix
      nix/overlay scripts/bump-agents.sh` empty. Evidence (2026-09-20):
      empty over `internal/hostd/nixbuild internal/menu internal/basebump
      nix/guest/compose.nix nix/guest/contract.nix nix/overlay
      scripts/bump-agents.sh` (the package is `internal/hostd/nixbuild`
      and the contract module `nix/guest/contract.nix`, DECISIONS I-45,
      I-43).
