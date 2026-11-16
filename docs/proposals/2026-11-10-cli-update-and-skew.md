# Updating the laptop CLI, and saying when an old one matters (proposal, 2026-11-10)

**Status: proposal; nothing here is decided.** The product value council
(`reports/2026-11-04-product-value-council.md`, "install-login-cli", all
four members) asked for `repose update` and a version-skew warning on
attach. This file checks main as of `a408bf2f` (2026-11-06). Half of the
request is already on main: the api names the newest release and an
older CLI says so. What is left is the update command, its placement,
the warning's text, and a stronger warning for a CLI that the api no
longer serves in full.

## Summary

| Question | Answer |
|---|---|
| Does a skew warning exist? | Yes, since I-626 (2026-10-09). The api sends `X-Repose-CLI-Latest` on every answer; a release build older than it prints one line on stderr, once per newer release, at the end of any command or just before an attach hands over to ssh |
| How does the api know the latest release? | It reads the `Location` of `https://github.com/heracraft/repose/releases/latest` once an hour (`internal/api/clirelease/clirelease.go:23-27,77-101`), the same redirect `install.sh` follows |
| How does the machine know the laptop's version? | `run` and `attach` write it to `~/.repose/cli-version` as a carry part, once per version (`internal/cli/carry.go:253-257,360`, I-412) |
| Does an update command exist? | No. `install.md:71` says "Update command coming soon", an exception the owner kept on purpose until the command ships (I-749) |
| Proposed placement | `repose version update`, under the existing `version` noun. Alternative: top-level `repose update` (decision 1) |
| Checks | The same as `install.sh`: the signature on `checksums.txt` against the release key, then the archive against `checksums.txt`. Nothing is replaced unless both pass |
| Package managers | A binary under `/nix/store`, a Homebrew Cellar, `/usr/bin`, `/bin` or `/snap` is never replaced; the command names the owner and exits 1 |
| Windows | No Windows release exists (`.goreleaser.yaml:13-15`); WSL is Linux and works as Linux does |
| "Only when it matters" | Keep the once-per-release line, with new text. Add one line that prints on every `run`, `attach` and `sync` while the CLI is older than the oldest release the api still serves in full (`X-Repose-CLI-Oldest`, new) |
| Size | M: the command and its tests are most of it; the header is S |

## 1. The repo today

**The notice.** `internal/cli/cliversion.go:14-71` keeps the header's
value (`noteLatestCLI`, called from the client) and prints, through
`noteNewerCLITo` at `:51-71`:

