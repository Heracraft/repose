# How a machine joins an account (research, 2026-11-19)

**Status: research, saved. Nothing here is decided, no decision id is
reserved, and nothing is to be built from this file until an entry in
`docs/DECISIONS.md` says so.** The owner asked, twice on 2026-11-19, that
repose not invent a join pattern, because what we invent will be
unintuitive: find what already works and emulate it. This file is that
search, written by a research session in the worktree `join-research`.

The question: how do widely used products let a machine, or an agent
acting for a human on that machine, join an account **without a long-lived
secret passing through argv, shell history, or an AI agent's transcript**?
It matters now because the next thing a customer will say is "set up
repose on my Mac" to Claude, and today the install is `repose host add
--join <339-character bundle>`, where the bundle carries a WireGuard
private key as a command-line argument (ENG-42). An agent-run install
writes that key into a conversation log the customer never reads.

Evidence kinds are marked throughout: **(spec)** an RFC, **(vendor)** a
vendor's documentation, **(issue)** a bug tracker, forum or mailing-list
thread, **(advisory)** a security writeup or postmortem, **(blog)** a
practitioner's writeup, **(read)** read in this repository at `fdf0ce95`,
**(ran)** run on this box. Where a claim about what people get wrong rests
on a vendor doc rather than a complaint, it says so. Nothing in the
comparison was measured on a product by this session; the four product
groups were researched from documents on 2026-11-19 by subagents and
checked against each other here.

## Summary

| Question | Answer |
|---|---|
| What every network-peer product does | Tailscale, Headscale, NetBird and ZeroTier have the node generate its own key pair; the control plane only ever holds public keys; "joining" is the approval of a public key, by a login URL or an admin ticking a box. Nothing secret crosses the human (vendor, all four) |
| What every cluster and infra product does | kubeadm, SPIRE, Teleport and GitHub's runner split a short-lived enrolment credential from a long-lived per-machine key that the machine generates; the enrolment credential is the only thing that can leak, and it still leaks through argv and logs in every one of them (issue, each) |
| Where the one-shot-secret-in-the-hand pattern fails | k3s, cloudflared tokens, Buildkite, CircleCI, Jenkins, GitLab's new tokens: the human carries the long-lived secret itself, and the recorded failures are exactly ours: argv, `ps`, unit files, CI logs, a public repo, and in one case a k3s token pasted into an AI chat that forced a fleet-wide rotation (issue) |
| What repose already does | `repose login` is already an RFC 8628 device flow against Logto (read, I-101, I-627); `repose host add` on a logged-in Mac already keeps the bundle in process memory and a 0600 file, never on stdout (read); the api mints every host's WireGuard private key itself, at `host add` and again at Register (read) |
| The recommendation | Emulate `tailscale up`: the Mac makes its own WireGuard key, `repose host add` on the Mac shows a short code and a URL, the human approves the named machine on the dashboard or with `repose host approve CODE` on their laptop, and the Mac then receives its place on the hub plus the one-shot token it already takes today, inside the CLI process and never on a screen. Register and the appliance image do not change |
| Smallest first | (0) docs and the agent guide: on the Mac, `repose login` then `repose host add`, which already shows nothing secret; (1) ENG-42 as filed, the argument from a file or stdin, to end the `ps` exposure; (2) the device-style join above, which retires the bundle from every human surface; (3) later, Register takes a host-made key too, so the api stops minting private keys for anyone |
| The strongest counter-evidence | Device-code phishing: Storm-2372 and three Russian actors used exactly this flow against Microsoft users in 2025, and Entra and Okta now say block it unless needed (advisory). For repose the attacker's gain is a host that receives the victim's projects, so the approval page has to name the machine and say what approving does, and the code has to be short-lived. The bundle has the mirror-image risk today and leaks a key on top |

## 1. Where the secret is today

Read in this repository, not measured on a Mac except where ENG-42's
figure is quoted.

- **On a Mac, logged in**, `repose host add` calls `POST /hosts`, holds
  `install.argument` in the CLI process, writes it to a 0600 file under
  `~/Library/Application Support/repose/share/join`, and the appliance
  reads it at boot, splits it into `/run/repose/join-token` and
  `/run/repose/hub.json`, and deletes the share's copy. stdout carries
  nothing; stderr says "NAME: installing the appliance on this Mac" and
  "NAME is starting and joins your account in a few minutes"
  (`internal/cli/hosts.go`, `nix/hosts/mac.nix`; read). On this path an
  agent's transcript carries no secret today.
- **Anywhere else**, `repose host add` prints the install line
  `repose host add --join <bundle>` on stdout (read), and the dashboard's
  Hosts page shows the same line in a one-time dialog with a copy button
  (`OwnHosts.svelte`; read; ENG-29 fixed what the dialog shows). The
  customer carries that line through their clipboard, a chat, or an agent
  prompt to the Mac. This is the path an agent-run install takes when
  the Mac is not logged in, and it is the path ENG-42 describes.
- **The bundle** is base64url JSON `{v, token, wg: {private_key, address,
  edge_pubkey, edge_endpoint}}` (`docs/interfaces/api.md`; read). The api
  mints the WireGuard key pair at `PrepareHub`, keeps no copy of the
  private half, and mints a second pair at Register, which hostd takes
  over mTLS and uses from then on (`internal/api/hostmgr/hostmgr.go`;
  read). So every host's private key, owned or hosted, is born on the api.
  The bundle's copy sits in argv of `repose host add --join` for the whole
  install, 97 s on the AWS Mac (ENG-42; measured by the Mac council on
  2026-11-19), readable in `ps` by every process on that Mac.
- **Why the bundle exists.** A Mac has no route to the api's VNet address;
  the hub is the one path a host is forwarded, so the host must be a
  WireGuard peer before it can register, so `host add` pre-peers it
  (I-1380, I-1382; read). But the Mac and the appliance VM, which has NAT
  networking, can reach `https://api.repose.run`, which is where the CLI's
  own calls already go (`internal/cli/config.go`; read). Only gRPC
  Register is hub-only. An HTTPS bootstrap is reachable from the machine
  being added.
- **The product's existing answer** to "a machine joins a human's account"
  is a device flow: `repose login` prints "Open <url>. The page shows code
  XXXX; check it matches, then sign in. Waiting..." and polls at the
  server's interval, honouring `slow_down` (`internal/cli/oidc.go`,
  `login.go`; read; I-101, I-627). The agent guide already tells users to
  log Claude Code in inside a guest the same way: approve in the browser,
  paste the code back (`apps/web/src/content/docs/agents.md`; read).
