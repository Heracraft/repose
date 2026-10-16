---
title: Installing software
description: Add packages and services to a project with repose config, so they're there on every rebuild, from the CLI, the dashboard or Nix.
section: Using repose
order: 13
---

There are two ways to install software on a project's machine:

- **[On the machine](/docs/machine#installing-more)**, with `nix profile add`, `npm i -g`, `go install` and the like. It takes seconds and lasts as long as the machine's disk.
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

Any package from nixpkgs works, nested names like `python312Packages.black` included; search names at [search.nixos.org](https://search.nixos.org/packages). An unfree package is refused unless it's on the platform's allowlist: the agents, `terraform`, `ngrok`, `google-chrome`, `vscode` and `cursor`. A few names are menu entries that do more than install a package: `postgresql`, `redis` and the other databases also start the service.

The build usually takes under a minute, and it's switched into the running machine without a restart, so your agents keep running. A `Waiting for a build slot` step means the server's builders are all busy with other builds. `-v` adds Nix's own output, and so does a run with no terminal.

A failed build leaves the machine on the revision it has, and the name you got wrong never reaches the menu:

```
$ repose config add gcc-typo
Added gcc-typo to todo-app. Building revision 7d03b1c5 ...
config error: nixpkgs has no package "gcc-typo";
search https://search.nixos.org/packages
Nothing changed in todo-app; the previous revision is still active.
```

`repose config remove air` takes a package out, with the same build and switch.

Press Ctrl-C and the CLI stops, not the build: `Interrupted. The build keeps going on the machine.` It's applied when it finishes, and `repose config revisions` says when.

## The menu

The dashboard's project Config page is this menu: tick an entry, choose a version where there's a choice, **Apply**. It groups the databases under **Services**. Packages that aren't menu entries show under **Extra packages**, each with **Remove**.

| Group     | Entries                                                                                                                     |
| --------- | --------------------------------------------------------------------------------------------------------------------------- |
| runtimes  | `bun`, `deno`, `nodejs` (20 or 22), `python` (3.11 or 3.13), `go-tools`, `zig`, `elixir`, `ruby`, `jdk` (17 or 21), `dotnet` |
| databases | `postgresql` (15, 16 or 17), `redis`, `mysql` (MariaDB), `memcached`, `rabbitmq`, `meilisearch`, `nats`                      |
| tools     | `awscli`, `opentofu`, `kubernetes` (kubectl, Helm, k9s), `shell-extras` (lazygit, delta, tldr, hyperfine)                    |
| deploy    | `wrangler`, `supabase`, `flyctl`, `vercel`, `portless`, `cloudflared`                                                        |

Those are the names `repose config add` takes, not the labels the dashboard shows. `repose config add` can't pick a version and takes the default: `nodejs` 22, `python` 3.13, `jdk` 21, `postgresql` 16.

Databases listen on localhost only: PostgreSQL on 5432, Redis 6379, MariaDB 3306, Memcached 11211, RabbitMQ 5672 with its management UI on 15672, Meilisearch 7700, NATS 4222. PostgreSQL has a `dev` superuser and a `dev` database with no password, so `psql` and `postgres://localhost/dev` work; MariaDB has a `dev` database too. Meilisearch runs in development mode, with no master key.

nixpkgs carries neither `vercel` nor `portless`, so those two entries install them from npm at the machine's next boot.

## Write it in Nix

Under the menu is a Nix file, a [home-manager](https://nix-community.github.io/home-manager/) module applied to the user `dev`. Write it for what the menu can't say, such as dotfiles or environment variables.

```
repose config show            # print it
repose config edit            # edit in $EDITOR, apply on save
repose config apply ./repose.nix
```

The dashboard's Config page has the same file under its **Nix** tab. While the menu manages the project, that editor is read-only until **Edit as Nix**.

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

`home.sessionVariables` and `home.sessionPath` reach every process on the machine: agents and the commands they run, your shells, `repose exec` and user services. Directories in `home.sessionPath` go first on `PATH`. A value can use `$HOME`, and one you set wins over the machine's own, such as `EDITOR`. Agents and shells already running keep the environment they started with; new ones get the change. `PATH` itself is refused, as are `BASH_ENV`, `ENV`, `REPOSE_ENV_GEN` and `REPOSE`, and a value can't contain a double quote.

A tmux config the file adds, changes or removes (`xdg.configFile."tmux/tmux.conf"` or `home.file.".tmux.conf"`) reaches the running session at the switch, and takes with it whatever you set by hand there: every global option and key binding goes back to tmux's default before the files are read again. Window and pane options stay; a platform update that changes `/etc/tmux.conf` resets the same way.

On a machine that runs herdr, a `~/.config/herdr/config.toml` written from Nix replaces the seeded one, and herdr can't save its own settings into a file Nix manages. Keep its keys: `shell_mode = "login"` under `[terminal]`, or new panes start shells that skip your `~/.bash_profile`; `version_check = false` under `[update]`, or herdr offers updates the base's pin won't install; and the `[ui.toast]` key that [shows repose's messages in the session](/docs/run-and-attach#herdr-instead-of-tmux). A running herdr reads the file again at each apply.

The file is a home-manager module, not a flake. A `flake.nix` given to `repose config apply` fails with `this file is a Nix flake`. A project's `flake.nix` does something else: it gives agents a dev shell in the checkout ([Projects with a flake.nix](/docs/machine#projects-with-a-flake-nix)).

Once the file is written by hand, the menu and `repose config add` are off for that project: they can't read arbitrary Nix, so `config add` stops and names `repose config edit` instead. Nothing turns the menu back on but applying the file a new project starts with:

```nix
{ pkgs, ... }:
{
  home.packages = [ ];
}
```

The file can't set NixOS options outside the database services under `repose.system`, define a user service whose name starts with `repose-`, fetch without a hash, fetch from a private or local address (a build reaches the public internet and nothing else), read files outside itself, or pick its own nixpkgs.

Keep secrets out of it and use [Secrets](/docs/secrets). A file carrying a secret's current value, whole, base64-encoded or one line of it, is refused before the build with `fragment contains the value of secret STRIPE_KEY`. A value that reaches a build log or a build error is stored as `[redacted]`. Values shorter than 4 characters are left alone.

### repose.nix in your repository

Commit the file as `repose.nix` at the root of your repository and you never apply it yourself. `repose run` and `repose sync` send it whenever it changed since they last sent it, the run that creates the machine included, and the build goes on while you work:

```text
Applying repose.nix (revision 4f1c2a9e) in the background.
```

An unchanged file costs nothing and prints nothing. A failed build changes nothing, and later runs name the error instead of building the same file again; fix the file, or `repose config apply` retries it as it is. Only the project's own checkout counts: a run from another repository never replaces this machine's configuration. With the file in the repository it is the configuration, so an edit on the dashboard's **Nix** tab stands only until the file changes again.

## Your machine.nix

`repose config` changes one project. Your machine.nix is the same kind of file for every machine of your account: your packages, shell aliases, prompt, dotfiles and environment. It lives on your account, on your laptop at `~/.config/repose/machine.nix`, and on the dashboard under **Settings** → **machine.nix**.

```
repose config --global add ripgrep fd  # add packages
repose config --global edit            # edit, push on save
repose config --global show            # the account's copy
repose config --global apply           # push the laptop's
```

It is a home-manager module under the same rules as a project's file.

```nix
{ pkgs, ... }:
{
  home.packages = with pkgs; [ ripgrep fd jq ];

  home.shellAliases = {
    gs = "git status --short";
  };

  xdg.configFile."starship.toml".text = ''
    add_newline = false
  '';
}
```

`home.shellAliases` work in every shell on the machine, and yours replace the machine's own of the same name. `programs.bash.initExtra` is not carried: put shell code in `~/.bashrc` (from machine.nix, `home.file.".bashrc".text`). The machine already runs starship, so the prompt needs only its config file.

Your machine.nix and the project's file go into one configuration, so lists such as `home.packages` merge. If both set one option to different values, the build fails and names the option; wrap the value that should win in `lib.mkForce`. An error in machine.nix names its line there:

```
config error: attribute 'ripgrepp' missing at machine.nix:3:21
```

**When it applies.** A save rebuilds every machine of your account that has it on: a running one switches in place, a stopped one at its next start. A new machine, temporary ones included, gets it too. The machine never waits for it. When the server hasn't built that combination before, the machine boots on the project's configuration alone and switches once yours is built, usually within a minute. If machine.nix doesn't build, your machines keep what they had and you get a notification naming it.

**From your laptop.** `repose run` pushes `~/.config/repose/machine.nix` when you changed it since its last push, and the build runs in the background:

```text
Applying machine.nix (changed) to 3 machines in the background.
```

If you saved it on the dashboard since and haven't changed the laptop's copy, the laptop's is brought up to date instead. If both changed, `run` pushes nothing and says so. `repose config --global apply` then keeps the laptop's copy, and `repose config --global show > ~/.config/repose/machine.nix` keeps the account's.

**Leaving it off.** `repose run --no-personal` creates a machine without it, or turns it off on an existing one, for a machine shown in a demo, say. Turn it back on with `repose config --global on todo-app`, or the switch on the project's Config page; `repose config --global off todo-app` turns it off without a run.

**Removing it.** Empty the file and push it: `: > ~/.config/repose/machine.nix`, then `repose config --global apply`. Every machine switches back without it. Deleting the file removes nothing: the account keeps its copy and new machines still get it. Shells already open keep the aliases and functions they loaded.

## Revisions and base updates

Every change to a project's configuration is saved as a revision. `repose config revisions` lists them with the error of any that failed. To get back from a change that broke something, `repose config apply --revision 9c41d2e7` switches the running machine to a revision that built; the dashboard's Config page re-applies one from its Revisions list.

The platform updates the base (agents, tools, kernel) most weeks, sooner for a security fix. Each project is rebuilt on it. A running machine switches in place; a stopped one gets the new base at its next start. A revision that changes the kernel, from a base update or a change of your own, is built but applies only when the machine restarts: `repose stop todo-app && repose start todo-app` when the agents are idle. If your configuration doesn't build on it, the project stays on the revision it runs and you get a [notification](/docs/notifications) naming the error. To hold a project on its current base, tick **Hold base updates** on its Config page; there's no CLI flag for it.

Holding has a price: a held project doesn't get what a new base adds. [herdr](/docs/run-and-attach#herdr-instead-of-tmux) arrived in base 2026.10.07, so a project on an older base, held or not yet rebuilt, can't switch to it. `repose run --multiplexer herdr` stops with `todo-app runs base 2026.09.15; herdr needs 2026.10.07 or newer.` Untick the hold and let it update first.
