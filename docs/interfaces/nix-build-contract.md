# Nix build contract: hostd ⇄ the platform flake

What hostd runs when it receives `Build` (docs/interfaces/grpc-hostd.md),
what the platform flake in `nix/` exposes for it, and what the user reads
when it fails. Workstream 03 invokes; workstream 12 authors the Nix, the
flags, the limits and the messages (`internal/hostd/nixbuild` is the one
implementation of both). DECISIONS I-28, I-43.

## The flake

The repository's `nix/flake.nix`, checked out at
`/var/lib/repose/base/<base_ref>/` (hostd clones the repository with
`--base-repo-url` when the checkout is missing, over SSH with
`--base-repo-ssh-key` for a private repository; `base_ref` is a git
revision), declares:

```nix
inputs.fragment = { url = "path:./guest/fragment-placeholder"; flake = false; };
outputs = { self, nixpkgs, home-manager, microvm, fragment, ... }: {
  # A NixOS system: base module + home-manager fragment at
  # "${fragment}/fragment.nix" applied to user dev (nix/guest/compose.nix,
  # nix/guest/contract.nix). Only config.system.build.toplevel is required.
  guestSystem = <nixosSystem>;
};
```

The placeholder input exists so the lock file is valid; hostd always
overrides it. The system closure is independent of the guest's class and
address (DECISIONS I-34, I-43), so one `Build` per revision serves the
project's guest wherever it runs. The only inputs are the fragment's text,
the `base_ref` and the `base_version` label, which is why the api sends no
`Build` for a create whose fragment and published base match a closure
already applied on the chosen host (DECISIONS I-160). A change that lets
anything else into the closure (the project id, the class, a timestamp)
breaks that reuse and needs a new decision.

## What hostd runs

For a `Build` with `revision_id` R, `fragment` F, `base_ref` B and `limits`
L, in order:

1. Writes F to `/var/lib/repose/builds/R/fragment.nix` (0600), and the
   `Build`'s `base_version` label, when given, to `base-version` beside it
   (one line; DECISIONS I-118), then hands the directory to the build user
   (`--build-user`, `nixbuild` on a host; the builds directory is 0711 so
   that user reaches its own directory and nothing else). The flake's
   `guestSystem` stamps `repose.baseVersion` (`/etc/repose/base-version`,
   the NixOS label) from that file; without it the stamp is the flake's
   own `shortRev`, which a flake evaluated with `--override-input` does not
   have, so it reads `dirty`.
2. Reads `/var/lib/repose/base/B/nix/flake.lock` and derives
   `allowed-uris`: every locked input exactly as Nix names it
   (`github:<owner>/<repo>/<rev>?narHash=<hash>`), plus
   `path:/var/lib/repose/builds/R`. The flake machinery fetches locked
   inputs during evaluation and restricted mode refuses any URI not listed;
   a URI with the revision and hash admits that one tree and nothing a
   fragment could name (`internal/hostd/nixbuild/lock.go`).
