# Security

The threat model for repose, the boundaries that hold it up, the rules that
are not negotiable, and the things the first release deliberately does not
mitigate. `workstreams/14-security.md` is the work that verifies this doc.

## Assets

- A tenant's project files, Docker images and shell history on their volume.
- A tenant's tool logins inside the guest (gh, Codex, opencode) and their
  Claude session.
- A tenant's named secrets (API keys they chose to store).
- The platform CAs (user and host), hostd client certificates, the Key
  Vault wrapping key, Paddle and Logto credentials.
- The host itself: root on a host is every guest on it.
- Billing integrity: usage rows, the subscriptions table and the Paddle customer mapping.

## Actors

- **Another tenant**: has a guest on the same host, a valid certificate for
  their own project, and arbitrary code execution inside their guest. This
  is the primary adversary; agents run untrusted code by design.
- **An anonymous internet user**: can reach the gateway on 22, the api and
  dashboard on 443, and Logto.
- **A malicious or compromised agent** inside a tenant's own guest: can do
  anything the tenant can inside the guest, including using their tool
  logins. This is the tenant's risk, bounded by what the guest can reach.
- **The operator**: root on hosts. Trusted, audited, and (until LUKS) able
  to read volumes.
- **A stolen laptop**: holds a refresh token and an SSH certificate.

## Boundaries

