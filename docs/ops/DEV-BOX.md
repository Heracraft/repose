# The dev box

`woker-1` in resource group `dev-cluster`, `Standard_D8alds_v7` (AMD, 8 vCPU,
16 GB), East US, Tailscale `100.89.156.32`. This is where docs are written,
agents run, and Go and Nix builds happen. It is **not** a Repose host: it is
an AMD size and cannot tell you anything about nested virtualization.

## The Nix store lives on the temp disk

The OS disk is 30 GB, which a swarm of agents building guest closures fills
in an afternoon. The size comes with a 440 GB local temp disk (`nvme1n1`)
that is fast and free, so `/nix` is bind-mounted from it:

```
/dev/nvme1n1  ->  /mnt/nixstore  -bind->  /nix
```

Set up by `sudo scripts/nix-store-on-tempdisk.sh`, which is idempotent.

**What "temp disk" means.** It is scratch space on the physical server the
VM runs on, and it does not belong to the VM. A reboot from
inside the VM keeps it. **Stopping or deallocating the VM from Azure, or an
Azure maintenance move, brings it back empty.** Project closures are a cache
that rebuilds from `nix/flake.lock`, so nothing is lost there except time:
expect 10 to 20 minutes of downloads to get a guest closure back.

**The user environment is not just a cache, and used to be unrecoverable.**
This bit the owner once; it is why `~/config` exists. `~/.nix-profile` points
through `~/.local/state/nix/profiles/profile` into the store. Those generation
symlinks are on the OS disk and survive the wipe, but what they point at does
not, so the chain still looks intact and resolves to nothing: the whole
toolchain (`git`, `go`, `node`, `python`, `rust`, `nix`, `home-manager`)
disappears with no error to explain it. Worse, home-manager writes `~/.bashrc`
and `~/.profile` as symlinks **into the store**, so the shell config goes too
-- no aliases, no prompt, no PATH.

**After a deallocate**, run:

```
sudo ~/config/bin/restore-nix-env.sh
```

It calls `scripts/nix-store-on-tempdisk.sh` (reformat, remount, reinstall
Nix), then clears the generation symlinks that now dangle -- a fresh Nix
alongside 22 dead generations will not build a new one -- rebuilds the user
environment from `~/config` (a home-manager flake on the OS disk), and
verifies the toolchain came back. Then `nix develop ./nix` again. Anything in
`~/.cache/nix` and `~/go` is on the OS disk and survives.

A login shell also warns on its own via `/etc/profile.d/00-check-nix-store.sh`,
which is a real file on the OS disk and outside home-manager's control, so it
still runs when every dotfile is a dangling symlink. Both fstab entries carry
`nofail`, so without that warning the box boots clean and silent with an empty
`/nix`.

**Never bake an absolute `/nix/store/...` path into a file that lives on the
OS disk.** `~/.gitconfig` did this and broke git auth after the wipe; the
culprit was home-manager's `programs.gh.gitCredentialHelper`, now disabled in
`~/config/home.nix` in favour of a PATH-resolved `!gh auth git-credential`.
Equally, do not `nix profile install` a tool: that creates a generation with
no source on the OS disk, which is the whole problem. Add it to
`home.packages` in `~/config` and switch.

**Do not put anything that matters on `/mnt/nixstore`** other than the
store. Worktrees, the repo, `~/.azure`, `~/.config` all stay on the OS disk.

If the box gets resized to a size without a local disk (no `d` after the
size number, e.g. `D8as_v7`), the script refuses at step 0; move `/nix` back
to the OS disk first (`umount /nix; mv /mnt/nixstore /nix` with the OS disk
grown to fit).

## Azure CLI here

`az` is not installed on the box. `nix develop ./nix` provides it, as does
`nix shell nixpkgs#azure-cli`. Login is the copied `~/.azure` from a laptop
browser login, because security defaults block device-code login from a
browserless VM. The refresh token lasts about 90 days of inactivity.

## Free space

Keep 40 GB free on `/nix` before launching a wave of agents; `df -h /nix`.
`nix-collect-garbage -d` reclaims old closures; it never touches anything
the current flake lock references once rebuilt.

