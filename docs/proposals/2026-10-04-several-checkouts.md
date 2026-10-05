# Several checkouts on one machine (proposal, 2026-10-04)

**Status: built as DECISIONS I-480 on branch `multi-checkout`.** This file
keeps the reasoning and the questions left open; the decision entry is
the contract.

## The problem

`repose run` in a repository finds or creates that repository's machine.
A user with agents in three repositories at once needs three machines.
On the flat plans (`2026-09-27-flat-plans.md`) stopped projects are
unlimited, but running memory is the limit: Solo runs one large or two
small guests. Agents spend most of their time waiting on the model API,
so one small guest can carry agents in three repositories. The memory
cost of three guests buys nothing there.

## Options weighed

1. Keep one machine per checkout and rely on suspend when idle. It helps
   a user who works in one repository at a time, and does nothing for
   three at once.
2. One machine holds several checkouts. Chosen.
3. An empty machine where the user picks what goes in (Box style). That
   is the `--temp` half of `2026-09-29-run-and-the-working-directory.md`,
   a way to get a scratch machine, and leaves the plan limit as it is.

For option 2 the owner chose `repose run --on PROJECT` with agent
windows grouped by name in the one tmux session, over a session per
checkout (every reader of the session, `ps`, questions and hooks would
need to learn about several) and over a `repose checkout add|ls|rm` noun
(one more top-level command).

## Where it touches

Laptop side: resolution (a folder with its own remote fails the by_dir
remote check, so a separate `checkouts` key), the early and boot probes
(they read the machine's own checkout), sync (`origin` from the folder's
remote), the carry (machine-wide markers), the `repose` git remote,
agent windows and worktrees, attach and its reconnect and fast path,
`exec`, `ssh`, `cp :PATH`, `code`, `sync`, and the drop handler.

Guest side: nothing in guestd. `~/.repose/checkouts` lists the added
names; the agent guide tells an agent the other folders are not its own.

## Open questions

- A way to remove an added checkout. Today it is by hand: the folder,
  its line in `~/.repose/checkouts`, and the laptop's `checkouts` entry.
  `repose rm` takes a whole project, so a removal may want `--on` on
  another verb, or `repose rm PROJECT:CHECKOUT`.
- Per-checkout `.env` carry state, so running in two folders by turns
  stops resending each set.
- `repose ls` and the dashboard show nothing of added checkouts. Listing
  them needs either an ssh per machine or a guestd report in `Hello`.
- Notifications and `repose questions` name the project and the window.
  The window name already carries the checkout (`api/claude`); whether
  the message should name it on its own line is open.
- The git identity carry writes one `includeIf` set for the laptop
  folder it ran from; two repositories with different identities on one
  machine need both sets kept.
