# Workstream 14: security

## 1. Goal

Verify, not assume, that one tenant cannot reach another tenant's guest,
data, or credentials, and that the platform's own privacy promises are
mechanically true. This workstream runs alongside every other one and ends
with the isolation tests in `../CHECKLIST.md` passing on a shared host and
the policy text published.

## 2. Scope: builds

- `../SECURITY.md`: the threat model, kept current as decisions land.
- `test/isolation/`: a Go test suite that runs against a real host with two
  guests owned by two users and asserts every boundary in §5.
- The privacy policy and terms of service text in `apps/web/src/content/
  legal/`, with the two required passages below verbatim.
- A review checklist for each other workstream, applied at their PR time,
  recorded as a comment in `STATUS.md`.
- `repose-admin audit` to query `audit_log`, and the audit coverage list in
  §5.
- An incident response note in `../ops/RUNBOOK.md`: who is told, what is
  captured, how a tenant is notified.

## 3. Scope: does not build

- The isolation mechanisms themselves: nftables and bridge layout
  (workstream 01), KVM and virtio-fs configuration (01, 03), sshd
  principals (02), certificate issuance (05), gateway checks (06), secrets
  encryption (05). This workstream tests them and blocks release on them.
- Per-project LUKS. Recorded as not mitigated (R3-10).
- A bug bounty or external audit. After launch.

## 4. Interfaces

Owns: `../SECURITY.md`, the isolation test suite, the legal text.

Consumes: every interface, read-only.

## 5. Design detail

### Boundaries and what proves each