3. Evaluates, inside a transient scope as the build user:

   ```
   systemd-run --scope --unit repose-build-R-eval \
     -p CPUQuota=<L.cores*100>% -p MemoryMax=16G -p RuntimeMaxSec=<L.eval_s+30> -- \
   setpriv --reuid=nixbuild --regid=nixbuild --init-groups \
     --bounding-set=-all --inh-caps=-all --no-new-privs -- \
   env HOME=/var/lib/repose/nixbuild USER=nixbuild LOGNAME=nixbuild NIX_REMOTE=daemon \
   timeout -k 5 <L.eval_s> nix eval --raw --no-write-lock-file --show-trace \
     --option restrict-eval true --option allow-import-from-derivation false \
     --option pure-eval true --option eval-cache false \
     --option allowed-uris "<step 2>" --max-call-depth 10000 \
     --override-input fragment path:/var/lib/repose/builds/R \
     "git+file:///var/lib/repose/base/B?dir=nix#guestSystem.config.system.build.toplevel.drvPath"
   ```

   The flake is named through `git+file://` with `dir=nix`, not `path:`,
   because `nix/packages.nix` builds the Go binaries from the repository
   root, which a `path:` flake rooted at `nix/` cannot see. `--show-trace`
   is what carries the fragment's line and column for errors raised inside
   the module system (a truncated trace loses them).

   `timeout` exiting 124, or the scope's `RuntimeMaxSec` killing the
   command after the cap, is `eval_failed` "evaluation exceeded <eval_s>
   s". Any other failure is `eval_failed` with a summary line (below),
   `fragment_line` from the first `fragment.nix:L:C` after the final
   `error:` line, else the last one in the trace, and the verbatim stderr
   (last 32 KB) after a blank line.
   Before evaluating, hostd looks the inputs up in its eval cache
   (`/var/lib/repose/builds/.evalcache/<key>`, DECISIONS I-405). The key
   is a SHA-256 over B, the flake's subdirectory and scheme, the eval
   attribute, the `base_version` label and F, which with `pure-eval`,
   `restrict-eval` and no import-from-derivation is all the evaluation
   reads. A hit whose `.drv` is still in the store (`nix path-info`)
   skips the `nix eval` above and goes on to step 4 with that derivation;
   the build log still prints `evaluating configuration`. An entry is
   written only after a build that passed steps 4 to 6, and a hit whose
   build fails is removed and the whole build runs again with a fresh
   evaluation, so a cached evaluation never changes a build's outcome.
4. Builds the derivation, same scope shape (`--unit repose-build-R`,
   `RuntimeMaxSec=<L.build_s+30>`), same user:

   ```
   timeout -k 5 <L.build_s> nix build --no-link --print-out-paths --print-build-logs \
     --option sandbox true --max-jobs 2 --cores <L.cores> \
     --option substituters "https://cache.nixos.org <overlay cache>" \
     <drvPath>^*
   ```

   `--max-jobs 2` since DECISIONS I-326 (it was 1). Every stderr line is streamed as `BuildLog`, with the step lines of
   `docs/interfaces/api.md` "Build log lines" (I-320). A timeout is
   `build_timeout` "build timed out after 30 minutes while building
   <derivation>" (the cap printed in minutes when it is whole minutes,
   else seconds). Any other failure is `build_failed` "build of <derivation>
   failed", the verbatim tail, then the last 200 lines of that derivation's
   `nix log`. A substituter failure line in the output sets the host
   warning `cache_unreachable`; the build goes on from source.
5. `nix path-info -S <out>`; over `L.closure_bytes` is `closure_too_large`
   "closure is 31.2 GB, limit is 20 GB; largest paths:" followed by the ten
   largest paths from `nix path-info -rs`, one per line, and no GC root.
6. Registers `/nix/var/nix/gcroots/repose/rev-<project_id>-<revision_id>`
   and keeps the newest three per project.
7. Reads `<out>/kernel` and `<out>/initrd` targets; `kernel_changed` is
   true when either differs from what the project's guest last booted or
   applied (`nixbuild.KernelChanged`).

The attribute path after `#` is the hostd flag `--eval-attr`
(default `guestSystem.config.system.build.toplevel.drvPath`), so 12 can
rename the output without a hostd change. The daemon on a host caps the
same numbers (`nix/hosts/gc.nix`: sandbox, max-jobs 2, cores 8, the
substituters), so an unprivileged client passing them changes nothing it
could not already have.

## What the user reads

`message` is a summary line, a blank line, then the verbatim output. The
CLI prints the summary with a prefix chosen by the code, the fragment line
with two lines of context from its local copy when `fragment_line` is set,
then the verbatim block; the dashboard shows the same.

