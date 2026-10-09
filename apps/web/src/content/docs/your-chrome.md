---
title: Lend the agents your Chrome
description: Let the agents on a machine use your laptop's Chrome, with your logins and extensions, for as long as you keep the bridge open, and keep them to the sites you name.
section: Using repose
order: 14.5
---

Some jobs need the browser you already have: the admin tool behind your company's SSO, an account protected by a hardware key, a site where an extension does half the work. With the bridge open, the machine's two browser tools (`playwright` and `chrome-devtools`) drive your laptop's Chrome instead of the machine's own browser. Agents keep running through the switch: the next browser call an agent makes lands in your Chrome, and the one after you close the bridge lands back on the machine.

## Once: turn Chrome's switch on

The bridge needs Chrome 144 or newer. Open this page in Chrome and turn remote debugging on:

```
chrome://inspect/#remote-debugging
```

It stays on until you turn it off. If you skip this step, the bridge opens the page for you and waits up to 5 minutes for you to turn it on.

With the switch on, Chrome asks you to allow each connection to it: a dialog in the Chrome window, once for every browser tool that connects. Chrome shows its "Chrome is being controlled by automated test software" bar for as long as a connection is open.

## Bridge while you're attached

Add `--bridge` to `run` or `attach`:

```
repose run --bridge -p "test the staging checkout in my Chrome"
```

Once the bridge is up, a message at the bottom of tmux says:

```text
Your laptop's Chrome is bridged in:
the browser tools on this machine drive it now.
```

The bridge lasts as long as you're attached. Detach, or close the terminal, and it closes with the attach; the agent keeps working on the machine's own browser. `repose attach --bridge` bridges into a session that's already running.

## Bridge from a second terminal

To keep the bridge open while you attach and detach, or to watch which pages the agents open, run it on its own:

```
$ repose browser bridge
Chrome 144 → todo-app: the agents there browse in your Chrome now,
with your logins. Ctrl-C hands them back the machine's browser.
Chrome asks you to allow each new connection.
Pages the agents open are listed below (host and path only).
An agent on todo-app is in your Chrome.
14:03:21  admin.internal.example/signups
14:03:40  admin.internal.example/signups/export
```

Run it in the project's checkout, or name the project: `repose browser bridge todo-app`. Leave that terminal open and give the agent its job in another one. `Ctrl-C` closes the bridge:

```text
Bridge closed.
The agents on todo-app are back on the machine's browser.
```

The tabs the agents opened stay in your Chrome for you to close.

There is no way to leave a bridge running in the background: either you're attached with `--bridge`, or a terminal shows `repose browser bridge` running.

The bridge needs the machine running; it doesn't start it. On a stopped machine it says so and exits.

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

`github.com` is that host only. `*.vercel.app` is `vercel.app` and every name under it. Give `--allow` once per host, or several separated by commas. With `--bridge`, the same list is `--bridge-allow`, which also turns the bridge on:

```
repose run --bridge-allow github.com \
  "review the open pull requests and reply to the comments"
```

With an allowlist:

- The agents see only the tabs that are on those sites, and the blank tabs they open themselves.
- In the tabs they can see, a page from any other site fails with `ERR_BLOCKED_BY_CLIENT`, however it was started: a tool's navigation, a click, a script, a redirect, a form, a popup, a frame inside an allowed page. The agent is told the site isn't on the allowlist and to ask you, and the bridge terminal prints a `blocked` line.
- Your own tabs are not affected. A tab you open on an allowed site while the bridge is open becomes one the agents can see, though.
- Chrome asks you once more, at the start: the bridge makes a connection of its own to Chrome, which is what stops the pages. If that connection ends (Chrome quits, or you turn remote debugging off), the bridge closes too, rather than carry on without the list.

When a login goes through another site (Google or GitHub sign-in, an SSO provider), add that site as well, or log in yourself before you start the bridge.

## The page list

