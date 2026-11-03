# Definition of done

This is the global checklist. Every workstream doc ends with its own list; this
one applies to all of them and to the release. Only the evidence named in
an item closes it. "It compiles" closes nothing. "I
ran it once on my machine" closes nothing that says "on a host" or "in a
guest".

The failure this prevents: a workstream that is 99 percent done with the last
percent being the thing that makes it usable (the migration that was never
written, the systemd unit that is not enabled, the error path that panics, the
README that says "TODO"). Each of these has happened to someone; the list is
written so none of them goes unnoticed again.

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
- [ ] A new migration takes the next free version, and the loader proves
      it: two files claiming one version is an error, not an overwrite
      (DECISIONS I-854). Evidence: `go test ./internal/db -run
      TestTwoMigrationsMayNotClaimOneVersion` passes, and `ls
      internal/db/migrations | sed 's/\.\(up\|down\)\.sql$//' | sort -u |
      cut -c1-4 | uniq -d` prints nothing (the dedupe matters: every
      version has an up and a down, so counting filenames reports them
      all). The
      failure this prevents: two branches written in parallel both add
      `00NN_`, the second read overwrites the first's script, and the
      columns of one of them are never created on any database.
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
- [ ] A change to `boot.kernelParams` is confirmed by the behaviour it is
      supposed to produce, never by `/proc/cmdline`. A parameter the
      kernel does not recognise is accepted silently into the command
      line and handed to userspace: `panic_on_oops=1` rode a switch and a
      reboot into `/proc/cmdline` while `kernel.panic_on_oops` stayed 0,
      because it is a sysctl and not a parameter (DECISIONS I-988).
      Evidence: `dmesg | grep "Unknown kernel command line"` is empty on
      the booted host, and the parameter's own effect is read directly —
      `Dentry cache hash table ... linear` for `hashdist=0` (I-982),
      a non-zero `sysctl` for anything that is really a sysctl. Prefer
      `boot.kernel.sysctl` where the knob is one: it applies on a switch
      with no reboot, and it can be read back. `nix/hosts/tests`' "the
      kernel honoured what it was given" subtest holds the general form —
      no name the node passed appears in the kernel's Unknown-parameter
      line, and `kernel.panic_on_oops` reads back 1 — and `ci.yml` asserts
      the sysctl is still 1 in host-02's *config*. Neither retires this
      item: the test speaks for a VM's kernel and the eval for a config
      file, and the thing that bit us was a real host whose config was
      already right (DECISIONS I-1012).
      A knob worth setting is
      worth reading back *continuously*, not once: `repose_kernel_check`
      and `repose_host_panic_on_oops` are where a host's own answer goes,
      and a parameter added without one is checked at the moment somebody
      remembers to look (I-1010).
- [ ] A new or changed test asserts the property it is named for, not a
      proxy for it, and names the probe that proves it would fail without
      the fix. A proxy fails in both directions: red when the runner is
      busy and green when the fault is there but fast, which is how
      `main`'s CI stayed red for sixteen days on three rotating tests
      while each one was retuned in turn (DECISIONS I-968). Wall-clock
      budgets, counters another goroutine decrements, and "connect again
      and see what happens" are the three that have bitten. Evidence: the
      probe is pasted in the commit message with the assertion's output
      under it — the leak test reads "5 calls: 5 still open; 25 calls: 25
      still open" with `DisableKeepAlives: false`, the ban test reads
      "logged but not enforced" with the ban lookup disabled — and the
      test passes repeated `-race` runs under load, not once on an idle
      box.
- [ ] A test that configures a daemon asserts it is running, or says why
      it does not care. `host-network`, `host-storage` and `host-caches`
      stand up the hostd stub and were green for as long as `host-reboot`
      was red, with that stub crash-looping in all four nodes: they use
      the `hostd` user and the state directory and never ask whether the
      daemon is up, so they reported a healthy system around a dead
      process until the one test that does ask went red (DECISIONS
      I-1020). A check that is green in precisely the situation it exists
      for is worth less than no check, because it is read as evidence.
      Evidence: the test names the unit it waits for, or a comment says
      which part of the system it is deliberately not exercising.
