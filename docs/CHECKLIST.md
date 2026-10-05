# Definition of done

This is the global checklist. Every workstream doc ends with its own list; this
one applies to all of them and to the release. An item is closed by the
evidence named in it, not by a passing build. "It compiles" closes nothing. "I
ran it once on my machine" closes nothing that says "on a host" or "in a
guest".

The failure this prevents: a workstream that is 99 percent done with the last
percent being the thing that makes it usable (the migration that was never
written, the systemd unit that is not enabled, the error path that panics, the
README that says "TODO"). Each of these has happened to someone; the list is
written so they cannot happen quietly.

## For every change

- [ ] The change matches `DESIGN.md` and the workstream doc, or a
      `DECISIONS.md` entry under "Made during implementation" explains the
      deviation. Evidence: the entry exists, with alternatives.
- [ ] Every interface the change touches is updated in `interfaces/` in the
      same commit. Evidence: `git show --stat` includes the interface doc.
- [ ] No hardcoded values that belong in config: hostnames, IPs, ports, sizes,
      prices, paths outside the documented conventions. Evidence: `rg` for the
      literal returns only the config definition.
- [ ] Errors are handled at every call site, with the failure named in the
      message. No `_ = err`, no bare `panic` outside `main`. Evidence: `rg
      '_ = err|panic\(' cmd internal` is empty or each hit has a comment
      saying why.
- [ ] Logs are structured, carry `component`, `project_id` where relevant,
      and never carry secrets, tokens, certificate bodies, prompts, terminal
      contents, process arguments, or user email. Evidence: reviewer grepped
      new log calls.
- [ ] A metric or log event exists for the new failure modes (see
      `ops/OBSERVABILITY.md`). Evidence: named in the PR description.
- [ ] Tests: unit tests for logic; an integration test against a real
      Postgres for anything touching the schema; a host-level test for
      anything in hostd that touches LVM, nftables, CH, or nix, run on a real
      host and its output pasted in the PR. Evidence: CI green plus the
      pasted output.
- [ ] `go vet`, `staticcheck`, `gofmt`, `nix flake check`, `tofu validate`
      clean.
- [ ] The workstream doc's checklist has been re-read and every item that
      the change affects has its evidence updated.
- [ ] A user-visible change (a command, flag, config key, environment
      variable, exit code, message, limit, price, dashboard action or guest
      behaviour) ships with its /docs update (`apps/web/src/content/docs/`)
      in the same commit, and anything the change removes leaves the docs
      too (DECISIONS I-242). Evidence: `git show --stat` includes the docs
      page, and `go test ./internal/cli -run TestDocs` passes (it fails on a
      CLI command, flag, config.toml key, environment variable or exit code
      missing from, or left behind in, `cli.md`).
- [ ] Text a user reads (docs, landing, dashboard, emails, agent guide)
      gives only help the reader asked for (DECISIONS I-485): a page
      delivers what its title promises; no reassurance, no narrating
      what the screen shows, no "next, read X", no fact repeated on a
      second page instead of linked. The failure this prevents: each
      helpful line passes review alone, and together they make every
      page longer for the reader who came for one answer. Evidence:
      `pnpm --filter web exec vitest run src/lib/docs.test.ts
      src/lib/copy.test.ts` passes (`release-queue add` runs it too), and each new or changed paragraph was read against the user on
      their fiftieth visit, with what was cut named in the commit.
- [ ] Output from a command that worked, and every listing, says what
      happened or what is, and stops: no `repose ...` command to run next.
      A next command goes only on a failure, a refusal, a warning that
      work did not go, or a change that does nothing until the user acts
      (DECISIONS I-484, narrowing I-153). The failure this prevents: each
      feature adds one kind hint, reviewed alone it looks harmless, and
      the user who runs the command for the hundredth time reads every
      one of them under the table they asked for. State that belongs to a
      row is a column, not a line under the table. Evidence: `go test
      ./internal/cli -run 'TestSuccessOutputNamesNoCommand|TestCLIReassures'`
      passes (`release-queue add` runs it too), any new
      `quietAllowed` entry carries a reason a reviewer agrees with, and
      the new success lines are pasted in the commit message (the test
      cannot see a command built at run time).
- [ ] A CLI command that takes paths or several words behaves as the Unix
      tool it wraps or resembles would with what a shell hands it (a glob
      expands to many words; `cp`, `scp`, `rm` take several sources), and
      every argument refusal names what it received (DECISIONS I-346).
      Evidence: `go test ./internal/cli -run TestArgErrorsSayWhatTheyGot`
      passes, and the new command was run once with a glob.