## VM tests need KVM

`nix flake check ./nix` runs the host NixOS VM tests (`nix/hosts/tests`).
They are declared as needing the `kvm` builder feature; without it Nix
refuses to build them, and without `/dev/kvm` access they would crawl
under emulation. One-time setup on the box:

```
sudo usermod -aG kvm azureuser
echo 'system-features = nixos-test benchmark big-parallel kvm uid-range' >> ~/.config/nix/nix.conf
```

Log in again (or prefix one command with `sg kvm -c '...'`) so the group
applies. Each test boots one or two VMs and takes two to five minutes.

## Heavy Nix runs in a memory-capped scope, one at a time

This box has 16 GB and usually a dozen Claude sessions on it. A
whole-flake `nix flake check ./nix` evaluates every host, edge and guest
system in one process and wants **8.5 to 12 GB**; the evaluator's memory,
not parallel building, is what does not fit, so `--max-jobs 1` changes
nothing. It has now cost three mini-conductor sessions over two nights:
2026-11-19 twice, and 2026-11-20 at 06:06 UTC, where the kernel killed
`nix` at 12.5 GB RSS and the session died minutes later from the pressure
around it rather than from the kill.

So **do not run `nix flake check` with its builds here at all.** Build
what you need one check at a time, each inside a cap:

```
flock /mnt/nixstore/repose-ws/nix-build.lock \
  systemd-run --user --scope -p MemoryMax=6G --quiet -- \
  choom -n 1000 -- nix build --no-link -L ./nix#checks.x86_64-linux.<name>
```

Each piece earns its place. `flock` keeps one heavy job on the box at a
time. `systemd-run --user --scope -p MemoryMax=6G` is the part that
protects the sessions: the cgroup kills nix at the cap instead of letting
the box swap, so a bad evaluation loses a build rather than somebody's
context. `choom -n 1000` only decides who dies once the box is already in
trouble, which is why it was not enough on its own. Verified on
2026-11-20: a 512M test scope reported `memory.max` 536870912, and
`nix build ./nix#guest-system`, the `guest-closure-size` check and the
whole `guest-base` VM test (29 subtests) all completed inside 6 GB.

Point `TMPDIR` at the big disk while you are at it: **`/mnt/nixstore/tmp`**.
`/tmp` is on the 29 GB root filesystem, a NixOS VM test writes its disk
images into `TMPDIR`, and on 2026-11-20 the root was at 84 % with 4.7 GB
free while a 2 GB appliance image sat staged in `/tmp/rt`. A VM series
that fills the root fails in a way that looks like anything but disk. The
whole invocation is then:

```
flock /mnt/nixstore/repose-ws/nix-build.lock \
  systemd-run --user --scope -p MemoryMax=6G --quiet -- \
  env TMPDIR=/mnt/nixstore/tmp choom -n 1000 -- \
  nix build --no-link -L ./nix#checks.x86_64-linux.<name>
```

**That variable is for `nix` only. Do not export it for `go test`.** One
test in `internal/cli`, `TestClaudeSettingsMergeGolden/rewrite`, compares
a golden whose line order was produced by sorting *unnormalised* paths:
the merge script's jq ends in `unique`, which sorts, and the test
substitutes the stable home into the output only afterwards. One of the
sorted strings is the run's own temporary path, so with `/mnt` sorting
before `/opt` and `/opt` before `/tmp`, a Go run under
`/mnt/nixstore/tmp` fails it and the same run under `/tmp/rt` passes.
Proven both ways in about a tenth of a second each on 2026-11-20, and it
cost the mac-mini conductor a red that its own verification script had
manufactured by putting this variable at the top rather than inside the
nix invocation. Until ENG-139 is fixed, keep Go runs on `/tmp/rt` (or
`/tmp/r`, see below) and this one inside the capped `nix` command.

Run anything long **detached** (`setsid`, redirect to a log) and watch the
log with a `Monitor`: the harness kills a session's background waiters
when memory is low, and the job then finishes with nobody reading it.

