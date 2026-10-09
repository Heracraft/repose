# Backlog triage: the owner's open notes (proposal, 2026-09-24)

**Status: settled on 2026-09-25; every item has an outcome below.** The
body is a snapshot of the repo and the research on 2026-09-24, kept as the
reasoning record: its "Today" sections describe the repo before those
outcomes and are not updated. Where this file and a decision entry
disagree, the entry wins.

| # | Item | Outcome |
|---|---|---|
| 1 | Vercel CLI and portless | recorded: stay menu entries (I-256) |
| 2 | Voice mode | recorded: dropped (I-256) |
| 3 | Image paste | built: `repose paste` (I-252) |
| 4 | Preview URLs | not approved on 2026-09-25 |
| 5 | Quick path to production | recorded: stays deferred (I-256) |
| 6 | Log into Claude once | open: `2026-09-24-claude-login-shared-folder.md` |
| 7 | Home page additions | not approved on 2026-09-25 |
| 8 | Test the browser features | done in round 3 (I-246, checked live) |
| 9 | Try exe.dev | the owner's own trial, not repose work |
| 10 | Something special because it's a microVM | built: `repose fork` (I-254, I-255) |
| 11 | `bypassPermissions` as the default | built for Claude Code (I-250) |
| 12 | Several runs of one project | built: any number of windows, `--worktree` (I-253) |
| 13 | Platform CLAUDE.md / AGENTS.md | done in round 3 (I-243, I-244) |
| 14 | cloudflared | built: menu entry (I-251) |
| 15 | Coolify or hosting in a guest | recorded: terms line, no technical block (I-257) |
| 16 | Private invite beta | not approved on 2026-09-25 |

Sources:
- Repo state as of `a2aad99` on local main. Local main was 10 commits ahead of
  origin: I-238..I-241 were merged but not pushed.
- Web research done on 2026-09-24. Each claim cites its URL. Anything
  marked *unverified* was not confirmed.
- `2026-09-23-dev-ergonomics.md`, which already covers items 3, 7, 8, 9
  and 13 of the earlier list.

## Summary

| # | Item | Today | Recommendation |
|---|---|---|---|
| 1 | Vercel CLI and portless preinstalled | Menu entries plus tools carry | Close it. No base change |
| 2 | Voice mode | Nothing | Drop it. It can't work over SSH |
| 3 | Image paste | OSC 52 copy-out only | Build `repose paste` |
| 4 | Preview URLs | Designed, not built | Make it next after M5 |
| 5 | Quick path to production | Deferred (item 13, 09-23) | Keep deferred |
| 6 | Log into Claude once for N projects | Open | See the separate file. Not a blocker for the invite beta |
| 7 | Home page additions | Partly on the page | Add only what is live |
| 8 | Test the browser features | VM tests only | Deploy I-241, then run the real-guest rows |
| 9 | Try exe.dev | n/a | Owner, priority 1. Test plan below |
| 10 | Something special because it's a microVM | n/a | Not alone. `repose fork` is the idea |
| 11 | `bypassPermissions` as the default | Docs tell users to set it | Set it in the guest baseline |
| 12 | Several runs of one project | `--name` plus one `-2` window | Allow any number of windows. Consider `repose fork` |
| 13 | Platform CLAUDE.md / AGENTS.md | Nothing | Managed CLAUDE.md, generated from nix |
| 14 | cloudflared | Docs mention it only | Menu entry |
| 15 | Coolify or hosting inside a guest | Nothing blocks it | Accept it, or add one terms line |
| 16 | Private invite beta, and Theo | No gating | Invite flag. Pitch repose to Theo as a T3 Code host |

---

## 1. Vercel CLI and portless preinstalled

**Today**
- Both are opt-in menu entries in group `deploy`:
  `internal/menu/catalog.yaml:278` (vercel) and `:292` (portless).
- Proposal 09-23 item 3 says "agreed: no base change".
- The tools carry (I-221) reinstalls the laptop's global npm tools. Its test
  fixtures include both (`internal/cli/carry_tools_test.go:76,81`).
- The project scan (I-222) maps `portless` to its package.
- Vercel's login file is carried (`internal/cli/creds.go:28`).
- Auto-forward never silently remaps portless's port 1355
  (`internal/cli/forward.go:43`, I-199).