- [ ] A new or changed guest capability (something installed, a port or
      network rule, a limit, a command agents can run, how secrets, the
      browser or notifications work) updates the agent guide,
      `nix/guest/base/agent-guide.md`, in the same commit, so the agents on
      the machine are told what the user docs say (DECISIONS I-243).
      Evidence: `go test ./internal/cli -run AgentGuide` passes (it fails
      when a section of the machine, agents or limits page has no guide
      line, a guide reference has no such heading, or a command the guide
      names is not in the guest), and for a new command the
      `guest-agent-guide` VM test output is pasted.

## For every change to the web app's look

`docs/DESIGN-LANGUAGE.md` is the spec (DECISIONS I-369). Run these from
the repository root; each prints nothing when the rule holds. `$L` leaves
out the landing (`routes/+page.svelte`, `routes/landing.css`,
`lib/components/landing/`), which `LANDING.md` governs and "The landing"
below checks:
`L=(--glob '!**/landing/**' --glob '!**/routes/+page.svelte' --glob
'!**/routes/landing.css')`. A hit is fixed, or the rule changes through a
DECISIONS entry and this list with it. Counts as of 2026-10-01.

- [ ] Text colours come from the ink tokens, and zinc-400 and lighter is
      never text. Evidence: `rg -n 'text-zinc-[3-6]00' apps/web/src --glob
      '*.svelte' "${L[@]}"` prints 0 lines.
- [ ] Blue is only the palette, links, focus and selection, all in
      `layout.css`. Evidence: `rg -n 'blue-[0-9]' apps/web/src "${L[@]}"
      --glob '!**/routes/layout.css'` prints 0 lines (`layout.css` itself
      has 23: eleven palette steps, then `--sh-accent`, `--pic-accent`,
      `--accent`, `--accent-strong`, `--selection` and `--focus` twice
      each, once per scheme; `.link` and the docs' links use
      `text-accent`, I-392, I-397).
- [ ] No shadows, and no gradient of more than one colour. Evidence: `rg
      -n 'shadow-|box-shadow: [^n]' apps/web/src "${L[@]}"` prints 0
      lines; `rg -nP 'gradient\((?!(var\([^)]*\)|#[0-9a-fA-F]+|currentColor),
      \1\))' apps/web/src` prints 0 lines.
- [ ] Toasts are house banners. Evidence: `rg -n 'richColors' apps/web/src
      --glob '*.svelte' | rg -v '<!--'` prints 0 lines.
- [ ] No page asks a third party for a font, and the CSP allows none.
      Evidence: `rg -n 'fonts\.(googleapis|gstatic)' apps/web/src
      apps/web/static apps/web/svelte.config.js` prints 0 lines, and a page
      load's network panel shows only the site's host.
- [ ] No native dialogs. Evidence: `rg -n 'window\.confirm|[^.\w/]confirm\('
      apps/web/src --glob '*.{svelte,ts}' | rg -v '//|onconfirm'` prints 0
      lines.
- [ ] Sizes come from the scale, not overrides or hand-written values.
      Evidence: `rg -n '![pm][xytrbl]?-' apps/web/src --glob '*.svelte'
      "${L[@]}"` prints 0 lines (a compact button uses `.btn--sm`);
      `rg -n 'text-\[[0-9.]+(px|rem|em)\]' apps/web/src --glob '*.svelte'`
      prints 0 lines; `rg -n '\[var\(--' apps/web/src --glob '*.svelte'
      "${L[@]}"` prints 0 lines (use `text-ink-muted`, `border-rule` and
      the other token utilities).
- [ ] Mono under 13px is only a badge. Evidence: `rg -n
      'font-mono[^"]*text-x?xs|text-x?xs[^"]*font-mono|font-mono[^"]*text-2xs|text-2xs[^"]*font-mono'
      apps/web/src --glob '*.svelte' "${L[@]}"` prints 0 lines (running
      mono is `text-compact` or larger; a badge is `.badge`, I-393).
- [ ] Corners stay at 4px or less, weight stays at semibold, and no
      stray hue. Evidence: `rg -n 'rounded-(md|lg|xl|2xl|3xl|full)\b|font-bold|<b>|<strong'
      apps/web/src --glob '*.svelte' "${L[@]}"` and `rg -n
      'purple|violet|fuchsia|pink-|orange-|indigo|teal|cyan|lime-'
      apps/web/src --glob '*.{svelte,css}' "${L[@]}"` each print 0 lines.
- [ ] One scheme mechanism and a visible focus ring. Evidence: `rg -n
      'data-theme|class="dark' apps/web/src` prints 0 lines; `rg -n
      'outline-none' apps/web/src "${L[@]}"` prints only the docs drawer
      (`routes/docs/+layout.svelte`, which is focused by script, not by
      the keyboard).
