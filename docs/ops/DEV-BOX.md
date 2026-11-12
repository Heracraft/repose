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
