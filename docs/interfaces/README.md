# Interfaces

Contracts between workstreams. A consumer codes against the doc, not against
the producer's current code. A producer that changes a contract updates the
doc in the same commit and keeps the old shape accepted for one release.

| Doc | Between | Fake for consumers |
|---|---|---|
| [grpc-hostd.md](grpc-hostd.md) | api ⇄ hostd | `internal/fakes/hostd` (in-memory host that "runs" guests) |
| [vsock-guestd.md](vsock-guestd.md) | hostd ⇄ guestd | `internal/fakes/guestd` (unix-socket stand-in for vsock) |
| [api.md](api.md) | cli, dashboard, gateway ⇄ api | `internal/fakes/api` (httptest server with canned projects) |
| [db-schema.md](db-schema.md) | api, billing ⇄ Postgres | none; use a real Postgres in tests |
| [ssh-gateway.md](ssh-gateway.md) | cli ⇄ gateway ⇄ guest sshd; api CA | a test CA in `internal/ca/testca` |
| [cli-config.md](cli-config.md) | cli ⇄ user's filesystem | |
| [guest-conventions.md](guest-conventions.md) | guest image ⇄ cli, guestd, hooks | |
| [host-conventions.md](host-conventions.md) | host image ⇄ hostd, infra | |
| [nix-build-contract.md](nix-build-contract.md) | hostd ⇄ the platform flake in `nix/` | |

Naming rules across all of them: ids are UUIDv7 strings; timestamps are RFC
3339 UTC; sizes are bytes as integers; durations are seconds as integers;
enums are lower_snake strings; the size class enum is `small|large|xl`; the
guest state enum is `creating|building|starting|running|stopping|stopped|
restoring|destroying|destroyed|error`. `resting` is **not** an eleventh
state (DECISIONS I-818): it is a marker on a guest that is still
`running`, carried beside the state on the wire
(`GuestStatus.resting_at`, `GuestSample.resting`), on the row
(`projects.resting_since`) and in hostd's bbolt record, because the
guest's vCPUs are stopped but its memory, its processes and the minute it
bills are all still there. The api's error codes are listed in
[api.md](api.md) (the error envelope at the top); hostd's are in
[grpc-hostd.md](grpc-hostd.md).
