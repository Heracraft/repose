---
title: Watch the agent's browser
description: See the page the agent is on, live, and take over when it needs a human.
section: Tutorials
order: 21
---

Every machine has one browser for its agents: a real Chromium on a virtual screen, driven through the `playwright` and `chrome-devtools` tools. It starts the first time an agent reaches for it and keeps its cookies and logins from then on. You can look at that screen whenever you like, and click in it.

## Give the agent a browser job

Start with something that has a UI. In a project with a dev server:

```
repose run "start the dev server, open the signup page \
with playwright, fill the form with a test address and \
screenshot the result"
```

In tmux you'll see the agent start the server, call the browser tool and describe what it saw. The screenshot lands in the checkout on the machine.

## Open the view

From another terminal on your laptop, or the same one after the agent has started:

```
$ repose browser
Watching todo-app's browser at http://localhost:6080/#p=5m2k8Q1p
(the view sleeps after 30 idle minutes).
```

A tab opens with the machine's screen and you're looking at the agent's Chromium, the same window it's driving, with the page it's on. Nothing to type: the password is the part of the link after `#`, and your browser never sends it anywhere. The screen takes the size of your tab. It updates live: when the agent navigates, you see the navigation. The command returns at once and keeps its forward running in the background; run it again for the same link.

If 6080 is taken on your laptop (another project's view, usually), a free port is used and the link shows it.

## Take over

The screen is not a recording. Click in it and you're using the agent's browser. This is for the steps only a person can do:

- **A login.** Sign in to the site the agent needs. The session stays in the browser's profile, so the agent's next call is logged in, and so is the next agent tomorrow.
- **A captcha, a passkey, a 2FA prompt.** Do it, then tell the agent to carry on.
- **A quick check.** Scroll to the part of the page the agent described and see it for yourself.

Agents know this too. Their guide to the machine tells them to ask you to run `repose browser` when a page needs a human, so a prompt like "log in to the staging site and check the dashboard" ends with the agent asking rather than guessing at credentials. When you get that message, `repose browser`, log in, and reply.

## Close it

Close the tab and the view stays up for 30 minutes without a viewer, then sleeps; opening the link again wakes it. `repose browser --stop` closes the view and the forward now. The agent's browser is separate: it keeps running while an agent uses it and stops after 30 minutes with neither an agent nor you on it. Stopping the view never interrupts an agent.

## Things worth knowing

- **One browser, one profile.** Both browser tools see the same tabs, and anything you log in to is there for every agent on that machine. Treat it like a browser on a shared computer: don't log in to more than the job needs.
- **Test suites are unaffected.** `playwright test`, Cypress and Puppeteer scripts run headless as always, unless their config asks for a headed browser. When it does, they appear on the desktop too.
- **Memory.** The browser is capped (1.5 GB on a small machine, more on larger ones), so a runaway page kills a tab, not the agent.
- **Your own Chrome instead.** When the job needs your laptop's logins or extensions, [lend the agent your Chrome](/docs/your-chrome) rather than logging in on the machine.

[The machine](/docs/machine#browser) has the reference for all of this.