- [ ] A test that forces state in the database first stops whatever else
      writes it. A connected fake host reverts `hosts.state` 50-75 ms after
      a test sets it, so an assertion that races the next heartbeat is green
      on an idle machine and red on a shared runner, and says nothing about
      which (DECISIONS I-1014). Evidence: the test passes repeatedly under
      load, not once on an idle box, and the commit names what was stopped
      (`DisconnectHost`, `StopEngine`) and why.
- [ ] A message a user or an operator reads has one owner, and a test
      that quotes it greps a substring of that owner. A VM test or a
      runbook retyping the words is a second spelling of them, and the
      release that reworded `control.ErrNotRunning` shipped a Go test
      forbidding the old words beside a VM test requiring them -- two host
      VM tests red on `main` for a day, unseen, because the VM tests need
      KVM and CI has no job for them (DECISIONS I-1024). The same goes for
      a *rendering* nobody here owns: nftables decides whether it prints
      `skuid "nixbuild"` or `skuid 997`. Evidence: the test reads the
      literal out of the other file rather than restating it, and reverting
      the quoted side fails it in seconds without a VM.
- [ ] A test is named for the property it asserts, not for the subject it
      touches. `TestBuildInfo` passed on every run while
      `repose_build_info{component="hostd"}` read `version="dev"` on every
      host, because it asserted that the labeller labels what it is handed
      and nothing asserted that hostd hands it anything (DECISIONS
      I-1030). A name that claims the subject is read as coverage of the
      subject by everyone who greps for it, which is how such a test
      survives: the three VM tests that stood a daemon up without asking
      whether it ran never claimed otherwise and were caught sooner.
      Evidence: the name says what would have to be false for the test to
      fail — `TestBuildInfoCarriesTheBinaryVersion`, not `TestBuildInfo`.
- [ ] A test that walks a table, a directory or a list asserts how many
      items it walked. A guard that matches nothing passes, and it reads
      exactly like a guard that matched everything and was happy: eleven
      rows of a gate's test ran and asserted nothing because the name
      column split on spaces, and the test reported ok against the very
      bug it was written for (DECISIONS I-1026); a decision-citation check
      fired on its own examples and a wording check on its own comment,
      which is the same thing from the other side. Evidence: delete a row,
      or point the walk at an empty directory, and the test fails saying
      what it expected to find.
- [ ] A shell tool the dev shell runs uses no builtin that nixpkgs' bash
      leaves out. `compgen` and `complete` are not there, and a missing
      builtin does not announce itself inside `!` -- it answers 127, which
      reads as a plain "no". `needs_of` walked past every Go package on
      that answer and charged `none` to Go-only changes inside `nix
      develop` while answering correctly outside it (DECISIONS I-1028).
      Evidence: the tool's own harness passes in both shells, with the
      counts written down.
- [ ] A change to `ops/dev/closure-equiv.py`, or to what it reads, keeps
      both of its answers. It decides whether a branch touching `nix/` is
      charged a deploy target, so an "unchanged" it gets wrong ships a host
      or the edge without a switch, and a "changed" it gets wrong charges a
      docs commit a deploy. Evidence: `go test ./test/supplychain -run
      TestDevToolHarnessesRun` executes `ops/dev/closure-equiv-test` (five
      cases, both directions, no nix needed) — since I-996 it is no longer a
      harness someone has to remember to type — and the two answers are
      confirmed against revisions of this repository — a branch that really
      changes the target reports it with the derivations between the change
      and the root, and a docs-only branch against the same target still
      reports the same system. The failure this prevents: the tool hashed
      `env` and not `structuredAttrs`, where nixpkgs now puts every
      generated file's content, so for 2646 of the edge's 4657 derivations
      it compared nothing at all and answered "unchanged" to opening a port
      (DECISIONS I-958).
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

`docs/DESIGN-LANGUAGE.md` is the spec (DECISIONS I-837). Run these from the
repository root with `W=apps/web/src`; each prints nothing unless the item
says otherwise. A hit is fixed, or the rule changes through a DECISIONS
entry and this list with it. Counts as of 2026-10-21.

