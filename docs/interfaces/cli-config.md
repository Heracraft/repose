# CLI files on the laptop

All under `~/.config/repose/` (respecting `$XDG_CONFIG_HOME`), mode 0700.

| File | Contents |
|---|---|
| `credentials.json` | `{refresh_token, access_token, expires_at, logto_issuer, logto_client_id}` (`logto_client_id` since v0.1.1; absent means the built-in id); on macOS the refresh token goes to the keychain (`repose` service) and this file holds only the issuer. Mode 0600. |
| `projects.json` | cache: `{"<normalised remote>": {"project_id", "slug", "name"}, ...}` plus `{"by_dir": {"<abs path>": "project_id"}}` for projects named on `run` or `sync` (`repose run NAME`, I-603) with no remote, keyed by the repository root (else the directory). `by_dir` is written when `run` creates such a project there, and when a sync into a named project with no remote (`repose sync NAME`, `repose run NAME`'s first sync, or `--project`) comes from a directory with no remote and no entry (DECISIONS I-575, I-603), never otherwise for a named one, and never for a temporary one; a Ctrl-C after the create and before the connect removes the entry it wrote; an entry is used only when its project's `remote_url` equals the directory's remote (or both are empty), and a mismatched or vanished entry is deleted (DECISIONS I-152). A temporary project (`--temp`) is never written, nor is a second named project made in a checkout whose remote another project has; both are reached by name (I-348, I-351). `{"checkouts": {"<abs path>": {"project_id", "checkout"}}}` maps a folder `repose run --on` added to a machine to that machine and the checkout's name in its home (DECISIONS I-480); it is read before `by_dir`, with no remote check (the folder's remote is its own), and such a folder is never in `by_dir`. A CLI from before I-480 ignores the key's meaning, so it finds nothing for the folder and never syncs it over the machine's checkout. `by_dir` and the remotes are regenerable from `GET /projects`; `checkouts` is not, and a lost entry means `--on` again. |
| `idle-noted.json` | cache: `{"<project_id>": "<idle.since>"}` for the other projects' idle stretches `run` and `attach` have already mentioned (DECISIONS I-262), so each stretch is mentioned once; rewritten to the projects idle now, minus the one being attached. Regenerable; deleting it costs one repeated mention. Mode 0600. |
| `carry-hashes.json` | cache: `{"<abs laptop path>": {"size", "mtime_ns", "sha256"}}` for the Claude files the carry reads (I-196), so a run whose Claude config did not change reads none of them; entries not used by the last carry are dropped. Holds hashes only, never contents. Regenerable; deleting it costs one re-read. Mode 0600 (DECISIONS I-211). |
| `repo-config.json` | `{"<project_id>": {"sha256", "revision_id"}}`: the hash of the `repose.nix` `run` or `sync` last sent from the project's checkout and the revision it made (DECISIONS I-489). The same file is not sent again while that revision builds, is built or applied; after it failed, the run names the error instead. Hashes only, never the file. Regenerable; deleting it costs one resend. Mode 0600. |
| `config.toml` | `api_url` (default prod), `default_size` (the size of new projects; `default_class` is its old name, still read, and `default_size` wins over it; DECISIONS I-621), `default_agent`, `editor` (`code`, `cursor` or `zed`: what `repose code` opens when neither `--editor` nor `REPOSE_EDITOR` names one; I-622), `default_multiplexer` (`tmux` or `herdr`, the multiplexer of projects `run` creates, a temporary one included since I-602; absent means the CLI picks, see `features/run-and-attach.md` "Choosing the multiplexer"; any other value fails the load naming the key; DECISIONS I-502. A CLI before I-502 ignores the key), `sync.exclude` (extra gitignore-style patterns; a `[sync]` table with `exclude = [...]`, or the older quoted key `"sync.exclude"`, I-241), `logto_issuer` (default the owner's Logto), `logto_client_id` (default the App ID of the `repose-cli` application there; DECISIONS I-99), `logins.skip` (a `[logins]` table: the logins `run` leaves on the laptop, of `gh`, `codex`, `opencode`, `env`, `mcp` (the Claude Code MCP servers, DECISIONS I-556); absent means copy everything), `mcp.forward` (an `[mcp]` table: the laptop MCP servers the session helper forwards beside every attach, as `repose mcp forward` does; DECISIONS I-557), `projects` (`[projects.NAME.logins]` with `skip`, the project's own list, replacing `logins.skip` for the project of that name; DECISIONS I-422; `[projects.NAME.mcp]` with `forward`, added to `mcp.forward` for that project, I-557). `repose secrets choose` writes the two `skip` keys, rewriting only its own table. |
| `machine.nix` | the user's own file, never a cache: the personal layer (DECISIONS I-490), a home-manager module. `run` pushes it (`PUT /me/config` with `base_revision_id`) when its SHA-256 differs from `machine.nix.state`'s and the account's revision is still the state's; it is rewritten from the account when unchanged here and changed there; `repose config --global` edits, pushes and creates it (from the account's copy, else a template). `logout --purge` keeps it. |
| `machine.nix.state` | `{api, revision_id, sha256}`: the account revision and the text's SHA-256 the last push or pull left both sides on, for the `api` it names (another api ignores it). Deleting it makes the next run compare the texts instead: equal is agreement, different with an account copy is refused as both changed. Mode 0600. |

`~/.ssh/repose/` (0700) holds `id_ed25519` and `id_ed25519.pub` (the
CLI's own key pair, generated without a passphrase, private half 0600;
DECISIONS I-149), `id_ed25519-cert.pub` (the current certificate, for
that key), `known_hosts`, `config` and `hosts` (see ssh-gateway.md "CLI
side"; before I-281 `config` held the Host blocks itself), `.prepare.lock`
(serialises `repose ssh-prepare`), and `cm-*`, the ControlMaster sockets
of open multiplexed connections. The CLI never
creates, reads or changes `~/.ssh/id_*`. In `~/.ssh/config` it owns one
`Include ~/.ssh/repose/config` line before the first `Host` or `Match`
line; a symlinked config is edited at its target, or, when that is read
only, left alone with a message saying where to add the line (I-151).

In a project's own checkout (its origin is the project's remote, or its
root is the project's `by_dir` entry), `run` and `attach` own the
`[remote "repose"]` section of `.git/config` when its `url` has the shape
`<slug>.repose:~/<checkout>` (the checkout's name under the guest's home,
I-368; `<slug>` on a machine set up before): `url` (retargeted when it
differs),
git's default `fetch` refspec, `pushurl = this remote is fetch-only;
repose sync sends your work to the machine` (I-367; a remote added
before says `repose run` and is left so, since ownership goes by `url`)
and `skipFetchAll = true`.
A `repose` remote with any other URL is the user's and is never
changed; `repose.remoteNoted = true` records that the CLI has said so
once. `destroy` removes the section (not the fetched `refs/remotes/repose/*`)
when it points at the destroyed project (DECISIONS I-272).

**The laptop's herdr** (DECISIONS I-510). When `herdr` 0.9.0 or newer
is on the laptop's PATH and `~/.ssh/config` includes repose's file, the
CLI keeps herdr's machine list in step with the account: it runs
`herdr machine list --json`, `herdr machine add <slug>.repose --label
<slug> --remote-session default` (stdin from `/dev/null`) and `herdr
machine remove <id>`, and never edits herdr's files itself. It owns
exactly the entries whose target is `<slug>.repose` for any slug: one
for a running herdr project that has none is added, one whose slug is no
live project of the account is removed, so is each entry past the first
for one live slug (the enabled one kept when there is one), and every
other entry, a disabled one included, is left as it is. A temporary
project is added like any other (I-602, before it never was). Each add holds `herdr-sidebar.lock` in this
directory (an advisory lock, no contents, unlocked on Windows) and reads
the list again under it, so two commands at once add one entry. `run`,
`attach` and `sync` add and remove; one that read no project list from
the api (the fast attach, I-223) adds its own project and removes
nothing; a certificate refresh only removes, since `ssh-prepare` may run
it in a process that ends before an add could finish (I-542). An add that fails prints
`Could not add <slug> to herdr's sidebar: <herdr's last line>` once per
command. A herdr older than 0.9.0, or a `machine list` that fails, makes
the reconcile do nothing and say nothing. The CLI reads `HERDR_ENV`
(set to `1` by herdr in its panes) to pick herdr for a new project and to
choose the attach path; it is not a user setting and is not in
`userEnvVars`.

Remote URL normalisation: strip scheme and `git@`, replace `:` after host
with `/`, strip trailing `.git`, lowercase the whole result (DECISIONS
I-73: not just the host, so the worked example below actually holds).
`git@github.com:A/B.git` and `https://github.com/a/b` both become
`github.com/a/b`.

Environment overrides: `REPOSE_API_URL`, `REPOSE_PROJECT` (project id or
slug, same as `--project`), `REPOSE_NO_BROWSER=1` (forces device code; device code is the default since v0.1.2, `--browser` asks for the loopback PKCE flow, DECISIONS I-101), `REPOSE_NO_FORWARD=1` (no automatic port forwards while attached, DECISIONS I-199; `repose open` still works). `REPOSE_TIMING=1`, `REPOSE_NO_SPINNER=1`, `REPOSE_NO_FASTPATH=1` (I-223). `REPOSE_NO_INPUT_PROXY=1` (`run` and `attach` exec ssh instead of running it under the input proxy, DECISIONS I-280) and `REPOSE_NO_CLIPBOARD_PATH=1` (I-341); their first spellings, `REPOSE_INPUT_PROXY=0` and `REPOSE_CLIPBOARD_PATH=0`, are still read (I-621). `REPOSE_SESSION` is internal: the session helper's options (DECISIONS I-206), never set by hand. Every variable a user may set is in `userEnvVars` (`internal/cli/env.go`) and in the public CLI reference, `apps/web/src/content/docs/cli.md`; `internal/cli/docs_test.go` checks both ways (DECISIONS I-242). The `gateway` key was dropped in I-242 (it was never read); a file that still sets it loads as before.

`repose cp [-r] SRC... DST` (DECISIONS I-201, I-346): one side is `PROJECT:PATH`
or `:PATH` (this checkout's project), the other a laptop path; a path
starting with `/` or `.` is always local, as with scp. Several sources
are all on one side (and name one project) and go into the directory
DST. A relative guest
path is taken from the checkout (`guest-conventions.md` "The checkout",
I-368; the home directory when the machine has none), which costs one
ssh to ask the guest. It runs `scp` with the project's ssh
target (the multiplexed `<slug>.repose` alias), so it refreshes the
certificate like `run`, needs a running guest (exit 5 otherwise), and
exits with scp's code.

`repose paste [PROJECT] [--window NAME] [--print]` (DECISIONS I-252):
reads a PNG from the laptop's clipboard (pngpaste/osascript, wl-paste,
xclip; `WAYLAND_DISPLAY` then `DISPLAY` pick the Linux tool), writes it
over the project's ssh target to `/tmp/repose-paste/<ts>.png` in the
guest (0700 directory, 0600 file, pastes over a day old or past the
newest 50 deleted), and pastes the path into the session's active pane
(`paste-buffer -p`, no Enter). `--print` only prints the path. Exit 1
for no image, no tool, Windows or over 20 MB; 4 and 5 as for any
project command.

`repose scan [DIR] [--json]` (DECISIONS I-222): prints what the next
`run` would ask the guest to install, the laptop's global tools and the
checkout's commands, with where each was found and why the rest were
left out. It reads files only: no api, no guest, no package manager.
`DIR` defaults to the current checkout's root.

Exit codes: 0 ok; 1 generic; 2 usage; 3 not logged in; 4 project not found;
5 guest not running; 6 dirty remote tree (sync refused); 7 payment required;
8 capacity (also `waitlisted`: a checkout refused for want of a free seat, I-269/I-290); 10 build failed (Nix error printed); 130 interrupted (Ctrl-C).
Usage covers cobra's own refusals too: an unknown command or flag, a
wrong number of arguments, and two different projects named at once
(DECISIONS I-155), and an unknown subcommand under a group (`repose
secrets lsit`), which names the closest commands (I-276). Once `repose
exec` has started its command, the exit code is the command's and
nothing is added; the codes above come only from failures before it
(I-275). `run`, `attach` and `ssh` exit with ssh's code once connected.

Output: results on stdout; progress (phase lines, or one spinner line
on a terminal), warnings and errors on stderr; `REPOSE_NO_SPINNER=1` or
`TERM=dumb` gives the plain phase lines on a terminal too (I-154).
