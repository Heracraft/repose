# Tools from system packages and Homebrew (proposal, 2026-10-03)

**Status: proposal. Nothing here is decided beyond DECISIONS I-423, which
took the Homebrew reader out.** The owner leans towards a curated list
(option C). Code changes only through a `DECISIONS.md` entry that picks an
option.

## How this came up

On the owner's unwrap machine, `az` printed nothing. Two things were
wrong. The command-not-found hint died silently when one nixpkgs package
has the command (fixed by I-410). And `az` never reached the machine: the
owner installed it on an Arch laptop with Octopi, a pacman front end, and
the tools carry (I-221) reads npm, pnpm, bun, Go, cargo, uv and pipx only.
unwrap's `~/.repose/tools-wanted.json` listed eleven tools and no `az`.

I-413 added a Homebrew reader the same day, on the guess that `az` came
from `brew`. It did not, and the owner asked for it to come out (I-423)
before any CLI shipped it. Their concern is the load a long list puts on a
machine that has just started.

## What the carry costs

Nothing waits for the carry: `run` attaches, and the guest installs the
missing tools in the background (`repose-tools-install`, I-221). Each tool
is a nixpkgs substitution or a package manager install, so a list of forty
means minutes of downloads and CPU on a fresh machine, in the same minutes
the user's agent starts work. A tool the user forgot about costs the same
as one they use daily. The npm, Go and uv lists stay short in practice,
because people install few global tools with them. System package lists
and Homebrew lists do not stay short.

## The options

### A. Read the system package manager, filtered

Read pacman's `/var/lib/pacman/local/*/desc` (`%REASON%` absent means
explicitly installed) and `files`, and apt's `extended_states` with the
dpkg file lists. Keep a package when it was installed explicitly, puts a
command in `/usr/bin`, ships no `usr/share/applications/*.desktop`, and
the guest base lacks the command. Install each from nixpkgs by command.

Against it: an Arch install marks a hundred or more packages explicit, and
the filter still keeps many CLI tools nobody wants on a VM (`htop`,
`neovim`, `ranger`, the user's fonts tools). dnf needs rpm's sqlite
database. That is three readers to maintain.

### B. Read Homebrew

I-413's reader: `Cellar/*/*/INSTALL_RECEIPT.json` with
`installed_on_request`. Same problem as A on a Mac that has used `brew`
for years: dozens of formulae. Taken out by I-423.

### C. A curated list of development CLIs (the owner's lean)

Keep a short table in the CLI of commands worth carrying, each with its
nixpkgs attribute: `az` (`azure-cli`), `aws` (`awscli2`), `gcloud`
(`google-cloud-sdk`), `kubectl`, `helm`, `terraform`, `tofu`
(`opentofu`), `flyctl`, `doctl`, `heroku` and similar. The laptop side
checks its own `PATH` for each command (`exec.LookPath`, no package
manager run, so pacman, apt, Homebrew, a curl installer and a manual
download all count). A command found there and missing from the base is
carried as a nixpkgs item.

For it: the list is bounded, so the carry stays small whatever the laptop
has. It covers every way of installing the tool. It is one table to read
in review.

Against it: a tool off the list needs `repose config add`. The table needs
an owner, and a name that maps to the wrong attribute installs the wrong
thing.

### D. Carry nothing more

Rely on the hint I-410 fixed: typing `az` prints `nix profile add
nixpkgs#azure-cli` and `repose config add azure-cli`. This is the state
after I-423.

## Open questions for C

- Which commands go on the first list, and who adds one later. A test can
  check every entry resolves in the base's nixpkgs (`nix eval
  nixpkgs#<attr>.name`).
- Whether `repose scan` shows curated items apart from the managers' ones,
  so a user sees why `az` is coming.
- Whether a user can turn the curated part off (`config.toml`
  `[tools] curated = false`) or remove one item.
- A size bound per item: `google-cloud-sdk` is large, and the carry
  should say so before it starts.