`ops/dev/release-queue add` evaluates the systems for its ships
computation, so it counts as a nix evaluation too: do not run it beside
another one.

## aarch64 builds go through a remote builder

This box is x86 with no `binfmt`, so nothing `aarch64-linux` builds
here: `nix build ./nix#packages.aarch64-linux.guest-system` with no
builder fails at the first derivation. Evaluation is another matter:
every aarch64 output evaluates here (`nix eval --raw
./nix#packages.aarch64-linux.guest-system.drvPath`), which is how a
change to the flake is checked before a Mac day, and it costs the same
memory as an x86 evaluation, so it goes through the build lock like
every other `nix` command (`docs/ops/ORCHESTRATION.md`).

`ops/dev/aarch64-base` runs the three things an aarch64 build day needs:
`check` (the closure limit and that the runner, host and installer
build), `build` (the guest system, printing its closure size) and `push`
(the aarch64 agent overlay to Cachix). With `--builder ssh-ng://<user>@<host>`
it evaluates here, under the lock, and builds there; with no builder it
expects to be running on the aarch64 machine itself (the Mac's appliance
VM, where the lock does not apply) and builds locally. The builder is
the appliance VM's ssh address from the spark (`ops/dev/macmini ssh`
prints the Mac; the VM is behind it), with this box's key in its
`authorized_keys` and `nix` on its PATH.

The one thing CI builds for aarch64 is the agent overlay, on a GitHub
arm64 runner (`ci.yml`, job `nix-aarch64-overlay`), pushed to Cachix
the way the x86 overlay is. The base closure itself is never pushed:
it is 6 GiB of mostly cache.nixos.org paths, and the Cachix cache is
the free 5 GB tier (I-46). An aarch64 host builds its base the way an
x86 host does, from cache.nixos.org plus the overlay from Cachix.

## A check that is not installed fails in zero seconds

`golangci-lint` is **not on `PATH` outside `nix develop ./nix`**. A
verification run that calls it from a plain shell reports `FAIL lint`
immediately, which reads exactly like a real red and is not one: nothing
was linted. The mac-mini conductor lost a verification run to this on
2026-11-20, and every "lint 0 issues" on that program came from a session
that happened to have the dev shell. Run it as `just lint`, or inside
`nix develop ./nix -c sh -c '...'` with the rest of the Go set, and read
the tool's own first line rather than only its exit code: zero seconds is
the tell.

This is the same family as a check's exit read through a pipe
(`CONDUCTOR-HANDOFF.md`, Lessons) and as a filtered `go test` that matches
nothing and prints `ok`. An absent check and a passing check are
indistinguishable from the exit code alone.

## Go tests and Playwright

Inside `nix develop` the temporary directory is `/tmp/nix-shell.*`, and the
tests that put unix sockets under `t.TempDir()` (tmux and vsock fakes) fail
with "File name too long" on the socket path limit. Run them with a short
one, set **inside** the shell: `mkdir -p /tmp/rt && nix develop ./nix -c sh
-c 'TMPDIR=/tmp/rt go test ./...'`. A `TMPDIR` set outside is replaced by
the shell with `/tmp/rt/nix-shell.*`, which is 17 bytes longer, and the
longest test name (`internal/guestd`'s
`TestStraceReadsEnvironOnlyForTheHookPaneLookup`) then lands on 108
bytes exactly and fails with `bind: invalid argument`, on main as on any
branch (2026-11-12).

Run the Go tests in the dev shell or with `DATABASE_URL` set. Outside the
shell with neither, `internal/db/testdb` finds no `initdb` on PATH and
every database-backed test skips, printing `ok` in milliseconds.

Playwright's downloaded Chromium does not run here (glibc mismatch). Point
the web tests at a nixpkgs one; the configs in `apps/web` read
`PLAYWRIGHT_CHROMIUM_PATH`:

```
export PLAYWRIGHT_CHROMIUM_PATH=$(nix build --no-link --print-out-paths nixpkgs#chromium)/bin/chromium
```