```
repose 0.1.43 is older than v0.1.44, the latest release. `curl -fsSL https://repose.herakraft.co/install.sh | sh` updates it.
```

It prints once per newer release (it records the release in
`~/.config/repose/cli-latest-noticed`, `:22-23,64-70`). It never prints
on a machine (`REPOSE=1`, `:54`; `internal/cli/env.go:34`), for a
development or pre-release build (`parseVersion` at `:89-107` refuses
`dev` and anything with `-` or `+`), after shell completion or a hidden
command (`noticeAfter`, `internal/cli/cli.go:31-41`). It is called at the
end of each command (`cli.go:67-72`) and just before an attach replaces
the process with ssh (`internal/cli/ssh_exec.go:180`).

Two flaws in the line itself. Release builds carry the version without
its `v` (`.goreleaser.yaml:20` sets `main.version={{ .Version }}`, and
`cmd/repose/main.go:13` defaults to `dev`), while the header carries the
tag with it, so the real line mixes `0.1.43` and `v0.1.44`. The test
uses `v0.1.20` on both sides and does not see this
(`internal/cli/devx_errors_test.go:103-120`). And the line names a
command of 55 characters that a user must copy.

**The api side.** `clirelease.Latest.Fetch` follows no redirect and
keeps the tag only when it matches `^v[0-9]+\.[0-9]+\.[0-9]+$`
(`clirelease.go:29,77-101`); a failed read keeps the last good one. The
app starts it (`internal/api/app/app.go:279`) and the server sets the
header (`internal/api/http/server.go:372-376`). The contract is
`docs/interfaces/api.md:27-30`. The CLI sends `User-Agent:
repose-cli/<version>` (`internal/cli/client.go:139`), and the api logs
only `client: cli` (I-626). I-626 left out "a minimum version with its
own exit code (the api has no version it refuses; nothing to enforce
yet)".

**The machine side.** The agent guide tells agents to compare
`~/.repose/cli-version` with the docs and, when it is older, to "tell
them to update by running the install command again"
(`nix/guest/base/agent-guide.md:13`). The file is listed in
`docs/interfaces/guest-conventions.md:939`.

**Install and its checks.** `install.sh` (216 lines) resolves `latest`
through the same redirect (`:108-117`), downloads
`repose_<tag>_<os>_<arch>.tar.gz` and `checksums.txt` (`:123-130`),
checks the signature `checksums.txt.sig` with `openssl dgst -sha256
-verify` against the ECDSA P-256 key it carries (`:17-21,138-158`),
checks the archive against `checksums.txt` (`:160-164`), and installs
with `install -m 755` into `~/.local/bin` or, with `--system`, into
`/usr/local/bin` through `sudo` (`:168-174`). Releases up to v0.1.27 are
pinned by hash instead (`:22-56`, I-430). The release workflow signs
`checksums.txt` and checks the signature against the key it reads out of
`install.sh` (`.github/workflows/release.yml:65-82`), so `install.sh` is
the one source of the public key today. After the install it warns when
another `repose` is first on `PATH` (`:211-214`, I-15: Arch ships an
unrelated `repose`).

**Platforms.** Releases are `darwin` and `linux`, `amd64` and `arm64`
(`.goreleaser.yaml:13-18`). `install.md:8` says to use WSL on Windows.
`internal/cli/filelock_windows.go` exists so the package compiles there,
but no Windows archive is published.

**No package manager installs repose today.** There is no Homebrew tap,
no nixpkgs package and no distribution package (I-423 dropped the
Homebrew reader for tools; it has nothing to do with the CLI). A user
can still put the binary under a package manager by hand, or build one
with `go install`, which gives version `dev`.

**The CLI surface.** 35 visible top-level commands, `help` included
(`repose --help`). `version` prints `repose 0.1.43 (herakraft)`
(`cli.go:2131-2143`; the suffix is I-15). The word "update" already has a
meaning for users: the platform's **base updates** of the machine
(`apps/web/src/content/docs/config.md:176-182`, the `base updated` and
`base update failed` notifications in `notifications.md:63-64`, the
`base updates held` setup line in `cli.md:291`).

## 2. Placement

The owner's rule for new commands: fold under an existing noun, avoid
near-duplicate names, and use a verb that says what the user does.

| Option | For | Against |
|---|---|---|
| **`repose version update`** (recommended) | Folds under the noun that already answers "which CLI do I have". Leaves the top level at 35. "Update" next to "version" can only mean the CLI, so it cannot be read as a base update. flyctl uses the same shape (`fly version upgrade`) | Two words where one would do. A user who types `repose update` gets cobra's did-you-mean, which points at `repose version` (I-276), one step short |
| `repose update` | One word. The council's wording, and I-749's text ("When `repose update` ships, this line becomes its documentation") | A 36th top-level command. Reads as "update my machine" next to base updates, `config apply` and `resize` |
| `repose version --update` | No new command | A flag that changes a print command into one that writes a binary; `--help` hides it |

With the recommended option, `version` gets `SuggestFor: update,
upgrade, self-update`, so `repose update` answers:

```
unknown command "update" for "repose"
Did you mean `repose version`?
`repose --help` shows its usage.
```

and `repose version --help` lists `update`. `repose version` alone keeps
its output byte for byte, since scripts read it.

## 3. `repose version update`

```
repose version update [VERSION]
```

VERSION is a tag such as `v0.1.40`, for a rollback; without it, the
latest release. Only signed releases (v0.1.28 and later) are accepted.

**Steps.**

1. Refuse on a machine (`REPOSE=1`), on a build whose version does not
   parse, on Windows, and when the binary belongs to a package manager
   (below). None of these touch the network.
2. Resolve the target: VERSION, or the tag in the `Location` of
   `https://github.com/heracraft/repose/releases/latest`, the redirect
   `install.sh` follows. The CLI does not trust the api's header for
   this: a release is checked by its signature, and the header is only a
   hint.
3. If the target equals the running version, print the "latest" line
   and exit 0.
4. Download the archive for `runtime.GOOS`/`runtime.GOARCH`,
   `checksums.txt` and `checksums.txt.sig` into a temporary directory.
5. Verify the signature with Go's `crypto/ecdsa` (`VerifyASN1` over the
   SHA-256 of `checksums.txt`), against the release key compiled into
   the CLI. Then check the archive's SHA-256 against its line in
   `checksums.txt`. No `openssl` is needed.
6. Extract `repose`, write it as a new file beside the running one
   (same directory, so the rename stays on one file system), `chmod
   755`, run it with `version` and check that it prints the target, then
   `rename` it over the old path. A rename leaves the running process
   alone. On Apple silicon an in-place write over a signed binary gets
   the next run killed, so the bytes of the old file are never
   rewritten.
