---
title: Lend the agents your Chrome
description: Let the agents on a machine use your laptop's Chrome, with your logins and extensions, for as long as you keep the bridge open, and keep them to the sites you name.
section: Using repose
order: 14.5
---

Some jobs need the browser you already have: the admin tool behind your company's SSO, an account protected by a hardware key, a site where an extension does half the work. With the bridge open, the machine's two browser tools (`playwright` and `chrome-devtools`) drive your laptop's Chrome instead of the machine's own browser. Agents keep running through the switch: the next browser call an agent makes lands in your Chrome, and the one after you close the bridge lands back on the machine.

## Once: turn Chrome's switch on

The bridge needs Chrome 144 or newer. In Chrome, open the switch's page and turn remote debugging on:

```
chrome://inspect/#remote-debugging
```

It stays on until you turn it off. With it off, the bridge opens that page itself and waits up to 5 minutes for you to turn it on.

Chrome then asks you to allow each new connection to it.

## Bridge while you're attached

Add `--bridge` to `run` or `attach`:

```
repose run --bridge -p "test staging in my Chrome"
```

With no terminal of its own, it reports on the session's status line (a [herdr notification](/docs/run-and-attach#herdr-instead-of-tmux) on herdr):

```text
Your laptop's Chrome is bridged in:
the browser tools on this machine drive it now.
```

The bridge lasts as long as the attach. Detach, or close the terminal, and the bridge goes with it; the agents carry on with the machine's own browser.

## Bridge from a second terminal

To keep the bridge open while you attach and detach, or to watch which pages the agents open, run it on its own: `repose browser bridge`. Leave that terminal open and give the agent its job in another one. `Ctrl-C` closes the bridge:

```text
Bridge closed.
The agents on todo-app are back on the machine's browser.
```

The tabs the agents opened stay in your Chrome for you to close.

There is no way to leave a bridge running in the background: either you're attached with `--bridge`, or a terminal shows `repose browser bridge` running.

## Keep the agents to some sites

`--allow` names the sites the agents may use. Everything else fails:

```
$ repose browser bridge --allow github.com,'*.vercel.app'
Chrome asks you to allow the bridge's own connection first:
it is what holds the agents to --allow.
Chrome 144 → todo-app: the agents there browse in your Chrome now,
with your logins. Ctrl-C hands them back the machine's browser.
Chrome asks you to allow each new connection.
Only github.com, *.vercel.app: other sites fail in the agents' tabs.
Pages the agents open are listed below (host and path only).
An agent on todo-app is in your Chrome.
14:10:02  github.com/acme/shop/pull/41
14:10:19  blocked  accounts.google.com/o/oauth2/v2/auth
```

`github.com` is that host only; `*.vercel.app` is `vercel.app` and every name under it. On `run` and `attach` the same list is `--bridge-allow`, which turns the bridge on too:

```
repose run --bridge-allow github.com \
  -p "review the open pull requests and reply to the comments"
```

With an allowlist:

- The agents see only the tabs that are on those sites, and the blank tabs they open themselves.
- In the tabs they can see, a page from any other site fails with `ERR_BLOCKED_BY_CLIENT`, however it was started: a tool's navigation, a click, a script, a redirect, a form post, a popup, a frame inside an allowed page. The agent is told the site isn't on the allowlist and to ask you, and a `blocked` line goes in [the page list](#the-page-list).
- Your own tabs are not affected. A tab you open on an allowed site while the bridge is open becomes one the agents can see, though.
- The bridge keeps a connection of its own to Chrome, and that connection is what stops the pages; with Chrome's switch you allow it before any tool can connect. If it ends, because Chrome quit or remote debugging went off, the bridge closes with it.

When a login goes through another site (Google or GitHub sign-in, an SSO provider), add that site too, or log in yourself before you start the bridge.

## The page list

One line per page the browser tools see load. Time, site, path. A `blocked` line is a page the bridge stopped, either a site off the allowlist or an address that isn't a web page. What comes after `?` or `#` is never shown, because that's where sign-in links and reset links carry their tokens.

The list is printed in your terminal and nowhere else: repose stores none of it, and nothing about it leaves your laptop. Without an allowlist the browser tools usually watch every tab, so pages you load yourself are listed too. With `--bridge` there's no terminal, so only the blocked pages are reported, on the status line.

## What the agents can and can't do in your Chrome

With the bridge open, the agents do in your Chrome what [the browser tools](/docs/machine#browser) do on the machine. They're logged in wherever you are. Anything running on the machine can reach the browser tools' endpoint, so while a bridge is open, any program on the machine can drive your Chrome.

The bridge never lets them, whatever you pass:

- read your cookies out of Chrome, or clear them. Pages use them as usual; a copied cookie would outlive the bridge, so the tools never get one.
- see your sign-in headers or what goes over the network. The tools see which requests a page makes, their addresses and their status. They don't see `Authorization` or API-key headers, request or response bodies, WebSocket messages or server-sent events. A tool that asks for a response body is told to read the page instead.
- read what sites keep in your Chrome (local storage, databases, offline caches), except through a page of that site.
- open `file://` pages, Chrome's own pages (`chrome://settings`, passwords, extensions), or your extensions' pages and background workers.
- upload a file from your laptop, drag one into a page, or pick where downloads are saved. Downloads go where Chrome puts them.
- grant a site permissions (clipboard, camera, microphone, location), turn off certificate checks, or close Chrome.

A page's own JavaScript still reads what that page can: cookies not marked HttpOnly, local storage, what it fetches from its own site, the messages on its own WebSockets. The agents can run JavaScript in a page, so keep a bridge to sites you'd let an agent act on.

The bridge checks each page the agents open against `--allow`. Images, scripts and fetches go to any host the page names, with whatever cookies those sites allow cross-site. The agents can't open or read those sites' pages.

## Stop it

`Ctrl-C` in the bridge's terminal, or detach if you used `--bridge`.

Close the laptop, or lose the network, and it ends on its own: the machine gives up on a laptop it can't reach within two minutes and puts the browser tools back on its own browser. For browser work that has to carry on after you close the laptop, log in on [the machine's own browser](/docs/machine#browser) with `repose browser` instead.

Only one bridge to a machine at a time. A new one ends the bridge a sleeping laptop left behind, and says `Another bridge to todo-app is still open` if that one hasn't let go of the port yet; run it again in a moment.

## Troubleshooting

**The agent says the browser tool can't connect.** On the machine, `repose-guest-profile browser bridge status` prints `on` while a bridge is open. With Chrome's switch, `on` means Chrome is waiting for you to allow that connection, in a window that may be behind your terminal or on another desktop. Decline, and the call the agent made fails; its next one asks again.

**The bridge waits for Chrome with remote debugging already on.** It reads the switch out of Google Chrome's own profile directory, so it never sees another browser's. A browser you started with `--remote-debugging-port` writes the same file, so [`--user-data-dir DIR`](/docs/cli#repose-browser-bridge-project) bridges it; with that flag the bridge doesn't open `chrome://inspect` for you.

## Another browser, or an older Chrome

`--cdp` bridges a Chromium you started with a remote debugging port, whatever its version, with no switch to turn on and no dialog to allow:

```
chromium --remote-debugging-port=9222 \
  --user-data-dir=$HOME/agent-chrome
repose browser bridge --cdp http://127.0.0.1:9222
```

That's a browser of the agents' own on your laptop, on a profile you log in to once. `--allow` holds them to a list there the same way. `--bridge` finds Google Chrome's default profile only; `--cdp` and `--user-data-dir` are `repose browser bridge`'s flags.