| Code | CLI prefix | Summary line |
|---|---|---|
| `eval_failed` | `config error: ` | `syntax error at fragment.nix:1:32, unexpected ';'` |
| `eval_failed` | `config error: ` | `attribute 'ripgrepp' missing at fragment.nix:1:22 (did you mean ripgrep?)` |
| `eval_failed` | `config error: ` | `eval-time fetch not allowed at fragment.nix:1:39; use pkgs.fetchurl { url = ...; hash = ...; }` |
| `eval_failed` | `config error: ` | `access to absolute path '/etc/passwd' is forbidden in pure evaluation mode (use '--impure' to override) at fragment.nix:1:37; a fragment may only read files it carries` |
| `eval_failed` | `config error: ` | `<nixpkgs> is not available at fragment.nix:1:44; use the pkgs argument, which is the platform's pinned nixpkgs` |
| `eval_failed` | `config error: ` | `import-from-derivation is not allowed at fragment.nix:1:37; a fragment cannot import a file that a build produces` |
| `eval_failed` | `config error: ` | `option 'services.postgresql' does not exist in a fragment; a fragment is a home-manager module: packages go in home.packages, databases come from `repose config add` or repose.system` |
| `eval_failed` | `config error: ` | `this file is a Nix flake; `repose config apply` takes a home-manager module such as repose.nix (https://repose.herakraft.co/docs/config#write-it-in-nix), and agents already load a flake's dev shell from the checkout` (the unknown option is `description`, `inputs`, `outputs` or `nixConfig`; no did-you-mean, DECISIONS I-483) |
| `eval_failed` | `config error: ` | `repose.system: option 'networking.firewall' is not allowed in a fragment; system services come from `repose config add` or the dashboard's Config menu (allowed: ...)` |
| `eval_failed` | `config error: ` | `nixpkgs has no package "no-such-package"; search https://search.nixos.org/packages at fragment.nix:11:23` (a menu `{package}` item nixpkgs lacks, thrown by the generated fragment; `repose config add` drops the ` at fragment.nix:…` part and the verbatim block, DECISIONS I-220) |
| `eval_failed` | `config error: ` | `nixpkgs attribute "python312Packages" is not a package; search https://search.nixos.org/packages at fragment.nix:13:10` |
| `eval_failed` | `config error: ` | `evaluation exceeded 60 s` |
| `build_timeout` | none | `build timed out after 30 minutes while building sleep-forever-1.0` |
| `build_failed` | none | `build of fails-1.0 failed` (`; build exceeded 16 GB RAM` when the builder was killed) |
| `closure_too_large` | `config too large: ` | `closure is 31.2 GB, limit is 20 GB; largest paths:` then ten paths |

Line and column are Nix's; the columns in the workstream doc's examples
are illustrative. The code `eval_timeout` in the workstream doc's table is
the interface's `eval_failed` with the message above: the error code enum
in `grpc-hostd.md` is what the api, the CLI and the dashboard switch on,
and the message tells the two apart.

## What the built closure must contain

hostd boots the guest from the closure directly (DECISIONS I-27): it needs
`<out>/kernel`, `<out>/initrd`, `<out>/init` and `<out>/kernel-params`,
which every NixOS toplevel has. Anything microvm.nix needs on the kernel
command line must be in `boot.kernelParams`; hostd appends `init=`,
`console=ttyS0` and `ip=<guest>::<gateway>:<netmask>::eth0:off`.

## Fixtures and the test corpus

Real Nix output for the error mapping lives in
`internal/hostd/nixbuild/testdata/*.stderr`, produced by exactly the eval
command above against the platform flake (`TestMapEvalErrorFixtures`
pins the summary line and `fragment_line` of each). `realnix_test.go` runs
the whole pipeline against `testdata/miniflake` (the same `fragment` input
and `guestSystem` output, nixpkgs at the platform's locked revision, no
home-manager) when `REPOSE_NIX_TESTS=1`: the five canonical cases with the
timeout lowered to 5 s and the cap to 100 MB, the restrict-eval checks,
the success path with its GC root, and `kernel_changed` across a
package-only change and a kernel bump. The fixed-output fetch case needs
the network (`REPOSE_NIX_NETWORK=1`).