7. Print the result. When another `repose` comes first on `PATH`, say so
   as `install.sh` does.

**Who owns the binary.** The path is `os.Executable()` with symlinks
resolved. The command refuses and exits 1 when that path is:

| Path | Owner named |
|---|---|
| under `/nix/store/` | Nix |
| under a `Cellar/` directory, `/opt/homebrew/` or `/home/linuxbrew/` | Homebrew |
| under `/snap/` | snap |
| `/usr/bin/repose` or `/bin/repose` (`install.sh` never writes there) | your system's package manager |

A path whose directory the user cannot write, such as `/usr/local/bin`
after `install.sh --system`, is not refused outright; the command says
how to run it with `sudo`.

**Exact output.** Progress goes to stderr as for other commands
(`cli.md` "Output"); results go to stdout.

| Case | Text | Exit |
|---|---|---|
| Updated | `Updated ~/.local/bin/repose from 0.1.43 to 0.1.44.` | 0 |
| Already the latest | `repose 0.1.44 is the latest release.` | 0 |
| Rolled back to VERSION | `Updated ~/.local/bin/repose from 0.1.44 to 0.1.40.` | 0 |
| Another `repose` first on `PATH` (after an update) | `Another repose is first on your PATH, at /usr/bin/repose; this one is at ~/.local/bin/repose.` | 0 |
| Package manager | `/nix/store/...-repose-0.1.43/bin/repose belongs to Nix. Update it with Nix.` | 1 |
| Directory not writable | `Can't write /usr/local/bin. Run it as root: sudo repose version update` | 1 |
| On a machine | `The repose on a machine comes with its base and updates with it.` | 2 |
| Development build | `This repose is a development build (dev), so there is no release to update it to.` | 2 |
| Windows | `repose has no Windows release. Install it inside WSL: https://repose.herakraft.co/docs/install` | 1 |
| VERSION before signing | `v0.1.20 came out before releases were signed. Install it with install.sh --version v0.1.20.` | 2 |
| VERSION does not exist | `There is no release v0.1.99.` | 1 |
| Network failure | `Not updated: could not reach github.com (<the error>).` | 1 |
| No signature | `Not updated: release v0.1.44 has no signature (checksums.txt.sig).` | 1 |
| Bad signature | `Not updated: the signature on checksums.txt of v0.1.44 does not verify.` | 1 |
| Archive mismatch | `Not updated: repose_v0.1.44_darwin_arm64.tar.gz does not match checksums.txt.` | 1 |
| New binary does not run | `Not updated: the downloaded repose did not start (<the error>).` | 1 |

Each refusal leaves the old binary as it was. The once-per-release
notice does not print after `version update`.

**One key, three copies.** The public key would live in `install.sh`
and in the CLI. A test in `test/supplychain` checks that the key compiled
into `internal/cli` equals the one `install.sh` carries, as
`release.yml:77-82` already reads it from there. A key rotation then
fails the test until both change.

## 4. The skew warning

**4.1 The once-per-release line (exists; new text).** The version gets
the same form on both sides, and the line names the new command:

```
repose 0.1.43 is older than 0.1.44, the latest release. `repose version update` updates it.
```

For a binary that a package manager owns (the table in section 3):

```
repose 0.1.43 is older than 0.1.44, the latest release. Update it with Homebrew.
```

Everything else stays: once per newer release, never on a machine,
never for a dev build, never after completion or a hidden command.

**4.2 A line that repeats while it matters (new).** The api adds a
second header, `X-Repose-CLI-Oldest: vX.Y.Z`: the oldest CLI release
whose requests this api still answers as documented. The repository
already keeps an old request or answer shape for one release after a
contract change (`CLAUDE.md`, "Docs are the spec"). The commit that
removes such a shape raises `clirelease.Oldest`, a constant beside
`DefaultURL`, to the first release that no longer needs it. A CLI older
than `Oldest` prints, on stderr, at the end of every `run`, `attach` and
`sync` (and before the ssh hand-over of an attach), whatever the
once-per-release file says:

```
repose 0.1.30 is too old for some of repose's answers. `repose version update` updates it.
```

(with "Update it with Nix." and the like in place of the command for a
package-managed binary). It does not print on other commands, so `ls`
and `status` stay quiet. It never changes an exit code; I-626's reason
for no refusal still holds.

While `Oldest` is unset the header is absent and 4.2 never prints, so
this can ship before the first shape is removed.

**4.3 The machine.** The guide line changes to: "when it is older, a
command may not work as the docs show, so tell them to run `repose
version update` on their laptop". `~/.repose/cli-version` stays as it is.