| Boundary | Mechanism | Test in `test/isolation/` |
|---|---|---|
| Guest A cannot reach guest B | per-guest tap, nftables `guest_fwd` default drop, no shared L2 | from A: `ping`, `arping`, `nmap -p 22,5000` against B's IP all fail; `tcpdump` on B's tap sees nothing from A's MAC |
| Guest cannot reach host | `guest_in` drop (ICMP echo excepted at 5/s, DECISIONS I-18; replies to flows the host itself opened, I-74, which no packet a guest sends first can match) | from A: connect to host `.1` on 22, 9101, 8080 fails; a `ping` flood of `.1` loses most packets |
| Guest cannot reach IMDS | explicit drop of `169.254.169.254/32` in `guest_fwd` | `curl -H Metadata:true http://169.254.169.254/...` times out from A |
| Guest cannot reach other hosts' guest ranges | drop `10.64.0.0/12` | connect to another host's guest IP fails |
| Guest cannot write the store | virtio-fs exported read-only, `virtiofsd --sandbox namespace` as an unprivileged user (I-48) | `touch /nix/store/x` fails; `ls /nix/store/.links` is absent or unreadable |
| A guest escape does not land as root | `guest@<id>` runs Cloud Hypervisor as `hostd` with `DevicePolicy=closed`, no capabilities, a private guests directory (I-51) | on the host: `ps -o user= -p $(systemctl show -p MainPID --value guest@<id>)` is `hostd`; `systemctl show guest@<id> -p User,NoNewPrivileges,DevicePolicy` |
| Guest cannot see host block devices | only its own thin volume is a virtio-blk device | `lsblk` shows one disk |
| Guest cannot escape memory or CPU limits | `MemoryMax` and `CPUQuota` on the transient unit, and CH's own limits | a fork bomb and a memory hog in A leave B's benchmark within 10 percent |
| A's certificate cannot open B | gateway principal check, guest sshd `AuthorizedPrincipalsFile` | `ssh b.user@ssh...` with A's cert is rejected at the gateway with `certificate not valid for this project`; a direct `ssh` to B's IP over the operator WireGuard with A's cert is rejected by sshd |
| An expired or revoked certificate is rejected | validity window, revocation list | issue, revoke, connect: rejected within 30 s of revocation |
| A user cannot read another user's project via the api | every query scoped by `user_id` from the JWT | `GET /projects/<B's id>` as A is 404, not 403 (existence is not leaked) |
| A user cannot read secret values | api never returns them | `GET /secrets` has no `value` field; `audit_log` has no values |
| hostd for host X cannot act on host Y | mTLS CN is the host id and every command is checked against it | a replayed command for Y's guest on X's stream returns `forbidden` |
| Hook socket cannot be used by one agent to spoof another project | the socket is inside the guest, and guestd stamps the guest id; hostd stamps the project id from its own table | a forged payload with another project id is ignored and logged `hook_bad_payload` |
| Console logs do not carry terminal contents | the guest's serial console shows only kernel and systemd output; user shells are on ptys | grep a session's typed text in Loki: absent |

### Policy text requirements

The privacy policy must contain, verbatim:

> We sample the processes running in your environment once a minute and
> record their names, CPU time, memory use and network bytes. We never
> record command-line arguments, environment variables, file paths, file
> contents, terminal contents, or the prompts you give to any agent.

The terms must contain, in substance:

> Coding agents such as Claude Code run inside your environment under your
> own account with that agent's provider. repose does not hold, proxy, or
> resell those credentials. You are responsible for complying with each
> provider's terms for hosted use.

The reason these are here rather than in a legal doc alone: the first is a
promise the `obs` redaction and the `Sample` message shape must keep, and
the second is the condition under which Anthropic permits Claude Code on a
hosted platform (each user authenticates with their own credentials).

### Secrets handling review

Applied to workstreams 05, 07 and 04 at review:

- Named secrets: ciphertext only in Postgres; DEK wrapped by Key Vault;
  the api's Key Vault permission is wrap/unwrap only; plaintext exists in
  api memory for the duration of a request and in the guest's tmpfs.
- Tool logins: copied laptop to guest over the user's own SSH session; the
  api does not see them; the guest file modes are 0600 `dev`.
- Claude credentials: never copied; `rg 'credentials.json' cmd internal`
  returns only the explicit exclusion in the CLI's sync list.
- CA private keys: in the api's secret store (Coolify secret injected as
  an env var pointing at a mounted file), never in Postgres, never logged.
- Join tokens and hostd certificates: single-use and rotated monthly.

### Audit coverage

`audit_log` rows for: every certificate issue and revoke, every `Exec` over
gRPC or vsock, every `repose-admin` command, every operator SSH login to a
host or the edge, every secret set or delete (name only), every user
suspension, every restore. `repose-admin audit --user`, `--project`,
`--since` query it. Retention: indefinite.

### Operator access

Operators reach hosts and the edge only over the edge's WireGuard with a
certificate from the Host CA (`repose-admin operator-cert`), 8-hour
validity. There are no operator passwords. Touching a guest goes through
`repose-admin exec`, which is audited; `virsh`-style direct access to a
guest's console is available only from the host and is logged by the PAM
hook.

### Incident basics

If a tenant boundary is found broken: stop scheduling (`repose-admin hosts
drain --all`), snapshot the affected guests, capture hostd and gateway logs
for the window, notify affected users within 72 hours with what was
exposed, record the timeline in `docs/incidents/YYYY-MM-DD.md`. The runbook
entry "Suspected cross-tenant access" has the commands.

## 6. Failure modes

| Failure | Outcome |
|---|---|
| An isolation test fails in CI | the release is blocked; the owning workstream's checklist item reopens. |
| The policy text and the `Sample` message drift (a new field) | the `obs` naming test includes a fixture of the policy sentence and fails if `ProcSample` gains a field not listed in `SECURITY.md`. |
| An operator logs in without a certificate | impossible by sshd config; a password login attempt is logged and alerts as `GatewayAuthSpike` if repeated. |

## 7. Testing

`test/isolation/` needs a real host with two guests. It runs in a nightly
job against the staging host and before every release. Each test is named
after the boundary row above. The api-level tests (`404 not 403`, no secret
values) run in ordinary CI against the fake hostd.

## 8. Rollback

Not applicable; this workstream adds tests and text. If a test is found to
be wrong, fix the test with a `DECISIONS.md` note explaining what the
boundary actually is.

## 9. Checklist

Ticked 2026-09-20 by the M3 integration session from `STATUS.md` (the 14
lines of 2026-09-20), `docs/security/review-2026-09-20.md` and the tree;
`[~]` is a row whose local half is closed and whose host half is an
`ops/checks/` run on host-01 (`ops/checks/README.md`).

- [x] `../SECURITY.md` matches the current design: every boundary in
      `ARCHITECTURE.md` has a row here. Evidence: the review by someone
      other than the author, main `afc5411` ("m3-web(14): the
      SECURITY/ARCHITECTURE boundary review, by someone else",
      2026-09-20).
- [~] Every row in the boundary table has a passing test on a shared host
      with two guests from two users. Evidence: `ops/checks/out/
      isolation-go-20260921T003759Z.txt`, host 01a0bfd8-120c-7ec8-b0a0-
      ed83a33edd41, 2026-09-21 00:37Z, A = m3-check (heracraft, through
      the gateway), B = m3-iso-c (repose-m3-b, through audited exec): PASS
      TestGuestACannotReachGuestB (ping, arping, 22, 5000 refused; tcpdump
      on B's tap saw nothing from A's MAC), TestGuestCannotReachHost (22,
      9100, 9101, 8080 refused, echo flood rate-limited),
      TestGuestCannotReachIMDS (IMDS and the wire server),
      TestGuestCannotReachOtherHostsGuests (the next /22 and every private
      range refused, cache.nixos.org reachable),
      TestGuestCannotSpoofAnotherAddress, TestGuestCannotWriteStore
      (`.links` empty and unwritable), TestGuestSeesOneDisk (vda only;
      zram is the guest's own), TestHypervisorRunsAsHostdUser (User=hostd,
      NoNewPrivileges, DevicePolicy=closed, ProtectSystem=strict, the
      volume node root:hostd:660), TestCertificateForACannotOpenBAtGateway
      (`certificate not valid for this project`),
      TestForgedHookPayloadCannotNameAnotherProject (204 and 400,
      `hook_bad_payload`, hostd carried neither forged id nor the summary),
      TestConsoleLogCarriesNoTerminalContents,
      TestOtherUsersProjectIs404NotForbidden (project and its five
      sub-routes), TestSecretsListHasNoValues. TestRevokedCertificateRejected
      passed once at 00:15Z (rejected 7 s after `POST /certs/revoke`) and is
      opt-in since, because it revokes a certificate of the logged-in
      account (the owner's). SKIP by design: the direct-sshd half of the
      certificate row (it would put the operator's key on the host). The
      limits row is the next item (run 00:39Z, same file set:
      `isolation-go-20260921T003951Z.txt`, 18 PASS, 1 SKIP). Open: "hostd
      for host X cannot act on host Y" (one host). — waits on: second host
      ("hostd for host X cannot act on host Y").
- [x] The fork bomb and memory hog test leaves the neighbour within 10
      percent. Evidence (`TestForkBombAndMemoryHogLeaveNeighbourWithinTenPercent`,
      host-01, 2026-09-21 00:40Z, A = m3-check running a real fork bomb
      under `systemd-run -p TasksMax=8000` and a python memory hog to
      MemoryError, B = m3-iso-c hashing 256 MiB from /dev/zero, best of 3):
      baseline in B 0.256 s, during the bomb and hog 0.260 s, ratio 1.02;
      A answered afterwards.
- [x] Privacy policy and terms contain the two required passages. Evidence:
      the page URL and a grep of the source. `test/isolation/policy_test.go`
      `TestPolicyTextContainsTheRequiredPassages` pins both passages in
      `apps/web/src/content/legal/{privacy,terms}.md` and the same
      sentence in `docs/SECURITY.md`; the live URLs
      (`https://repose.herakraft.co/privacy`, `/terms`) are the m3-web
      session's text row. — closed: STATUS 2026-09-20 m3-web progress line
      ("14 text rows": both passages live at
      https://repose.herakraft.co/privacy and /terms) and
      apps/web/tests-live/public.spec.ts against the served pages.
- [x] `rg 'credentials.json'` shows only the exclusion. Evidence
      (2026-09-20, `rg -n 'credentials.json' cmd internal`, non-test):
      `internal/cli/creds.go:29` (the comment on the allowlist that never
      includes it), `internal/cli/mux_tmux.go:179` (moved from
      `tmux.go` by mux-cli, 2026-10-05; `test -s
      ~/.claude/.credentials.json` in the guest, which decides whether
      `run` attaches for the user to log in instead of sending a prompt,
      DESIGN §11), and `internal/cli/config.go`, `keychain_other.go` (the
      CLI's own `~/.config/repose/credentials.json`, a different file).
      Pinned by `TestClaudeCredentialsAppearOnlyAsAnExclusion`
      (`test/isolation/policy_test.go`) and
      `internal/cli/run_integration_test.go` (a laptop home holding
      `.claude/.credentials.json`, `.gemini/oauth_creds.json` and an SSH
      key: none travels).
- [~] Every audited action in §5 writes an `audit_log` row. Evidence
      (`ops/checks/audit-rows.sh`, 2026-09-21 00:22Z, project m3-check):
      rows since the start by action, cert_issue 2, cert_revoke 1,
      secret_put 1, secret_delete 1, exec 1 (the gRPC Exec, hostd's journal
      carrying `audit_id` and length, no argv, I-53), project_snapshot 1;
      plus project_create and project_destroy rows from the synthetic
      tenants (I-113) and host_smoke. No producer, still open (review
      L-13): an operator SSH login (journal line `operator_login` from
      `hostd audit-login`, seen on host-01 at every operator login tonight,
      nothing in `audit_log`); a restore through the user route. User
      suspension not triggered (it stops the user's guests). M5 review,
      `audit_log` 48 h on production (2026-09-21 02:05Z): 22 actions,
      `exec` 22 matching the api-driven `exec_audit` lines on host-01
      (`audit_id`, `argv_len`, no argv), `cert_issue` 20, `cert_revoke` 4,
      `secret_put` 5, `secret_delete` 3, `project_*`, `host_*`, `ca_*`,
      `base_*`, `user_*`, `billing_enforce`; the `operator_login`
      producer is I-140 (same session: PAM hook to control socket to host
      event to `audit_log`, with the certificate's key id and serial),
      live on host-01 at the next switch; the user-route restore row is
      still open (L-13's other half, 05). — open: the user-route restore
      writes no `audit_log` row (review L-13; internal/api/http/restore.go and
      snapshots.go have no audit call), and no production `operator_login` row
      after the I-140 switch is pasted.
- [~] Operator access works only with a certificate; a password attempt is
      logged. Evidence (2026-09-21 00:38Z): `ssh -o
      PreferredAuthentications=password -o PubkeyAuthentication=no` to
      root@10.255.0.2 through the edge and to root@<edge>:2222 both exit
      255; host sshd journal `Connection closed by authenticating user root
      10.255.0.1 port 41694 [preauth]`, edge sshd journal `Connection
      closed by authenticating user root 20.102.97.100 port 53484
      [preauth]`; host `sshd_config`: PasswordAuthentication no,
      KbdInteractiveAuthentication no, PermitRootLogin prohibit-password,
      TrustedUserCAKeys /run/repose/host_ca.pub, ListenAddress 10.255.0.2.
      Half open: the host still admits the bootstrap key on the WireGuard
      address rather than a certificate (review M-1, `RegisterResponse` has
      no Host CA), so "only with a certificate" is not yet true. Re-read
      as deployed by the M5 review (2026-09-21 02:00Z,
      `security/review-2026-09-21.md`): edge `sshd -T` on 2222 has
      password and keyboard-interactive off, `prohibit-password`, verbose
      logging, admitted by nft only from the tunnel and the operator
      address (the 1,597 brute-force lines of the day all predate the
      23:36Z switch; 3 auth lines since, all accepted operator keys), so
      the 2026-09-20 review's M-2 is closed; host-01 `host_ca.pub` is
      still 0 bytes and its 750 operator logins in 24 h are all by the
      bootstrap key, each with a `hostd audit-login` journal line. I-139
      (same session) puts the Host CA in `RegisterResponse` and
      `host.json`; host-01 gets it at the switch carrying I-137 and I-139
      with the runbook's by-hand step, after which an operator
      certificate opens it; "only" then waits on `bootstrap.enable` being
      turned off (01/11). — waits on: owner (RUNBOOK "Retiring a host's
      bootstrap key", DECISIONS I-177: Host CA on host-01, an
      operator-certificate login seen, then `bootstrap.keyUntilHostCA = true`
      and a switch).
- [x] Secrets review comments exist in `STATUS.md` for workstreams 04, 05,
      07. Evidence: the lines. 04: 2026-09-20 (14 review line). 05 and
      07: 2026-09-20 (M3 integration session lines, below the 14 lines). —
      closed: STATUS 2026-09-20 lines `14-security | review 04-guestd` and the
      m3-integration `14-security | review 05` and `review 07` lines.
- [~] Incident runbook entry exists and the commands in it were run once
      on staging. Evidence: notes. The entry is `ops/RUNBOOK.md`
      "Suspected cross-tenant access"; the suite invocation in it is what
      `ops/checks/isolation-host01.sh` runs on host-01 (there is no
      staging host; the capture commands have not been run). — open: the
      capture commands in RUNBOOK "Suspected cross-tenant access" have never
      been run; run them once on host-01 against a test project (there is no
      staging host) and paste the notes.
