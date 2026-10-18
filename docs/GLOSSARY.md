# Glossary

Words used in a specific way across these docs. When a doc uses one of these,
it means exactly this.

**user**, a Logto identity, signed in with GitHub. Owns projects and a
card. One user, no teams.

**handle**, the user's short name, derived from their GitHub login,
lowercase `[a-z0-9-]`, unique. The second half of the SSH login name
(`todo-app.heracraft`).

**project**, one guest plus its volume, snapshots, config revisions,
secrets, events, and meter rows, owned by one user. Identified by
`(user, remote_url)` or `(user, name)`.

**slug**, the project name normalised to lowercase `[a-z0-9-]`, unique
per user. The first half of the SSH login name, the tmux session name, and
the directory name under `/home/dev`.

**guest**, the microVM that runs a project: a Cloud Hypervisor instance
on a host, booted from a NixOS closure in the host's store, with its own
thin volume, tap, and vsock. "Guest" never means a container.

**host**, an Azure VM (Intel `D64s_v7`) running NixOS and `hostd`, holding
many guests from many users. Has no public IP and dials out for everything.

**edge**, the small NixOS VM with a public IP that runs the SSH gateway and
the WireGuard hub. The only inbound path to guests.

**gateway**, the Go SSH relay on the edge that authenticates a user's
certificate, resolves the login name to a guest, and re-dials the guest's
sshd. Later also the HTTPS preview proxy.

**control plane**, the API, dashboard, Logto and Postgres, running as
containers on Coolify on an Ubuntu VM. Everything that is not a host, an
edge, or a guest.

**hostd**, the daemon on each host that executes guest lifecycle commands
from the API, runs builds, and reports samples and events.

**guestd**, the agent inside each guest, reachable only over vsock, that
freezes the filesystem, switches configurations, sets up tmux, samples
processes, and relays agent hooks.

**base**, the platform's NixOS module that every guest gets regardless of
user config: kernel, `dev` user, Docker, tmux, agents, browser, tools. Has a
version string (`2026.09.15`) and a changelog.

**fragment**, the user's own home-manager module, written by hand or
generated from the menu, merged on top of the base. Evaluated pure and
restricted, built on the host.

**menu**, the dashboard's catalog of packages and services a user can pick
without writing Nix. A selection is rendered into a fragment by the API.

**closure**, a Nix store path and everything it references. A guest's
*system closure* is the built NixOS system for base plus fragment; it lives
in the host's store and is a GC root while any revision references it.

**runner**, the package microvm.nix produces for a guest: a script that
launches Cloud Hypervisor with the right kernel, disks, shares and devices.
hostd builds one per guest and runs it as a transient systemd unit.

**revision**, one stored version of a project's config (fragment plus
base version), with a status of building, built, applied, or failed, and
the closure it produced.

**thin volume**, the guest's disk: an LVM thin-provisioned logical volume
on the host's pool, sized by class, resizable upward, snapshot-capable at
the block level.

**snapshot**, a point-in-time copy of a thin volume, taken with a guest
filesystem freeze, streamed compressed to Azure Blob, restorable onto any
host.

**principal**, the identity an SSH certificate asserts. User certificates
carry project ids as principals; a guest's sshd accepts only its own
project id. A certificate for one project cannot open another.

**certificate**, an OpenSSH certificate signed by the platform's User CA
(for users, 12 hours) or Host CA (for gateway and guest host keys). Issued
by the API, never by a host.

**op**, one asynchronous operation on a project (create, start, stop,
build, apply, snapshot, restore, destroy) with an id the CLI polls or
streams. Maps to one or more commands sent to hostd.

**command**, one message from the API to hostd over the gRPC stream, with
a `command_id` that makes it idempotent.

**sample**, the once-a-minute measurement hostd sends per guest: resources
from the host's view, signals and process samples from guestd's view.
Append-only, rolled up hourly into usage.

**signal**, a fact about what a guest is doing, recorded so the idle and
pricing policies can be designed later: SSH sessions, tmux clients, agent
windows and their state, Docker containers, listening ports, git state.

**process sample**, the per-process part of a sample: process name, CPU,
memory, network. Never arguments, environment, paths or terminal contents.
The privacy policy repeats this boundary verbatim.

**event**, something that happened in a project that the user should know
about: an agent completed, needs input, or errored; a base bump; a failed
build. Delivered by email and ntfy, listed in status and the dashboard.

**hook**, the mechanism by which an agent tells the platform about an
event: the agent's own hook system (Claude Code `Notification` and `Stop`,
Codex `notify`) running `repose-hook`, which posts to guestd's socket. For
agents without one, guestd's pane-idle heuristic stands in and is labelled
as such.

**class**, a guest size: `small` (1 vCPU, 4 GB), `large` (2, 8), `xl`
(4, 16). Fixed per project while running; changeable when stopped.

**cap**, the monthly maximum a project of a class is billed for guest-
hours: $49, $99, $199. Hourly rate is the cap divided by 720, so running
all month costs the cap and never more.

**meter**, one of the three measured quantities: guest-hours by class,
disk allocated, egress GB. Recorded in `usage_hours` hourly; since I-289
none is priced per hour (a plan buys memory, disk and egress, and only
egress past the allowance becomes an invoice line).

**held**, a project whose base updates are paused by the dashboard's
**Hold base updates** checkbox (`hold_base_updates`). It keeps its base
version until the box is unticked. Not the same as an abuse hold (I-239),
which stops a project from starting.

**workstream**, a chunk of the build that one agent or session can own
end to end, with named interfaces and its own checklist. Listed in
`workstreams/README.md`.

**interface**, a contract between workstreams, written in `interfaces/`,
that consumers code against and producers keep. Changing one is a decision.
