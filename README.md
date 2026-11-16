# repose

Persistent remote environments for coding agents. `cd` into a project, run
`repose run`, and an agent keeps working in a NixOS microVM after your laptop
closes. Multi-tenant, billed by the hour with a monthly cap, hosted at
`repose.run`.

Everything about what this is and how it is built lives in [`docs/`](docs/README.md).
Start there. Contributors and agents also read [`CLAUDE.md`](CLAUDE.md)
(`AGENTS.md` is the same file).

## Layout

```
cmd/         api, hostd, guestd, gateway, repose (CLI), repose-admin, repose-hook;
             hostdev (M1 stand-in), fakeapi and fake-logto (test fakes)
internal/    shared Go; fakes for every interface; generated protobuf under gen/
proto/       gRPC and vsock contracts (docs/interfaces/ is the prose)
nix/         one flake: hosts, edge, guest base, agent overlay, dev shell
infra/       OpenTofu for the Azure pieces and DNS
apps/web/    SvelteKit dashboard
packages/    shared TypeScript config and UI packages
docs/        the spec
```

## Developing

```
nix develop ./nix     # Go, buf, opentofu, az, just, nixos-anywhere ...
just build
just test
just proto            # regenerate internal/gen from proto/
```

Do not install anything from `nix/` on this machine; hosts and guests are
remote (see `CLAUDE.md`).
