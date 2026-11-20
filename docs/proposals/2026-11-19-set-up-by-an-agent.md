# Setting repose up by telling an agent to do it

An owner's idea, 19 November 2026, recorded at the end of the Mac mini
program and deliberately **not** built: it comes after what is already
queued. The owner's words:

> "Imagine we have a bunch of users already having Mac minis and they SSH
> into them. They should be able to record 'yo, I saw this repose thing' in
> the same way that I pasted an instruction from AWS to set up the CLI for
> the agent to use. It should be so easy to start and use the product via
> agents. Since it's just their local machine, they can just be like, yo
> Claude, I saw this, let's do it. We can even just have a copy-paste prompt."

## Why this is the right shape for this product

Everyone who would buy bring-your-own-hardware already has an agent on the
machine. That is the whole premise of the product: the customer runs coding
agents and wants them to keep running. So the customer installing repose is
already sitting in front of the thing that could install it, and asking them
to read a docs page and type commands is asking them to do by hand the one
job they bought the product to stop doing by hand.

It is also the cheapest acquisition path we have. The install is already one
command with one argument. What is missing is not capability; it is a form a
customer can hand to an agent without reading anything.

## Three different things, which should not be confused

1. **A paste-prompt.** One block of text a customer gives their agent: what
   repose is, what to install, where the credential comes from, and what to
   do when it fails. Costs a page and a release note. This is the thing the
   owner described and it is available today.
2. **A skill or plugin the agent installs**, so the knowledge is versioned
   with the product rather than pasted from a page that may be older than the
   CLI. The precedent is already in this repo's own working rules: a skill is
   how a method survives a session ending.
3. **An MCP server** exposing repose operations — create a project, start,
   stop, park, read logs — to any agent, on the customer's machine or ours.
   This is a product surface of its own, with its own authentication, its own
   docs and its own review. It is **not** a bigger version of (1); it answers
   a different question, which is how an agent *uses* repose rather than how
   an agent *installs* it.

Shipping order should be (1), then (2), then (3) only if customers ask for it,
because (1) and (2) cost almost nothing and (3) is a surface we would then
have to keep.

## The constraint that makes this harder than it looks, and it is already on the board

**An agent-driven install puts a secret in an agent's transcript.**

`repose host add --join <argument>` takes a 339-character bundle that carries
a WireGuard **private key**, and it arrives as a command-line argument. That
is already filed as a finding in its own right — ENG-42, the bundle's private
key on the command line, where it reaches the process table and the shell
history. An agent doing the install makes it strictly worse: the bundle also
lands in the agent's conversation log, which may be stored, synced or shared,
and the customer never sees it happen.

So this idea does not sit on top of the current install; **it requires the
join path to stop carrying a secret on the command line first.** Options, none
chosen here: a short-lived code the host exchanges for the bundle itself; the
bundle read from a file the agent never prints; a device-style flow where the
machine polls and the customer approves in the dashboard. The last is the only
one of the three where nothing secret is ever in the agent's context at all.

**This is the owner's own earlier concern arriving from a new direction.** On
the same day the owner said, of a secret appearing in a transcript: "I think
it will happen that they print a secret that was added in — explain it plainly
and in a few words." An agent-run install is the case where that stops being
an accident and becomes the design.

## What a session picking this up should do first

1. Read ENG-42 and decide the join path's shape, because every other part of
   this depends on it. That needs a decision entry.
2. Write the paste-prompt against the **real** published install path and run
   it, unmodified, with an agent on a machine that has never seen repose.
   Whatever the agent gets stuck on is the deliverable.
3. Only then write the docs page, and keep it to what the agent needs, not
   what a reader might like.

Not done: all of it. Filed as ENG-72.