## Things the 2026-11-11 conductor had to find out (keep current)

- **Docker's bridge is missing** (`docker0` does not exist): any container
  must run with `--network host` and pick its own port. A throwaway Postgres
  for the pre-release run:
  `docker run -d --name relcheck-pg --network host -e POSTGRES_USER=relcheck -e POSTGRES_PASSWORD=relcheck -e POSTGRES_DB=relcheck -e PGPORT=55433 postgres:16-alpine`,
  then `DATABASE_URL='postgres://relcheck:relcheck@127.0.0.1:55433/relcheck?sslmode=disable'`.
  `docker rm -f relcheck-pg` when done.
- **The harness kills a background command when the root disk is nearly
  full**, and it says "low on memory" when it does. Root is 29 GB. What fills
  it: `~/.cache/go-build` (2 GB a day), `apps/web/test-results` and
  `playwright-report` in every worktree, Docker's build cache (`docker builder
  prune -af`, 2 GB), `/tmp/rt`. Free those before a batch; `df -h /` under
  85 % is safe. A VM check that must not be killed runs in a detached private
  tmux (`tmux -L relcheck new-session -d '... > log'`), outside the harness.
- **Memory**: 16 GB. A VM check (3 GB guest) alongside `go test -race ./...`
  and playwright's Chromium is what gets killed. Run the VM checks after the
  test runs, one at a time.
- **Lighthouse is not here**: the a11y playwright suite fails on every test
  with `spawn lighthouse ENOENT`. CI installs it (`npm i -g lighthouse` in
  `ci.yml`); treat that suite as CI-only on this box.
- **`repose` is not installed.** Build it (`nix develop ./nix -c go build -o
  /tmp/repose ./cmd/repose`) or install the released one into a scratch HOME
  (`HOME=/tmp/h sh -c 'curl -fsSL https://repose.run/install.sh | sh'`). The
  owner's login is in `~/.config/repose/credentials.json`, so a built CLI acts
  as the owner: throwaway machines only (`repose run --temp 2h --size small -d`
  from a scratch git repo; `repose rm NAME --yes` after). `repose sync` with
  no PROJECT resolves the directory and will create a project; name it.
- **`az` lives in `nix develop ./nix#infra`** (with `tofu`); the login is the
  owner's. `tofu` state for staging: never `make plan ENV=staging` without
  `staging.local.tfvars` (gone from this box since the checkout moved; the
  host lives only in that file, so a plan without it proposes destroying it).
- **Live machines**: edge `ssh -p 2222 root@20.102.98.254`; control VM
  `ssh -J root@20.102.98.254:2222 root@10.255.255.1` (api containers are
  named by Coolify uuid prefix: `8kpq…` api, `v2fe…` grpc, `kw2g…` web;
  `docker exec` the api one for `repose-admin`; the image has no `sh`, so a
  file argument is `-i` with `/dev/stdin`); host-02 `ssh -F
  ~/.ssh/repose-operator.conf host-02` (host-01 is retired since
  2026-11-02); staging `ssh -F ~/repose-ws/staging-ssh-config stg-edge|stg-host`,
  hostdev at `/var/lib/hostdev/hostdev --state-dir /var/lib/hostdev` on the
  staging edge (`exec` prints base64 JSON; decode `.exec.stdout`). Run
  `ops/dev/operator-cert.sh --if-stale` first (8 h certificate).
- **`guest-keystroke-path` cannot pass here.** This box's nix has no build
  users (`build-users-group` is empty), so a build runs as `azureuser`, and
  the check's `ulimit -u 128` counts every process of that user, about a
  hundred before the build starts: every handler fails with `fork: retry:
  Resource temporarily unavailable`, on main too. CI builds it with build
  users (`ci.yml`) and is its gate; do not call it the batch's.
- **Switch from a clean checkout.** A switch evaluated in a dirty checkout
  stamps `-dirty` into hostd's version (host-02 said `374aeab-dirty` for two
  minutes on 2026-11-11). `git worktree add --detach ~/kanali-release-<sha> <sha>`.

## Things the 2026-11-18 conductor had to find out (keep current)