**Research: portless** (https://github.com/vercel-labs/portless)
- `portless myapp next dev` gives the app a random `PORT` between 4000 and
  4999.
- A local proxy on 443 (HTTP/2, with a local CA added to the trust store,
  loopback only) serves it at `https://myapp.localhost`.
- It also has `--lan`, `--tailscale` and `--ngrok` modes and an agent skill.

**Analysis** (reasoning, not from docs)
- Random ports don't suit per-port `ssh -L`.
- Forwarding the proxy port works, because the browser sends
  `Host: myapp.localhost`. But the guest's CA isn't trusted on the laptop,
  and binding local 443 needs privileges.
- portless fits hostname-routed preview URLs (item 4) better than SSH
  forwarding.

**Recommendation:** close it. If portless users complain, item 4 is the
fix; the base stays as it is.

## 2. Voice mode

**Today:** nothing. Proposal 09-23 item 8 said "build nothing, OS
dictation instead; Remote Control may cover it; verify".

**Research** (https://code.claude.com/docs/en/voice-dictation.md)
- Voice needs "a local microphone: voice dictation does not work in cloud
  sessions or SSH sessions".
- It also needs a claude.ai login, not an API key or setup-token.
- On a headless host it fails with "SoX could not open an audio capture
  device".

**Recommendation:** drop it as a repose feature. The paths that remain:
- OS dictation on the laptop, which types into the terminal.
- Remote Control from the Claude phone app. That needs a full `/login` in
  the guest, which is one more reason the login must not be a setup-token
  (see the login file).

## 3. Image paste

**Today**
- Only copy-out: `set -g set-clipboard on` (`nix/guest/base/tmux.nix:62`),
  tested at `nix/guest/tests/default.nix:252`.
- Proposal 09-23 item 8 rejected a clipboard socket into the guest.
- It proposed, but did not build, a CLI pty proxy that uploads images.

**Research**
- Ctrl+V reads the clipboard of the machine that runs Claude, so remote
  paste fails.
- The OSC 52 / OSC 5522 image-paste request was closed as not planned:
  https://github.com/anthropics/claude-code/issues/42712
- Every community workaround copies the file across and pastes its path,
  which Claude attaches:
  - clipssh (https://github.com/samuellawrentz/clipssh)
  - claude-ssh-image-skill over a reverse tunnel
    (https://github.com/AlexZeitler/claude-ssh-image-skill)
  - a VS Code Remote-SSH extension
  - plain scp
- Theo names "no image paste" as one reason SSH workflows are broken
  (item 16).

**Recommendation:** build `repose paste`.
1. Read the image on the laptop's clipboard (`pngpaste` or `osascript` on
   macOS, `wl-paste` or `xclip` on Linux).
2. Copy it over the existing ControlMaster to `/tmp/repose-paste/<ts>.png`.
3. Type the path into the active pane with `tmux send-keys`.

This reuses `repose cp` (I-201) plumbing and needs no socket into the guest.
A terminal key binding that runs it is optional, documented rather than
shipped.

## 4. Preview URLs

**Today**
- Auto-forward while attached (I-199, which rejected preview URLs as the
  dev path) and `repose open PORT`: `docs/features/ports-and-previews.md:8-86`.
- The later design is at `ports-and-previews.md:120-160` (R2-6, I-6):
  - `<port>-<slug>-<handle>.repose.herakraft.co`
  - wildcard TLS on the edge
  - Logto cookie, owner-only by default
  - `preview: public`
  - 100 req/s cap
- Not built: no gateway HTTPS listener, no DNS.
- It is first on the MILESTONES "Later" list.
- User docs say there are no public URLs and suggest `cloudflared`
  (`apps/web/src/content/docs/machine.md:92`).

**Research:** every comparable product has previews, private by default
with an explicit public toggle.

| Product | URL | Default access | Public |
|---|---|---|---|
| exe.dev | `vm.exe.xyz[:port]` | exe login | `share set-public`, one port; email and link shares ([proxy](https://exe.dev/docs/proxy.md), [sharing](https://exe.dev/docs/sharing.md)) |
| Sprites | `name-orgid.sprites.app` | bearer token | `sprite url update --auth public` ([networking](https://docs.sprites.dev/concepts/networking/)) |
| Codespaces | `NAME-PORT.app.github.dev` | private | org or public per port ([docs](https://docs.github.com/en/codespaces/developing-in-a-codespace/forwarding-ports-in-your-codespace)) |
| Coder | `PORT--agent--ws--user--apps.host` | owner | authenticated or public ([docs](https://coder.com/docs/admin/networking/port-forwarding)) |

**Recommendation:** make this the first thing after M5. It also lets you
check an app from a phone, and it gives portless (item 1) a home. The
existing design already matches the market pattern.

## 5. Quick path to production

**Today**
- Proposal 09-23 item 13 deferred it: "Repose should not host production";
  the idea is a base skill that deploys to the user's own Vercel, Coolify or
  Fly.
- The menu has wrangler, supabase, flyctl and vercel. ngrok is on the unfree
  allowlist (`nix/guest/unfree-allowlist.nix:5`).

**Recommendation:** keep it deferred until the invite beta shows people
asking for it.

## 6. Log into Claude once for N projects

See `2026-09-24-claude-login-shared-folder.md`. In short:
- Anthropic's terms forbid a platform that collects, stores or
  intermediates Claude.ai credentials. That rules out the stored
  setup-token fallback and CLI copying.
- A per-user shared folder holding only `.credentials.json` is the
  candidate. It needs two experiments and a question to Anthropic.
- One login per new project is acceptable for a handful of invited users,
  so this does not block the beta.

## 7. Home page additions

**Today:** `apps/web/src/routes/+page.svelte`, hero "Let your agents run
with full permissions" (see memory: positioning). Extras at `:66-78`:

| Proposed line | On the page? |
|---|---|
| Dev server on localhost | Yes |
| Headless Chromium and Playwright, plus `repose open --desktop` | Yes; "take over" is not stated |
| Docker works | Yes |
| Config changes without a reboot | No |
| Agent status at a glance with cost today | No |

The `web/landing-page` branch has copy for the missing two: `:86` "no
reboot for packages", `:111` "Docker and databases", `:184` "`repose
status` shows… cost so far today", "Five agents, one notification feed".

**Recommendation:** add the two missing lines only after checking each is
live and documented under `/docs` (rule: no ghost features). Keep the
three-column shape agreed on 09-23.

## 8. Test the browser features

**Today**
- `nix/guest/base/browser.nix`: chromium, playwright-mcp and
  chrome-devtools-mcp, both headless, in `/etc/repose/mcp.json`.
- `nix/overlay/agents/agent-setup.nix:8,54-60` merges them into
  `~/.claude.json`.
- `repose-browser.slice` caps memory at 37.5% of the guest (`:84-91`).
- `repose open --desktop` / `--stop` goes through `repose-guest-profile`
  (I-241, `internal/cli/open.go`). Before I-241 it called a unit that
  didn't exist.
- `repose browser bridge` is planned only (`docs/features/browser.md:81`).
- VM tests in `nix/guest/tests/default.nix`:
  - mcpServers present (`:280`)
  - Playwright seed (`:801-819`)
  - desktop chain on 6080, VNC password, 30-minute idle stop (`:859-892`)
  - headless screenshot (`:895-903`)
- No test covers the memory cap. No CI runs the VM tests.
- Real-guest rows are open in `docs/workstreams/02-guest-base.md:348-361`
  and in `CHECKLIST-AUDIT.md`.

**Recommendation**
1. Push main.
2. Release the CLI and publish the base that carries I-241, then deploy
   the web app.
3. On a real guest, record evidence for:
   - an agent-driven headless screenshot of a live page;
   - both MCP servers answering with no network beyond the page;
   - `open --desktop`, then a headed browser launched by the agent,
     then the user clicking in it, then `--desktop --stop`;
   - a runaway tab (allocate in a loop) killed by the slice while the
     agent survives.
4. Paste that evidence into the 02 checklist rows.

## 9. Try exe.dev (owner, priority 1)

**Research** (https://exe.dev/pricing, https://exe.dev/docs.md)
- **Price:** Personal is $20/month for 2 vCPU / 8 GB / 100 GB pooled across
  up to 50 VMs, with 200 GB transfer. Team is $25 per user.
- **Access:** `ssh exe.dev`, persistent VMs with root, apt and systemd.
  Claude, codex and pi are preinstalled.
- **Shelley:** a web and mobile agent with a Chromium tool and a browser
  terminal.
- **Proxy:** private HTTPS by default, with sharing (see item 4).
- **Other:** `cp` clones a VM; custom domains; email; edge-injected
  secrets.
- **Not found in their docs:** snapshots or restore, Nix, a
  Claude-subscription integration (Claude access is by API key or their
  gateway).
- **Series A:** $35M, from their homepage (*unverified*).

**Where repose still differs:**
- `repose run` brings the exact working state: uncommitted edits, `.env`,
  tool logins, TZ.
- A Nix-declared machine that switches without a reboot.
- Snapshot and restore.
- Price is not one of them: $49 per project against $20 for 50 VMs.

**The owner's day on exe.dev should record:**
1. Time from a cold account to an agent working on nuru-playground with
   uncommitted changes. What was lost?
2. Opening the dev server on a phone.
3. Pasting a screenshot into Claude.
4. What happens after the laptop closes and a VM restarts.
5. Where Claude's login lives and how often it's asked for.

Then decide continue, shrink or shelve (see memory:
repose-competitive-landscape).

Fly Sprites, briefly (https://fly.io/sprites/):
- Persistent VMs that sleep; about 1 s checkpoints, automatic by default.
- `*.sprites.app` URLs.
- Agent CLIs preinstalled.
- Usage billing (CPU $0.07/h, RAM $0.04375/GB-h).

Conductor Cloud (https://www.conductor.build/docs/cloud/getting-started):
- Invite-only; Vercel Sandbox microVMs.
- "Personal subscription token or API key" for agents.
- Pricing not found.

## 10. Something special because it's a real microVM

exe.dev and Sprites are microVMs too, so repose needs more than the VM to
stand apart.

**The difference:** real Docker, systemd and a kernel per project, combined with
snapshot and restore and a Nix switch without a reboot. The concrete
feature that uses all of it is **`repose fork`**: snapshot a project and
start N guests from it, so N agents try N approaches from the same state,
each with full permissions. It builds on existing snapshots and restore,
and it answers item 12.

## 11. `bypassPermissions` as the default

**Today**
- Not set. The platform settings hold only the repose-hook Notification
  and Stop entries (`nix/guest/base/agents.nix:6-15`).
- User docs tell users to set it themselves on the laptop
  (`apps/web/src/content/docs/agents.md:27-40`).
- The I-196 merge (`internal/cli/claude_merge.jq:79-88`) is `$G * $L`, so a
  laptop `defaultMode` wins and the guest's value stays when the laptop has
  none.
- The guest user `dev` is non-root, partly for this
  (`nix/guest/base/users.nix:1`).

**Research** (https://code.claude.com/docs/en/permission-modes.md)
- `permissions.defaultMode: "bypassPermissions"` is honoured from user,
  `--settings` or managed settings. It is ignored from a project's
  `.claude/settings*.json`.
- The first interactive start shows a warning dialog. Accepting it writes
  `skipDangerousModePermissionPrompt: true`, which can be pre-set.
- It refuses to run as root or under sudo.
- Managed settings (`/etc/claude-code/managed-settings.json` and `.d/`)
  outrank the user, so using them would override a user who wants another
  mode. Don't use them for this.
- Even in bypass, deny rules, explicit ask rules, `rm` of critical paths
  and some cross-session prompts still apply.
- Anthropic now makes auto mode the default on Pro/Max/Team and describes
  bypass as for "isolated containers and VMs only", which is what a guest
  is.

**Recommendation**
- Add `permissions.defaultMode: "bypassPermissions"` and
  `skipDangerousModePermissionPrompt: true` to the guest's baseline user
  settings (the `$G` side of the merge). The laptop's own `defaultMode`
  still wins, which is "unless they have another default".
- It needs a decision entry, the `agents.md` / `index.md` docs changed from
  "set it yourself" to "on by default; here's how to change it", and a VM
  test that a fresh guest starts in bypass with no dialog.
- It matches the page's headline.

## 12. Several runs of one project

**Today**
- One microVM per project (R1-2), keyed on (user, remote).
- `repose run --name todo-app-experiment` gives a second guest for the same
  repo (`docs/features/projects.md:22`, I-152).
- New accounts may have 3 projects (1 xl), and 10 after the first paid
  invoice.
- Inside one guest, a second `run` opens window `<agent>-2` with a
  shared-working-tree warning (`internal/cli/tmux.go:37-48`). There is no
  `-3` and no worktree automation.
- No decision covers N agents per project.

**Recommendation**
- Allow `<agent>-N` for any N; this is small.
- Optionally offer `--worktree`, which puts each extra window on its own
  git worktree.
- For separate attempts, use `repose fork` (item 10).
- Users want it: it's the "three agents, three clean machines" line in
  the positioning. Measure through the beta before building fork.

## 13. Platform CLAUDE.md / AGENTS.md

**Today**
- Nothing writes one.
- The carry copies the laptop's `~/.claude/CLAUDE.md` onto the guest
  (`internal/cli/carry_claude.go:40`), so a platform file at that path
  would be overwritten.
- Custom notifications already work: `repose-hook` accepts
  `{agent,kind,summary}` JSON on stdin and posts to
  `/run/repose/hooks.sock` (`internal/guestd/hooks/hooks.go:33`). This is
  undocumented, and there is no `repose notify` inside the guest.

**Recommendation**
- Write the platform file to Claude Code's managed memory path
  `/etc/claude-code/CLAUDE.md` (*unverified path, check the memory docs*).
  It loads alongside the user's own and the carry never touches it.
- Codex gets a global `AGENTS.md`; check each of the five agents' global
  instruction path.
- Generate both from one nix module that reads the same sources as
  `tool-list.nix`, `browser.nix` and the features list, so a new tool
  changes the text without anyone remembering to. Content:
  - the tools installed;
  - ports auto-forwarded while attached;
  - headless browser and MCP servers;
  - `repose open --desktop` (the user runs it);
  - secrets in `/run/repose/secrets`;
  - `repose notify` for asking the user something.
- Add a guest command `repose notify "<text>"` (kind `needs_input` or
  `completed`) that wraps `repose-hook`.
- Add a `CHECKLIST.md` rule: any user-visible guest feature changes the
  generated file.

## 14. cloudflared

**Research**
(https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/)
- Quick tunnels need no account and give a random `*.trycloudflare.com`
  URL.
- Limits: 200 in-flight requests, **no SSE**, testing only.
- They don't work if `~/.cloudflared/config.yaml` exists.
- Named tunnels on the user's own domain are stable.

**Recommendation:** add `cloudflared` as a menu entry in group `deploy`.
The user docs already point at it, and they should note the SSE limit.

## 15. Coolify or hosting inside a guest

**Today**
- There's no inbound path to a guest (R4-6).
- Egress is open except:
  - tcp 25 (I-238)
  - mining-pool ports (I-239)
  - 200 new flows/s, burst 2000 (I-240)
  - 200 Mbit/s shaping (I-217)
- The terms (`apps/web/src/content/legal/terms.md:48-62`) say "for software
  development" and forbid proxy/VPN/relay for others, spam, scanning and
  illegal content. They don't ban tunnels or hosting.
- Egress over 500 GB is billed.

**Recommendation:** accept it. Price already makes a guest a poor host,
egress is metered, and there's no stable IP. If the owner wants the rule
written down, add one line to the terms: "not for serving production
traffic to others". No technical block.

## 16. Private invite beta, and Theo

**Today**
- No invite, waitlist or beta code. Sign-in is GitHub through Logto.
- No free tier; the trial credit is $3.36 (I-205).
- `billingGate` (`internal/api/http/projects.go:137-156`) requires a card
  before a start.
- `docs/features/projects.md` still says "cannot start a guest at all"
  without a card, while the homepage dropped card wording (c38fd3d). Check
  which is current.

**Recommendation**
- Add `users.invited` (or an invite-code table), `repose-admin invite
  <github-login>`, and a refusal `not_invited` in the same gate.
- The error code needs adding to `docs/interfaces/README.md`.
- Don't wait for item 6.

**Theo, research**
- June 2026: "~6 months from most devs moving their code agents off of
  their laptops" (https://x.com/theo/status/2071083700385955906, snippet
  only).
- Setup: an always-on Mac Mini driven through the T3 Code web UI over
  Tailscale, with the laptop as a thin client (podcast summary,
  https://finance.biggo.com/podcast/c7c3cb2193d150d2, secondary source).
- He previously ran about 30 tmux sessions on a Linux box and now calls
  SSH/terminal workflows "structurally wrong", citing sticky keys and no
  image paste (secondary summary).
- T3 Code can SSH to a host and install its server component there
  (https://github.com/pingdotgg/t3code/blob/main/docs/user/remote-access.md).
- He called Fly the host least likely to survive the year
  (https://fly.io/blog/kurt-scott-money-sprites/).
- No flake.nix or NixOS setup of his was found. The owner's note says they
  saw one; the link is needed.
- **Pitch:** repose as the box behind T3 Code, a per-project disposable
  VM with snapshots, where T3 Code's server runs. This is *untested*:
  first check that T3 Code's remote install works against a repose guest
  over `ssh <project>.repose`.
