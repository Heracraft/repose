---
title: Notifications
description: Get told by email, on your phone or in Google Chat when an agent is done, needs you, or asks you something, and answer from there.
section: Using repose
order: 16
---

## Email

Email is on from the start and goes to the address on your account. Every notification email ends with a **Stop these emails** link; it opens a page with an **Unsubscribe** button and works for 90 days. `repose notify set --email off` stops them, `--email on` brings them back.

Notification settings cover every project on your account. The dashboard's **Settings** page has the same controls, plus **Send test**.

## Your phone, with ntfy

[ntfy](https://ntfy.sh) turns an HTTP POST into a push notification.

1. Install ntfy from the [App Store](https://apps.apple.com/app/ntfy/id1625396347) or [Google Play](https://play.google.com/store/apps/details?id=io.heckel.ntfy), and subscribe to a topic name nobody could guess. Anyone who knows a topic's name on ntfy.sh can read it, so make it long and random; `openssl rand -hex 5` does that.
2. Give repose the URL and send a test:

```
repose notify set --ntfy https://ntfy.sh/repose-4f9c2a7e1b
repose notify test
```

```text
email: sent
ntfy: sent
gchat: off
```

A self-hosted ntfy works the same, as long as it answers on the public internet. repose refuses a URL on `localhost` or a private address when you save it, never sends to a name that resolves to one, and follows no redirect. For a server behind a login, put the login in the URL (`https://user:password@ntfy.example.com/topic`). `repose notify set --ntfy off` stops it.

## Google Chat

Google Chat posts each notification to a space you choose, as a card with buttons. It needs a Google Workspace account: a personal Gmail account can't add webhooks.

1. In a desktop browser, open the space, then **Apps & integrations**, **Webhooks**, **Add webhooks**.
2. Name it `repose` and copy its URL.
3. Give it to repose in single quotes, since the URL carries an `&`, and send a test:

```
repose notify set --gchat 'WEBHOOK_URL'
repose notify test
```

The URL lets anyone who has it post to the space, so repose treats it like a password: it's never shown again, and the CLI and the dashboard show the space's id instead.

Each notification starts its own thread, so it alerts like a new message. When a question closes, a reply in its thread says how, such as `Answered yes from the dashboard`.

Google refuses a webhook that was deleted, or whose space removed it. repose then stops sending and emails you once; nothing goes to Chat until you save a new URL or a test gets through. `repose notify set --gchat off` stops it.

## What you'll get

| Title                                           | When                                                                                                                                                                                         |
|-------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `todo-app: claude is done`                      | The agent ran `repose-notify --done`. The body is its report.                                                                                                                                 |
| `todo-app: claude says`                         | An agent, or you, ran `repose-notify`. The body is the message.                                                                                                                              |
| `todo-app: claude asks`                         | An agent ran `repose-ask` and is waiting for your answer.                                                                                                                                    |
| `todo-app: claude needs input`                  | The agent is waiting for your permission; [not every agent reports one](/docs/agents#agent-states).                                                                                          |
| `todo-app: claude hit an error`                 | The agent reported an error.                                                                                                                                                                 |
| `todo-app: snapshot failed`                     | A snapshot couldn't be taken; the body says why.                                                                                                                                             |
| `todo-app: base update failed`                  | A [platform update](/docs/config#revisions-and-base-updates) didn't build with your configuration, or built and didn't switch in. The machine keeps what it had.                              |
| `todo-app: base updated`                        | A platform update reached the machine. The body says whether it's running or waits for a stop and start, which a kernel change needs.                                                         |
| `todo-app: machine.nix did not apply`           | Your [machine.nix](/docs/config#your-machine-nix) didn't build or switch on this machine, which keeps what it had; the body has the error.                                                    |
| `todo-app: new system did not boot`             | A start or reboot gave the machine a new system that did not boot, so it runs its previous one; the body says why.                                                                            |
| `todo-app: destroy failed`                      | A destroy didn't finish; the body says why.                                                                                                                                                  |
| `todo-app: stop failed`                         | A stop didn't finish; the body says why.                                                                                                                                                     |
| `todo-app: host moved`                          | The project was restored onto another server from its latest snapshot.                                                                                                                       |
| `The server running your machines restarted`    | The server under your machines restarted and some of them stopped with it. One per restart, with a line for each that stopped ([When a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts)). |
| `todo-app: abuse stopped`                       | The machine was stopped because a miner was running ([Limits](/docs/limits#what-isnt-allowed)). By email: `Your machine was stopped: a cryptocurrency miner was running`.                     |
| `todo-app: unused for 24h, holding plan memory` | Nobody has used the machine for 24 hours and its memory counts against your plan. Once per [unused stretch](/docs/lifecycle#unused-machines).                                                 |
| `tmp-k3f9: destroyed in an hour`                | A [temporary machine](/docs/lifecycle#temporary-machines) made with more than an hour has an hour left.                                                                                       |
| `tmp-k3f9: temporary machine destroyed`         | Its time ran out and it was destroyed, with no snapshot.                                                                                                                                      |
| `todo-app: notifications paused`                | The project hit its hourly cap ([Limits](#limits)).                                                                                                                                          |
| `repose: test`                                  | You ran `repose notify test`, or pressed **Send test**.                                                                                                                                      |

A notification goes to every channel you have on.

While you're at the machine, nothing an agent sends reaches a channel. It waits until you leave, or 10 minutes, whichever comes first. Being at the machine means a tmux client or an SSH connection; your editor and a herdr bridge count as SSH connections. One that's over by the time it would go isn't sent: a question you answered in the terminal or the agent withdrew, a permission prompt the agent has moved past. Notices from repose itself, such as a failed snapshot, are never held.

On ntfy, questions and permission prompts come at urgent priority, done reports, failures and server restarts at high, and the rest at default. Tapping one opens the project's page.

Every email comes as HTML with a plain-text version. There are no images and no tracking in any of them. Times in emails, and in the paused notice, are in the timezone on the **Settings** page, or UTC when none is set.

The body is what the agent said at that moment, never your prompt and never your terminal. It does pass through ntfy.sh, Google or your email provider, so run your own ntfy server if that matters.

An agent finishing its turn sends you nothing. The turn lands in `repose events` as `finished`.

## Emails about your account

Account emails are email only, they're sent even with notification emails off, and they carry no unsubscribe link, because each one answers something you did or are about to be charged for. Polar, which takes the payments, sends the receipt for each charge, the confirmation of a new plan and the reminder when your card is about to expire. The rest are ours.

| Subject                                        | When                                                                                                                       |
| ---------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| `Welcome to repose`                            | You signed in for the first time.                                                                                          |
| `You're on the waitlist`                       | Every seat was taken when you tried to choose a plan ([When repose is full](/docs/limits#when-repose-is-full)).            |
| `A seat is yours for 72 hours`                 | It's your turn.                                                                                                            |
| `Your seat hold ran out`                       | You didn't choose a plan in time.                                                                                          |
| `Your free week ends soon`                     | Two days before the first charge ([The free week](/docs/billing#the-free-week)).                                           |
| `Your payment failed`                          | A charge didn't go through ([If a payment fails](/docs/billing#if-a-payment-fails)).                                      |
| `Your plan is ending`                          | You cancelled ([Changing and cancelling](/docs/billing#changing-and-cancelling)).                                          |
| `Your plan has ended`                          | The period ended after a cancellation.                                                                                     |
| `Your plan changed`                            | The day you upgrade, or the renewal a scheduled downgrade takes effect on; scheduling one sends nothing.                   |
| `Your machines were stopped: egress limit`     | The month's egress passed four times the plan's allowance ([Egress](/docs/limits#egress)).                                 |
| `Your projects hold more than your plan's disk` | Once a month while your projects hold more than the plan's disk ([Projects](/docs/limits#projects)).                      |
| `Google Chat notifications stopped`            | Google refused your Chat webhook.                                                                                          |

## Agents can message you and ask questions

Two commands on every machine let an agent reach you:

```
repose-notify "Deploy to staging is green"
repose-notify --done "PR 12 is up: retries for the webhook sender"
repose-ask --options yes,no "Drop the legacy sessions table?"
```

`repose-notify` sends the message and returns at once; with `--done` it says the work you asked for is finished. `repose-ask` sends the question and waits for your answer, then prints it. Run them yourself too, for example at the end of a long script.

A question can be answered in five places:

- **ntfy:** if the question has options, the notification has a button for each. Tapping one answers it. No login is needed.
- **Google Chat:** the card has a button for each option. It opens a page with the question and a button; pressing it answers.
- **Email:** the email has a link for each option, which opens the same page. Replying to the email doesn't work.
- **The dashboard:** waiting questions show under **Waiting on you** at the top of the Projects page and of the project's page, with a button per option or a text box. In the text box, Enter sends and Shift+Enter starts a new line.
- **Your laptop:** `repose questions` lists the waiting ones, `repose reply` answers and `repose reply --dismiss` dismisses.

You can also answer the agent in its own terminal: any prompt you send the agent that asked closes its open questions. OpenCode 2 doesn't report your prompts, so its questions stay open until answered here.

The first answer wins. A link or button used after that says the question was already answered, and what the answer was. **Dismiss** on the dashboard ends a question without answering.

A question whose `repose-ask` was killed closes 90 seconds after it last checked for an answer.

`repose-ask` options:

| Option            | Default    |                                                                                                                                                       |
| ----------------- | ---------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--options A,B,C` | none       | Up to three fixed answers. Without options, any text is an answer.                                                                                    |
| `--timeout 30m`   | 30 minutes | How long to wait, up to 24 hours. A duration like `90s` or `2h`, or seconds.                                                                          |
| `--agent NAME`    | detected   | Who is asking. Detected from the agent running the command, else `shell`.                                                                             |
| `--cancel [ID]`   |            | Withdraw this agent's open questions, or the one with the ID `repose-ask` printed. With `--all`, every open question on the machine. Prints how many. |

It exits with:

| Code | Meaning                                                                                                        |
| ---- | -------------------------------------------------------------------------------------------------------------- |
| 0    | You answered. The answer is on standard output.                                                                |
| 1    | The machine's agent service couldn't be reached, or refused the question.                                      |
| 2    | Wrong usage, such as no question or more than three options.                                                   |
| 3    | No answer before the timeout.                                                                                  |
| 4    | No channel is on (email off, no ntfy topic, no working Google Chat webhook), so nobody would see the question. |
| 5    | Cancelled: you dismissed it, the agent withdrew it, the machine stopped, or it restarted.                      |
| 6    | You answered the agent in its terminal. Nothing is on standard output.                                         |
| 130  | Interrupted, for example with `Ctrl-C`. The question is cancelled.                                             |

Messages and questions are capped at 1 KB and count toward the [hourly cap](#limits). Neither is written to any log.

## Limits

Repeats of the same event from the same agent within 60 seconds become one event, with both summaries in it; what an agent sends with `repose-notify` or `repose-ask` never collapses. A project sends at most 30 notifications in any hour; past that, one message says when they resume, and its events still show in `repose events`. Google Chat takes one message a second per space, so a burst arrives a second apart.

Each machine can raise 30 agent events, messages and questions at once, then one every 2 seconds. Anything over that rate is dropped before it becomes an event, so it never shows in `repose events`, and a question dropped this way ends with exit code 3 at its timeout. A question's first close is free of that rate, so a question never sits open because the machine was too noisy. A project's event history keeps at most 600 of these an hour. Window names are cut to 64 bytes.
