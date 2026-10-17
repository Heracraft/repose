---
title: Watch the agent's browser
description: See the page the agent is on, live, and take over when it needs a human.
section: Tutorials
order: 21
---

Every machine has [one browser](/docs/machine#browser) for its agents: a real Chromium on a virtual screen that keeps its cookies and logins between runs. Agents reach it through the `playwright` and `chrome-devtools` tools.

## Give the agent a browser job

In a project with a dev server:

```
repose run -p "start the dev server, open the signup page \
with playwright, fill the form with a test address and \
screenshot the result"
```

The first `playwright` call starts the browser on the machine.

## Open the view

From another terminal on your laptop, or the same one after the agent has started:

```
$ repose browser
Watching todo-app's browser at http://localhost:6080/#p=5m2k8Q1p
(the view sleeps after 30 idle minutes).
```

The password is the part of the link after `#`, and your browser never sends it anywhere. The command returns at once and keeps its forward running in the background; run it again for the same link.

## Take over

Clicks in the view go to the agent's browser. Use it for the steps only a person can do:

- **A login.** Sign in to the site the agent needs. The session stays in the browser's profile, so the agent's next call is logged in, and so is the next agent tomorrow.
- **A captcha, a passkey, a 2FA prompt.** Do it, then tell the agent to carry on.

The agents' guide to the machine tells them to ask you for `repose browser` when a page needs a human.

## Close it

Close the tab and the view stays up for 30 minutes without a viewer, then sleeps; opening the link again wakes it. `repose browser stop` closes the view and the forward now, and leaves [the agent's browser](/docs/machine#browser) running.

## Things worth knowing

- **One browser, one profile.** Anything you log in to is there for every agent on that machine. Treat it like a browser on a shared computer: don't log in to more than the job needs.
- **Test suites are unaffected.** `playwright test` and the like still run headless unless the config asks for a headed browser ([Browser](/docs/machine#browser)).
- **Your own Chrome instead.** When the job needs your laptop's logins or extensions, [lend the agent your Chrome](/docs/your-chrome).