- **What an agent's transcript holds.** Claude Code keeps session
  transcripts "locally in plaintext under `~/.claude/projects/` for 30
  days by default" (vendor), with Bash stdout and stderr stored verbatim
  and no write-time redaction: the researching subagent found its own
  Bash output unaltered in its transcript minutes later (ran), and three
  issues report tokens from `pass show` and from exception text landing
  in the files, closed as duplicate, not planned, or stale (issue
  claude-code#63593, #71654, #50014). Codex, Copilot CLI, Cursor and
  Gemini CLI keep the same kind of file (vendor, blog). On a stock Mac,
  zsh does not set `HIST_IGNORE_SPACE`, so the leading-space trick does
  not keep a pasted line out of history either (vendor, zsh manual). A
  line the customer pastes to an agent is therefore on disk for a month
  in a file they never open, and uploaded if they ever say yes to "can
  Anthropic look at your session transcript".
- **What I-1380 said the bundle is worth**: the power to register one
  machine as the user's host, and no more, for 24 hours or until used;
  dead after Register replaces the key (read). That account is correct and
  is not the problem. The problem is that the carrier is a human, a
  clipboard, a shell, and now an agent's log, and that the thing carried
  is a private key rather than a code.

## 2. The comparison

One row per product and path. "Key made by" is who generates the
long-lived credential the machine keeps. "On a human surface" is whether
anything that confers access by itself crosses a screen, a clipboard,
argv or a transcript in the normal flow. "Admin approves" is whether the
account holder sees and approves the specific machine before it gets
anything.

| Product, path | The human types | The machine gets | Key made by | Enrolment credential, life | Secret on a human surface | Admin approves the machine | What goes wrong, with evidence |
|---|---|---|---|---|---|---|---|
| RFC 8628 device grant (spec) | opens `verification_uri`, enters an 8-char `user_code`; or opens `verification_uri_complete` | an access and refresh token after the user approves | the server mints bearer tokens | `device_code`, high entropy, in the client's memory only; `user_code` on screen; both die at `expires_in` (GitHub: 15 min) | the user code, which is low-entropy and rate-limited, not the device code (§3.3, §5.2, §6.1) | no; the user approves "a device" and §5.4 says tell them so | device-code phishing: Storm-2372 and three Russian actors, Feb 2025 (advisory, Microsoft, Volexity); relay kits that mint the code on click, Apr 2026 (advisory); GitPhish against GitHub (advisory); Entra and Okta now say block the flow unless needed (vendor) |
| `gh auth login` | `gh auth login`, copies "one-time code XXXX-XXXX", presses Enter, browser opens github.com/login/device | an OAuth user token, non-expiring by default (`--expiring` since 2.103) | server | the device code; the token is written to the OS keyring, with a silent plain-text fallback to `hosts.yml` | the user code only; `--with-token` takes a PAT on stdin, never argv (ran: `gh auth login --help`, 2.100.0) | no | plain-text fallback complaints since 2024 (issue cli#10108, #8954); `--with-token` echoes on a tty (issue cli#14597); headless browser-open failures (issue cli#5500) |
| `aws sso login` | `aws sso login --profile X`; since 2.22 PKCE in the browser, `--use-device-code` prints a URL and code | an SSO access token, then STS keys per role | server | device code; the token cache is plain JSON in `~/.aws/sso/cache`, 1 h access token, 8 h to 90 day session | the user code | no | cache in plaintext (advisory, Zscaler); phishable like any device flow (advisory, christophetd); `--no-browser` took two years (issue aws-cli#5301); 2.22.0 broke headless (issue #9092) |
| cloudflared, `tunnel login` | `cloudflared tunnel login`, picks a zone in the browser | `cert.pem`: an account-wide API token "valid for at least 10 years" (vendor) | server | none; the result is the long-lived secret | no, it is written to a file by the process | no | the file is a decade-long account credential on the machine (vendor) |
| cloudflared, tunnel token | `cloudflared service install <TOKEN>` or `tunnel run --token`, from the dashboard | the same token forever: base64 `{account, tunnel id, tunnel secret}` | server | none; the token is the credential; `--token-file` and `TUNNEL_TOKEN_FILE` only since 2025.4 | yes: argv, the systemd unit's `ExecStart`, Docker `Cmd` | no | the file option was asked for in May 2022 and merged April 2025 (issue cloudflared#645, PR 1316); a docs PR warning about `ps` and the unit file was closed unmerged Sep 2026 (issue cloudflare-docs#33157); a token leaked through a container dump, rotation took the tunnel down (blog) |
| Tailscale, interactive | `tailscale up` prints "To authenticate, visit: https://login.tailscale.com/a/<id>" | nothing from the human; the node had already made its node key and machine key | **the node**; "the private key never, ever leaves its node" (vendor) | the login URL binds the node's public key to whoever signs in; no bearer secret | no | optionally: device approval holds the node until an admin approves it; tailnet lock makes a trusted node sign the key (vendor) | node-key expiry on headless hosts cannot re-run the browser step, so people disable expiry (blog, perun.au) |
| Tailscale, auth key | `tailscale up --auth-key tskey-auth-...`, key made in the admin console or by an OAuth client | the node key, pre-authorised by the key's properties | the node; the auth key only authorises | a bearer key: one-off or reusable, ephemeral, pre-approved, tagged, 1 to 90 days; `--auth-key file:/path` exists (ran: `tailscale up --help`, 1.104.5) | yes, unless `file:` is used | no, when pre-approved | Tailscale's own KB puts the key in argv and never mentions `file:` (issue tailscale#21084, open); the NixOS module `cat`s it back into argv (issue nixpkgs#572160); Hugging Face: an escaped agent read a reusable tagged key and enrolled 181 nodes (advisory, Tailscale, 2026-07-31); a reusable key expired fleet-wide (issue); revoking a key does not deauthorise nodes made with it (vendor) |
| ZeroTier | `zerotier-cli join <16-hex network id>`; admin ticks the node in Central | nothing; `identity.secret` was generated at first start; the controller issues a certificate of membership | **the node** | none; the network id is public for a private network | no | **yes**: the node sits in `ACCESS_DENIED` until the admin authorises its 10-hex address (vendor) | a denied node never reappears and must be typed in by hand (issue); cloned disk images carry the identity and collide (vendor FAQ, issue); 2021 address-hijack advisory fixed in 1.6.6 (advisory) |
| Headscale | `tailscale up --login-server <url>` shows a register page; admin runs `headscale auth register --user U --auth-id ID` | node keys as Tailscale | the node | the registration id is the node's public key; preauth keys are one-hour single-use bearer keys by default (vendor) | no on the interactive path; yes with a preauth key in argv | **yes**: the admin's `register` command is the approval (vendor) | expired nodes set to never-expire do not come back (issue headscale#1851) |
| NetBird | `netbird up` runs PKCE or, headless, a device flow and prints a URL; or `--setup-key`/`--setup-key-file` | WireGuard key made locally; "never leaves the machine" (vendor) | the node | none interactive; setup keys reusable with unlimited uses by default (vendor) | no interactive; yes with a setup key | by SSO login; peer approval exists in the dashboard | revoking a setup key keeps its peers connected (vendor); device flow timeouts on headless boxes (issue netbird#1656) |
| GitHub Actions runner, `config.sh` | copies `./config.sh --url ... --token <reg token>` from the UI | an RSA key pair made on the machine; GitHub keeps the public key and a client id; per-job tokens are short (vendor, actions/runner auth.md) | **the machine** | registration token, 1 hour, reusable within the hour, distinct from the kept key (vendor) | yes: the registration token in argv | no | token in CloudWatch via `set -x` (issue cdk-github-runners#1008); projects reworked installers to stdin or env (issue gitsafe#25, runnerctl#111); `.credentials` stolen and replayed from another machine (advisory, Synacktiv); attackers mint registration tokens to plant runners (blog, Sysdig) |
| GitHub Actions runner, JIT | API `generate-jitconfig`, then `./run.sh --jitconfig <blob>` | the same files, but GitHub generated the private key and sent it in the blob (blog, decoded) | the server | one runner, one job | yes, if the orchestrator's log shows argv | no | the controller, not the machine, now holds the secret (blog) |
| GitLab Runner, 16.0 and later | creates the runner in the UI, copies `glrt-...` shown once, `gitlab-runner register --token glrt-...` | the same `glrt-` token in `config.toml` forever, plus a locally generated `system_id` | the server; the enrolment credential **is** the kept credential | none short-lived; reusable across machines by design (vendor) | yes: argv, then the config file | no, but the creator is audited | GitLab's stated reasons for the change: shared tokens, no traceability, "copied in many places" (vendor, blueprint); automation broke (issue, forum 89630; terraform-aws-gitlab-runner#850); `410 Gone` on upgrade (vendor) |
| Buildkite agent token | dashboard token shown once, into `buildkite-agent.cfg`, `--token` or the env | a session token in memory per run | server | the agent token, fleet-wide, never expiring by default; expiry optional since 2025 (vendor) | yes | no | a file reference was declined (issue agent#458, #1769); `bk agent run` left a non-expiring token per run (issue cli#1069); job acquisition tokens in 2026 move the long-lived token to a controller (vendor) |
| CircleCI runner | `circleci runner token create`, pasted into a YAML | nothing of its own; the shared resource-class token | server | none; the token is shared, never expires (vendor) | yes, stdout then a file | no | the Jan 2023 breach forced every customer to rotate every runner by hand (advisory, CircleCI) |
| Jenkins inbound agent | copies `java -jar agent.jar ... -secret <secret>` from the node page | the same secret; derived from the agent name and a controller secret, so re-using the name re-uses the secret (vendor) | server | none; long-lived | yes, argv unless `-secret @file` | no | readable in `/proc/<pid>/cmdline` by build steps (issue createos-sandbox-plugin#5); CVE-2026-84651 let one agent's user read another's secret (advisory, Jenkins 2026-09-02) |
| Azure Pipelines agent | `config.sh --auth pat --token <PAT>` | an RSA key pair made on the machine; a listener OAuth token afterwards (vendor) | **the machine** | a user PAT, used only at registration, scope "Agent Pools (read, manage)" (vendor) | yes: a broad user PAT in argv | no | `remove` fails unless the PAT covers all organisations (issue azure-pipelines-agent#3308) |
| kubeadm | `kubeadm token create --print-join-command` on the control plane, pastes `kubeadm join --token id.secret ... --discovery-token-ca-cert-hash sha256:...` | a client certificate for a key the kubelet generated and submitted as a CSR; the CA hash pins the server (vendor) | **the machine** | bootstrap token, 24 h, reusable until expiry, distinct from the cert (vendor) | yes: argv, `ps`, auditd, shell history | **optional**: delete the auto-approve binding and `kubectl certificate approve` is the gate (vendor) | token on argv (issue lok8s#252); kubeadm logged tokens at `-v=1` (issue kubeadm#2287); expired tokens (issue #1452); a recovered bootstrap token plus auto-approve let researchers become any node on AKS (advisory, Synacktiv) |
| k3s | `cat /var/lib/rancher/k3s/server/node-token`, then `K3S_TOKEN=... sh -` or `--token` or `--token-file` | its own certificates; but the agent keeps the token on disk forever | the machine for certs; the server for the token | the server token: "a static password... does not expire", admin-equivalent; `k3s token rotate` only since 2023 and restarts the fleet; short bootstrap tokens exist (vendor, ADR) | yes | no | committed to a public repo (issue rpi-k8s#34); **`tail` of `config.yaml` pasted into an AI chat session forced a server-then-every-agent rotation** (issue bvrinfra#95); snapshots need the old token (vendor) |
| Vault AppRole, response-wrapped | orchestrator runs `vault write -wrap-ttl=60s -f .../secret-id`, hands the wrapping token to the machine, which `unwrap`s | a SecretID then a Vault token; no key is made on the machine | server | the wrapping token: single use, seconds; the SecretID behind it bounded by TTL and uses (vendor) | the wrapping token, dead after one use | no | "just moves secret zero to the proxy" (issue, HashiCorp Discuss 58201); the provisioner cannot see the SecretID's accessor (issue vault#12173); Agent restart fails on a spent wrapped SecretID (issue vault#16148) |
| SPIRE, join token | `spire-server token generate -ttl 600`, `spire-agent run -joinToken ...` | an agent SVID for a key the agent made; renews over mTLS (vendor) | **the machine** | join token, single use, 600 s default (vendor) | yes: argv once | no; the SPIFFE id is pre-bound to the token | restart could not re-attest with a spent token until the disk key manager (issue spire#242); SVID expired while off means a new token (issue, Weaverlab PR 1102) |
| SPIRE, platform attestation | nothing on the node | an agent SVID after presenting an AWS IID, Azure MSI token, GCP IIT, k8s PSAT, TPM DevID or an existing x509 (vendor) | the machine | none; the platform vouches | no | not per machine; by selector | needs a platform that vouches, which a Mac on a desk lacks |
| Teleport, token | `tctl tokens add --type=node --ttl=5m`, `teleport configure --token ... --ca-pin sha256:...` | host certificates for a key the node made; renewable without the token (vendor) | **the machine** | the token: 30 min default, reusable until expiry; static tokens "strongly discouraged" (vendor) | yes: stdout, argv, `teleport.yaml` | no | 30-minute expiry versus systemd start order (issue teleport#3732); static tokens show "01 Jan 70" and break the UI script (issue 45723); token literal in YAML blocked rotation until a file path landed (issue teleport#2515) |
| Teleport, IAM and OIDC joins | nothing secret; the node signs an STS `GetCallerIdentity` with a challenge, or presents a cloud or CI OIDC token | host certificates | the machine | none (vendor, RFD 0041) | no | by rule, not per machine | needs a cloud identity; a Mac has none |
| Teleport, bound keypair (2025) | either a single-use registration secret in the config, or `tbot keypair create --static` on the machine and the admin pastes the **public key** into the token resource | an ED25519 identity the machine made; every join is challenge-response; recoveries are counted (spec, RFD 0205; vendor) | **the machine** | the registration secret: single use, optional deadline; or none at all | the registration secret, once; or nothing | **yes** in the static variant: the admin registers this machine's public key before it gets anything (vendor) | "Auth can't 100% trust that the client is using a hardware keystore" (spec, RFD 0205) |
| Consul and Nomad | gossip key and ACL token in config; auto-encrypt makes clients request certs; auto-config takes a Vault JWT | certificates from the server CA, in memory | the machine for TLS; the operator for the gossip key and tokens | long-lived shared gossip key; JWT for auto-config (vendor) | yes | no | secret zero moves to "how did the VM get a Vault JWT" (vendor, blog) |
| Syncthing | shows its device id on A, pastes it on B, accepts the "new device" prompt on A | nothing; each device made its TLS key at first start; the id is the SHA-256 of the certificate (vendor) | **the device** | none; the id is public: "Should I keep my device IDs secret? No" (vendor FAQ) | no | **yes**, mutually: the prompt on the other side is the approval | users assume the 50-character id is secret and that it is too long to dictate (issue, HN 38980553); ids in posted logs let strangers query discovery for IPs (issue, forum 14824) |
| SSH host key, Bluetooth numeric comparison | compares a fingerprint, or a six-digit number on both screens, and says yes | nothing typed enters the key (vendor, ssh(1); advisory, NIST SP 800-121r2) | the device | none | no | yes: the human confirms the specific peer | TOFU's first answer is unchecked; "Just Works" removes the display and the protection (advisory) |
| Apple: Apple Account on a new device; AirPlay; MDM enrolment | taps Allow on a trusted device and types the six-digit code shown there; types a four-digit code off the TV; or nothing, the Mac is matched by serial in Apple Business Manager (vendor) | a session; a pairing; a management profile | Apple | a one-shot approval code; or none | the code, once | yes: the trusted device shows the sign-in's location and asks | a non-technical Mac owner already knows all three |
| Nebula, Defined Networking | `nebula-cert keygen` on the host, the CA signs the `.pub`; or `dnclient enroll -code <one-time code, 24 h>` (vendor) | a certificate for a key made on the host | **the host** | none, or a one-shot code | the code, once | signing is the approval | no later admin step |
| Netmaker, Twingate | pastes a server-made enrollment key or connector tokens (vendor) | the same secret | the server | long-lived | yes | Netmaker: networks are assigned after; Twingate: none | the family B shape again |
| Stripe, Netlify, Fly, Vercel, Railway logins | `stripe login` prints "Your pairing code is: humour-nifty-finer-magic" and a URL; `netlify login --request` makes a ticket an agent polls with `--check`, "for agent/human-in-the-loop auth"; `fly auth login` skips the paste prompt without a tty, built for "coding agents, CI runners" (vendor, issue) | a token in the OS keychain or a 0600 file | server | the device code in memory | no; Stripe shows a masked key only under `--interactive` | no | Netlify and Fly are the two CLIs found that name the agent as the caller of a login |
| **repose today, `--join`** | `repose host add` on a laptop or the dashboard, carries `repose host add --join <bundle>` to the Mac | the token and a WireGuard private key the api minted; Register replaces the key (read) | **the api**, twice | the bundle: 24 h, one use; carries a private key | **yes**: stdout, the dashboard dialog, the clipboard, argv for 97 s, an agent's transcript | no | ENG-42 (measured 2026-11-19); the agent-run install writes a private key into a log the customer never reads |
| **repose today, logged-in Mac** | `repose host add` on the Mac | the same bundle, inside the CLI process, into a 0600 file the appliance deletes (read) | the api | the same | no: stderr names the host and nothing else | no | needs `repose login` on the Mac first, which is itself a device flow (read) |
| **repose `login`** | `repose login` prints a URL and a code, polls | a refresh token in the config dir (read) | Logto | device code in memory, user code on screen | the user code only | no | the same device-code phishing exposure as every row above |

## 3. What the table says

Three families, and ours is in the wrong one.

**Family A: the machine makes its key, the human approves a public key.**
Tailscale, Headscale, NetBird, ZeroTier, kubeadm's TLS bootstrap, SPIRE,
Teleport's certificates, GitHub's runner, Azure Pipelines' agent. The
private key is born where it will be used and never moves. What crosses
the human is either nothing (a login URL, a network id, a public key) or a
short-lived enrolment credential whose only power is to let one machine
ask. Where a product in this family still has a leak, it is the enrolment
credential in argv (kubeadm, Teleport, SPIRE, GitHub's `config.sh`,
Tailscale's auth key), and in each case the recorded fix is the same: a
file or stdin, and ideally no credential at all (Tailscale's login URL,
Teleport's bound keypair, ZeroTier's tick box).

**Family B: the server makes a long-lived secret and the human carries
it.** k3s, cloudflared tokens, Buildkite, CircleCI, Jenkins, GitLab's
`glrt-` tokens, Vault AppRole's SecretID. Every complaint we were asked
to prefer over vendor docs comes from this family: `ps`, unit files,
Docker `Cmd`, CI logs, a public repo, a chat with an AI. GitLab rewrote
its registration flow in 2023 for ownership and traceability and
deliberately kept the token long-lived and reusable, and the migration
pain was real and the leak class unchanged. CircleCI's 2023 breach is
what the family costs at scale: every customer rotating every machine by
hand.

**Family C: no secret, the platform vouches.** SPIRE's cloud and TPM
attestors, Teleport's IAM and OIDC joins, Consul auto-config. These are
the strongest and they do not apply: a Mac mini on a desk has no cloud
identity document, and its Secure Enclave is not something our control
plane can verify from the outside today.

repose today is in family B with a twist that makes it worse: the secret
carried is not a bearer token but a private key, and the api that mints
it is the one party in the system that should never have seen it. I-1380
is right that the key dies at Register and that the bundle grants only
what the token granted. But every product in family A reached the same
conclusion about its own bootstrap and still moved key generation to the
node, because a private key that exists in two places is a private key,
whatever its lifetime.

The second thing the table says: **a device flow and a key approval are
the same shape.** `tailscale up` prints a URL; the user signs in; the
server binds the node's public key to the account. `repose login` prints
a URL and a code; the user signs in; Logto hands a token to the process
that asked. The only difference for a host is what the machine sends with
its request (a public key and a name) and what it receives after approval
(its hub address and the edge's peer, plus the one-shot token Register
already takes). The pattern already exists in the product, the customers
already know it from GitHub, AWS and Claude Code's own login, and the Mac
owner knows it from Apple's six-digit "approve this sign-in on your other
device".

## 4. The recommendation

**Emulate `tailscale up` with device approval: the Mac makes its own
WireGuard key; `repose host add` on the Mac shows a code and a URL; the
customer approves the named machine on the Hosts page or with `repose host
approve CODE` on their laptop; the Mac then receives, inside the CLI
process, its place on the hub and the one-shot token it takes today.**
Nothing secret appears on a screen, in argv, in a clipboard or in an
agent's transcript, because nothing secret exists outside the Mac and the
api's database at any moment.

What the customer sees, on the Mac, logged in or not:

```
$ repose host add
Open https://repose.run/hosts/approve/WDJB-MJHT
The page shows this Mac (Dapo's Mac mini) and code WDJB-MJHT; check both, then approve.
Waiting...
mac-mini: installing the appliance on this Mac.
mac-mini is starting and joins your account in a few minutes.
```

And on the Hosts page, or in `repose host ls` on the laptop:

```
HOST      STATE     MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  approve?  -       -         -        asked 40 s ago from 86.x.x.x
```

with one action, approve, which says what it does: "This Mac will run
your projects and hold everything in them." A customer who says "set up
repose on my Mac" to Claude on that Mac reads one line from the agent, a
URL and a code, opens it on their phone, sees their Mac's name, approves.
The agent's transcript holds a URL, a code that died in fifteen minutes,
and two status lines.

What moves and what does not:

- **The key.** The launcher on the Mac generates the WireGuard key pair
  (one curve25519 operation, as hostd's `wgKeypair` does today) and sends
  the public half with the join request. The api's `PrepareHub` takes a
  public key instead of minting one. The private half goes into the same
  0600 share file the bundle goes into today, in the same bundle shape,
  so **the appliance image does not change**: `repose-launcher-join`
  still finds `{v, token, wg: {private_key, address, edge_pubkey,
  edge_endpoint}}`, the only difference being who wrote `private_key`.
- **The token.** Register keeps taking the one-shot 24-hour token. It
  travels api to CLI process to 0600 file to VM, which is the logged-in
  Mac's path today, and never a human surface. **Register does not
  change**, and the hosted fleet's path does not change.
- **Approval is the dashboard's.** The Hosts page gains a pending row
  with approve and deny; the one-time dialog that shows a secret goes.
  `repose host approve CODE` and `repose host ls` showing the pending row
  give the CLI the same powers, which is the parity rule in both
  directions. Approval needs a login, never an api token, as `host add`
  does today.
- **Secrets' three homes** are untouched. A WireGuard key born on the
  host and never leaving it is host identity, the category I-1380 already
  placed outside the three homes, with one fewer place it has been.
- **`--join` stays for the lab**, reading its argument from a file, and
  the recorded `host_install_command` stops carrying a bundle.

Why this and not the alternatives the table offers:

- **Not an auth key** (Tailscale's `--auth-key`, NetBird's setup key,
  Headscale's preauth key): the pre-authorised bearer key is exactly the
  path every one of those products' failures came from, Hugging Face's
  181 nodes included, and it is what the bundle is today minus the
  private key.
- **Not a pasted public key** (Teleport's static bound keypair, ZeroTier's
  manual add): sound, and nothing secret anywhere, but a 44-character
  key is not a thing a Mac owner reads back to a phone. The code is the
  friendly form of the same approval, and the approval page shows the
  key's fingerprint for whoever wants to check it.
- **Not platform attestation**: a Mac on a desk has nothing our control
  plane can verify. If Apple's Managed Device Attestation ever becomes
  readable from outside an MDM, it slots in as a second factor on the
  approval page, not a replacement.
- **Not a signed app, Keychain, or a QR**: each is a new carrier or a new
  home, and none removes the need for an approval.

## 5. Shipping order, smallest first

Each step is useful alone, and each later step removes the one before it
from the user's path without breaking it.

0. **Say what already works, and tell the agent.** `apps/web/src/content/docs/own-hardware.md`
   and `agents.md`: on the Mac, `repose login` then `repose host add`;
   never paste a `--join` line into an agent. The agent guide an agent
   reads on a customer's Mac says the same. Zero code. The transcript on
   this path already holds nothing secret (read, §1). It does not cover
   the dashboard's dialog or a Mac the customer will not log in on.
1. **ENG-42 as filed.** `--join` takes `-` for stdin or a path; the
   recorded install line uses it; the flag value stays for the lab. This
   ends the 97-second `ps` exposure and nothing else: the bundle still
   crosses stdout, the dialog, the clipboard and the transcript. The
   kubeadm, Tailscale, Teleport and cloudflared threads above are this
   exact fix, each taken years after the first report, so take it now,
   and say in the card that it is the smallest step and not the answer.
2. **The device-style join**, §4. api: a join request that takes a public
   key and a name and returns a device code, a user code, a URL and an
   interval; a poll; an approve and a deny bound to a login; `PrepareHub`
   from a public key. CLI: `repose host add` on a Mac prints the code and
   polls; `repose host approve`; a pending row in `host ls`. Dashboard:
   the pending row and the approve action replace the one-time dialog.
   Appliance: unchanged. Register: unchanged.
3. **Register takes a host-made key.** hostd generates its rotation key
   and sends the public half; the api stops minting any private key for
   anyone, hosted or owned. This is the end state every product in
   family A is at, and it changes `grpc-hostd.md`, so it ships with the
   old shape accepted for one release. Not needed for the agent-run
   install, and separable.
4. **x86 and the lab.** An owned x86 host still takes a token through
   `/run/repose/join-token` by an installer's `--extra-files`. hostd can
   learn the same device flow on a first boot with no `host.json`,
   printing the code on its console, which also covers a Mac whose
   launcher died before the VM read the share. Later, if anyone asks.

## 6. Counter-evidence against the recommendation

- **Device-code phishing is real and recent.** Microsoft's Storm-2372
  (2025-02-13) and Volexity's three actors used this flow to take Entra
  sessions, the kits now mint the code on the victim's click so the
  fifteen minutes start late (Microsoft, 2026-04-06), GitHub's flow is
  phishable the same way (Praetorian), and Entra and Okta both recommend
  blocking the flow unless needed (advisory, vendor). For repose the
  prize is worse than a session: an approved attacker Mac becomes the
  account's own host and, with `placement` defaulting to `own`, receives
  that account's projects and every secret delivered to them. The
  mitigations that exist are the RFC's own (§5.4): the page says "you are
  approving a machine", names it, and shows where it asked from; the code
  is short-lived (15 minutes, as GitHub); the request is rate-limited per
  address; approval needs a logged-in session and never an api token;
  and no `verification_uri_complete` ever travels by email. None of this
  is new to repose: `repose login` has the same exposure today, and the
  bundle has the mirror-image one, where anyone holding the line adds a
  machine, plus a key leak on top.
- **An agent on the laptop can approve.** Parity gives `repose host
  approve CODE` to a logged-in CLI, so an agent on the laptop under
  prompt injection could approve a stranger's Mac if told the code. The
  dashboard cannot be driven that way. One answer is to make `approve`
  print the machine's name and ask, which an agent can still answer; the
  honest position is that a logged-in CLI can already `host add` and
  hand out a bundle, so this adds no power, only a new verb for it.
- **The belief that nothing secret enters the agent context needs one
  caveat.** It holds for step 2 and step 0 on a logged-in Mac. It does not
  hold for step 1, which only moves the key out of `ps`; and on step 0
  the refresh token `repose login` writes to the config dir is readable
  by the agent as a file, as every credential on a laptop is, though the
  flow itself never prints it. The user code is not secret in the RFC's
  sense, but for its lifetime it is the thing a phisher wants.
- **Two polling clients instead of one.** Today the Mac has one bootstrap
  dependency, the edge's wgsync pass within 30 s of `host add`. The device
  flow adds an HTTPS dependency on the public api from the machine being
  added, and a pending state the customer can walk away from. Tailscale
  and NetBird live with exactly this (the "waiting for approval" node) and
  their complaints are about expiry on headless re-auth, which our 24-hour
  token and Register's key rotation do not have.
- **Vendor docs, not measurements, carry most of this.** No product in the
  table was run end to end by this session; two `--help` outputs were.
  The complaint threads are real and dated, but the shape of each flow is
  taken from documentation and could be out of date in detail.

## 7. What we would have to build, and what changes about ENG-42

What changes, by interface and component, for step 2:

- **`docs/interfaces/api.md`.** `POST /hosts/join` without a login:
  `{public_key, hostname, platform}` to `{device_code, user_code,
  verification_uri, verification_uri_complete, expires_in, interval}`,
  rate-limited, codes from the RFC's base-20 alphabet (§6.1), 15 minutes.
  `POST /hosts/join/poll` with the device code: `authorization_pending`,
  `slow_down`, `expired_token`, `access_denied`, or the `install.argument`
  minus `private_key`, plus the token, that is `{token, wg: {address,
  edge_pubkey, edge_endpoint}}`. `GET /hosts` lists pending requests with
  `state: pending`, `user_code`, `hostname`, `asked_at`, `asked_from`.
  `POST /hosts/:id/approve` and `/deny`, login only. `POST /hosts` keeps
  minting a bundle for one release, for the lab and x86, with the
  dashboard no longer calling it for a Mac.
- **`docs/interfaces/db-schema.md`.** A pending request: the public key,
  hostname, device code hash, user code, asked-from address, expiry; on
  approval it becomes the `hosts` row with `wg_pubkey` set, which is what
  `GET /internal/hosts` lists for wgsync.
- **`internal/api/hostmgr`.** `PrepareHub(pubkey)`; the approve path
  allocates addresses as `POST /hosts` does today.
- **`internal/cli`.** `host add` on a Mac: generate the key pair, request,
  print, poll, compose the bundle locally, call `applianceInstall` as
  today. `host approve CODE`. `host ls` with the pending row. `--join`
  from a file. `docs_test.go` and `cli.md` move together.
- **`apps/web`.** The Hosts page: the pending row, approve and deny with
  the sentence that names the consequence; the dialog that shows a
  secret goes; `own-hardware.md` and `agents.md` say the new line.
- **Nothing in `nix/`.** The appliance reads the same file.
- **Log fields**: ids, states, the user code's existence, never its
  value, and never the public key beyond its fingerprint
  (`docs/ops/OBSERVABILITY.md`).

What changes about ENG-42. Its finding stands and its fix, a file or
stdin for `--join`, is step 1 and should be done. Its scope changes: the
card should say the fix ends the `ps` exposure and leaves the bundle on
every other surface, and link here for the rest. A second card, filed as
the open question it is, not as a design: "the install line carries a
private key through the customer's hands; the pattern every network peer
product uses is key-on-the-node plus approval; decide whether to adopt it
and in which release". The decision itself is `DECISIONS.md`'s, with an
id reserved by whoever builds it; this file reserves none.

What the docs page would say instead of "treat it as a password until the
machine has joined": nothing, because there is nothing to treat as one.

## 8. What I could not check

- **No product was run end to end.** `gh auth login --help` (2.100.0)
  and `tailscale up --help` (1.104.5) were read on this box; everything
  else is a document. In particular, the exact text each product prints
  and the exact token formats are vendor claims.
- **The Mac council's 97 s** is quoted from ENG-42 and not re-measured.
- **Whether the appliance VM can reach `https://api.repose.run`** was
  read from the NAT networking in I-1380 and not tested; the launcher on
  the Mac can, because the logged-in CLI does.
- **Apple's Managed Device Attestation** as a future second factor was
  not researched beyond its existence.
- **Rate limiting on the public api** for an unauthenticated join
  endpoint was not surveyed in the code; the threads above say GitHub
  allows 50 submissions an hour per application.
- **Three forum pages redirected or returned snippets only** (ZeroTier's
  Discourse, two threads), and are marked as such by the subagent;
  I kept them because the claims are minor.
- **Cursor's runtime-secret redaction** is quoted from its docs; a
  secondary report that values still show in the agent's terminal and
  that encodings bypass the pattern could not be fetched at its source
  and is left out of the table.
- **Whether a user code can be bound to the approver's session** in a
  way that defeats the phishing kits was asked and not answered by any
  vendor doc found; Entra's November 2026 "Approve / This wasn't me" page
  is the closest, and it is a wording change, not a binding.

## Sources

Grouped by family. The kind is in brackets.

**Specs and the device flow.** RFC 8628 [spec]
https://www.rfc-editor.org/rfc/rfc8628. Microsoft, Storm-2372 [advisory]
https://www.microsoft.com/en-us/security/blog/2025/02/13/storm-2372-conducts-device-code-phishing-campaign/.
Volexity [advisory]
https://www.volexity.com/blog/2025/02/13/multiple-russian-threat-actors-targeting-microsoft-device-code-authentication/.
Microsoft, relay kits, 2026-04-06 [advisory]
https://www.microsoft.com/en-us/security/blog/2026/04/06/ai-enabled-device-code-phishing-campaign-april-2026/.
Praetorian, GitPhish [advisory]
https://www.praetorian.com/blog/introducing-github-device-code-phishing/.
AWS SSO device-code phishing [advisory]
https://blog.christophetd.fr/phishing-for-aws-credentials-via-aws-sso-device-code-authentication/.
Entra, block authentication flows [vendor]
https://learn.microsoft.com/en-us/entra/identity/conditional-access/policy-block-authentication-flows.
Okta [advisory]
https://www.okta.com/blog/threat-intelligence/device-code-phishing--it-s-phishing-with-dynamite/.
Auth0 [vendor]
https://auth0.com/docs/get-started/authentication-and-authorization-flow/device-authorization-flow.
GitHub OAuth apps [vendor]
https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps.
gh manual [vendor] https://cli.github.com/manual/gh_auth_login; gh issues
10108, 8954, 14597, 5500, 12925 [issue]. AWS CLI SSO [vendor]
https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sso.html;
aws-cli issues 5301, 9092 [issue]; Zscaler on the token cache [advisory]
https://www.zscaler.com/blogs/cybersecurity-best-practices/aws-sso-access-tokens.

**Network peers.** Tailscale, how it works [vendor]
https://tailscale.com/blog/how-tailscale-works; identity [vendor]
https://tailscale.com/docs/concepts/tailscale-identity; auth keys [vendor]
https://tailscale.com/kb/1085/auth-keys; `up` reference [vendor]
https://tailscale.com/docs/reference/tailscale-cli/up; OAuth clients
[vendor] https://tailscale.com/kb/1215/oauth-clients; tailnet lock [vendor]
https://tailscale.com/kb/1226/tailnet-lock; device approval [vendor]
https://tailscale.com/kb/1099/device-approval; Hugging Face intrusion
[advisory] https://tailscale.com/blog/hugging-face-intrusion; KB puts the
key in argv [issue] https://github.com/tailscale/tailscale/issues/21084;
NixOS module [issue] https://github.com/NixOS/nixpkgs/issues/572160;
headless expiry [blog] https://perun.au/insights/tailscale-production/.
ZeroTier CLI and protocol [vendor] https://docs.zerotier.com/cli/,
https://docs.zerotier.com/protocol/, https://docs.zerotier.com/faq/resetaddress/;
Pulse Security [advisory]
https://pulsesecurity.co.nz/advisories/Zerotier-Private-Network-Access.
Headscale registration [vendor] https://headscale.net/stable/ref/registration/;
issue 1851 [issue]. NetBird [vendor]
https://docs.netbird.io/about-netbird/how-netbird-works,
https://docs.netbird.io/manage/peers/register-machines-using-setup-keys,
https://docs.netbird.io/get-started/cli; issue 1656, discussion 6475 [issue].
cloudflared permissions and run parameters [vendor]
https://developers.cloudflare.com/tunnel/features/locally-managed-tunnels/tunnel-permissions/,
https://developers.cloudflare.com/tunnel/reference/run-parameters/;
issue 645 and PR 1316 [issue]; docs PR 33157 closed unmerged [issue]
https://github.com/cloudflare/cloudflare-docs/pull/33157; token leak and
rotation [blog]
https://dev.to/iam-tech/gitleaks-cloudflare-tunnel-token-leak-rotating-it-took-my-tunnel-down-2jm8.

**CI runners.** GitHub runner auth design [vendor]
https://github.com/actions/runner/blob/main/docs/design/auth.md; REST
reference [vendor] https://docs.github.com/en/rest/actions/self-hosted-runners;
token in CloudWatch [issue] https://github.com/CloudSnorkel/cdk-github-runners/issues/1008;
stdin reworks [issue] https://github.com/go-gitsafe/gitsafe/pull/25,
https://github.com/runnane/runnerctl/pull/111; Synacktiv on runner
credentials [advisory]
https://www.synacktiv.com/en/publications/hijacking-github-runners-to-compromise-the-organization;
Sysdig [blog]
https://www.sysdig.com/blog/how-threat-actors-are-using-self-hosted-github-actions-runners-as-backdoors;
JIT config decoded [blog]
https://zenn.dev/kesin11/articles/20230607_runner_jitconfig?locale=en.
GitLab new creation workflow [vendor]
https://docs.gitlab.com/ci/runners/new_creation_workflow/; blueprint
[vendor]
https://gitlab.com/gitlab-org/gitlab/-/blob/master/doc/architecture/blueprints/runner_tokens/index.md;
forum 89630 [issue]
https://forum.gitlab.com/t/gitlab-16-x-get-authentication-token-for-registering-new-runners-via-api/89630;
terraform-aws-gitlab-runner 850 [issue]. Buildkite tokens [vendor]
https://buildkite.com/docs/agent/self-hosted/tokens; agent issues 458,
1769, cli PR 1069 [issue]; changelogs 272, 401, 440 [vendor]. CircleCI
incident [advisory] https://circleci.com/blog/jan-4-2023-incident-report/
and rotation guide
https://support.circleci.com/hc/en-us/articles/11816211460891-Rotating-Secrets-for-January-4th-Incident.
Jenkins inbound agents [vendor]
https://github.com/jenkinsci/remoting/blob/master/docs/inbound-agent.md;
createos-sandbox-plugin 5 [issue]; advisory 2026-09-02 [advisory]
https://www.jenkins.io/security/advisory/2026-09-02/. Azure Pipelines
[vendor]
https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/personal-access-token-agent-registration?view=azure-devops,
https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/agents?view=azure-devops;
issue 3308 [issue].

**Clusters and attestation.** kubeadm join and bootstrap tokens [vendor]
https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-join/,
https://kubernetes.io/docs/reference/access-authn-authz/bootstrap-tokens/,
https://kubernetes.io/docs/reference/access-authn-authz/kubelet-tls-bootstrapping/;
kubeadm issues 659, 1452, 668, 2287 [issue]; lok8s 252 [issue]
https://github.com/kernpilot/lok8s/issues/252; Synacktiv on AKS bootstrap
tokens [advisory]
https://www.synacktiv.com/en/publications/so-i-became-a-node-exploiting-bootstrap-tokens-in-azure-kubernetes-service.
k3s token [vendor] https://docs.k3s.io/cli/token,
https://docs.k3s.io/installation/configuration; rotation ADR [vendor]
https://github.com/k3s-io/k3s/blob/main/docs/adrs/server-token-rotation.md;
k3s issues 2645, 11674 [issue]; public repo leak [issue]
https://github.com/jamescottrill/rpi-k8s/pull/34; pasted into an AI chat
[issue] https://github.com/BODAPATI88/bvrinfra/issues/95. Vault AppRole
[vendor] https://developer.hashicorp.com/vault/docs/auth/approle,
https://developer.hashicorp.com/vault/docs/agent-and-proxy/autoauth/methods/approle;
response wrapping talk [vendor]
https://www.hashicorp.com/en/resources/vault-response-wrapping-makes-the-secret-zero-challenge-a-piece-of-cake;
Discuss 58201 [issue]
https://discuss.hashicorp.com/t/approle-response-wrapping-without-trusted-orchestrator-in-between/58201;
vault issues 12173, 16148 [issue]. SPIRE [vendor]
https://spiffe.io/docs/latest/deploying/spire_server/,
https://spiffe.io/docs/latest/deploying/configuring/; spire issues 242,
1847, 1644 [issue]; Weaverlab PR 1102 [issue]. Teleport join token and
methods [vendor] https://goteleport.com/docs/installation/agents/join-token/,
https://goteleport.com/docs/reference/deployment/join-methods/,
https://goteleport.com/docs/installation/agents/aws-iam/; RFD 0041 and
RFD 0205 [spec]
https://github.com/gravitational/teleport/blob/master/rfd/0041-aws-node-join.md,
https://github.com/gravitational/teleport/pull/52546; bound keypair
[vendor] https://goteleport.com/docs/installation/agents/bound-keypair/,
https://goteleport.com/docs/reference/machine-workload-identity/bound-keypair/admin-guide/;
teleport issues 3732, 2515, discussion 45723 [issue]. Consul [vendor]
https://developer.hashicorp.com/consul/docs/secure/encryption/gossip/enable,
https://developer.hashicorp.com/consul/docs/secure/acl/token/agent,
https://www.hashicorp.com/en/blog/automate-consul-agent-security-with-auto-config.

**Pairing by identity.** Syncthing device ids, getting started, FAQ,
introducer [vendor] https://docs.syncthing.net/dev/device-ids.html,
https://docs.syncthing.net/intro/getting-started.html,
https://docs.syncthing.net/users/faq.html,
https://docs.syncthing.net/users/introducer.html; HN on ids assumed secret
[issue] https://news.ycombinator.com/item?id=38980553; ids in logs [issue]
https://forum.syncthing.net/t/censor-device-ids-in-the-logs/14824;
unknown device prompts [issue]
https://forum.syncthing.net/t/connection-request-from-unknown-device/13189.
NIST SP 800-121r2 on numeric comparison [advisory]
https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-121r2.pdf;
Bluetooth Core 5.4 security specification [vendor]
https://www.bluetooth.com/wp-content/uploads/Files/Specification/HTML/Core-54/out/en/br-edr-controller/security-specification.html.
ssh(1), ssh_config(5) [vendor] https://man.openbsd.org/ssh.1,
https://man.openbsd.org/ssh_config. Apple: two-factor code [vendor]
https://support.apple.com/en-us/102660, https://support.apple.com/en-us/102606;
AirPlay device verification [vendor]
https://support.apple.com/guide/deployment/use-airplay-dep9151c4ace/web;
Automated Device Enrollment and profile enrolment [vendor]
https://support.apple.com/guide/deployment/automated-device-enrollment-management-dep73069dd57/web,
https://support.apple.com/guide/deployment/device-enrollment-and-device-management-depd1c27dfe6/web;
profiles(1) [vendor] https://manp.gs/mac/1/profiles. Nebula [vendor]
https://nebula.defined.net/docs/guides/sign-certificates-with-public-keys/;
Defined Networking [vendor]
https://docs.defined.net/get-started/dnclient-server/install/; Netmaker
[vendor]
https://docs.netmaker.io/docs/netmaker-network-setup-concepts/2-add-non-user-devices;
Twingate [vendor] https://www.twingate.com/docs/deployment-semi-automation;
NetBird peer approval [vendor] https://docs.netbird.io/manage/peers/approve-peers
and the request behind it [issue] https://github.com/netbirdio/netbird/issues/1783;
Tailscale `up` and device approval [vendor] https://tailscale.com/kb/1241/tailscale-up,
https://tailscale.com/docs/features/access-control/device-management/device-approval.

**CLIs that pair with a code.** Stripe [vendor]
https://docs.stripe.com/cli/login.md; Netlify `login --request` and
`--check` [vendor] https://cli.netlify.com/commands/login/; Fly [vendor]
https://docs.fly.io/flyctl/auth-login and the change for agents [issue]
https://github.com/superfly/flyctl/pull/5209; Vercel [vendor]
https://vercel.com/docs/cli/login; Railway [vendor]
https://docs.railway.com/cli; Supabase [vendor]
https://supabase.com/docs/reference/cli/supabase-login; Render [vendor]
https://render.com/docs/cli; 1Password `op signin` and `op run` [vendor]
https://www.1password.dev/cli/sign-in-manually/,
https://www.1password.dev/cli/reference/commands/run.

**Agents and transcripts.** Claude Code data usage [vendor]
https://code.claude.com/docs/en/data-usage; secrets in transcripts
[issue] https://github.com/anthropics/claude-code/issues/63593,
https://github.com/anthropics/claude-code/issues/71654,
https://github.com/anthropics/claude-code/issues/50014; Cursor cloud agent
security [vendor] https://cursor.com/docs/cloud-agent/security; Copilot CLI
session state [vendor]
https://docs.github.com/en/copilot/concepts/agents/copilot-cli/chronicle;
Gemini CLI telemetry [vendor] https://geminicli.com/docs/cli/telemetry/ and
its env scrubbing [issue] https://github.com/google-gemini/gemini-cli/issues/18302;
Codex re-reading its own logs [issue] https://github.com/openai/codex/issues/27131;
where the CLIs keep sessions [blog]
https://allaboutcoding.ghinda.com/where-ai-coding-clis-store-session-logs/,
https://www.vincentschmalbach.com/where-claude-code-and-codex-store-session-transcripts-and-how-to-keep-secrets-out-of-them/,
https://dev.to/momin_aziz_1/your-ai-coding-transcripts-are-a-secret-store-where-claude-code-codex-cursor-copilot-and-3g2a;
the Comment and Control injection [blog]
https://oddguan.com/blog/comment-and-control-prompt-injection-credential-theft-claude-code-gemini-cli-github-copilot/.
proc(5) cmdline [vendor] https://man7.org/linux/man-pages/man5/proc_pid_cmdline.5.html;
Smallstep on command-line secrets [blog]
https://smallstep.com/blog/command-line-secrets/; GitGuardian [blog]
https://blog.gitguardian.com/secrets-at-the-command-line/; zsh options
[vendor] https://zsh.sourceforge.io/Doc/Release/Options.html.

**This repository (read at `fdf0ce95`).** `internal/cli/hosts.go`,
`hostscmd.go`, `appliance.go`, `login.go`, `oidc.go`, `config.go`;
`internal/api/hostmgr/hostmgr.go`; `apps/web/src/lib/components/OwnHosts.svelte`;
`nix/hosts/mac.nix`; `docs/interfaces/api.md`, `host-conventions.md`;
`docs/features/own-hardware.md`, `secrets.md`; `docs/SECURITY.md` "A host
you do not own"; DECISIONS I-92, I-101, I-627, I-1380, I-1382; the board's
ENG-42 and ENG-29.