From `ARCHITECTURE.md`, with the mechanism and the actor it stops. Its
five trust boundaries are rows 1, 2, 4 and 3 here in that order; its
fifth, **operator to tenant data**, has no mechanism to describe and is
under "Not mitigated in the first release" instead, which is what
`ARCHITECTURE.md` itself says of it ("Recorded, not mitigated, until
per-project LUKS"). Rows 5 to 11 are boundaries this document adds because
they stop an actor the component map does not draw.

1. **KVM between guest and host.** Cloud Hypervisor on KVM, launched by
   hostd from the guest's system closure (DECISIONS I-27) as the
   unprivileged `hostd` user in a systemd sandbox that allows it
   `/dev/kvm`, `/dev/net/tun`, its own volume, its own guest directory
   and no network address family (I-51); the guest sees
   one block device (its thin volume), one tap, one vsock, one virtio-fs
   mount and a serial console. The virtio-fs share is
   `/run/repose/store-export`, a read-only `nosuid,nodev` bind of the
   store with an empty tmpfs over `.links`, served by an unprivileged
   `virtiofsd` in a user and mount namespace sandbox (I-48). Stops: a
   tenant or their agent reaching the host or the store's write path, or
   enumerating other tenants' closures through the hard-link farm.
2. **The bridge and nftables between guests.** Per-guest tap attached
   `isolated on learning off flood off` with a static FDB entry; the
   `bridge repose` table drops every switched frame and admits ARP and
   IPv4 to the host only from the `(mac, ip, tap)` tuple hostd registered
   (I-18: frames between two taps never reach the inet forward hook, so
   the drop has to live in the bridge family, and `learning off` is what
   stops a guest claiming another guest's MAC). The `inet repose` table
   drops guest traffic to the host except rate-limited ICMP echo and the
   reply direction of flows the host itself opened (I-74, which is how an
   operator reaches a guest's sshd until the gateway exists; a packet a
   guest sends first is the original direction and is dropped), to IMDS
   and the Azure wire server, to every other guest range, and to every
   private range, and NATs the rest out of the provider NIC. Stops: a
   tenant reaching another tenant's sshd, guestd or dev servers, the
   host, the VNet, or the mesh.
3. **SSH certificates with project principals**, checked twice: at the
   gateway (route lookup plus principal match) and at the guest's sshd.
   24-hour validity (I-267), revocation list held for the whole lifetime
   (I-434). An open connection ends when its certificate is revoked or
   expires (I-436). A login under another user's handle is refused before
   any lookup, so it does not reveal whether the project exists (I-437).
   Stops: a tenant or a stolen certificate opening another project; a
   stolen laptop, open connections included, after 24 hours or within
   30 s of `repose logout` from another device.
4. **mTLS per host** with the host id as CN; every command checked against
   the stream's identity. The api admits only the serial it last issued to
   that host (and the one a rotate replaced, until the new certificate
   connects), and refuses a host marked `lost` or `retired` on Rotate, on
   Session and at the next heartbeat of an open stream (I-432). The
   `/internal` listener admits only the gateway's certificate (CN
   `gateway`); a host certificate from the same CA fails its handshake
   (I-431). Stops: a compromised host acting on another host's guests or
   as the gateway; a lost or retired host's certificate outliving it.
5. **JWT scoping**: every api query is scoped by the `sub` in the token;
   cross-user lookups return 404. Stops: enumeration.
6. **Envelope encryption for named secrets**: Postgres holds ciphertext and
   a wrapped DEK; Key Vault holds the wrapping key; the api can wrap and
   unwrap but not export. Each ciphertext is bound to its project id and
   name, so a row copied into another project does not decrypt there
   (I-433). Stops: a Postgres dump (including the R2 backups) revealing
   secrets; a database write moving one project's secrets, or the
   platform's CA keys, into another project's guest.
7. **Resource limits per guest**: `MemoryMax`, `CPUQuota`, shaping both
   ways on the tap (200 Mbit/s out, 1 Gbit/s in from outside the host),
   a disk rate limit per class, a cap on tracked connections, a console log
   rate, build time and closure caps, and the host's thin volumes together
   bounded at 1.5 times the shared pool (DECISIONS I-217, I-449..I-453).
   Stops: a tenant degrading neighbours, or filling a table or buffer the
   host's other guests depend on. Not stopped: one tenant writing its own
   volume, up to its plan's disk, can still fill a pool smaller than that
   (the owner's choice in I-449; the remedy is a larger data disk).
8. **Restricted Nix evaluation** of user fragments: pure, `restrict-eval`,
   no import-from-derivation, sandboxed builds, capped. A fixed-output
   build shares the host's network namespace (Nix gives only those
   network), so the `inet repose` output chain sends every socket of the
   `nixbld` group and of the build user to `build_out`, which admits the
   public internet on the provider NIC and DNS to the resolved stub and
   drops IMDS, the wire server, loopback, the guest bridge, the mesh and
   every private and link-local range (DECISIONS I-439). Stops: a fragment
   reading host files, running unsandboxed code during a build, or
   reaching from a build anything a guest may not reach.
9. **Guest-reported events are guest events.** A guest reports agent
   events over vsock (guestd, through hostd) or, when guestd is down, to
   the edge's hook ingest. Both the edge and the api accept only the
   kinds a guest produces (`completed`, `needs_input`, `error`,
   `agent_message`) and the agent names guestd sends; every platform
   kind, `notifications_paused` included, is written by the api alone,
   and guest events go through the dedupe and the hourly cap (I-441).
   Stops: a tenant's code sending its owner mail worded as a platform
   notice, or flooding the shared outbox past the cap.
10. **The api reaches user-chosen URLs only on the public internet.** The
    ntfy sender dials through a check on the resolved address that refuses
    loopback, private (the mesh and the VNet included), link-local
    (IMDS), CGNAT and other reserved ranges and the Azure wire server,
    ignores proxy settings, and follows no redirect; a literal such
    address or `localhost` is refused when the URL is saved (I-444).
    Stops: a user using the api as a probe into the platform's network.
11. **Guest-sent data is bounded at hostd and again at the api** (I-445..
    I-447). Root in a guest can replace guestd, so every notification and
    sample reply is treated as written by the tenant. hostd forwards agent
    events, questions and warnings only with kinds, ids and states from
    fixed sets, names and text cut to their caps as clean UTF-8, a warning
    detail it writes itself, and at most a burst of 30 then one every 2
    seconds per guest; guest-raised events wait for their ack in a list of
    their own, so they never evict or delay the host's own events. The api
    cleans the same fields again, counts warnings under a fixed set of
    label values, keeps at most 600 guest-raised events per project an
    hour, stores each guest's sample rows apart from its neighbours', acts
    on a question id only inside the sending guest's project, and takes a
    host's events, Hello entries and samples only for guests of projects
    placed on that host. Stops: a tenant growing the api's memory or
    metrics, writing its own text into platform logs, flooding the host's
    stream, zeroing a neighbour's metering, or closing another tenant's
    question; and a compromised host changing the state of, or notifying
    for, another host's projects.

## Non-negotiables

Rules that hold regardless of convenience. Each names its failure.

- **No inbound to hosts.** A host with a public port is a host whose
  hypervisor is reachable from the internet.
- **IMDS is blocked from guests and from tenant builds.** The Azure
  metadata service issues managed-identity tokens; a guest that can reach
  it can act as the host. Tenant code runs in two places with a route to
  it: the guest NIC (dropped in `guest_fwd`) and a fixed-output build on
  the host, which runs in the host's network namespace as a `nixbld` user
  (dropped in `build_out`, I-439). A new way for tenant input to run code
  on a host needs the same drop before it ships.
- **Guests never share a bridge without the drop rules.** A shortcut that
  puts two taps on a plain bridge is a shared L2 between tenants.
- **Claude credentials are never copied, read or proxied by the platform.**
  Anthropic's terms require users to authenticate with their own
  credentials; the copied file also does not refresh. The one place a
  Claude login rests outside a guest is the login share (DECISIONS I-278):
  `/var/lib/repose/users/<user_id>/claude-auth/.credentials.json` on the
  host, written only by Claude Code inside that user's guests.
  - Who can read it: root on the host; the `repose-auth` account (every
    user's share; only its virtiofsd runs as it, each pivot_rooted into one
    user's directory); and every guest of that user. No other user's guest
    has the tag, and hostd never opens the file.
  - What an agent can do with it: read the token, which it already could in
    its own guest, or corrupt it, which signs the user out of every guest
    until the next `/login`. It cannot reach `settings.json` or hooks of
    another project: only the one file is shared.
  - Attack surface added: one writable virtio-fs share per guest, served by
    an unprivileged virtiofsd with `--cache never` and uid translation; a
    virtiofsd escape lands as `repose-auth` (group `hostd`, like the store's
    `virtiofsd`), which can read other users' shares, so it is the same
    class of bug as a store-virtiofsd escape, which already reads guest
    volumes through group `hostd`.
  - Lifetime: 30 days after the user's last guest on the host, then removed
    by hostd's sweep; not in snapshots.
- **Secret values never leave `secrets.ciphertext` and the guest tmpfs.**
  Not in logs, not in `audit_log`, not in api responses, not in build logs.
- **Process samples carry names, CPU, memory, bytes. Nothing else.** The
  privacy policy says, verbatim: "We sample the processes running in your
  environment once a minute and record their names, CPU time, memory use
  and network bytes. We never record command-line arguments, environment
  variables, file paths, file contents, terminal contents, or the prompts
  you give to any agent." `ProcSample` is `{comm, cpu_ns_delta,
  rss_bytes}` and `test/isolation/policy_test.go` fails if it, or
  `GuestSignals`, gains a field. guestd reads `/proc/<pid>/stat` and
  `/proc/<pid>/status` and nothing else of a process: it never opens
  `/proc/<pid>/cmdline` and never opens `/proc/<pid>/environ`, with the single
  exception below. `TestStraceNeverOpensCmdlineOrEnviron` in
  `internal/guestd` runs guestd under `strace -e openat` while it serves a
  Sample and asserts it, because a library added later that reads a command
  line would keep every other test green.
- **The one environment guestd reads is `$TMUX_PANE` of a hook's caller.** An
  agent wrapper that POSTs to `/run/repose/hooks.sock` without naming its tmux
  window is resolved by taking the peer's pid from `SO_PEERCRED` and reading
  that one variable out of its environment. Nothing else from that environment
  is read, kept, logged or sent. The alternative, sending the window name in
  the payload, is what every wrapper does when it can; this is the fallback
  so that a hook still reaches the user rather than being dropped.
  `TestStraceReadsEnvironOnlyForTheHookPaneLookup` asserts that this is the
  only environ open, and only on that path.
- **Every `Exec` is audited.** Operator convenience that skips the audit is
  an unrecorded access to tenant data.
- **Certificates expire in 24 hours** and the gateway checks revocation.
  A longer lifetime makes a stolen laptop a longer problem.
- **Code from outside the repository never runs next to a write token.**
  A workflow job that downloads and runs upstream code (the daily agent
  bump) holds a read-only token, keeps no credentials in `.git` and runs the
  binaries with an empty environment; the job that commits runs nothing it
  downloaded, and its result is a pull request a person reviews (DECISIONS
  I-428). Every action is pinned to a commit and every tool to a version
  (I-429). A shortcut that gives the downloading job `contents: write` puts
  a path to `main`, and so to production, in an upstream release.
- **install.sh installs only a release it can verify.** A release's
  `checksums.txt` is signed by the release workflow's `sign` job with a key
  only that job's environment holds; install.sh embeds the public half and
  refuses a missing or failing signature, and pins the checksums of the
  releases cut before signing (I-430). A checksum fetched from the same
  place as the archive proves only that the download finished.
- **`security_type = Standard` and Intel hosts.** Not security in itself,
  but a Trusted Launch host silently has no `/dev/kvm`, and a fallback to
  containers "just for now" would collapse boundary 1.

## The abuse watch list

`internal/guestd/sample/watch.go` holds process names that are always reported
in a guest sample even when they fall below the top fifty by CPU, so a miner
that throttles itself to stay off the top of the list still appears in the
Grafana "Abuse" panel. It is a list of names worth seeing, not an accusation,
and it changes nothing about what is collected: only which of the names already
collected survive the trim.

```
xmrig  minerd  cpuminer  ccminer  cgminer  bfgminer  ethminer  nbminer
phoenixminer  t-rex  lolminer  xmr-stak  kdevtmpfsi  kinsing  tsm  sysrv
masscan  zmap  hashcat  john
```

Keep this list and `watch.go` in step; `TestWatchListIsNotEmpty` checks the
file is populated, and a reviewer checks the two agree.

Beside the list, every name `internal/abuse.MinerName` matches is watched
too (DECISIONS I-239): the miners whose name gets a guest stopped by the
api, matched in any case and with a fork's suffix (lolMiner,
xmrig-notls, SRBMiner-MULTI). The matching reads the name only, as the
sample always has. The same decisions block a guest's outbound tcp 25 and
the mining-pool ports and rate-limit its new outbound flows at the host
(I-238, I-240), counting each attempt per guest as a number, never an
address.

## Verifying the boundaries

`test/isolation/` has one test per row of the boundary table in
`workstreams/14-security.md` §5, plus policy tests that run without a
host. The host tests take a real host with two guests of two users and run
scripts through command prefixes given in the environment
(`REPOSE_ISOLATION_EXEC_A`, `_EXEC_B`, `_HOST_EXEC`, the addresses; the
package doc lists them); a test whose inputs are missing skips by name,
so a green run with skips proves only the rows that ran. They run before
every release and against the staging host nightly; `ops/RUNBOOK.md`
"Suspected cross-tenant access" runs them during an incident. The policy
tests pin the sample message shape, the verbatim policy sentence, the
watch list and the credentials exclusion, and run in ordinary CI.

Dated reviews of the code against this document live in `security/`:
[security/review-2026-09-20.md](security/review-2026-09-20.md) (the tree
before deployment) and [security/review-2026-09-21.md](security/review-2026-09-21.md)
(`main` as deployed on host-01, the edge and the control VM, the M5
final review).

## Reviewing a workstream

Applied at each workstream's PR, recorded as a comment line in
`workstreams/STATUS.md`:

- Every new log call checked against `ops/OBSERVABILITY.md` "Never in a
  log field".
- Every new listener: which interface, which firewall rule admits it,
  which identity it checks.
- Every new secret or credential: which of the three homes it lives in,
  who can read the file, whether it ever crosses a process boundary as an
  argument or an environment variable.
- Every new command from a peer: which fields are validated (ids, paths,
  sizes) before they touch the filesystem or a shell.
- Every new privileged process: which user, which capabilities, which
  sandbox; "root because it was easier" is a finding.
- The workstream's boundary rows above: which test in `test/isolation/`
  covers them, or which one is added.

## Not mitigated in the first release

Written down so nobody believes otherwise.

- **Every guest's hypervisor is the same `hostd` user** (review L-11,
  after H-2 was fixed by I-51). A KVM escape lands as `hostd`, whose unit
  can open only its own volume and sees only its own guest directory; but
  every tap is owned by that uid, so an escaped hypervisor could attach to
  another guest's tap (its frames are still admitted per tap by the bridge
  ruleset). One user per guest would close it.
- **One Blob identity for every host** (review M-3). Each host can read
  and delete every tenant's snapshots fleet-wide. Per-host containers or
  api-issued SAS tokens close it.
- **Hosts registered before I-139 trust no Host CA until their first
  rotate** (review M-1, closed in code 2026-09-21 by I-139:
  `RegisterResponse.host_ca_pub`). host-01 is one of them; the runbook's
  "Operator certificate refused by a host" is the by-hand step at the
  next switch. And the bootstrap key stays on every host while
  `repose.host.bootstrap.enable` is on (I-92), so operator access is
  "certificate or the bootstrap key", not certificate-only, until 01/11
  turn bootstrap off.
- **Operator access to tenant volumes.** Root on a host can read any thin
  volume. Mitigation is per-project LUKS with keys held by the api
  (DECISIONS R3-10). Until then the audit log records operator logins and
  the privacy policy does not claim otherwise.
- **Side channels between guests** (cache timing, Spectre-class). Cloud
  Hypervisor and the kernel mitigations are enabled; no further isolation
  (core pinning, no SMT sharing) is done. A tenant extracting another
  tenant's secrets by side channel is judged unlikely at this scale and
  price; a paying customer who needs that gets a dedicated host, which the
  scheduler does not yet support.
- **Nested-virtualization escape.** The guest-to-host boundary is KVM
  running inside Hyper-V. A KVM escape lands in the Azure VM, not in
  Azure. This is the same posture as AKS Pod Sandboxing.
- **Abuse detection is partly automatic.** A guest running a known
  cryptocurrency miner (by process name) is stopped with a snapshot, and
  three such stops in 24 hours hold the project until an operator runs
  `repose-admin abuse clear` (DECISIONS I-239). Outbound tcp 25 and the
  mining pools' default ports are blocked (I-238, I-239), and new outbound
  flows are rate-limited per guest (I-240). Anything else (a miner under
  another name, a scanner, spam over a submission port) is a sample or a
  counter on a dashboard, and a human decides.
- **Denial of service against the gateway or api.** Rate limits exist per
  user. The gateway bounds connections still in the handshake per source
  and in all, separately from authenticated relays, and the edge's
  nftables caps open and new connections per source on 22 (I-435), so one
  address cannot fill the gateway. A distributed flood can still fill the
  64 handshake slots and delay logins; there is no upstream DDoS
  protection beyond what Azure gives a public IP.
- **Supply chain of the agent overlay.** Agents are repackaged from
  upstream binary releases with pinned hashes; there is no independent
  verification of upstream builds. The hash is what upstream served when
  the bump ran; the bump PR's review is the check, and the bump job holds
  no write access (I-428).
- **A forged `main`.** install.sh and its signing public key are served by
  the site `main` deploys, so whoever can push to `main` can change both.
  The `main` ruleset (owner's setting, I-428) is what stops that.
- **A tenant's agent misusing the tenant's own tool logins.** Inside the
  guest, gh and Codex tokens are readable by any process as `dev`. That is
  the same exposure as on the tenant's laptop.

## Fixed since the reviews

- **The api's `/metrics` was on the public entry point** (M5 review,
  High). Fixed by I-136: the user listener no longer serves it; I-133's
  allow-list router is defence in depth.
- **The edge's sshd ran with NixOS defaults** (review M-2). Closed on the
  live edge 2026-09-21: operator sshd on 2222 with password and
  keyboard-interactive off, `prohibit-password`, verbose logging, admitted
  only from the tunnel and the operator address
  ([security/review-2026-09-21.md](security/review-2026-09-21.md)).
- **hostdev held secrets in plaintext on the edge** (review M-4). Closed
  2026-09-21: `hostdev` is gone from the edge and its state directory and
  the M1 identity on host-01 were removed with no copies kept.

## Reporting

Security reports go to the owner's email listed on `repose.herakraft.co`.
Incident handling is in `ops/RUNBOOK.md` under "Suspected cross-tenant
access": who is told, what is captured, how a tenant is notified within
72 hours, and the isolation-suite invocation that confirms or refutes.