The conductor session on this box is named `conductor`; its log is in
its scratchpad (`CONDUCTOR-LOG.md` under
`/tmp/claude-1000/-home-azureuser-projects-factory/8fd79c42-.../scratchpad/`),
one section per batch and release from 2026-11-11 to 2026-11-18.

- **A signed-in dashboard session, without the owner's browser.**
  `ops/dev/dashboard-session [logto-user-id] [out]` mints one as the
  owner (Logto id `c7fh26yzrl93`): the M2M app `t9ogvrzosgfpq1361ua4z`
  (now holding the Management API role) asks Logto for a subject token for
  the user, the dashboard's client `osfcu4s5rg0p6tn91lpb8` exchanges it for
  an api token (its `allowTokenExchange` was set true on 2026-11-18), and
  the tool writes Playwright's storage state in the `@logto/browser` keys
  to `apps/web/tests-live/.auth/state.json` (0600). The M2M secret lives
  in `~/.config/repose-ops/logto-m2m-secret` (0600, written with
  `read -rs`, never pasted in chat). The session lasts an hour and has no
  refresh token, so `account.spec.ts`'s refresh test and its "lands on
  /projects" test fail by design; every project page, Settings and the
  api work. Logto's token endpoint answers 403 to Python's default
  User-Agent; the tool sends its own.
- **The queue is the pull requests since I-1352** (`docs/PR.md`): agents
  queue with `release-queue add --body pr.md`, which pushes the branch and
  opens a PR against `integrated`; `RQ_PR_OFF=1` keeps the old local
  entry. A batch cut by the old script does not unlabel its PR when
  `done` pushes `integrated`; take `queued` off by hand.
- **Never record a check's exit through a pipe.** `cmd | tail -1; echo
  $?` records tail's exit; a red eslint and a failed build hid behind it
  on 2026-11-15. Write each check to its own file and read the status
  from the command itself.
- **The throwaway Postgres leaves an anonymous volume** on `docker rm
  -f`; 13 of them held 4 GB by 2026-11-18. Use `docker rm -f -v`.
- **The queue's path analysis and `closure-equiv.py` disagree** about
  what reaches hostd and the gateway: a branch that changed only
  `internal/cli` queued as `base,cli,web` while the closure comparison said
  the host-02 and edge systems really change (their units embed the
  binaries). Both switches were done; which of the two is wrong is open.
- **VM checks that are red for reasons that are not the batch's**:
  `guest-base`'s I-264 keys probe under load (green alone);
  `guest-keystroke-path` on this box's single-user nix (CI's gate);
  `guest-tools-carry`'s I-417 subtest, fixed on 2026-11-18 (the harness's
  /tmp mounts and a boot longer than the purge timer).
- **Burst on host-02 (I-1326)**: a guest's cap rises a core a tick while
  it is hungry and is lowered only when the gate shuts, so a quiet guest
  keeps the cores it earned; a new small reads `cpu.max 400000 100000`
  after its tools install. Not a defect.
- **Owed live checks that need a signed-in dashboard** (the tool above
  now gives one): dash-acts' say field and Revoke certificates (revoking
  cuts the owner's own SSH, so do it on a throwaway account or not at
  all), browser-view's frames and clipboard on a live machine,
  notify-hold's typing hold.
- **The owner's product rules of this week** are in the conductor's
  memory and in DECISIONS I-1266: a feature is not done until the
  dashboard can create, change, run and remove it; and on 2026-11-18 the
  owner called `repose say`'s mechanism and UI ugly and wants it
  redesigned as a conversation, not polished.
- `tmp-wyyd` and the stopped `e2e-*` snapshots are the owner's; the
  conductor's throwaways are destroyed after each check.
- **A batch worktree's web checks need their own modules**: `pnpm install
  --frozen-lockfile` in the worktree, under two seconds from the store. A
  symlink to the main checkout's `node_modules` carries that checkout's
  age (it lacked `@novnc/novnc` on 2026-11-19 and made vitest, the build
  and playwright red for a reason that was not the batch's).