- [ ] Colours come from tokens. Evidence: `rg -n
      '\b(text|bg|border|ring|fill|stroke|outline|decoration|from|via|to|divide|placeholder|caret|accent)-(zinc|gray|slate|stone|neutral|blue|sky|cyan|teal|emerald|green|lime|yellow|amber|orange|red|rose|pink|fuchsia|purple|violet|indigo)-[0-9]{2,3}\b'
      $W --glob '*.svelte'` prints 0 lines; `rg -n '#[0-9a-fA-F]{3,8}\b' $W
      --glob '*.{svelte,css}' --glob '!**/lib/hk/**' --glob
      '!**/routes/layout.css' --glob '!**/routes/design/**' --glob
      '!**/components/landing/**'` prints 0 lines,
      and `rg -n '#[0-9a-fA-F]{3,8}\b' $W/routes/layout.css` prints only the
      first-paint pair, 6 lines (`--page` and `--ink` in light, the dark
      media block and `[data-theme='dark']`); `rg -n
      'var\(--(page|ink|surface|sunken|rule|accent|frost|sh-|pic-)' $W --glob
      '*.svelte' --glob '!**/components/landing/**'` prints 0 lines. The
      landing's drawn pictures (`lib/components/landing`) keep their own
      palette, mapped onto the kit in `landing/pictures.css` (I-843).
- [ ] Every shipped SVG parses as XML. Evidence: `python3 -c "import
      glob,xml.dom.minidom as m;[m.parse(f) for f in
      glob.glob('apps/web/static/**/*.svg',recursive=True)+glob.glob('apps/web/src/**/*.svg',recursive=True)]"`
      exits 0. A hand-written comment with a double hyphen in it is not
      well-formed, and a favicon that does not parse is dropped for the
      PNG without an error anywhere (I-938).
- [ ] Status colour stays in badges, dots and banners. Evidence: `rg -n
      'color:\s*var\(--hk-(ok|warn|err)\)' $W --glob '*.{svelte,css}' --glob
      '!**/lib/hk/**' --glob '!**/routes/design/**'` prints only the outage
      bar (`routes/+layout.svelte`) and the error toast (`routes/layout.css`),
      which are banners, 2 lines.
- [ ] One scheme mechanism. Evidence: `rg -ln 'prefers-color-scheme' $W`
      prints only `app.html`, `lib/app-html.test.ts`, `lib/hk/tokens.css`,
      `lib/hk/repose.css`, `lib/theme.svelte.ts`, `lib/theme.test.ts`,
      `routes/layout.css`, and the pictures `landing/Hero.svelte`,
      `landing/Browser.svelte`, `landing/Localhost.svelte` and
      `landing/pictures.css`, which pair each OS rule with the switch's
      `data-theme` rule (I-843), 11 files; `rg -n
      'class="dark|classList\.(add|toggle)\(.dark' $W` and `rg -n
      'theme="system"|richColors' $W --glob '*.svelte'` print 0 lines.
- [ ] The inline script's hash is in the CSP, and the first paint matches the
      tokens. Evidence: `pnpm -C apps/web exec vitest run
      src/lib/app-html.test.ts` passes.
