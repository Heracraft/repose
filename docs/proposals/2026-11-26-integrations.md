# Integrations: connect a provider once, every agent can use it (proposal, 2026-11-26)

**Status: proposal for the owner's judgment. Docs only; no product
code.** Nothing here is decided. Each item that changes behaviour needs
its own `DECISIONS.md` entry (section 15) and ships with its public
docs. If this file and a decision entry disagree, the entry wins. Code
references are to `main` at `449fa8e6`. Provider facts were read in the
providers' own docs or in secondary guides on 2026-11-26 and carry a
source; facts no source stated are marked *unverified*.

Prompted by the owner in the session of 2026-11-26: "I want to be able
to let agents request tokens and instead of just copying and laundering
text around, I can just approve and they get the secrets"; "I have
something like Linear and I want to connect Linear to all my agents";
"make this as open and modifiable and flexible and robust enough so that
in the future we just open source this whole thing so that users can add
their own thing the same way that users of Coolify can just add
templates". The owner named the starter set: AWS, Azure, Linear, Kaneo,
GitHub, Grafana. The owner also set the bar for friction: "saying agents
can only ask for secrets predefined by the user ... will actually limit
velocity, they'll hate it".

## Headline

- **An integration is a provider account the owner connects once, on
  the dashboard, for the whole account.** Every machine of every project
  can then use it. The owner connects it with OAuth or a pasted token.
  The agent asks for a time-boxed grant, and the owner approves it in
  one tap. Linear's MCP server, the GitHub CLI, the AWS SDK and a
  Coolify deploy script then work on that machine with no named secret
  set per project.
- **repose holds the parent credential, and a machine gets something
  derived from it.** There are three ways to derive it, and the
  template for each provider picks one:
  1. **Relay.** repose adds the real token to the request on its way to
     the provider, so the token never reaches the machine. This works
     for any provider that takes a token in a header at a fixed HTTPS
     host: Linear, Kaneo, Coolify, Grafana, Resend and most SaaS.
  2. **Mint.** repose asks the provider for a short-lived child token
     and writes it to the machine as a secret: GitHub App installation
     tokens, Grafana service account tokens, Cloudflare tokens.
  3. **Federate.** repose acts as an OIDC issuer. AWS and Azure trust it
     and give the machine one-hour credentials, and repose stores no AWS
     or Azure secret at all.
- **A named secret stays what it is today.** It is a value the machine
  holds: SMTP passwords, database URLs, SSH keys, signing keys. An
  integration is one way to get a credential. It does not replace a
  secret.
- **Each provider is a template, and a template is data, never code.**
  A YAML file names the auth kind, the host, how the token goes into a
  request, the scope presets and a check request. A small engine in Go
  implements the auth kinds. Community templates can then be reviewed
  the way Coolify's one-click templates are, and the engine can be open
  source.
- **Scopes have three layers.** The provider's own scopes (OAuth
  scopes, mint permissions), relay rules that repose enforces (method
  and path, GraphQL operation type, MCP tool name), and time. A template
  names presets such as `read`, `deploy` and `admin`, and the agent asks
  for one of them by name.
- **This reopens a choice repose made twice.** The 2026-09-27
  credentials proposal turned down a placeholder proxy
  (`docs/proposals/2026-09-27-credentials-convenience-first.md:396-400`),
  and I-929 repeated it (`docs/DECISIONS.md:27410`). Section 2 answers
  each of its four reasons. The main change in this design is to start
  with an **explicit relay**: the tool is pointed at a loopback address,
  so there is no TLS interception and no certificate to trust. A
  transparent proxy is a later stage, and only on request.
- **The relay is a fourth home for a credential.** While a grant lives,
  the edge holds the provider token in memory. CLAUDE.md forbids a
  fourth home without the owner's sign-off. Section 8 limits what that
  home holds and for how long, so the owner can sign it off the way
  I-653 was signed off.
- **Size:** about 30 to 40 engineering days in six stages (section 14),
  *estimate*. Stage 1 alone (custom static-token integrations through
  the relay, with grants and one-tap approval) is about 8 to 10 days and
  covers Coolify, Kaneo and Grafana.

## Summary

| Question | Answer |
|---|---|
| What the owner does | Connects a provider once, under Settings, Integrations. OAuth button for named providers, a form for the rest |
| What the agent does | `repose-ask --grant linear --scope read --for 5h "triage the inbox"`. Exit 0 means the integration works on this machine |
| What the owner approves | One tap for a grant like one already given. The dashboard, with a confirm, for a first grant, a wider scope or a new host |
| Where the token lives | Ciphertext in Postgres under the account data key, like an account secret. In the edge's memory while a grant lives (relay). In the guest's tmpfs (mint, federate) |
| How a request reaches the provider | Relay: tool → `http://127.0.0.1:9228/<integration>/...` in the guest → guestd → edge (existing hook-ingest path) → provider, with the token added at the edge |
| Expiry | The grant's TTL, enforced by the relay or by the provider. OAuth tokens are refreshed by the api. A static token's own expiry is entered at connect time or found by a 401 |
| Scopes | Presets per template, mapped to provider scopes and relay rules. A request outside the preset gets 403 from the relay, with the preset named |
| Templates | YAML, schema in `docs/interfaces/integration-template.md`, official ones in `integrations/templates/`, private ones per account in Postgres |
| Starter set | AWS (federate), Azure (federate), GitHub (mint), Grafana (mint or relay), Linear (OAuth, relay), Kaneo (relay) |
| MCP | A template may declare an MCP server. `repose-mcp sync` renders it into every agent's config, pointed at the relay |
| CLI | `repose integrations` on the laptop (a new noun, section 9.3); `repose-ask --grant` and `repose-integrations` in the guest |
| Not covered | Databases over their own protocol, SMTP, SSH, request signing other than AWS's, Anthropic's API (never relayed, I-278) |
| New decisions | About ten (section 15), including the fourth home and repose as an OIDC issuer |

---

## 1. The repo today

**Named secrets.** A project secret is a row in `secrets`, an account
secret a row in `account_secrets` (migration 0024). Both are AES-GCM
ciphertext under a per-user data key that Azure Key Vault wraps
(`internal/api/secrets/azurekv.go`). The ciphertext is bound to project
and name, or to user and name for an account secret
(`internal/api/secrets/secrets.go:185`). `DecryptForGuest`
(`secrets.go:346`) is the one place a value is decrypted; it merges the
two sets so the project's own value wins. A change queues an
`UpdateSecrets` op carrying the whole set (`docs/interfaces/grpc-hostd.md:345`).
hostd sends `WriteSecrets` over vsock (`docs/interfaces/vsock-guestd.md:23`),
and guestd writes `/run/repose/secrets/NAME` (0400, owner `dev`, tmpfs)
and regenerates `secrets.env` (`internal/guestd/secrets/secrets.go:85,286-358`).
Each bash command loads the current set through `BASH_ENV`
(`nix/guest/base/env.nix:50-55`, I-475). Every change writes an
`audit_log` row (`secret_put`, `account_secret_put`;
`docs/interfaces/db-schema.md:323`).

