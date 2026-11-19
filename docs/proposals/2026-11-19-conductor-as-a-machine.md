# The conductor as a repose machine (proposal, 2026-11-19)

**Status: thinking, saved. Nothing here is decided and nothing is to be built
from this file.** The owner, on the night the queue became pull requests
(I-1352): "if we now have PRs as the main way of integrating code into
production, then we can just have webhooks from GitHub reaching a repose
machine and that becomes the conductor. We have an always-on machine called
a conductor. Extrapolate from that idea completely." This file is the
extrapolation, from the smallest true version to the end of the road, written
by the conductor session of 2026-11-19 on the owner's word to write it down.

## Summary

| Question | Answer |
|---|---|
| What is already true | The queue is GitHub state (I-1352); CI runs on every pull request; triggers turn a webhook into a session on a machine (I-1248..); api tokens with scopes let an agent drive the platform (I-1314..); questions let an agent stop and ask the owner from the dashboard or phone (I-929); `repose say` lets the owner talk back (I-1144) |
| The smallest true version | A project `conductor` on a small always-on machine, three triggers (a PR labelled `queued`, a CI run finishing, a message from the owner), the handoff document as its brief, a token scoped to projects and triggers. Nothing new is built for it |
| What moves | Verification moves to GitHub (push `integrated` after each batch and run CI on it); the owner's word becomes a recorded answer to a question; memory becomes a file in the repo the machine commits to; workers become temporary machines the conductor creates through the api |
| What a guest cannot do | Boot a VM check (no nested KVM). CI builds them with build users and is their gate; the conductor reads a result, never runs one |
| Extrapolated | A role is a machine plus triggers plus a brief plus a budget plus one channel to a human. Conductor is the first role; reviewer, triage, bump, docs-drift are the next. Repose builds and ships repose, which is the demo the always-on pitch lacks. The node UI the owner sketched is the dashboard view of a role; a role template is a project you clone; the marketplace is roles |
| Before it is safe | Scoped token; host switches stay with the owner at first; the trigger filter excludes the conductor's own PRs (loops); one seat of cost; the Claude login is the owner's (I-278) |
| The one-week test | Make pushes to `integrated` run CI; create `conductor` with the handoff as its brief, the three triggers and the scoped token; let it run the next two batches while a person watches; switches and the base publish stay with the owner. Two batches without the dev box and the dev-box conductor is retired |

## 1. What is already true

- **The queue is GitHub state.** A branch is queued by a pull request against
  `integrated` with the label `queued` and a record block a tool reads
  (`docs/PR.md`). Since I-1352 nothing about the queue lives on the dev box
  except entry files, which are a cache of the PRs.
- **CI runs on pull requests.** Every queued PR is verified by GitHub before
  the conductor touches it. What the conductor runs in the batch worktree
  repeats that on the merged result, on a box whose load, `TMPDIR`, pipe
  exits and garbage-collected builds have each cost a release
  (`docs/ops/CONDUCTOR-HANDOFF.md` "Lessons").
- **Triggers** bind a webhook route or a schedule to a prompt on a project's
  machine, with the payload in the session (`docs/features/triggers.md`).
- **Api tokens** with scopes let an agent create, start, stop and destroy
  projects and make triggers (`docs/features/tokens.md`).
- **Questions** let an agent stop and ask the owner, who answers from the
  dashboard or a phone (`docs/features/notifications.md`); `repose say` is
  the owner talking to a running agent.

So a conductor is a project named `conductor` on a small machine that never
stops, with three triggers: a pull request labelled `queued`, a CI run
finishing, and a message from the owner. Its brief is the handoff document,
checked in as that machine's instructions. The first version needs nothing
built.

## 2. What changes when the conductor is a machine

