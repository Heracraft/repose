# Where a project runs (proposal, 2026-11-19)

**Status: research, saved. Nothing here is decided and nothing is to be built
from this file.** The owner asked for prior art rather than a new pattern: "tell
somebody to go through the work of researching existing patterns that we can
emulate that work really well, because we don't want to invent new patterns,
we're most likely gonna give something unintuitive." His instinct was "a target
kinda thing where the user has to decide... you know how we can do
remote/branch name in GitHub maybe that." This file judges that instinct against
ten tools and says which part of it the prior art supports.

## The problem

Since I-1366 a host has an owning account, and `projects.placement` defaults to
`own`, so the moment a customer adds a Mac mini every new project goes to that
Mac and the customer is never asked and never told. While the Mac answers,
`repose ls` and `repose status` name the host only once it has stopped
answering (`cli.md:488`); the name is in `repose status -v`, which also writes a
stderr line for every api request (`cli.md:27`), and `--json` carries `host_id`
and no host name, so the readout sits where nobody looks and the
machine-readable output has only a uuid. If the Mac is not ready the create is
refused as `capacity` and the project is left in `error`, and a second attempt
strands a second project, because the placement happens after the row exists.
The CLI has no flag for the other answer: `placement: "hosted"` is documented as
API-only (`own-hardware.md:10`), so a customer whose install died cannot create a
project anywhere until they remove the hardware they just bought, which no
message suggests. A project is pinned to its host for life, so this silent
default is not a setting a customer can correct later; it is a decision made for
them, once, per project.

## The patterns

| Pattern | What the user names | Where the default lives | Shown before you act | Gets right | Costs |
|---|---|---|---|---|---|
| **git remote + upstream** | a remote (`origin`), a branch | `branch.<n>.remote` in that clone's `.git/config`, written by `push -u` | `git status` names the upstream; `git branch -vv` lists them | A branch with **no** upstream makes `git push` refuse and print the exact command to choose one. The default is absent, not guessed | Per-clone, so a second clone starts over. Two names (remote, branch) for one target |
| **git `push.default`** | nothing; a policy | `push.default` in config, global or per-repo | only in the refusal | `simple` refuses a push whose names disagree instead of guessing | The thing the owner named is the weakest row: it is a policy with no target in it, and git changed it *because* the old default did what nobody asked |
| **kubectl context + namespace** | a context, a namespace | `current-context` in `~/.kube/config`; `--context` per call; `KUBECONFIG` per shell | `kubectl config current-context`, and nothing else | One name carries cluster, user and namespace. Every command takes `--context` | Ambient state with no readout where you look. The whole kubectx/kube-ps1/kubie/kubesafe industry exists to patch this |
| **docker context** | a context | `docker context use`, persisted; `DOCKER_CONTEXT` per shell | `docker context ls` marks the active one | One name for a whole remote daemon; the env var gives a one-off without a switch-and-revert | Same ambient state. "If you forget to switch back to default after staging work, you might accidentally deploy half-finished code to production" |
| **terraform workspaces** | a workspace | `.terraform/environment` in the working directory | `terraform workspace show` | Cheap to make, per-directory rather than global | HashiCorp recommends against it for prod. One config, one backend, one set of credentials, and a wrong `select` reaches production |
| **AWS `--profile`** | a profile | `AWS_PROFILE`, or `[default]` in `~/.aws/config` | nothing; you run `aws sts get-caller-identity` | Named credential sets, and every command takes `--profile` | The `[default]` profile is the documented foot-gun: a script with no profile hits whichever account is default. Env vars silently outrank the profile |
| **gcloud configurations** | a configuration (project, account, region in one) | the active config under `~/.config/gcloud`; `CLOUDSDK_ACTIVE_CONFIG_NAME` per shell or per call | `gcloud config list` | The fullest version of the pattern: named bundles, env override, per-call `--project` | Still invisible. The advice everyone gives is "show the active config in your prompt", which is advice to fix the tool by hand |
| **fly.io `primary_region`** | a region | `primary_region` in `fly.toml`, **in the repository**, written by `fly launch` | the file is in the diff; `fly status` lists machines by region | `fly launch` proposes the nearest region, shows it, lets you change it, then writes it where the code lives. Per-deploy `--region` overrides | A changed `primary_region` moves nothing that exists; machines stay where they were made. Two people, one repo, one answer |
| **heroku remotes + `--app`** | a git remote per app | `heroku.remote` in `.git/config` | `git remote -v` | **Refuses to guess.** With two app remotes: `Multiple apps in git remotes ... specify with --app or --remote`, naming both ways out | The target hides inside git's remotes, so `git push` and `heroku` disagree about what is current |
| **ssh `Host` aliases** | a host alias | nothing is current; every call names its target | the alias is in the command you typed | No ambient state at all. The name carries address, user, port and key | Nothing is implicit, so there is no default to be wrong about and no default to be convenient either |
| **nix `--system`** | a system double | per invocation only; no persistent target | nothing | Refuses with a sentence naming both sides: an `aarch64-linux` is required but I am an `x86_64-linux` | No default means no mistake, and no convenience |
| **naming scheme** (`registry/user/image`, `origin/main`) | the target as a prefix of the name | the name itself | you typed it | Impossible to get wrong; the target travels with the reference | `docker push myimage` going to Docker Hub is this pattern's silent default, and it is the thing people trip over |