**An agent asks for a secret (I-929).** `repose-ask --secret NAME
REASON` (`cmd/repose-hook/ask.go`) posts to guestd's hooks socket.
guestd relays it over vsock, hostd stamps the `guest_id` from its own
session record (`internal/hostd/guest/create.go:1281`) and sends it up
its mutual-TLS stream, and the api inserts a `questions` row with
`secret_name` (`internal/api/questions/questions.go:225,329`). The owner
pastes the value into `SecretDialog.svelte` or `repose reply`.
`AnswerSecret` (`questions.go:519-579`) stores it with `Secrets.PutTx`,
writes the audit row, queues the push and closes the question in one
transaction. The close reaches the guest only after the push is done.
A secret question has no signed reply links, so no notification can
answer it (`docs/features/notifications.md:236-244`).

**How much the api knows about the asker.** The guest holds no api
credential. The api trusts the `guest_id` that hostd stamps, and hostd
treats every guest message as written by root in the guest (I-445). The
`agent` field is whatever the process claims (`--agent`,
`REPOSE_HOOK_AGENT`, or an ancestor's name;
`docs/interfaces/guest-conventions.md:839-844`). guestd keys a question
to `pid:starttime` through `SO_PEERCRED`, but that key stays in the
guest (`guest-conventions.md:908-911`). Since I-1676, `AgentEvent`
carries an HMAC'd `agent_id`. `Question` does not yet; the agent-model
proposal plans it (`docs/proposals/2026-11-26-agent-model.md:358`). So
today an approval can be bound to a machine, and not yet to an agent.

**The MCP carry (I-556).** `repose run` turns each literal credential in
the laptop's MCP config into `${NAME}` (`internal/cli/carry_mcp.go`).
The guest keeps `/home/dev/.repose/mcp/laptop.json`. `repose-mcp sync`
renders it into each agent's config, and `repose-mcp run NAME` fills
each `${X}` from `/run/repose/secrets/X` before it starts the server
(`internal/mcpreg/run.go`). An HTTP server whose secret is missing is
skipped (`docs/features/agents.md:555-559`). The user then has to set
`LINEAR_API_KEY` per project or once as an account secret. That is the
owner's "very mad" step.

**Guest networking.** One tap per guest on `br-guests`. nftables
`guest_fwd` drops IPv6, port 25, mining pools, IMDS, the Azure wire
server, other guests and private ranges, and allows the edge's 8443 and
the uplink with masquerade NAT (`nix/hosts/nftables.nix`,
`docs/interfaces/host-conventions.md:115-186`). There is no egress
proxy, no injected CA and no `HTTPS_PROXY` in any guest image. The CA
bundle is the standard `/etc/ssl/cert.pem` (`guest-conventions.md:1319`).
DNS goes to 1.1.1.1 and 8.8.8.8 (`nix/guest/base/network.nix`).

**A guest-to-edge path already exists.** Guests reach
`10.255.0.1:8443` on the edge. The gateway's `HookIngest`
(`internal/gateway/hookingest.go:34-65`) forwards to the api, which maps
the source IP to a project (`internal/api/events/events.go:791-806`,
`FromEdge`). The relay in this proposal reuses that path and that
mapping.

**Short-lived things repose already mints.** SSH user certificates
(24 h), gateway certificates (5 min) (`internal/ca/sshca/sshca.go:18-22`),
browser-view tickets, signed reply links, and `rpt_` API tokens with
scopes and expiry for users and scripts (`docs/features/tokens.md`).
Nothing is minted per guest. repose runs no OIDC issuer; it consumes
Logto's (`internal/api/auth/jwks.go`).

**The word "integration".** No product feature uses it. It appears for
herdr's agent integration (`docs/features/agents.md:101-104`), Google
Chat's "Apps & integrations" menu (`apps/web/src/content/docs/notifications.md:38`),
and "integration test". The name is free, and section 9.1 fixes it.

**Webhooks (I-1110 to I-1120).** The edge already terminates public TLS
for `hooks.repose.run`, keeps a table refreshed from the api every 5 s,
and reaches the guest through its own SSH connection
(`docs/features/webhooks.md`). It is the nearest built precedent: a
small Go service on the edge, a table pulled from the api, and logs that
never hold a path, header or body.

## 2. What was turned down before, and what changed

The 2026-09-27 proposal kept the placeholder proxy "on request only" and
gave four reasons not to build it then. Each is still true for a
*transparent* proxy. This design answers each in a different way.

