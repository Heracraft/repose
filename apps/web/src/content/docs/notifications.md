---
title: Notifications
description: Get told by email or on your phone when an agent is done, needs you, or asks you something, and answer from there.
section: Using repose
order: 16
---

## Email

Email is on from the start and goes to the address on your account. Each message about a machine has an unsubscribe link. It opens a page with an **Unsubscribe** button and works for 90 days after the email was sent. Mail apps that show their own unsubscribe button use the same link. To switch it yourself:

```
repose notify set --email off
repose notify set --email on
```

## Your phone, with ntfy

[ntfy](https://ntfy.sh) is a free push notification service: you subscribe to a topic in its app, and anything posted to that topic reaches your phone.

1. Install ntfy from the [App Store](https://apps.apple.com/app/ntfy/id1625396347) or [Google Play](https://play.google.com/store/apps/details?id=io.heckel.ntfy).
2. Pick a topic name nobody could guess. Topics on ntfy.sh are readable by anyone who knows the name, so make it long and random: `openssl rand -hex 10` gives a good suffix.
3. Subscribe to it in the app.
4. Tell repose, and send a test:

```
repose notify set --ntfy https://ntfy.sh/repose-4f9c2a7e1b
repose notify test
```

```text
email: sent
ntfy: sent
```

`repose notify` alone shows the settings. A channel that failed shows `failed` with the reason ntfy gave, such as `failed: the server answered 403`, and the command exits 1. You can send 6 tests a minute.

A self-hosted ntfy server works the same way, as long as it's reachable from the internet. repose refuses an ntfy URL on `localhost` or a private address when you save it, never sends to a name that resolves to one, and doesn't follow redirects. For one that needs a login, put it in the URL (`https://user:password@ntfy.example.com/topic`). Turn ntfy off with `repose notify set --ntfy off`.

Settings apply to every project. The dashboard's **Settings** page has the same controls, plus **Send test**. **Email notifications** and the timezone save as soon as you change them; the ntfy URL saves with the **Save** button next to it.

## What you'll get

| Title                                           | When                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `todo-app: claude is done`                      | The agent ran `repose-notify --done` to say the work you asked for is ready to look at. The body is its report.                                                                              |
| `todo-app: claude needs input`                  | The agent is asking you something, usually a permission.                                                                                                                                      |
| `todo-app: claude hit an error`                 | The agent reported an error.                                                                                                                                                                  |
| `todo-app: snapshot failed`                     | A snapshot couldn't be taken.                                                                                                                                                                 |
| `todo-app: base update failed`                  | A platform update didn't build with your configuration.                                                                                                                                       |
| `todo-app: base updated`                        | A platform update was switched into the machine.                                                                                                                                              |
| `todo-app: machine.nix did not apply`           | Your [machine.nix](/docs/config#your-machine-nix) didn't build or switch on this machine, which keeps what it had; the body has the error.                                                    |
| `todo-app: new system did not boot`             | A start or reboot gave the machine a new system that did not boot, so it runs its previous one; the body says why.                                                                            |
| `todo-app: destroy failed`                      | A destroy didn't finish; the body says why.                                                                                                                                                   |
| `todo-app: host moved`                          | The project was restored onto another server from its latest snapshot.                                                                                                                        |
| `The server running your machines restarted`    | The server under your machines restarted and some of them stopped with it. One per restart, with a line for each that stopped: running again, or not started and why ([When a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts)). |
| `todo-app: abuse stopped`                       | The machine was stopped because a miner was running ([Limits](/docs/limits#what-isnt-allowed)). By email: `Your machine was stopped: a cryptocurrency miner was running`.                     |
| `todo-app: unused for 24h, holding plan memory` | The machine has run 24 hours with nobody on it and no agent working, and its memory counts against your plan. Once per unused stretch ([Unused machines](/docs/lifecycle#unused-machines)).   |
| `tmp-k3f9: destroyed in an hour`                | A temporary machine has an hour left; `repose keep tmp-k3f9` keeps it. Only for one made with more than an hour ([Temporary machines](/docs/lifecycle#temporary-machines)).                   |
| `tmp-k3f9: temporary machine destroyed`         | A temporary machine's time ran out and it was destroyed, with no snapshot.                                                                                                                    |
| `todo-app: claude says`                         | An agent, or you, ran `repose-notify` on the machine. The body is the message.                                                                                                                |
| `todo-app: claude asks`                         | An agent ran `repose-ask` and is waiting for your answer.                                                                                                                                     |
| `todo-app: notifications paused`                | The project sent 30 notifications in the last hour. The body says when they start again.                                                                                                     |
| `repose: test`                                  | You ran `repose notify test` or pressed **Send test**.                                                                                                                                        |

Nothing an agent sends reaches you while you're at the machine: attached in tmux, or connected over SSH, which includes your editor and a herdr bridge. A question, done report, message, permission prompt or error waits while you're there and goes out once you leave, or 10 minutes after it happened if you stay. One that's over by then isn't sent: a question you answered in the terminal or that the agent withdrew, or a permission prompt the agent has moved past. Notices from repose itself, such as a failed snapshot, are never held.

On ntfy, questions and permission prompts come at urgent priority, done reports and failures at high, and the rest at default. Tapping one opens the project's page.

Every email comes as HTML with a plain-text version. There are no images and no tracking in any of them.

The body is what the agent said at that moment, up to 1 KB. It's never your prompt or your terminal. It does pass through ntfy.sh or your email provider, so use a self-hosted ntfy server if that matters.

An agent finishing its turn sends you nothing. The turn shows in `repose events` as `finished`, and the agent's state in `repose ls` changes. Claude Code, Codex and opencode (OpenCode 2 too) report through hooks, within about 10 seconds; permission prompts and errors reach you that way. Gemini CLI and pi have no hooks, so the machine records `finished` when their processes go quiet, within about 90 seconds, with the summary `gemini went idle` or `pi went idle`. On a [herdr project](/docs/run-and-attach#herdr-instead-of-tmux) the machine takes their state from herdr instead. If herdr sees one of them waiting for you, `repose ls` shows `needs input`, with no notification.

## Emails about your account

Some emails are about your account rather than a machine. They are sent even when you have turned notification emails off, and they have no unsubscribe link. Polar, which handles the payments, sends the receipt for each charge, the confirmation of a new plan, and a reminder when your card is about to expire. The emails about your free week ending, a failed payment, a cancellation, a plan ending and a plan change are ours.

| Subject                                        | When                                                                                                                                                                 |
| ---------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Welcome to repose`                            | You signed in for the first time. Install, `repose run`, choose a plan.                                                                                              |
| `You're on the waitlist`                       | Every seat was taken when you tried to choose a plan. Your place, and what happens next ([When repose is full](/docs/limits#when-repose-is-full)).                   |
| `A seat is yours for 72 hours`                 | It's your turn. Choose a plan within 72 hours; after that the seat goes to the next person.                                                                          |
| `Your seat hold ran out`                       | You didn't choose a plan in time. You're back on the list, at the back, and get another email at your next turn.                                                     |
| `Your free week ends soon`                     | Two days before the first charge: the plan, the amount, the date. Cancel before then and nothing is charged.                                                         |
| `Your payment failed`                          | A charge didn't go through. Update your card in the billing portal; your machines keep running for three days, then stop until a payment arrives.                    |
| `Your machines were stopped: a payment failed` | Three days after a failed payment your machines were snapshotted and stopped.                                                                                        |
| `Your plan is ending`                          | You cancelled. The plan runs until the end of the period; your machines stop then and their snapshots are kept 30 days.                                              |
| `Your plan has ended`                          | The period ended after a cancellation. Your machines are stopped; the email says until when the snapshots are kept and how to come back.                             |
| `Your plan changed`                            | You changed plan: from what, to what, and when it takes effect.                                                                                                      |
| `Your machines were stopped: egress limit`     | Your machines sent four times the plan's monthly egress and are stopped until the period renews ([Limits](/docs/limits#network)). Upgrading lifts the limit at once. |

## Agents can message you and ask questions

Two commands on every machine let an agent reach you from any shell, including its own shell tool:

```
repose-notify "Deploy to staging is green"
repose-ask --options yes,no "Drop the legacy sessions table?"
```

`repose-notify` sends the message and returns at once. `repose-ask` sends the question and waits for your answer, then prints it. Run them yourself too, for example at the end of a long script.

A question can be answered in four places:

- **ntfy:** if the question has options, the notification has a button for each. Tapping one answers it. No login is needed.
- **Email:** the email has a link for each option. The link opens a page with the question and a button; pressing it answers. Replying to the email doesn't work.
- **The dashboard:** a waiting question shows at the top of the project's page, with a button per option or a text box. The Projects page lists every waiting question, each with **Answer**.
- **Your laptop:** `repose questions` lists the waiting ones and `repose reply` answers:

```
repose questions
repose reply todo-app yes
```

The first answer wins. A link or button used after that says the question was already answered, and what the answer was. **Cancel question** on the dashboard cancels a question without answering.

Tell your agent about the commands in its instructions, for example in `CLAUDE.md`: "When you need a decision from me, run `repose-ask` with the question and use its output as my answer."

`repose-ask` options:

| Option            | Default    |                                                                                   |
| ----------------- | ---------- | --------------------------------------------------------------------------------- |
| `--options A,B,C` | none       | Up to three fixed answers. Without options, any text is an answer.                |
| `--timeout 30m`   | 30 minutes | How long to wait, up to 24 hours. A duration like `90s` or `2h`, or seconds.      |
| `--agent NAME`    | detected   | Who is asking. Usually detected from the agent running the command, else `shell`. |

It exits with:

| Code | Meaning                                                                   |
| ---- | ------------------------------------------------------------------------- |
| 0    | You answered. The answer is on standard output.                           |
| 1    | The machine's agent service couldn't be reached.                          |
| 2    | Wrong usage, such as no question or more than three options.              |
| 3    | No answer before the timeout.                                             |
| 4    | Email is off and there's no ntfy topic, so nobody would see the question. |
| 5    | Cancelled: you dismissed it, the machine stopped, or it restarted.        |
| 130  | Interrupted, for example with `Ctrl-C`. The question is withdrawn.        |

Messages and questions are capped at 1 KB and count toward the project's 30 notifications an hour. The text is shown to you and nobody else, and it isn't written to any log.

## Limits

Repeats of the same event from the same agent within 60 seconds are sent once. A project sends at most 30 notifications in any hour; past that, one message says until what time they're paused. Its events still show in `repose events`.

Each machine can raise 30 agent events, messages and questions at once, then one every 2 seconds. Anything over that rate is dropped on the host: it never shows in `repose events`, and a question dropped this way ends with exit code 3 at its timeout. A project's event history keeps at most 600 of these an hour. Agent names longer than 32 bytes show as `unknown`, and window names are cut to 64 bytes.

## Check what happened

```
repose events todo-app
repose events todo-app --since 72h -f
```

If an event is listed but nothing arrived, run `repose notify test`.