**How people get it wrong, in their words.** On kubectl: "the concept of a
current context is evil"; "got burned by this, pretty badly"; "accidentally
deleted a production deployment" while thinking it was local. On Terraform: an
apply the engineer believed was staging ran against production, and the reason
given is that nothing shows the workspace unless you run `workspace show`. On
AWS: a script with a bug "will be using the default credentials as a default,
resulting in potential data loss". Every one of these is the same failure repose
has tonight: a target chosen somewhere else, invisible at the moment of the
command.

**The counter-evidence, which matters.** `gh` refuses when a checkout has
several remotes, and it refuses when there is only one, and users hate it:
cli/cli#6792 ("Setting default remote is required even if only one is defined"),
#8659 ("default repo should be inferred"), #9399, #7216. A refusal is right when
the answer is genuinely ambiguous and wrong when it is not.

## What could work for repose

Three candidates, in the order the prior art supports them.

**1. Heroku's refusal, for the ambiguous case only.** An account that owns a
host and has a plan that sells fleet memory has two possible answers, and repose
currently picks one. Refuse instead, and name both ways out. An account with one
possible answer is never asked, which is where `gh` went wrong.

```
$ repose run
todo-app could go on your hardware or on repose's servers.
  repose run --host mac-mini
  repose run --host repose
```

**2. fly's `primary_region`: ask once, write it down, show it.** The moment is
`repose host add`, which already prints what it is about to do. It asks the
question there, once, and writes the answer where the other defaults live.

```
$ repose host add
...
mac-mini is online.
New projects: on mac-mini. `repose host default repose` changes it,
`--host` changes one.
```

```toml
# ~/.repose/config.toml
default_host = "mac-mini"   # or "repose"
```

**3. The readout, which every working pattern has and repose has in the wrong
place.** The HOST column appears whenever the account owns a host, not only
when that host has stopped answering.

```
$ repose ls
PROJECT     HOST      SIZE   STATE    UP     AGENTS
todo-app *  mac-mini  large  running  2h14m  claude: working
api         repose    small  stopped  -      -
```

And `--host` on `repose run`, taking a host name of the account's or the literal
`repose`, matching `--size` and `--agent` in shape. A name the account does not
have exits 2. `--host repose` on a plan that sells no fleet memory exits with
`hosted_plan_required`, which the API already answers.

**This reverses a decision, and the reason it may be reversed is I-1366
itself.** I-616 put the host behind `-v` on the ground that "the host and the
machine's address are platform internals nobody acts on"
(`internal/cli/status.go:454`, amending I-192), and on repose's own fleet that
was true: a customer could not act on `host-02` and had no reason to know it
existed. I-1366 ended that premise. A host with an owning account is a Mac on
the customer's desk, which they bought, plugged in, named, and can unplug, so
it is the opposite of an internal nobody acts on; it is the thing they act on
most. Item (2) therefore needs a decision entry that amends I-616 and says
this, rather than a quiet change to a column, and `host_name` needs to join
`host_id` in the project JSON (`internal/cli/api_types.go:67`) so a script can
print the name its owner gave the machine. The `--json` gap is the sharper half:
a flag can be found, and a missing field cannot be worked around at all.

## Recommendation

**A persistent setting plus a per-command flag plus a visible readout, with the
setting's value asked for once at `repose host add`.** Not one of the four
things, because no tool in the list is one of them: every pattern that works is
a named default, an override, and a way to see which is active. The parts, in
the order they should ship:

1. `--host NAME|repose` on `repose run`. This is the missing escape hatch, and
   it is a bug, not a design question: today a customer with a dead install can
   create nothing anywhere.
2. The HOST column whenever the account owns a host. This comes after (1) and
   not before it. The column tells a customer where they are; only the flag
   lets them act, so shipping the readout first would make the trap visible
   and still inescapable.
3. `default_host` in config.toml, asked for and written by `repose host add`.
4. The ambiguity refusal, only where both answers are possible.

**The naming scheme is out, and not on taste.** A project's slug is already its
address: its SSH hostname (`todo-app.repose`, `machine.md:8`), its guest
hostname, and part of its webhook path. `mac-mini/todo-app` changes the address
of everything a customer has bookmarked, and the prefix becomes a lie the moment
a restore moves the machine to another host. The owner's `remote/branch`
instinct was right about one thing and wrong about another: right that a *name*
should carry the target, which `--host mac-mini` does, and wrong that the name
should be the project's, which in this product is a hostname.