| Reason in 2026-09-27 | Answer in this proposal |
|---|---|
| "Holds real values in hostd memory (a fourth secret home)" | True for the relay. The relay runs on the edge, not in hostd. It holds a value only while a grant lives, only in memory, and never on disk or in a log. The owner signs it off as an exception, as I-653 was (section 8) |
| "Breaks on certificate trust per runtime" | The first stages use an explicit relay: the tool talks plain HTTP to `127.0.0.1` in its own machine, and the TLS to the provider starts at the edge. No CA is installed and no runtime has to trust one. The transparent mode, which does need a CA, is stage 6 and on request (section 6.4) |
| "Doesn't cover database URLs, request signing or signing secrets" | Correct, and it is not meant to. Those stay named secrets, or use federation (AWS, Azure) and minting. The relay is one of three deliveries |
| "Minting covers more with no new home" | Minting is one of the three deliveries here, and the first choice where a provider supports it. The relay covers the providers that cannot mint (Coolify, Kaneo, Stripe, Resend's expiry) and MCP servers. The 2026-09-27 table puts Stripe, Anthropic, Sentry, PostHog, Railway and Render in "can't mint" |

Three facts changed since September:

1. **MCP servers became the main way agents use SaaS.** Linear's
   official MCP server takes `Authorization: Bearer <token>` and has a
   read-only endpoint (`https://mcp.linear.app/mcp/readonly`) ([Linear
   MCP docs](https://linear.app/docs/mcp)). An MCP server over HTTP is
   one URL that repose already renders into every agent's config. This
   makes the explicit relay cheap: repose writes the relay's address,
   and no SDK needs a change.
2. **The owner's ask is convenience first.** The September position was
   "repose wins on convenience; users have not asked it for safety".
   Integrations follow it. "Connect once, every machine" removes a step.
   Keeping the token off the machine and letting it expire comes free
   with the relay, so the safety costs nothing extra.
3. **The edge has a guest-facing path** (hook ingest on 8443, mapped by
   source IP) and runs a Go service that already pulls a table from the
   api (webhooks). The relay has somewhere to live.

## 3. What the other tools do

| Tool | How it gives a credential to a workload | Providers covered | Human approval |
|---|---|---|---|
| HashiCorp Vault | Dynamic secrets: one secrets engine per provider; each credential has a lease and is revoked at its end | AWS, Azure, GCP, Kubernetes, many databases, PKI, some plugins | Not built in; policy only |
| Infisical | Dynamic secrets from a fixed template list; temporary access requests with approval policies; Agent Vault, an open-source HTTP proxy that adds the stored key on the way out | Mostly databases and clouds for dynamic; any HTTP API through Agent Vault | Access requests, with multi-step approvers and a maximum time |
| Doppler | Dynamic secrets on the Enterprise plan (default lease 30 min); service tokens with a maximum age | AWS and a few others | Not built in |
| 1Password | Secure Agentic Autofill: a human approves each credential request by default and the agent never sees it; Privileged Access (from the Apono acquisition) gives access for a window | Browser logins; just-in-time cloud access | Per request, in the 1Password app |
| Permit.io, OneCLI, AgentSecrets | HTTP egress proxies that swap a placeholder for the real key, bound to host patterns | Any header-token API | Varies |

Two lessons apply to repose. Nobody makes scoped, expiring credentials
for every provider; minting covers a list, and everyone else uses a
proxy. And the proxies that exist ask the agent to route through them.
repose controls the guest image and the MCP config, so it can do that
routing for the agent.

Sources: [Vault dynamic secrets](https://developer.hashicorp.com/validated-designs/vault/administration-guide/dynamic-secrets-management),
[Infisical dynamic secrets](https://infisical.com/docs/documentation/platform/secrets-mgmt/concepts/dynamic-secrets),
[Infisical access requests](https://infisical.com/docs/documentation/platform/access-controls/access-requests),
[Infisical agent proxy](https://infisical.com/docs/documentation/platform/agent-proxy/overview.md),
[Doppler dynamic secrets](https://docs.doppler.com/docs/dynamic-secrets),
[Doppler service tokens](https://docs.doppler.com/docs/service-tokens),
[1Password and browser agents](https://1password.com/blog/closing-the-credential-risk-gap-for-browser-use-ai-agents),
[1Password Privileged Access](https://siliconangle.com/2026/07/28/1password-launches-privileged-access-cut-standing-access-ai-agents/),
[Permit.io egress credentials](https://docs.permit.io/permit-mcp-gateway/http-egress-proxy/credentials).

## 4. The model

### 4.1 Words

- **Integration**: one provider account connected to one repose
  account. It has a template, a name (`linear`, `coolify-prod`), the
  parent credential, the template inputs (a host for a self-hosted
  provider) and a status (`ok`, `expiring`, `broken`).
- **Template**: the data file that says how a provider works with
  repose (section 7).
- **Grant**: permission for one project's machine to use one
  integration at one scope until a time. A grant is what an agent asks
  for and the owner approves.
- **Delivery**: how a granted machine gets the credential: `relay`,
  `mint` or `federate` (section 6).
- **Scope preset**: a named set of permissions in a template (`read`,
  `deploy`, `admin`).

### 4.2 Where each provider falls

Two questions decide the delivery.

1. **Can the request go through the relay?** Yes when the tool sends
   the token in a header to a fixed HTTPS host. No for AWS SigV4 and
   Azure (signing or token exchange in the machine), databases, SMTP and
   SSH.
2. **Can repose make short-lived tokens?** Yes when the provider has
   OAuth with refresh, a token-creation API, or trusts an OIDC issuer.

| | Can make short-lived tokens | Cannot |
|---|---|---|
| **Relay works** | Linear (OAuth), GitHub (App), Grafana (service account API), Cloudflare (token API) | Coolify, Kaneo, Stripe, Resend, most SaaS |
| **Relay cannot work** | AWS (STS), Azure (Entra), Postgres (`CREATE ROLE ... VALID UNTIL`) | SMTP, SSH keys, a pasted database URL |

The first three cells are integrations. The fourth is a named secret,
as today. Where both relay and mint work, the template picks one. Relay
is the default, because the token then never reaches the machine.

### 4.3 Account-wide, with a project switch

An integration belongs to the account, like an account secret (I-697).
By default every project may ask for a grant. The owner can limit an
integration to chosen projects. A project's own named secret of the same
env name still wins inside that machine, as I-697 says for account
secrets. That lets one project use a different Linear workspace.

## 5. Grants and approval

### 5.1 What the agent runs

```
repose-ask --grant INTEGRATION [--scope PRESET] [--for DURATION] REASON...
```

- `INTEGRATION` is the integration's name. `--scope` defaults to the
  template's `default_scope`. `--for` defaults to the owner's last
  choice for this integration and project, else 1h, and is capped by
  the template's `max_ttl` and by the owner's cap for that integration.
- Exit 0 once the grant is live on the machine: the relay accepts
  requests, or the minted token or federation token file is written. It
  then prints, on stderr, the env names and the MCP server the
  integration gives, such as `LINEAR_API_URL, LINEAR_API_KEY, mcp:linear`.
- Exit codes follow `repose-ask`: 3 no answer, 5 denied, 6 answered in
  the terminal (the owner ran a grant from the laptop), 7 still open.
  A new code, 8, means the integration does not exist or this project
  may not use it; it prints `repose-integrations list` output so the
  agent can pick one.
- A grant is a question of a new kind, `grant`, in the `questions`
  table (section 10). It uses the same hold, timeout and close paths.

`repose-integrations list` in the guest prints each integration the
project may use, its scopes, and any live grant with its end time. It
reads a file guestd keeps (`/run/repose/integrations.json`), which the
api pushes with the grant set. An agent can see what exists before it
asks.

### 5.2 What the owner approves, and where

The owner said most of the defences in the first answer "fuck up user
experience". The rule here is: one tap wherever a mistake costs little
and can be undone; a confirm on the dashboard only where a mistake
sends a credential somewhere new.

| Request | Where the owner can approve |
|---|---|
| A grant at a scope and a duration no wider than one already given to this project for this integration in the last 30 days | One tap: ntfy action button, Google Chat button, email button, dashboard, `repose integrations grant` |
| A first grant for this project, or a wider scope, or a longer duration | Dashboard, with the scope's rules shown and a confirm |
| A grant at a scope the template marks `confirm: true` (any `admin`) | Dashboard, with a confirm, each time |
| A custom integration whose host the owner has not confirmed | Dashboard only; this is part of connecting, not of granting (section 8.3) |

A one-tap grant uses the signed reply links that plain questions already
use (`api.md:738-739`). A grant carries no value, so the I-929 rule
"no notification can answer a secret question" does not apply. The
three ntfy buttons are "Allow 1h", "Allow LAST" (the last duration
given) and "Deny"; Chat and email show the same three.

**Auto-approve, by the owner's choice.** An integration's settings can
say "approve `read` for any project for up to 8h without asking". The
question is still recorded and a quiet event is written, so the owner
sees what was granted. Off by default. This is the owner's velocity
lever, and it lives on the owner's side, where no agent can set it.

### 5.3 Limits on asking

- One open grant question per machine per integration. A second ask
  joins the first.
- After a deny, the same machine cannot ask for the same integration
  for 10 minutes; `repose-ask` exits 5 at once in that time. This stops
  a script asking until the owner taps yes by mistake.
- The rate limits hostd applies to guest questions (I-445) apply.

### 5.4 What a grant ends on

- Its time runs out.
- The owner revokes it (dashboard, `repose integrations revoke`).
- The machine stops, is destroyed, or is restored from a snapshot.
  A grant does not survive a stop. The agent asks again after a start,
  and a one-tap approval covers it.
- The integration is disconnected or its credential is replaced.

A live grant can be extended from the dashboard or by a new ask, which
counts as a re-grant.

### 5.5 What this does not stop, said plainly

repose cannot tell an agent from malware inside a guest. `dev` has
passwordless sudo, and the guest is one trust zone. While a grant
lives, any process on that machine can use it, within the scope. What
the design gives:

- Malware cannot read a relayed token, so it cannot keep it after the
  grant or use it from elsewhere.
- It can use the integration only at the granted scope, only at the
  template's host, and only until the grant ends.
- It cannot widen a grant, add a host or turn on auto-approve; those
  are dashboard actions behind the owner's login.
- The owner sees the reason as text the machine wrote, labelled so,
  next to facts the api supplies (section 5.6).

When the agent-model work gives `Question` an `agent_id` (stage 2 of
`2026-11-26-agent-model.md`), a grant can also name the agent that
asked, and the relay can refuse a request from a different process.
That needs guestd to stamp the peer's `agent_id` on each relay request,
which guestd can do because the relay listener is guestd's own socket
(section 6.1). It is listed as a later stage, not a promise.

### 5.6 What the approval screen shows

From the api, which the machine cannot write: project and machine,
integration and template, scope preset and its rules in full, duration,
which grants this project already has and had in the last 30 days, and
the agent name with its source ("claimed by the process" until
`agent_id` exists). From the machine, labelled "the agent's reason": the
reason text, cleaned of bidi and zero-width characters as I-929 does.

## 6. Delivery

### 6.1 Relay

**Path of one request.**

1. The tool calls `http://127.0.0.1:9228/linear/graphql` inside the
   guest. 9228 is a loopback port guestd listens on; 9227 is the trigger
   receiver and 9224 to 9226 are the browser.
2. guestd checks that the integration has a live grant on this machine
   (it holds the grant list the api pushed). Without one it answers 403
   with a JSON body: `{"error":"no_grant","integration":"linear","ask":"repose-ask --grant linear ..."}`.
   An agent that reads the body knows what to run.
3. guestd forwards the request to the edge's hook-ingest address
   (`10.255.0.1:8443`, path `/relay/<integration>/...`) over TLS with
   the edge-internal certificate guestd already trusts. It drops any
   `Authorization`, `Cookie` and template-declared auth header the tool
   sent, and adds `Repose-Grant: <grant id>`.
4. The edge maps the source IP to a project, as `FromEdge` does. It
   finds the grant in its table, which it refreshes from the api like
   the webhook table, and checks project, integration, expiry and the
   scope's relay rules (section 7.3). A failed check answers 403 with
   the rule that failed.
5. The edge adds the provider credential as the template says
   (`Authorization: Bearer <token>`) and sends the request to the
   template's host over TLS that it verifies against the public CA
   set. It streams the reply back unchanged, server-sent events
   included, because MCP over HTTP uses them.

**Why loopback plain HTTP.** No CA in the guest, no TLS interception,
and every runtime works: Node, Python, Go, Rust with rustls, Java. The
cost is that the tool must accept a base URL. Most do:

- MCP servers over HTTP: repose renders the URL (section 6.5).
- SDKs with a base URL option: Linear's `@linear/sdk` (`apiUrl`,
  *unverified*), OpenAI-compatible clients (`base_url`), Grafana
  clients, the Coolify CLI (it takes an instance URL), `curl` in
  scripts.
- A template sets env names the tool reads, such as
  `LINEAR_API_URL=http://127.0.0.1:9228/linear` and
  `LINEAR_API_KEY=repose-relay` (a placeholder that is not a secret,
  for tools that refuse to start without a key).

A tool with a hard-coded HTTPS host cannot use the explicit relay. For
those, the template can choose `mint` if the provider can, the owner
can add the token as a named secret, or the owner can ask for the
transparent mode (section 6.4).

**Why the edge and not hostd.**

- One place holds relayed tokens, not every host. A fleet host runs
  other tenants' guests; the edge runs no guest.
- The edge already maps a guest's source IP to its project and already
  pulls a table from the api.
- hostd's job is guests. A relay in hostd would put tenant credentials
  in the process that a guest escape reaches first.

The costs: every relayed byte goes guest → edge → provider, so it adds
the edge's latency and counts as the edge's egress. Section 11 meters
it per project. A host the customer owns reaches the edge over
WireGuard; whether its guests reach `10.255.0.1:8443` the same way is
*to verify* (`host-conventions.md:518-549`). If they do not, the relay
on an owned host runs in that host's hostd. That host is the customer's
own hardware and holds only the customer's own credentials, which is a
different question from a shared fleet host. design 2's macOS hostd,
whose egress is Go code at the dial (`docs/proposals/2026-11-26-design2.md:213-269`),
would host it in the same place.

### 6.2 Mint

The api calls the provider's token API with the template's request,
names the child `repose-<grant id>`, records the child's handle before
the value leaves the api, and pushes the value as a named secret with
the name the template gives (such as `GH_TOKEN`). It goes through the
same `UpdateSecrets` op, so the homes stay Postgres ciphertext (for the
parent) and the guest's tmpfs (for the child). The child is never stored
in Postgres: a resent command revokes the recorded child and mints a new
one, as the 2026-09-27 proposal says for the credential manager.

- **Refresh.** A child shorter than the grant is minted again before it
  ends (GitHub's one hour). `gh` reads `hosts.yml` on every call, so a
  file that points at the tmpfs copy stays current; an env variable in a
  long-running process does not. The template says which.
- **Revoke.** At the grant's end the api revokes the child by its
  handle where the provider allows (Grafana, Cloudflare, GitHub's
  `DELETE /installation/token`) and removes the secret from the machine.
- **Sweep.** At api start and daily, list each provider's `repose-*`
  children and delete the ones with no live grant.

### 6.3 Federate (AWS, Azure)

repose runs an OIDC issuer at `https://id.repose.run`, with a discovery
document and a JWKS. The signing key is a Key Vault key that never
leaves Key Vault; the api signs through the Key Vault API. No new home
for a key.

**Connecting AWS.** The owner adds `id.repose.run` as an IAM OIDC
identity provider, creates one role per scope preset that trusts it,
with a condition on `sub`, and pastes the role ARNs into the form. A
CloudFormation template that the dashboard links to does this in one
step. repose stores the ARNs, which are not secrets.

**A grant on a machine.** The api signs a JWT: `iss` the issuer, `aud`
`sts.amazonaws.com`, `sub` `repose:account:<uid>:project:<pid>`, `exp`
10 minutes. It pushes it as the file `/run/repose/secrets/AWS_WEB_IDENTITY_TOKEN`,
with `AWS_WEB_IDENTITY_TOKEN_FILE` and `AWS_ROLE_ARN` set. Every AWS
SDK and the AWS CLI read these two variables and call
`AssumeRoleWithWebIdentity` themselves. The api pushes a fresh JWT
every 5 minutes while the grant lives and stops at its end. The STS
credentials the SDK already holds die by their own duration, which the
role caps (1 hour is AWS's default).

**Azure** is the same shape: a federated identity credential on an app
registration, `aud` `api://AzureADTokenExchange`, and
`AZURE_FEDERATED_TOKEN_FILE`, `AZURE_CLIENT_ID` and `AZURE_TENANT_ID`,
which `DefaultAzureCredential` reads (its workload identity path).

**What this gives.** repose stores no AWS or Azure secret, so there is
nothing to steal from Postgres. The scope is the role the owner wrote,
in the owner's own cloud account. The JWT in the machine is good for 10
minutes and only for the roles that trust that exact `sub`. GitHub
Actions uses the same pattern.

**To verify.** AWS fetches the JWKS over the public internet, so
`id.repose.run` must be reachable and fast. Azure allows 20 federated
credentials per app registration, which caps how many distinct `sub`
values one app can trust; a `sub` per project may need a flexible
federated credential (*unverified* for GA status) or one app per
project.

### 6.4 The transparent mode (stage 6, on request)

For tools with a hard-coded host. The edge cannot see a guest's TLS,
so this mode runs on the host: hostd redirects the guest's TCP 443 to a
local splice that reads the SNI and passes everything through untouched
unless the host is bound to a live grant. For a bound host it ends TLS
with a per-guest CA that the guest image trusts (`SSL_CERT_FILE`,
`NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`), swaps the
placeholder and forwards the request to the edge. Unbound traffic is
never decrypted.

It brings back every cost September named: a CA per runtime (Rust's
`webpki-roots`, Java's keystore and any pinned bundle break, and fail
only for bound hosts), and host-side plaintext. It is listed so the
template schema leaves room for it (`relay.mode: transparent`). It is
not proposed now.

### 6.5 MCP servers

A template may declare an MCP server:

```yaml
mcp:
  name: linear
  url: "{relay}/mcp"          # or /mcp/readonly for the read preset
```

When a grant is live, guestd adds the server to
`/home/dev/.repose/mcp/integrations.json` and runs `repose-mcp sync`,
which already renders `laptop.json` into each agent's config. The server
URL is the relay, so no `${NAME}` is needed and no secret is set. When
the grant ends, guestd removes it and syncs again. A laptop MCP server
with the same name wins, so a user who carries their own keeps it. The
`read` preset can point at Linear's read-only endpoint, so the provider
enforces the scope too.

An agent session that started before the grant does not see a new MCP
server until it restarts. Claude Code re-reads `.mcp.json` only at start
(*unverified* for the current version). The `repose-ask --grant` exit
message says so.

## 7. Templates

### 7.1 The split: the engine is code, a template is data

The engine is Go in `internal/integrations`. It implements the auth
kinds, the relay rules, minting, federation, OAuth refresh, the check
runner and the template parser. A template cannot run code: no scripts,
no hooks, no expressions beyond `{input}` placeholders the engine fills.
A template author picks from what the engine already does safely. A
review is then a read of one YAML file against a checklist (section
7.6), the same as a Coolify one-click template.

### 7.2 Auth kinds

| Kind | Connect | Delivery | Examples |
|---|---|---|---|
| `static_token` | Paste a token; optional expiry date | relay or secret | Coolify, Kaneo, Resend, Stripe |
| `oauth2` | Redirect to the provider; the api stores access and refresh tokens and refreshes them | relay or secret | Linear, GitHub (user tokens), Google |
| `mint` | Paste a parent token or install an app; the api creates children | secret, or relay with the child | GitHub App, Grafana, Cloudflare |
| `federation` | The owner trusts `id.repose.run` in their cloud and pastes role ids | token file in the machine | AWS, Azure, GCP |

### 7.3 Schema

A sketch. The full schema goes in `docs/interfaces/integration-template.md`
with a JSON Schema file the CI validates against.

```yaml
schema: 1
id: coolify                     # [a-z][a-z0-9-]{1,31}, unique in the registry
version: 3                      # bump on any change; integrations pin a version
name: Coolify
docs: https://coolify.io/docs/core/security/credentials/api-tokens
auth: static_token

inputs:                          # asked at connect time
  host:
    label: Your Coolify URL
    type: https_origin           # engine checks: https, no path, no userinfo, public address
  token:
    label: API token
    type: secret
    help: Keys & Tokens, API Tokens. Choose the permissions of the widest preset you will grant.
  expires_at:
    label: Token expiry
    type: date
    optional: true

relay:
  mode: explicit
  upstream: "{host}"
  inject:
    header: Authorization
    format: "Bearer {token}"
  strip_request_headers: [Authorization, Cookie]
  env:                           # set in the machine while a grant lives
    COOLIFY_URL: "{relay}"
    COOLIFY_TOKEN: repose-relay

expiry:
  detect: [status_401]

check:
  request: "GET /api/v1/teams/current"
  expect_status: 200

default_scope: read
max_ttl: 24h
scopes:
  read:
    rules:
      - "GET /api/v1/**"
  deploy:
    rules:
      - "GET /api/v1/**"
      - "GET /api/v1/deploy"
      - "POST /api/v1/applications/*/restart"
  admin:
    confirm: true
    rules:
      - "* /api/v1/**"
```

The Coolify paths above are illustrative; the real template takes them
from Coolify's API reference.

### 7.4 Relay rules

The engine supports four rule types. A request must match at least one
rule of the granted preset, or the relay answers 403.

1. **Method and path globs**: `GET /api/v1/**`. Enough for REST.
2. **GraphQL operations**: `graphql: {path: /graphql, allow: [query]}`
   or `allow_operations: [Issues, CreateComment]`. The relay parses the
   request body as GraphQL and refuses a `mutation` when only `query` is
   allowed. Linear and GitHub's v4 API need this, because everything is
   one `POST /graphql`.
3. **MCP tools**: `mcp: {path: /mcp, allow_tools: [list_issues, get_issue]}`.
   An MCP request is JSON-RPC. The relay reads `method` and, for
   `tools/call`, `params.name`, and refuses a tool not in the list. It
   also filters the `tools/list` reply so the agent does not see tools
   it cannot call. This gives per-tool scopes for any MCP server, even
   one whose provider has no scopes.
4. **Body size and rate**: `max_body`, `rate: 10/s`. Defaults apply.

A body the relay cannot parse for a GraphQL or MCP rule is refused, not
passed.

### 7.5 Where templates live

- **Official**: `integrations/templates/<id>.yaml` in this repository.
  CI validates each against the schema, runs a lint for the review
  checklist, and runs `check` against a sandbox account where one exists.
  The api embeds them at build time.
- **Private**: an account can write its own template on the dashboard
  (a YAML editor, plus a form for the simple `static_token` case). It is
  stored in Postgres, visible only to that account, and validated by the
  same parser. This is how the owner's "users can create their own"
  works on day one, with no review.
- **Shared**: a user opens a pull request that adds a file to
  `integrations/templates/`. A "Request an integration" button on the
  dashboard opens a GitHub issue with a form.

An integration pins the template version it was connected with. A new
version that changes `relay.upstream`, `inject`, `scopes` or `auth`
does not apply until the owner accepts it on the dashboard. A version
that changes only text applies at once.

### 7.6 Review checklist (what a template reviewer reads)

- `upstream` is the provider's own domain, or `{host}` for a
  self-hosted provider. For `oauth2`, the token URL and the API host
  share the provider's registered domain.
- `inject` puts the credential in a header, never in a query string.
- Every preset's rules match its name. `read` has no write method or
  mutation. Anything with delete or admin rights has `confirm: true`.
- `max_ttl` is no longer than the provider's own guidance.
- `check` is a read-only request.
- `env` names do not shadow names the guest relies on (`PATH`,
  `BASH_ENV`, `HOME`, anything starting with `REPOSE_`). The parser
  refuses those.
- The file has a `docs` URL to the provider's page on tokens.

### 7.7 Open source

The engine (`internal/integrations`), the schema, the templates and the
guest relay are written as a package with no imports from the rest of
the api beyond small interfaces (a store, a secret box, a clock). That
lets the owner publish it later as its own repository. The licence is
the owner's choice (section 16).

## 8. Security

### 8.1 The fourth home

| Home | What it holds | For how long |
|---|---|---|
| Postgres (existing) | Parent credential as ciphertext under the account data key, bound to user, integration name and template id | Until disconnected |
| Guest tmpfs (existing) | Minted children, federation JWTs, placeholders | While the grant lives |
| **Edge memory (new)** | The decrypted parent credential (relay) or child token (relay with mint), per integration with a live grant | From the first relayed request of a grant to the end of the last live grant for that integration, at most `max_ttl` |

The edge gets a credential from the api when a grant becomes live,
over the same internal channel that serves `/internal/webhook-routes`,
and drops it when the last grant ends. It never writes it to disk,
swap (the unit sets `MemoryDenyWriteExecute` and `MemorySwapMax=0`) or
a log. A crash loses it, and the next request fetches it again.

The decision entry for this (section 15) is the owner's sign-off, as
I-653 was for the parked image.

### 8.2 Threats and answers

| Threat | Answer |
|---|---|
| Malware in a guest asks for a grant | The owner sees the request with api-supplied facts; one tap only for a grant like one already given; deny cools down for 10 min; malware cannot widen, add hosts or auto-approve |
| Malware uses a live grant | Limited to the scope, the host and the time. It cannot read a relayed token |
| Malware tries to send a token to its own server | The relay only adds a token for the integration's `upstream`. A request for any other host gets no token |
| A guest sends a request with another guest's grant id | The edge checks the grant's project against the source IP's project |
| A malicious template sends the token to the wrong host | Official templates are reviewed; a private one is the account's own; the connect screen shows the host, and a version that changes it needs the owner's accept |
| A malicious template widens a preset after connect | Pinned versions; scope changes need the owner's accept |
| The provider token leaks from Postgres | Ciphertext under a Key Vault-wrapped key, as for named secrets |
| The edge is compromised | It holds only credentials with live grants, in memory. Federation and minting keep AWS, Azure and GitHub parents off the edge |
| The approval link is forwarded | Signed reply links are per question, single use and expire, as today |
| A relayed reply carries something harmful to the agent (prompt injection in a Linear issue) | Out of scope; same as without repose |
| Approval fatigue | One-tap only for repeats; first grants and wider scopes need a confirm; the owner's auto-approve is opt-in and per scope |

### 8.3 Connecting a custom integration

Connecting is where a credential gets a destination, so it is the one
step with friction. The owner types the host, pastes the token, and the
engine runs `check` before it saves. The screen shows the host in full,
the env names the machine will get, and each preset's rules. A private
template's `upstream` must be `https`, a public address, and not a repose
domain.

### 8.4 Anthropic

repose never relays `api.anthropic.com`, because the Claude login share
(I-278) talks to it and repose never opens that login. The engine
refuses a template whose `upstream` is an Anthropic host.

## 9. Names, CLI and dashboard

### 9.1 Names

"Integration" for the feature and the noun. "Grant" for a time-boxed
permission. "Template" for the provider file. "Relay", "mint" and
"federate" for the three deliveries. No synonym: not "connection",
"connector", "app" or "plugin". "Connector" is what Claude calls MCP
servers, and "plugin" is taken by agents.

### 9.2 Guest commands

- `repose-ask --grant` (section 5.1). A flag on the existing ask, the
  same as `--secret`.
- `repose-integrations list` (section 5.1). It is `repose-hook` under
  another name, like `repose-mcp`.

### 9.3 Laptop CLI

A new noun, because none of the existing ones fits: `secrets` holds
values, and an integration is not a value. The owner prefers new
commands under existing nouns (memory `cli-surface-taste`), so this is a
placement to approve before it is built.

```
repose integrations                       # list: name, template, status, projects, live grants
repose integrations add TEMPLATE [NAME]   # opens the browser for OAuth; asks inputs at a prompt otherwise
repose integrations rm NAME
repose integrations grant PROJECT NAME [--scope S] [--for D]   # the owner grants without being asked
repose integrations revoke PROJECT NAME
repose integrations templates             # official and private templates
```

Secrets typed at a prompt follow I-620 and I-365: masked, never from
arguments.

### 9.4 Dashboard

- **Settings, Integrations** (a tab next to Secrets): connected
  integrations as rows with logo, name, status, projects and live
  grants; "Connect" opens a gallery of templates with a search, plus
  "Custom" and "Request one".
- **The connect page**: inputs, the OAuth button or token field, the
  host and the preset rules shown in full, the check result.
- **The project's Secrets tab** lists the project's grants under a
  heading of their own, with revoke.
- **The question card** for a grant shows the section 5.6 facts and the
  three buttons.
- **An integration's page**: auto-approve settings, caps, the project
  list, the grant history (from the audit log), and "Replace token".

Visual work follows the Herakraft revamp and the dashboard taste notes;
a design pass is part of stage 1.

## 10. Data model

New tables, all keyed to the account. Migration numbers are assigned
at build time.

```
integrations(
  id uuid pk, user_id, name text,            -- unique (user_id, name)
  template_id text, template_version int, template_private_id uuid null,
  inputs jsonb,                               -- non-secret inputs: host, role ARNs
  credential bytea null,                      -- ciphertext; aad "repose-integration-v1"|user|name|template_id
  credential_expires_at timestamptz null,
  oauth_refresh bytea null,                   -- ciphertext, same aad with "|refresh"
  status text,                                -- ok | expiring | broken
  auto_approve jsonb, caps jsonb,
  created_at, updated_at)

integration_projects(integration_id, project_id)   -- empty means every project

integration_templates_private(
  id uuid pk, user_id, template_id text, version int, body text, created_at)

grants(
  id uuid pk, integration_id, project_id, scope text,
  starts_at, expires_at, revoked_at null, end_reason text null,
  question_id uuid null, granted_via text,    -- dashboard | link | cli | auto
  minted_handle text null)                    -- provider's id for a minted child, never the value
```

`questions` gains `grant_integration`, `grant_scope` and
`grant_duration`; `kind` gains `grant`. `audit_log` gains actions
`integration_connect`, `integration_disconnect`,
`integration_credential_replace`, `grant_issue`, `grant_revoke`,
`grant_expire`, with ids only in `detail`.

## 11. Interfaces that change

| Interface | Change |
|---|---|
| `docs/interfaces/api.md` | `/v1/me/integrations` CRUD, `/v1/me/integrations/{name}/oauth/callback`, `/v1/projects/{id}/grants`, grant answers on the existing question answer route |
| `docs/interfaces/grpc-hostd.md` | `UpdateGrants` op carrying the project's live grant set (integration, scope, expiry, env, MCP entry); grants also ride `StartGuest` |
| `docs/interfaces/vsock-guestd.md` | `WriteGrants`; `Question` gains `grant_*` fields; guestd's relay listener on 127.0.0.1:9228 |
| `docs/interfaces/guest-conventions.md` | `/run/repose/integrations.json`, `/home/dev/.repose/mcp/integrations.json`, the relay port, `repose-integrations`, `repose-ask --grant` and exit 8 |
| `docs/interfaces/ssh-gateway.md` (the gateway's edge services) | `/relay/` on 8443, `GET /internal/grants` and `POST /internal/grants/{id}/credential` |
| `docs/interfaces/integration-template.md` (new) | The template schema |
| `docs/interfaces/db-schema.md` | The tables in section 10 |
| `docs/interfaces/README.md` | Enums: grant end reasons, integration status, delivery kinds |

Each interface change ships with the code that changes it, and the old
shape stays accepted for one release (CLAUDE.md). An old hostd that
drops `grant_*` fields must fail closed, as I-929's `question_secret`
feature flag does: hostd names `question_grant` in its Ping, and guestd
refuses `--grant` until it sees it.

## 12. Observability and logs

Relay log fields: grant id, integration id, template id, project id,
status, rule outcome (`allowed`, `no_grant`, `scope_denied`,
`upstream_error`), duration and byte counts. Never the path, query,
headers or body, because they carry what the tenant's agent sent
(`docs/ops/OBSERVABILITY.md`). Metrics: relayed requests and bytes by
template and outcome, grant asks and answers by kind, OAuth refresh
failures, mint and sweep counts. Relayed bytes leaving the edge count
toward the project's egress, as webhook reply bytes do
(`webhook_egress`).

The privacy policy gains a line: the relay sees the requests an agent
makes through an integration, in memory, and keeps none of them.

## 13. The starter set

| Template | Kind | Delivery | Scopes | Notes |
|---|---|---|---|---|
| AWS | `federation` | token file | One IAM role per preset (`read`, `deploy`, `admin`), written by the owner or by the linked CloudFormation template | No AWS secret stored. Session 1h. `AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_ROLE_ARN` |
| Azure | `federation` | token file | One app registration or role assignment per preset | `AZURE_FEDERATED_TOKEN_FILE`, `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`. The 20-credential limit needs a spike |
| GitHub | `mint` (GitHub App) | secret, `hosts.yml` for `gh` | Per grant: `repositories` and a `permissions` object, from the preset | Installation tokens last 1 hour and can name repositories and permissions. The 2026-09-27 proposal's per-run token is this template. An `oauth2` variant for acting as the user can come later |
| Grafana | `mint` | relay with the child | Service account role per preset | `POST /api/serviceaccounts/:id/tokens` with `secondsToLive`; Grafana Cloud's and self-hosted APIs both take it. Revoke by token id |
| Linear | `oauth2` | relay, MCP | `read` → OAuth `read` scope and `/mcp/readonly`; `write` → `read,write` and MCP tool list | Linear moved to 24-hour access tokens with rotating refresh tokens in October 2025 (secondary sources). The api must store each new refresh token atomically, because the old one stops working at once |
| Kaneo | `static_token` | relay | Path rules | Self-hosted, so the host is an input. API keys are Bearer tokens made under account settings. Kaneo also has a device flow for CLIs (`kaneo-cli`, `kaneo-mcp`), which a later `oauth2`-style template could use |

These six cover all four auth kinds, REST and GraphQL rules, MCP, a
self-hosted host input and a public cloud. They test the engine before
anyone else writes a template.

Sources: [Linear MCP](https://linear.app/docs/mcp),
[Linear OAuth](https://linear.app/developers/oauth-2-0-authentication),
[Nango on Linear refresh tokens](https://nango.dev/blog/linear-oauth-refresh-token-invalid-grant),
[Grafana service account API](https://grafana.com/docs/grafana/latest/developers/http_api/serviceaccount/),
[Kaneo authentication](https://tangled.org/danieldaum.net/kaneo/blob/main/apps/docs/api-reference/authentication.mdx),
[Coolify API tokens](https://coolify.io/docs/core/security/credentials/api-tokens).

## 14. Staged plan

Estimates are *estimates*, in engineering days.

| Stage | What ships | Days |
|---|---|---|
| 1 | Engine with `static_token`; private templates via the custom form; `integrations` and `grants` tables; `repose-ask --grant` with one-tap repeats and dashboard confirms; guestd relay listener; edge `/relay/` with path rules; Settings, Integrations; `repose integrations`; Coolify and Kaneo templates; docs | 8 to 10 |
| 2 | `oauth2` with refresh and rotation; GraphQL and MCP rule types; MCP rendering through `repose-mcp sync`; Linear template | 6 to 8 |
| 3 | `mint` with revoke and sweep; GitHub App and Grafana templates; the `gh` `hosts.yml` path | 5 to 7 |
| 4 | OIDC issuer on `id.repose.run` with a Key Vault key; `federation`; AWS and Azure templates; the CloudFormation link | 6 to 8 |
| 5 | Official template registry in the repo, schema in CI, template versions and accept flow, "Request an integration", auto-approve | 4 to 5 |
| 6 (on request) | Transparent mode on the host; grants bound to `agent_id` once the agent-model work lands | 6 to 8 |

Stage 1 alone answers the owner's Coolify case: connect once, ask, tap,
deploy for 5 hours, ask again.

## 15. DECISIONS entries it needs

Ids are reserved with `ops/dev/release-queue id` when a stage is built.
None are reserved now.

1. Integrations: an account-level provider connection, with grants per
   project and three deliveries. Amends R2-8 and I-697 in that it adds a
   second kind of account-level credential.
2. **The edge holds a relayed credential in memory while a grant lives.**
   The owner's sign-off on a fourth home, written as an exception the
   way I-653 is. Amends CLAUDE.md's "Secrets have three homes".
3. Reopens the placeholder proxy turned down in the 2026-09-27 proposal
   and in I-929's *Rejected*, as an explicit relay with no CA.
4. `repose-ask --grant`, the `grant` question kind, exit 8 and the
   `question_grant` Ping feature.
5. One-tap approval for repeat grants by signed reply links; dashboard
   confirm for first grants and wider scopes. Amends I-929's "no
   notification can answer" only for grants, which carry no value.
6. The guest relay on 127.0.0.1:9228 and the edge `/relay/` path on the
   hook-ingest address. Amends the edge's port and path list.
7. The template format, its schema and the rule that a template carries
   no code.
8. repose as an OIDC issuer on `id.repose.run`, signing with a Key
   Vault key.
9. Minted children: named `repose-<grant id>`, recorded by handle,
   never stored, revoked at the grant's end, swept daily.
10. Relay logs and egress metering, and the privacy policy line.
11. The `repose integrations` noun and the Settings, Integrations tab.

## 16. Decisions for the owner

1. **The fourth home.** Sign off the edge holding relayed credentials in
   memory while a grant lives, or keep stage 1 to `mint` and `federation`
   only (which covers GitHub, Grafana, AWS and Azure, and leaves Coolify
   and Kaneo as named secrets).
2. **Edge or hostd for the relay.** The proposal says edge for fleet
   hosts, and hostd only on a host the customer owns if its guests
   cannot reach the edge.
3. **One-tap rule.** Repeats one tap, firsts on the dashboard, as in
   5.2. Or one tap for everything except `confirm: true` presets, which
   is faster and weaker.
4. **Grants end at stop.** Proposed yes. The alternative keeps a grant
   across a stop until its time ends, which saves a tap after each
   start.
5. **Account-wide by default.** Proposed yes, with a project list to
   narrow it.
6. **The CLI noun.** `repose integrations`, or fold it under an
   existing noun.
7. **Open source.** When, and under what licence, the engine and
   templates are published.
8. **Starter order.** The proposal builds Coolify and Kaneo first
   (stage 1), because they need the least. If Linear MCP matters most,
   stages 1 and 2 can merge.

## 17. Tests and closing evidence

Each stage closes with evidence, not "tests pass":

- **Relay**: on a real guest, `curl http://127.0.0.1:9228/coolify/api/v1/teams/current`
  returns 200 with a grant and 403 `no_grant` without one; the edge log
  line for each carries no path; `grep -r` of the guest's disk and
  tmpfs finds no Coolify token.
- **Scope**: a `read` grant answers 403 `scope_denied` to a `POST`; a
  Linear `read` grant refuses a GraphQL mutation; an MCP grant hides a
  tool from `tools/list` and refuses its `tools/call`.
- **Expiry**: a 2-minute grant stops working at 2 minutes, measured;
  a machine stop ends it; the audit log has `grant_issue` and
  `grant_expire`.
- **Approval**: an ntfy button grants a repeat; a first grant has no
  ntfy button and needs the dashboard; a deny blocks a new ask for 10
  minutes.
- **Old hostd**: a guest under a hostd without `question_grant` gets
  exit 1 from `--grant` and no plain question.
- **Mint**: a GitHub token minted for a grant is revoked at its end
  (GitHub answers 401 to it); the sweep deletes an orphan made by
  killing the api between mint and push.
- **Federation**: `aws sts get-caller-identity` on a guest with a grant
  shows the role; after the grant ends and the session ends, it fails;
  Postgres holds no AWS secret (`select` over `integrations`).
- **Logs**: the CHECKLIST greps for logged paths and values, run over
  the relay and the engine.
- **Docs**: `apps/web/src/content/docs/integrations.md`, `cli.md` and the
  agent guide in the same commit; `internal/cli/docs_test.go` passes.

## 18. Risks

- **Approval fatigue.** One-tap repeats are exactly what a tired owner
  taps without reading. The cooldown and the confirm on firsts limit it;
  they do not remove it.
- **The edge becomes a bottleneck.** All relayed traffic for the fleet
  goes through one VM. A Linear MCP session is small; an agent pulling
  Grafana dashboards in a loop is not. The per-grant rate limit and the
  egress meter show it early.
- **Templates drift from providers.** A provider changes a path and the
  `deploy` preset stops working. The scheduled `check` and the
  `scope_denied` count by template catch it.
- **OAuth app approval.** Each named `oauth2` provider needs a repose
  OAuth app registered with it, and some (Google) review apps before
  public use.
- **Explicit relay coverage.** Tools with hard-coded hosts and no base
  URL option do not work through it. How many of the tools agents use
  are like that is unmeasured; a survey of the 30 most common agent
  tools is a cheap first step in stage 1.
- **Federation reachability.** AWS and Azure must fetch the JWKS from
  `id.repose.run`. If it is down, new STS sessions fail for every
  federated grant.

## Sources

Read on 2026-11-26.

- HashiCorp, [Dynamic secrets management](https://developer.hashicorp.com/validated-designs/vault/administration-guide/dynamic-secrets-management)
- Infisical, [Dynamic secrets](https://infisical.com/docs/documentation/platform/secrets-mgmt/concepts/dynamic-secrets), [Access requests](https://infisical.com/docs/documentation/platform/access-controls/access-requests), [Agent proxy](https://infisical.com/docs/documentation/platform/agent-proxy/overview.md)
- Doppler, [Dynamic secrets](https://docs.doppler.com/docs/dynamic-secrets), [Service tokens](https://docs.doppler.com/docs/service-tokens)
- 1Password, [Closing the credential risk gap for browser-use AI agents](https://1password.com/blog/closing-the-credential-risk-gap-for-browser-use-ai-agents); SiliconANGLE, [1Password launches Privileged Access](https://siliconangle.com/2026/07/28/1password-launches-privileged-access-cut-standing-access-ai-agents/)
- Permit.io, [HTTP egress proxy credentials](https://docs.permit.io/permit-mcp-gateway/http-egress-proxy/credentials)
- Coolify, [API tokens](https://coolify.io/docs/core/security/credentials/api-tokens)
- Linear, [MCP server](https://linear.app/docs/mcp), [OAuth 2.0 authentication](https://linear.app/developers/oauth-2-0-authentication); Nango, [Linear OAuth refresh token invalid_grant](https://nango.dev/blog/linear-oauth-refresh-token-invalid-grant)
- Grafana, [Service account HTTP API](https://grafana.com/docs/grafana/latest/developers/http_api/serviceaccount/)
- Kaneo, [API authentication](https://tangled.org/danieldaum.net/kaneo/blob/main/apps/docs/api-reference/authentication.mdx) (a fork of `usekaneo/kaneo`; check against upstream)
- In this repository: `docs/proposals/2026-09-27-credentials-convenience-first.md`, `docs/DECISIONS.md` I-929, `docs/features/secrets.md`, `docs/features/webhooks.md`, `docs/proposals/2026-11-26-agent-model.md`, `docs/proposals/2026-11-26-design2.md`