Each line is the time, the site and the path of a page loaded in a tab the browser tools are watching, and `blocked` for one the allowlist stopped. The part of an address after `?` or `#` is never shown, because that's where sign-in links and reset links carry their tokens. The list is printed in your terminal and nowhere else: repose doesn't store it, and nothing about it leaves your laptop.

Without an allowlist the browser tools usually watch every tab, so pages you load yourself are listed too. With `--bridge`, there's no terminal to print in; only the `blocked` lines appear, as tmux messages.

## What the agents can and can't do in your Chrome

With the bridge open, the agents can do in your Chrome what the browser tools can do anywhere: open pages, click, type, read what's on a page, take screenshots, run JavaScript in a page. They're logged in wherever you are. Anything running on the machine can reach the browser tools' endpoint, so while a bridge is open, any program on the machine can drive your Chrome.

Whatever you pass, the bridge never lets them:

- read your cookies out of Chrome, or clear them. Pages use your cookies as usual; the tools can't copy them somewhere to use after the bridge closes.
- see your sign-in headers or what goes over the network. The tools see which requests a page makes, their addresses and their status. They don't see `Authorization` or API-key headers, request or response bodies, WebSocket messages or server-sent events. A tool that asks for a response body is told to read the page instead.
- read data that sites keep in your Chrome (local storage, databases, offline caches) except through a page of that site;
- open `file://` pages, Chrome's own pages (`chrome://settings`, passwords, extensions) or your extensions' pages and background workers;
- upload files from your laptop, drag files into a page, or pick where downloads are saved. Downloads go where Chrome puts them;
- grant a site permissions (clipboard, camera, microphone, location), turn off certificate checks, or close Chrome.

A page's own JavaScript can still read what that page can: cookies not marked HttpOnly, local storage, what it fetches from its own site, and the messages on its own WebSockets. The agents can run JavaScript in a page, so keep a bridge to sites you'd let the agent act on.

What `--allow` doesn't stop: a page on an allowed site can still load images, scripts and other requests from other sites, as any page does, and those requests carry whatever cookies those sites allow from other sites. The agent can't open or read those sites' pages.

## Stop it

- `Ctrl-C` in the terminal running `repose browser bridge`.
- Detach, if you used `--bridge`.
- Close the laptop. The machine notices within two minutes and its browser tools go back to its own browser. A bridge only works while your laptop is awake and connected; for work that should carry on after you close it, log in on the machine's own browser with `repose browser` instead.

Only one bridge to a machine at a time. A new one takes over from one left by a laptop that went to sleep.

## Troubleshooting

**"todo-app is stopped".** The bridge needs a running machine. `repose start todo-app`, then bridge again.

**The bridge waits and Chrome shows no dialog.** Check that remote debugging is on at `chrome://inspect/#remote-debugging`; the bridge opens that page when it can't reach Chrome. Chrome's dialog appears in a Chrome window, which may be behind your terminal or on another desktop. A Chrome older than 144 has no switch: update it, or use the `--cdp` way below. For a Chrome profile other than the default one (Chromium, Brave, Edge, or a second Chrome profile), pass its profile directory with `--user-data-dir DIR`.

**The agent says the browser tool can't connect.** On the machine, `repose-guest-profile browser bridge status` prints `on` while a bridge is open. If it's on, look for a Chrome dialog waiting to be allowed; a connection you declined fails, and the agent's next call asks again.

**"Another bridge to todo-app is still open".** A laptop that went to sleep keeps its bridge for up to two minutes. Wait and run it again.

**`Ctrl-b` does nothing in the machine's session.** If you run repose inside your own tmux, your tmux takes `Ctrl-b` first. Press `Ctrl-b` twice to send it to the machine's session. The bridge terminal itself doesn't need tmux.

## Another browser, or an older Chrome

Any Chromium browser started with a remote debugging port can be bridged as is, with no switch and no dialogs:

```
chromium --remote-debugging-port=9222 \
  --user-data-dir=$HOME/agent-chrome
repose browser bridge --cdp http://127.0.0.1:9222
```

That gives the agents a browser of their own on your laptop, with a separate profile you log in to once. `--allow` works the same way there.