- [ ] A new component that shows state in a fill or a coloured border has
      a rule in the `forced-colors` block of `layout.css`. Evidence: a
      capture with Chromium's `forcedColors: 'active'`.
- [ ] The keyboard path, forced colours, reduced motion and the phone
      column hold. Evidence: `pnpm build`, then `pnpm test:integration
      tests/design.spec.ts` passes; a panel that opens in place of a
      button adds its focus check there (I-392).
- [ ] The accessibility gate passes: `pnpm build`, then `pnpm a11y` in
      `apps/web`, 56 runs (14 pages, light and dark, 1440 and 390), with
      `KNOWN_FAILURES` empty or each entry carrying a reason and, where it
      fails at one width only, that width (DECISIONS I-389, I-399). The
      landing's four are audited under reduced motion, so two runs give
      the same result (I-397).
- [ ] Judged at real size: viewport captures or crops at 1x, 1440 and
      390 wide, light and dark, of every changed page (CLAUDE.md "Judge
      visuals at real size").

### The landing

The same rules over the landing's own files (I-397), with one allow-list:
the pictures of real tools keep their tool's colours and sizes
(`LANDING.md`, "Real, and whole, or not at all"; I-392). `$P` is the
landing. `$C` leaves out the two files that are a capture and nothing
else, `Editor.svelte` (LazyVim's Tokyo Night) and `Ready.svelte` (the
terminal's ANSI colours). `Browser.svelte` and `Localhost.svelte` hold
drawn parts too (chips, a ring, a title bar's dot), so the hue greps
read them; the hex and size greps take `$X`, which also leaves them
out, since their captured parts (the agent's white page and its log,
tmux's bar) are written in hex and drawn at the capture's scale (the
log at 10px in a narrow frame): `P=(apps/web/src/routes/+page.svelte
apps/web/src/routes/landing.css apps/web/src/lib/components/landing)`,
`C=(--glob '!**/landing/Editor.svelte' --glob '!**/landing/Ready.svelte')`,
`X=("${C[@]}" --glob '!**/landing/Browser.svelte' --glob
'!**/landing/Localhost.svelte')`. Counts as of 2026-10-01 (I-400).

- [ ] Text is never zinc-300 to zinc-600, weight stops at semibold for
      markup, and sizes take no overrides. Evidence: `rg -n
      'text-zinc-[3-6]00|font-bold|<b>|<strong|![pm][xytrbl]?-|text-\[[0-9.]+(px|rem|em)\]'
      "${P[@]}"` prints 0 lines (a large call to action is `.btn--lg`).
- [ ] Blue and every other hue come from the tokens: `--sh-*` for shapes,
      `--pic-*` for drawn pictures, the Foundation's for text. Evidence:
      `rg -n 'blue-[0-9]|purple|violet|fuchsia|pink-|orange-|indigo|teal|cyan|lime-'
      "${P[@]}" "${C[@]}"` prints only the Browser picture's two marks
      over its capture (`--chip` and `--on-log`, `LANDING.md`, "Since the
      owner's notes"), 2 lines; `rg -n
      'amber|emerald|green-|yellow|red-[0-9]|zinc-[0-9]|rose-|sky-|slate-|gray-|stone-'
      "${P[@]}" "${C[@]}"` prints only the green running dots
      (`emerald-500`, open for the owner in STATUS.md): the drawn title
      bars of Hero, OneCommand, ComesBack and Localhost, and OneCommand's
      "Ready" dot, 5 lines; `rg -n
      ':\s*#[0-9a-fA-F]{3,8}\b|="#[0-9a-fA-F]'
      "${P[@]}" "${X[@]}"` prints only the hero's Claude Code colours
      (`--claude`, `--mode`, and `--rogue`, the mascot turned red), 5
      lines.
- [ ] No shadows: an inset edge with no blur is a bar, and is the only
      `box-shadow`. Evidence: `rg -nP 'shadow-|box-shadow:
      (?!none|inset -?[\d.]+(px)? -?[\d.]+(px)? 0 )' "${P[@]}"` prints 0
      lines.
- [ ] Corners stay at 4px or less; `50%` draws a circle (a dot, a node,
      the globe), not a corner. Evidence: `rg -n
      'rounded-(md|lg|xl|2xl|3xl|full)\b|border-radius:
      *([5-9]|[1-9][0-9]+)(\.[0-9]+)?px|border-radius: *[0-9.]+(rem|em)'
      "${P[@]}"` prints 0 lines.
- [ ] One mono, and nothing a visitor reads under 11px outside the
      captures. Evidence: `rg -n "font-family:[^;]*(Menlo|Consolas|SF
      Mono|Courier|monospace)|font-\[" "${P[@]}"` prints 0 lines (a picture
      names `var(--font-mono)`); `rg -nP 'font-size:
      *(\d|10)(\.\d+)?px' "${P[@]}" "${X[@]}"` prints 0 lines.
- [ ] Every picture stops on its final frame when reduced motion turns on
      mid-session. Evidence: `rg --files-without-match watchReducedMotion
      apps/web/src/lib/components/landing/{Hero,OneCommand,ComesBack,Browser,Localhost}.svelte`
      prints 0 lines.

## For every workstream, before it is called done

- [ ] Every command, flag, endpoint, message, table and field named in the
      workstream doc exists with that exact name. Evidence: a script or grep
      listing each name and where it is defined, pasted in the PR.
- [ ] Every error path in the workstream doc's "Failure modes" section
      produces the documented user-visible message. Evidence: each one
      triggered deliberately, output pasted.
- [ ] The workstream runs from a clean checkout following only its doc and
      `ops/RUNBOOK.md`. Evidence: someone (or a fresh agent) did it and the
      steps they had to guess are now in the doc.
- [ ] Rollback is written: how to undo the workstream's migration, unit, or
      config without data loss. Evidence: the section exists and was
      exercised once.
- [ ] The `ops/RUNBOOK.md` has a symptom entry for each way this workstream
      breaks in production.
- [ ] Nothing is left as `TODO`, `FIXME`, `XXX`, or "not implemented" in the
      workstream's files unless a `DECISIONS.md` entry defers it. Evidence:
      `rg 'TODO|FIXME|XXX|not implemented' <paths>` is empty or each hit is
      referenced.

## Release (M5)

- [x] Benchmark numbers in `RESEARCH.md`, gate passed or Hetzner decision
      recorded. Evidence: the standalone M0 benchmark was deferred by the
      owner (DECISIONS I-12) and the first real host measures itself
      instead: `RESEARCH.md` §11 "First host timings" (host-01,
      `Standard_D16s_v7`, create 20 s, freeze p99 under 0.5 s), §12 (M2:
      build 38.6 s, snapshot 24.6 s, stop 4.5 s, start 14.5 s) and §13 (M3:
      create to running 47 s, menu apply 5 s, eval and build timings). No
      axis came close to the 20 percent question, so the Hetzner fallback
      (R3-20) stays a fallback; I-39 records the v7 sizes actually used.
- [ ] A second human has completed login, run, attach, stop, start, secrets,
      config apply, snapshot restore, destroy on their own laptop.
- [x] Tenant isolation verified on a shared host: guest A cannot ping,
      ARP, or port-scan guest B or the host; guest A cannot read the store's
      `.links`; guest A cannot reach 169.254.169.254; a certificate for A is
      rejected by B's sshd and by the gateway route. Evidence: `test/isolation`
      on host-01 with two tenants, 2026-09-21 00:37Z and 00:39Z
      (`ops/checks/out/isolation-go-20260921T003759Z.txt`, `...003951Z.txt`:
      18 PASS, 1 SKIP by design), rows listed in `workstreams/14-security.md`
      §9; the gateway half of the certificate row is
      `TestCertificateForACannotOpenBAtGateway`, the sshd half is the guest
      base's `AuthorizedPrincipalsFile` (02's VM test) since putting an
      operator key on the host to try it directly was declined. The
      mechanisms as deployed are re-read in
      `security/review-2026-09-21.md` "Verified as deployed".
- [x] A user Nix fragment that (a) has a syntax error, (b) references a
      missing attribute, (c) runs 31 minutes, (d) exceeds the closure cap,
      (e) uses `builtins.fetchurl` to an arbitrary URL each produces the
      documented error and nothing else happens. All five closed on
      host-01 through the deployed api and the CLI (`workstreams/
      12-nix-config-pipeline.md` §9 first row for the transcripts): (a)
      `config error: syntax error at syntax.nix:1:34, unexpected ';'`
      (M5 session, 2026-09-21 02:29Z), (b) `attribute 'ripgrepp' missing
      at missing.nix:1:36 (did you mean ...)`, (e) `eval-time fetch not
      allowed at fetch.nix:1:33; use pkgs.fetchurl { url = ...; hash =
      ...; }`, (d) `closure is 26.6 GB, limit is 20 GB; largest paths:`
      with ten paths (M3 session, 2026-09-21 00:47Z); each exit 10 and
      the guest untouched; (c) `build timed out after 30 minutes while
      building sleep-forever-1.0` from the api's op error, hostd
      `build_fail code=build_timeout duration_ms=1805018` at 03:33:15Z on
      repose-m5-frag, the revision `failed` and nothing applied (M5
      session, 2026-09-21).
- [ ] Postgres backup and restore are configured in the owner's Coolify
      (Backups tab); nothing here. Evidence: the schedule exists there
      (DECISIONS I-112).
- [ ] Snapshot restore of a guest onto a *different* host rehearsed.
- [ ] Host loss rehearsed: deallocate a host, restore its projects elsewhere
      from Blob, users notified.
- [ ] Paddle: the sandbox gate of `docs/ops/M4-GATE.md` passes (checkout to
      `trial`, `transaction.completed` to `active`, `payment_failed` to
      `past_due` and the 3-day stop, an egress overage line to the cent);
      one live charge of the owner's own card succeeded.
- [x] Grafana dashboards exist for: host capacity, per-guest resources,
      builds (duration, failures), gateway (sessions, auth failures),
      snapshots (age per project), billing (usage per hour), abuse (top
      processes by CPU across fleet, top egress). Evidence: all seven
      load into a real Grafana 12.4.0 with no provisioning error
      (`ops/check.sh --grafana`), and **41 of their 43** Prometheus panel
      queries return real production data against host-01
      (`ops/dashboards/validate.py --query`, 2026-09-21). The two that do
      not were Stripe's, off by I-16, and are gone with I-289; no panel
      is empty for a reason of its own.
- [x] Alerts wired: host memory 80 percent, host unreachable, snapshot older
      than 36 hours for a running project, build queue stuck, gateway auth
      failure spike, egress over 1 TB per project per day. Evidence: 17
      rules (those six plus I-56's two and billing's three and the rest),
      each with a `promtool test rules` case and a RUNBOOK heading of its
      own — all three checks run by `ops/check.sh`. Loaded against real
      production series they evaluate healthy: none firing, none in
      error. *Wired* here means loaded and tested, not yet delivering:
      routing them to the owner's Alertmanager is part of the WireGuard
      peer that `10-observability.md` §9 still has open.
- [x] Privacy policy and terms published, containing the process-sample
      boundary verbatim and the Anthropic hosted-use statement (users
      authenticate with their own credentials; the platform stores none).
      Evidence: `https://repose.herakraft.co/privacy` and `/terms` (the
      m3-web live Playwright suite asserts both passages in a real browser,
      11/11 green 2026-09-20); `test/isolation` `TestPolicyTextContainsThe
      RequiredPassages` pins the source; since the M5 review both routes
      are prerendered so the passages are in the served HTML (`curl -s
      https://repose.herakraft.co/privacy | tr -s '[:space:]' ' ' | grep -c
      'We sample the processes'` is 1, verified live at 2026-09-21 02:47Z
      after the web roll, and the terms phrase likewise).
- [x] The Anthropic API key leaked in commit `b1a5915` has been rotated
      (done 2026-09-17) and the history has been rewritten or the repo made
      private before it is shared with contributors. Closed by DECISIONS
      I-193: the key was rotated and stays in history.
- [ ] `repose --version` prints a version, and `curl -fsSL
      https://repose.herakraft.co/install.sh | sh` installs it on macOS
      arm64, macOS x86_64, Linux x86_64, Linux arm64. Partial (M5 session,
      2026-09-21 02:16Z): the served `install.sh` (200 from the dashboard)
      run in a fresh `$HOME` on Linux x86_64 downloaded
      `repose_v0.1.4_linux_amd64.tar.gz`, verified it against
      `checksums.txt`, installed `~/.local/bin/repose` and added the PATH
      line; `repose version` prints `repose 0.1.4 (herakraft)`; all four
      release archives match `checksums.txt`; the Linux arm64 binary runs
      under `qemu-aarch64` and prints the same; the two macOS archives are
      valid Mach-O for their architectures. `repose --version` was
      `unknown flag` in v0.1.4 and is fixed on `main` for the next tag.
      Still owed: the install on a real macOS arm64, macOS x86_64 and Linux
      arm64 machine (the second human, M5 step 3), and a tag carrying
      `--version`.
- [x] `ops/RUNBOOK.md` has entries for every alert above. Evidence:
      `ops/check.sh` fails when an alert in `ops/alerts.yaml` has no
      RUNBOOK heading and passes on `main` (17 rules, 17 headings,
      2026-09-21); HostMemory80, HostUnreachable, SnapshotStale,
      BuildQueueStuck, GatewayAuthSpike and EgressHigh are the six the
      row names.