- **Verification moves to GitHub.** Push `integrated` after each batch and
  let `ci.yml` run on it (today a push to `integrated` runs nothing, by
  I-651's design, which was about deploys, not checks). A batch is green when
  that run is green, not when a tmux window on the dev box says so. The load
  flakes, the pipe-exit trap, the `TMPDIR` trap and the GC'd host build do
  not exist on a runner.
- **The VM checks** are the one thing a guest cannot run: a repose guest has
  no nested KVM (`docs/ops/DEV-BOX.md`). CI builds them with build users and
  is already their gate (`guest-keystroke-path` cannot pass on the dev box
  either). The conductor reads a VM result; it never runs one.
- **The gate stays human and becomes a question.** The release is a question
  on the owner's phone: the branches, the targets, what each target's ship
  step does to tenants, yes or no. A host or edge switch is its own question.
  The owner's word is then a recorded answer with a timestamp instead of a
  sentence in a chat window that a later conductor has to find.
- **Memory becomes a file.** The handoff rewrite each conductor does becomes a
  commit the machine makes to its own brief. Lessons stop being lost between
  sessions because there is one session, and it is the machine.
- **Workers become machines.** A queued PR that needs a fix, or a feature in
  the owner's order, is a temporary machine the conductor creates through
  the api with a scoped token, briefed by a file, destroyed when its PR is
  queued. The fleet is bounded by a number in the brief, not by a person's
  memory of the owner's rule against big fleets.
- **The dev box stops being the book of record.** I-763 made woker-1 the one
  place main moves. With the conductor on a machine, main moves from a
  machine that outlives a session's context, the owner's laptop and the box.

## 3. Extrapolated all the way

- **A role** is a machine plus triggers plus a brief plus a budget plus one
  channel to a human. Conductor is the first role. The next ones write
  themselves from one week's work: a reviewer that comments on every PR
  opened against `integrated`; a triage role that takes a Grafana alert and
  asks the owner one question with a diagnosis attached; the daily agent bump
  as a cron role instead of a workflow that was red for four days before
  anyone read it; a docs-drift role that reads the diff and opens a PR.
- **Repose builds and ships repose.** That is the demo the always-on
  framework pitch (`docs/proposals/2026-11-12-always-on-framework.md`) has
  been missing. The job auto-apply agent proved trigger to session. The
  conductor proves trigger to session to shipped software, with a human gate,
  on the product itself. Every customer conversation can start with "this
  release was cut by a machine on repose and I said yes from my phone".
- **The node UI** the owner sketched (webhook, agent with skills, output) is
  the dashboard view of a role. A role template is a project you clone:
  brief, triggers, token scopes, question routing. The marketplace is roles,
  and the conductor is the first one in it.
- **A company** is then a set of roles with humans as the gates. Each role
  has a machine, a trigger set, a brief, a budget and a reporting channel;
  the humans answer questions. Repose sells the machine, the triggers, the
  questions and the templates.

## 4. What has to be true before it is safe

- **Rights.** The conductor holds release rights. Its token is scoped to what
  the role does and nothing else. Host and edge switches need operator SSH to
  a host and the Key Vault path; keep those with the owner at first. A tenant
  guest holding the fleet's keys is a different threat model from a tenant
  guest holding a GitHub token.
- **Loops.** A PR the conductor opens fires the webhook the conductor listens
  on. The trigger filter, or the brief, excludes its own author.
- **Cost.** One small machine always on is a seat. Workers are temporary and
  die with their task; the brief caps how many live at once.
- **Login.** The conductor uses the owner's Claude login share inside the
  guest, as I-278 allows. Its spend is the owner's spend and is visible
  nowhere else; a role needs a budget line before there are several.
- **Idempotence.** A webhook delivered twice, or a CI run that reports twice,
  must not cut twice. The trigger layer dedupes deliveries; the brief says
  what to do with a batch already cut.

## 5. The one-week test

In the owner's validation-first shape, with what exists:

1. Make pushes to `integrated` run `ci.yml` (one line in the workflow's
   `on:`; I-651's "nothing deploys from it" still holds).
2. Create the project `conductor` on a small machine from a checkout holding
   the handoff as `CLAUDE.md` for that machine, with the release skill.
3. Three triggers: `pull_request` labelled `queued`, `workflow_run` completed
   on `integrated`, and a schedule as a heartbeat. A token scoped to
   `projects` and `triggers`.
4. The release question goes to the owner as a repose question with the
   options yes and no; switches and the base publish stay with the owner.
5. Let it run the next two batches while a person watches the dev box. If it
   cuts and verifies both without the box, the dev-box conductor is retired
   and the role template is the next feature.