**Weaknesses, named.**

- A persistent setting is exactly the invisible state the kubectl complaints
  blame. The readout is the mitigation, and readouts get skipped; that is why
  kube-ps1 exists. The refusal in (4) is the only part that cannot be skipped.
- The prior art is all client-side, and repose's would have to be
  account-side. `default_host` in config.toml does not apply to a project made
  on the dashboard, which the parity rule says must be able to do everything
  the CLI can. So the setting probably belongs on the account, read by both
  clients, with `--host` overriding per command. That is a departure from every
  row in the table and should be a decision, not a detail.
- A per-command flag is heavier here than anywhere in the prior art. `fly
  deploy --region` makes a machine you can destroy and remake; `repose run
  --host` pins a project for life. A flag that cannot be taken back wants a
  confirmation the prior art does not have.
- Asking at `repose host add` catches the customer who adds hardware. It does
  not catch the customer who added hardware last week, before the question
  existed.

## What it costs to migrate from today's silent default

Small, because the field already exists and is read in only two places.

- `projects.placement` stays (`own` | `hosted`), and the old API shape stays
  accepted for a release. What changes is the default: unset means the
  account's setting, and the account's setting is written when the first host is
  added. An account with no host keeps today's behaviour exactly.
- **Nothing moves.** A project is pinned to its host for life, so every project
  already on a Mac stays there. There is no backfill and no re-placement.
- The accounts that already own a host get asked the next time they add one,
  and get the readout immediately. They need one line in the release notes
  saying where their new projects go, because for them the behaviour was never
  announced.
- The refused-and-stranded project is a separate fix from this proposal and a
  bigger one: the row exists before placement runs, so a refusal leaves
  `error` behind. Either placement answers before the row is written, or the
  failed create removes its own row. Until then every retry strands another
  project, and that is the part a customer meets tonight (ENG-66 holds the
  cause, ENG-28 the symptom).
- **The machinery underneath is fine, which is why this is a proposal about a
  choice and a readout rather than about placement.** The dashboard customer
  review of 2026-11-19 measured the recovery: a stranded project starts and
  runs within 17.0 seconds once the host is removed. Nothing is slow and
  nothing is broken below this; what is missing is the question nobody was
  asked and the answer nobody can see. The measurement is that review's, not
  this file's.
- Docs in the same commit: `own-hardware.md` (the paragraph that says new
  projects go to your hardware says who decided), the `repose run` flag table
  and the `repose host` rows in `cli.md`, and `internal/cli/docs_test.go`
  follows. A decision id is needed before any of it is written, and it has to
  amend I-616 rather than sit beside it, because that entry's reasoning is the
  thing being overturned.

## Sources

What this product does now, measured by the customer reviews of 2026-11-19 and
not by this file: the CLI review found the `status -v` host row, its stderr
price and the missing `host_name` in `--json`; the dashboard review measured
the 17.0 second recovery and the `error`-with-only-Restart page. Both are
theirs. The code and docs references were then read here:
`internal/cli/status.go:454`, `internal/cli/api_types.go:67`, `cli.md:27`,
`cli.md:488`, `machine.md:8`, `own-hardware.md:10`.

Real complaints, not documentation, where the claim is about a mistake:

- [Kubesafe thread, Hacker News](https://news.ycombinator.com/item?id=41578274) and [the wrong-cluster thread](https://news.ycombinator.com/item?id=27739134)
- [Terraform workspaces are not environments](https://cloudon-one.com/blog/terraform-workspaces-are-not-environments-i-spent-two-years-thinking-they-were/), [Why Terraform workspaces are the wrong tool for environments](https://handsonazure.com/terraform-workspaces-wrong-for-environments/)
- [Avoid the default profile (AWS)](https://dev.to/aws-builders/avoid-the-default-profile-1m2o), [aws-cli#3304](https://github.com/aws/aws-cli/issues/3304)
- [How Docker contexts let you work with multiple hosts](https://www.howtogeek.com/devops/how-docker-contexts-let-you-work-with-multiple-hosts/)
- [Error: Multiple apps in git remotes](https://github.com/thoughtbot/parity/issues/114)
- [cli/cli#6792](https://github.com/cli/cli/issues/6792), [#8659](https://github.com/cli/cli/issues/8659), [#9399](https://github.com/cli/cli/issues/9399), [#7216](https://github.com/cli/cli/issues/7216)
- [Managing gcloud like a champ, not a chump](https://medium.com/@briandealwis/managing-gcloud-like-a-champ-not-a-chump-d9fc22967387), [gcloud configurations](https://docs.cloud.google.com/sdk/docs/configurations)
- [Fly Launch overview](https://fly.io/docs/reference/fly-launch/), [fly.toml reference](https://fly.io/docs/reference/configuration/)
- [git push.default documentation](https://github.com/git/git/blob/master/Documentation/config/push.txt)
