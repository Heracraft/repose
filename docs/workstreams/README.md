# Workstreams

The design split into chunks that can be built in parallel by separate agents
or sequential sessions. Each workstream doc is self-contained: what it builds,
what it must not build, the interfaces it owns and consumes, the failure modes
it must handle, and a checklist that is its definition of done. Read
`../DESIGN.md` first, then the workstream, then every interface doc it names.

## The list

| # | Workstream | Milestone | Owns interfaces | Consumes interfaces |
|---|---|---|---|---|
| 00 | [benchmark](00-benchmark.md) | M0 | | |
| 01 | [host-nixos](01-host-nixos.md) | M1 | `host-conventions` | |
| 02 | [guest-base](02-guest-base.md) | M1 | `guest-conventions` | `vsock-guestd` |
| 03 | [hostd](03-hostd.md) | M1 | `grpc-hostd` (server side is api; message shapes owned here), `vsock-guestd` | `host-conventions`, `guest-conventions` |
| 04 | [guestd](04-guestd.md) | M1 | `vsock-guestd` (jointly with 03) | `guest-conventions` |
| 05 | [control-plane-api](05-control-plane-api.md) | M2, M3 | `api`, `db-schema`, `ssh-gateway` (CA side) | `grpc-hostd` |
| 06 | [gateway-edge](06-gateway-edge.md) | M2 | `ssh-gateway` (relay side) | `api` (internal routes) |
| 07 | [cli](07-cli.md) | M2 | `cli-config` | `api`, `ssh-gateway`, `guest-conventions` |
| 08 | [dashboard](08-dashboard.md) | M3 | | `api` |
| 09 | [billing](09-billing.md) | M4 | `db-schema` (usage, invoices tables) | `api`, `grpc-hostd` (samples) |
| 10 | [observability](10-observability.md) | M1 onward | metric and log naming | everything |
| 11 | [infra-opentofu](11-infra-opentofu.md) | M0 to M3 | | `host-conventions` |
| 12 | [nix-config-pipeline](12-nix-config-pipeline.md) | M1 | fragment contract, build limits | `grpc-hostd` (Apply), `vsock-guestd` (Switch) |
| 13 | [notifications](13-notifications.md) | M3 | event shapes | `api`, `vsock-guestd` (Event) |
| 14 | [security](14-security.md) | all | `../SECURITY.md` | everything |
| 15 | [dev-ergonomics](15-dev-ergonomics.md) | post-M5 | (changes `guest-conventions`, `cli-config`, `host-conventions` per I-195..I-205) | `api`, `ssh-gateway`, `guest-conventions` |
| 16 | [multiplexer](16-multiplexer.md) | post-M5 | (changes `guest-conventions`, `vsock-guestd`, `grpc-hostd`, `api`, `db-schema`, `cli-config` per I-501..I-511) | the same |

## Dependency graph

```
00-benchmark ─────────────────────────────┐ (informs host SKU; blocks nothing else)
01-host-nixos ──┬─▶ 03-hostd ──┬─▶ 05-api ──▶ 07-cli ──▶ 08-dashboard
02-guest-base ──┤              │            └─▶ 06-gateway-edge
04-guestd ──────┘              ├─▶ 09-billing
12-nix-config-pipeline ────────┘
11-infra-opentofu ───────────── (hosts, edge, blob, kv, coolify VM; needed to *deploy* 01, 06, 05)
10-observability ────────────── (starts with 01/03, continues through all)
13-notifications ────────────── (needs 04 Event + 05 ingest; UI in 08)
14-security ─────────────────── (review of all; policy text before M5)
```

What can start on day one with nothing else built: 00, 01, 02, 04, 12 (against
the vsock and gRPC contracts as written), 05 (against the gRPC contract, with a
fake hostd), 06 (against a fake api route endpoint), 07 (against a fake api),
11, 10 (naming and dashboards), 14 (threat model), 13 (event shapes and
delivery). That is every workstream. The contracts in `../interfaces/` are what
make that possible; treat them as law until a `DECISIONS.md` entry changes
them.

## Claiming and coordinating

- Work on `main`. Do not create a branch without asking (repo rule).
- Since 2026-09-23 most work runs as conductor "rounds": one conductor
  session splits the work across worker agents in their own worktrees,
  merges, deploys and tests live (`../ops/ORCHESTRATION.md`). STATUS labels
  such as "16-guest-tooling" or "round 3" name that work; they have no
  workstream doc here.
- Before starting, add a line to `STATUS.md` in this directory: workstream,
  who (session id or agent name), date, what you intend to finish. Update it
  when you stop, with what is done and what is not. Sequential sessions read
  this first.
- A workstream that needs a change in an interface it consumes writes the
  proposed change in `DECISIONS.md` ("Made during implementation") and, if the
  owning workstream is not active, makes the change itself in both the
  interface doc and the code, keeping the old shape accepted for one release.
- Fakes: each interface doc names the fake that lets its consumer be built
  without the producer (`internal/fakes/`). The consumer workstream writes the
  fake if it does not exist yet.

## Doc shape

Every workstream doc has these sections, in this order, so agents find the
same thing in the same place:

1. Goal (two sentences)
2. Scope: builds
3. Scope: does not build (and which workstream does)
4. Interfaces owned / consumed
5. Design detail (as long as it needs to be)
6. Failure modes and the exact user- or operator-visible outcome of each
7. Testing: how it is verified, including what needs a real host
8. Rollback
9. Checklist (definition of done, each item with its evidence)