**4.4 Optional: count the versions.** The api already receives
`repose-cli/<version>`. A counter `repose_api_cli_requests_total{version}`
(release versions only; anything else is `other`) shows how many laptops
are below a planned `Oldest` before the owner raises it. A version is not
something a tenant typed, so the logging rule allows it.

## 5. Docs that change

- `apps/web/src/content/docs/install.md:69-71`: the Update section
  becomes `repose version update`, with the package-manager and `sudo`
  cases and the rollback form. "Update command coming soon" goes, which
  ends I-749's exception as I-749 says it should. Keep "Run the install
  command again" as the fallback for a CLI too old to have the command.
- `apps/web/src/content/docs/cli.md:458`: a row for `repose version
  update [VERSION]`, and the two warning lines under "Output" or
  "Account".
- `apps/web/src/content/docs/troubleshooting.md`: "repose says it is too
  old" with the 4.2 line.
- `nix/guest/base/agent-guide.md:13` (4.3); reaches machines with the
  next base.
- `docs/interfaces/api.md:27-30`: add `X-Repose-CLI-Oldest`, absent while
  unset; a client must accept answers without it. Same commit as the api
  code.
- `docs/workstreams/07-cli.md` (the CLI's install section) and
  `docs/features/`: there is no feature doc for install today; add the
  command to the CLI workstream doc rather than start a new feature doc
  (decision 4).

## 6. Tests and the evidence that closes each item

| Item | Test | Evidence |
|---|---|---|
| Update path | `TestVersionUpdate*` against an `httptest` release server signed with a test key (as `install.sh` tests use `REPOSE_INSTALL_BASE_URL` and `REPOSE_INSTALL_PUBKEY`): updates; already latest; rollback; no signature; bad signature; archive mismatch; a new binary that will not start. Each refusal checks that the old file's SHA-256 did not change | Test output, with one mutation (skip the signature check) that fails it |
| Owner detection | Table test over the paths in section 3 with a fake `os.Executable` | Test output |
| Key equality | `test/supplychain`: the Go key equals `install.sh`'s | Test output |
| Notice text | `TestNewerCLINoticeOncePerRelease` with `cliVersion = "0.1.20"` (no `v`) and the header `v0.1.24`; the line shows `0.1.20` and `0.1.24` | Test output |
| 4.2 | `TestTooOldCLIWarnsOnEveryRun`: three runs below `Oldest` print three lines; `ls` prints none; above `Oldest` only 4.1 applies | Test output |
| api header | `TestAnswersNameTheOldestCLI` beside `TestAnswersNameTheLatestCLI` | Test output |
| Real binaries | Build a release-shaped binary at `0.1.43` with `-ldflags -X main.version=0.1.43`, run it in a temporary `HOME` on Linux amd64 and on macOS arm64 against the real GitHub release | Pasted terminal sessions: before and after `repose version`, and `shasum` of the file. A second session with the binary moved under a fake `Cellar/` path shows the refusal |
| Docs | `go test ./internal/cli -run 'TestDocs|TestAgentGuide'` | Pasted run |

## 7. Risks

- **A compromised release key** lets an attacker ship an update to every
  laptop that runs the command. That risk exists today through
  `install.sh`; the update command shortens the time a bad release takes
  to reach the same laptops. The signing key stays in the GitHub environment that only the
  owner approves (`release.yml:38-47`).
- **GitHub rate limits.** The redirect needs no API token and is not on
  the REST rate limit. Downloads come from `objects.githubusercontent.com`.
- **A user who ran `install.sh --system` with `sudo`** gets the `sudo`
  line. Under `sudo`, `HOME` may be root's; the command reads no config,
  so that does no harm.
- **`Oldest` raised by mistake** makes every older laptop print a line
  on each run. It never refuses anything, so the cost is noise until the
  next deploy.
- **The 4.2 line for a CLI that has no `version update`**: CLIs older
  than this change still print I-626's old line with the install
  command, which works.

## Decisions for the owner

1. **Placement:** `repose version update` (recommended, folds under
   `version`, keeps "update" away from base updates) or top-level
   `repose update` (one word, matches I-749's text).
2. **Rollback:** keep the optional `[VERSION]` argument, or update to
   the latest only.
3. **The repeating warning (4.2):** add `X-Repose-CLI-Oldest` now with no
   value set, or wait until the first removed shape needs it. And whether
   the once-per-release line (4.1) stays, or only 4.2 remains so the CLI
   speaks only when something will break.
4. **Docs home:** add the command to `docs/workstreams/07-cli.md`, or
   start `docs/features/install-and-update.md`.
5. **Version counter (4.4):** add it, or leave the api logging
   `client: cli` only.
