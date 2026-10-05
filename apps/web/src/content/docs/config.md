---
title: Installing software
description: Add packages and services to a project with repose config, so they're there on every rebuild, from the CLI, the dashboard or Nix.
section: Using repose
order: 13
---

There are two ways to install software on a project's machine:

- **On the machine**, with `nix profile add`, `npm i -g`, `go install` and the like. It takes seconds and lasts as long as the machine's disk. See [The machine](/docs/machine#installing-more).
- **In the project's configuration**, with `repose config`. The machine is rebuilt with it, so it's there after rebuilds, platform updates and a restore onto another server, and services such as PostgreSQL are set up and started.

## Add a package

```
$ repose config add postgresql air
Added postgresql and air to todo-app. Building revision 4f1c2a9e ...
✓ Evaluated your config  7.9s
✓ Fetched 38 paths (112.4 MiB)  21s
✓ Built 14 derivations  12s
✓ Switched the machine  3.1s
Applied revision 4f1c2a9e in 45s.
```

Any package from nixpkgs works; search names at [search.nixos.org](https://search.nixos.org/packages). Nested names work too, such as `python312Packages.black`. A few names are menu entries that set up more than a package: `postgresql`, `redis` and the other databases also start the service.

The build steps are: waiting for a build slot (only when the server is busy with other builds), evaluating your configuration, fetching what's already built from the package cache, building the rest, and switching the running machine to the result. `-v` also prints Nix's own output. Without a terminal, each step is one line and Nix's output follows it.

The build usually takes under a minute. It's switched into the running machine without a restart, so your agents keep running and new shells see the new packages. If the build fails, nothing changes:

```
$ repose config add gcc-typo
Added gcc-typo to todo-app. Building revision 7d03b1c5 ...
config error: nixpkgs has no package "gcc-typo";
search https://search.nixos.org/packages
Nothing changed in todo-app; the previous revision is still active.
```

Remove with:

```
repose config remove air
```

If you press Ctrl-C while it builds, only the CLI stops. The build carries on and is applied when it finishes; `repose config show --revisions` shows when it has.

A change to the kernel is built but not switched in, because that needs a restart. The machine starts on it the next time it starts: `repose stop && repose start`.

## The menu

The dashboard's project **Config** page has the same list as a menu: tick an entry, choose a version where there's a choice, **Apply**. Packages added with `repose config add` show under **Extra packages**, each with **Remove**. While a build runs, the page shows its log.

| Group     | Entries                                                                                           |
| --------- | ------------------------------------------------------------------------------------------------- |
| Runtimes  | Bun, Deno, Node.js (20 or 22), Python (3.11 or 3.13), Go tools, Zig, Elixir, Ruby, Java, .NET SDK |
| Databases | PostgreSQL, Redis, MySQL (MariaDB), Memcached, RabbitMQ, Meilisearch, NATS                        |
| Tools     | AWS CLI, OpenTofu, Kubernetes tools, Shell extras                                                 |
| Deploy    | Wrangler, Supabase CLI, flyctl, Vercel CLI, portless, cloudflared                                 |

Databases listen on localhost only. PostgreSQL has a `dev` superuser and a `dev` database with no password, so `psql` and `postgres://localhost/dev` work.

## Write it in Nix

Under the menu is a Nix file, a [home-manager](https://nix-community.github.io/home-manager/) module for the user `dev`. You only need it for things the menu can't express, such as dotfiles or environment variables.

```
repose config show            # print it
repose config edit            # edit in $EDITOR, apply on save
repose config apply ./repose.nix
repose config apply           # ./repose.nix, or else apply it again
```

### repose.nix in your repository

Commit the file as `repose.nix` at the root of your repository and you don't need to apply it yourself. `repose run` and `repose sync` send it whenever it changed since they last did, including the run that creates the machine, and the build goes on while you work:

```text
Applying repose.nix (revision 4f1c2a9e) in the background.
```

`repose config show --revisions` shows when it's applied. An unchanged file costs nothing and prints nothing. If the build fails, the machine keeps its configuration, and later runs name the error instead of building the same file again; fix the file, or run `repose config apply` to retry it as it is. Only the project's own checkout counts: a run from another repository never replaces the machine's configuration. With `repose.nix` in the repository, the file is the configuration: a change made from the menu or the dashboard is replaced the next time it's sent.

`repose config apply` with no file and no `./repose.nix` switches the running machine to the project's configuration again: the active revision, or a newer one that built but wasn't applied because its switch failed. Use it when the machine seems to be missing something the configuration has.

Or use the **Nix** tab on the dashboard's Config page (**Edit as Nix** from the menu).

An example:

```nix
{ pkgs, ... }:
{
  home.packages = with pkgs; [ deno shellcheck hyperfine ];

  home.sessionVariables.DENO_NO_UPDATE_CHECK = "1";

  xdg.configFile."htop/htoprc".text = ''
    tree_view=1
  '';

  repose.system = [
    {
      services.redis.servers.dev = {
        enable = true;
        port = 6379;
        bind = "127.0.0.1";
      };
    }
  ];
}
```

The file is a home-manager module, not a flake. A `flake.nix` given to `repose config apply` fails with `this file is a Nix flake`. A project's `flake.nix` does something else: it gives agents a dev shell in the checkout ([Projects with a flake.nix](/docs/machine#projects-with-a-flake-nix)).

Once you edit the Nix by hand, the menu and `repose config add` are off for that project, because they can't read arbitrary Nix. Applying from the menu later replaces your file.

What the file can't do: set NixOS system options other than the database services under `repose.system`, download without a hash, download from a private or local address (a fetch during the build reaches the public internet only), read files outside itself, or choose its own nixpkgs version. Don't put secrets in it; use [Secrets](/docs/secrets). A config that holds a secret's value, whole or any one line of it, is refused with the secret's name, and a value that shows up in a build log or a build error is stored as `[redacted]`. Values shorter than 4 characters aren't matched.

## Revisions and base updates

Every change is saved as a revision. `repose config show --revisions` lists them with any errors, and the dashboard can re-apply an earlier one. `repose logs --kind build` shows the last build's log.

The platform updates the base (agents, tools, kernel) about once a week. Each project is rebuilt on the new base and switched in place. If your configuration doesn't build on it, the project stays where it was and you get a notification. To hold a project on its current base, tick **Hold base updates** on its Config page.