- [ ] Headings are serif 400 and weight stops at 600. Evidence: `rg -n
      'font-(semibold|bold|extrabold|black)\b|<b>' $W --glob '*.svelte'`
      prints 0 lines; `rg -n '<strong' $W --glob '*.svelte' --glob
      '!**/routes/design/**'` prints 0 lines; `rg -nP
      'font-weight:\s*(700|800|900|bold)' $W --glob '*.{svelte,css}' --glob
      '!**/lib/hk/tokens.css' --glob '!**/components/landing/**'` prints 0
      lines (a picture draws a captured app's bold as the app does).
- [ ] Fonts are first-party and the kit's three. Evidence: `rg -n
      'fonts\.(googleapis|gstatic)' apps/web/src apps/web/static
      apps/web/svelte.config.js --glob '!**/lib/hk/tokens.css'` prints 0
      lines (the kit's file names Google Fonts in a comment); `rg -n
      'JetBrains Mono' apps/web/src apps/web/static --glob
      '!**/lib/hk/tokens.css' --glob '!**/components/landing/**'` prints 0
      lines; a page load's network panel
      shows only the site's host.
- [ ] Radii are the kit's. Evidence: `rg -n
      'rounded-(sm|md|lg|xl|2xl|3xl)\b|rounded-\[' $W --glob '*.svelte'`
      prints 0 lines; `rg -nP
      'border-radius:\s*+(?!var\(--hk-radius|0[;\s]|50%|inherit|9999?px)' $W
      --glob '*.{svelte,css}' --glob '!**/lib/hk/tokens.css' --glob
      '!**/components/landing/**'` prints 0 lines (`\s*+` is possessive: a plain `\s*` backtracks and lets every
      declaration through).
- [ ] Elevation is three levels. Evidence: `rg -n
      '\bshadow-(sm|md|lg|xl|2xl|inner)\b' $W --glob '*.svelte'` prints 0
      lines; `rg -nUP 'box-shadow:\s*+(?!none|var\(--hk-|inset
      -?[0-9.]+(px)? -?[0-9.]+(px)? 0 (-?[0-9.]+px )?var\(--hk-|0 0 0
      [0-9.]+px var\(--hk-)' $W --glob '*.{svelte,css}' --glob
      '!**/lib/hk/**' --glob '!**/components/landing/**'` prints 0 lines (`-U` reads a shadow that prettier
      broke over several lines from its first value).
- [ ] Motion uses the kit's curve and durations, and nothing moves on hover.
      Evidence: `rg -nP 'transition[^;]*\b\d+m?s\b' $W --glob
      '*.{svelte,css}' --glob '!**/lib/hk/**' --glob
      '!**/components/landing/**'` prints 0 lines; `rg -n
      'duration-[0-9]|hover:(-?translate|scale)' $W --glob '*.svelte'` prints
      0 lines; `rg -ln 'animejs' apps/web/package.json $W` prints only
      `apps/web/package.json` and the landing's pictures (I-843);
      under reduced motion `document.getAnimations()` is empty on `/`,
      `/projects` and a project page (design.spec, landing-motion.spec).
- [ ] Every picture stops on its final frame when reduced motion turns on
      mid-session. Evidence: `rg --files-without-match watchReducedMotion
      apps/web/src/lib/components/landing/{Hero,OneCommand,ComesBack,Browser,Localhost}.svelte`
      prints 0 lines. The suites above read the setting at load; this is
      the five drawn pictures answering a change made while the page is
      open (I-397, and the pictures restored by I-843).
- [ ] Sizes come from the scale. Evidence: `rg -n
      'text-\[[0-9.]+(px|rem|em)\]' $W --glob '*.svelte'` and `rg -n
      '![pm][xytrbl]?-' $W --glob '*.svelte'` print 0 lines.
- [ ] The legacy classes are gone. Evidence: `rg -nP
      'class="[^"]*(?<![\w-])(btn|btn-quiet|btn-ghost|btn-danger|badge|dot|card|banner|field|stat-row|codeblock|check-row|frost-band|table)(?![\w-])'
      $W --glob '*.svelte'` prints 0 lines (the lookarounds keep `hk-btn`
      and its kin from matching).
- [ ] Every shared class and custom property is namespaced and documented.
      Evidence: `rg -no '\.hk-[a-z0-9-]+' apps/web/src/lib/hk/repose.css |
      sed 's/.*:\.//' | sort -u | while read c; do rg -qP
      "(?<![\w-])$c(?![\w-])" docs/DESIGN-LANGUAGE.md || echo "$c"; done`
      prints 0 lines; `rg -n --glob '*.svelte' -e '--[a-z]+-[a-z]+:' $W | rg
      -v -e '--hk-|--i:|--n:|--cols:|--d:' | rg -v 'components/landing/'`
      prints 0 lines (with `-e`: after
      a bare `--`, rg reads `--glob` as a path).
- [ ] No native or modal dialogs outside the docs search palette and the
      secret dialog. Evidence:
      `rg -n 'window\.confirm|[^.\w/]confirm\(|<dialog|showModal' $W --glob
      '*.{svelte,ts}' | rg -v '//|onconfirm'` prints only
      `lib/components/DocsSearch.svelte` and
      `lib/components/SecretDialog.svelte`, 4 lines (I-841, amended
      2026-10-21; I-929: a pasted value gets a page of its own).
- [ ] Copy holds. Evidence: `pnpm -C apps/web exec vitest run
      src/lib/copy.test.ts src/lib/docs.test.ts` passes; `rg -n '—'
      $W/routes $W/lib/components --glob '*.svelte' | rg -v '<!--|//|\*'`
      prints 0 lines; `rg -niw
      'seamlessly|effortlessly|powerful|robust|unlock|leverage|empower' $W
      --glob '*.svelte'` prints 0 lines; `rg -nP '>[^<{]*!\s*<' $W --glob
      '*.svelte'` prints 0 lines.
- [ ] Every page is on the one frame and the one header (I-656). Evidence:
      `rg -n 'max-w-(5xl|7xl)|HeaderFrame' $W --glob '*.svelte'` prints 0
      lines; `pnpm test:integration tests/design.spec.ts -g 'logo stands|header
      row fits'` passes.
- [ ] Touch targets are 44px. Evidence: `pnpm test:integration
      tests/design.spec.ts -g 'touch targets'` passes (header and main of
      nine pages at 390 with touch).
- [ ] A new mark that shows state in a fill or a coloured edge has a rule in
      the `forced-colors` block of `layout.css` or `repose.css`. Evidence: a
      capture with Chromium's `forcedColors: 'active'`.
- [ ] The keyboard path, forced colours, reduced motion, the theme switch and
      the phone column hold. Evidence: `pnpm build`, then `pnpm
      test:integration tests/design.spec.ts` passes; a panel that opens in
      place of a button adds its focus check there (I-392).
- [ ] The accessibility gate passes: `pnpm build`, then `pnpm a11y` in
      `apps/web`, 76 runs (19 pages, light and dark, 1440 and 390), with
      `KNOWN_FAILURES` empty or each entry carrying a reason and, where it
      fails at one width only, that width.
- [ ] Judged at real size: viewport captures or crops at 1x, 1440 and 390
      wide, light and dark, of every changed page, plus the dark theme
      chosen by the switch on a light OS (CLAUDE.md "Judge visuals at real
      size").
- [ ] A release's notes exist before its tag and read as Simplified
      Technical English (I-798). Evidence: `gh release view <tag> --json body
      --jq '.body | length'` is over 400, and the notes name every
      user-visible change, what the release does not fix with its numbers,
      and what it costs. `release.yml` fails the publish below 400 bytes.

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
      (R3-20) stays a fallback; I-39 records the v7 sizes used.
- [ ] A second human has completed login, run, attach, stop, start, secrets,
      config apply, snapshot restore, destroy on their own laptop.
- [ ] **Paging reaches a human.** One contact point in the owner's Grafana,
      set as the default, and the 53 alert rules pushed in
      (`ops/README.md` step 3). Evidence: **Test** on that contact point
      arrives; and `GRAFANA_URL=... GRAFANA_TOKEN=... python3
      ops/grafana/verify.py --wait 120` exits 0, which means a rule for every
      alert the generator emits, under its own uid, pointed at a datasource
      Grafana can actually query, and every one of them evaluated without
      error. Acceptance is not enough: with `execErrState: OK` a rule whose
      query fails reads OK, so the datasource test and the health are the
      check (I-944).
      Until this is closed every page, including the host-down ones, reaches
      nobody: the 2026-10-10 outage paged no one because
      `ntfy.herakraft.co` answered 503 on every path (DECISIONS I-648), and
      the routing written after it was for an Alertmanager the server never
      had (I-942). Not covered, knowingly: a personal server that is wholly
      down notifies nobody, because the only machine available to watch it
      from is itself (I-944). Azure's VM email is the one thing left.
- [ ] **Prometheus scrapes the HTTP api process.** `api-grpc` publishes a
      host port and is scraped directly; `api` publishes none, because a
      published port would stop its rolling deploys (I-2), so its
      `/metrics` is reached only through the Traefik router — and that
      router needs ten custom labels pasted into Coolify by hand
      (`ops/coolify/README.md` "The api's metrics", I-133). Nothing else
      asserts that anyone pasted them. What is lost while `job="api"` is
      down is everything counted on an HTTP request: `ApiErrors` has no
      series to read, the gate-refusal panel is empty, and
      `BillingWebhookErrors` cannot fire — so a Polar delivery that is
      trusted and not applied, which leaves a subscription Polar believes
      in and the api does not, is silent. The money row is not affected:
      its gauges are set by the background ticks, which run only in
      `api-grpc` (DECISIONS I-800, I-802). Evidence: `up{job="api"}` is 1
      in Prometheus, and `curl` of
      `https://api.repose.herakraft.co/metrics` from outside the tunnel
      answers 403.
- [ ] **The Azure alert path is proven**, not just created. Evidence: the
      portal's **Test** on action group `repose-prod` delivers to
      `alert_email`. `az monitor action-group test-notifications create`
      refuses with "no valid receivers", so the portal is the way
      (I-648).
- [ ] **A console login exists on host-01 and the edge.** Both are built
      with root locked, so the Azure serial console shows output but cannot
      log in, and a host that will not boot cannot be rescued. Evidence: a
      yescrypt hash in `/var/lib/repose/root-console.hash` on each machine
      (made by the owner, never in the repo), a switch after it, and one
      login on the portal's serial console. SSH stays key-only; the boot
      menu waits 10 s so an older generation can be chosen (I-649, I-654).
- [ ] **A planned host-01 restart has been ridden out.** It is the only
      proof of the new initrd pool activation (I-643), the restart of
      machines that were running (I-645) and the guest park on prod
      (I-652, I-685, I-686), none of which a switch exercises. Evidence:
      `journalctl -u hostd` shows `guest_park` before the shutdown and
      `guest_unpark` after the boot, every guest back `running` with its
      earlier pids, `lvs -o seg_monitor` says `monitored`, and the pool
      unit logged `pool_activated`. `hostd park` is all-or-nothing, so
      this cannot be rehearsed on one guest while others must keep
      running (I-696).
- [ ] **The herdr laptop checks** from release r20261008-1 have been run on
      the owner's laptop. Evidence: the five steps in commit `7bd8807c`'s
      message, with their output (I-596, I-597, I-598).
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
- [ ] Polar: the sandbox gate of `docs/ops/M4-GATE.md` passes (checkout to
      `trial`, the trial's end and `order.paid` to `active`, a failed
      renewal to `past_due` and the 3-day stop, an egress overage line to
      the cent, I-604);
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
      own; `ops/check.sh` runs all three checks. Loaded against real
      production series they evaluate healthy: none firing, none in
      error. *Wired* here means loaded and tested, not yet delivering:
      delivery needs the WireGuard peer that `10-observability.md` §9 still
      has open. There are 57 alerts, 56 of them generated into Grafana's own
      alerting by `ops/grafana/alerts-gen.py` and pushed in by
      `ops/grafana/push.py` over the API; `ops/check.sh --grafana` proves
      every one of them against a running Grafana, twice, and the drift
      check finds no difference (I-942, I-944). Who gets told is one contact
      point the owner adds in the UI; repose provisions no notification
      policy. Delivery is live only once that contact point exists
      (`ops/README.md` step 3) and one test alert arrives.
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
      `unknown flag` in v0.1.4. That half is closed: `cli.go` sets the
      version template and `repose --version` prints `repose 0.1.37
      (herakraft)` from the v0.1.37 tag. Still owed: the install on a real
      macOS arm64, macOS x86_64 and Linux arm64 machine (the second human,
      M5 step 3).
- [x] `ops/RUNBOOK.md` has entries for every alert above. Evidence:
      `ops/check.sh` fails when an alert in `ops/alerts.yaml` or
      `ops/alerts-loki.yaml` (I-980) has no RUNBOOK heading and passes on
      `main` (17 rules, 17 headings, 2026-09-21); HostMemory80, HostUnreachable, SnapshotStale,
      BuildQueueStuck, GatewayAuthSpike and EgressHigh are the six the
      row names.
