# Launch runbook (owner)

The owner's steps between this round's merge and the first tweet. The
code is on `main` and deploys itself when pushed (Coolify redeploys `api`,
`api-grpc` and `web`, `coolify.md` fact 16); the steps below are the ones
that need an account, a key or money, so an agent cannot do them.
Each step names the check that proves it. Dated 2026-09-27; the round is
DECISIONS I-289 to I-292.

## 0. Right after this push (five minutes)

An api with no `POLAR_ACCESS_TOKEN` has billing disabled: the
plan page says so, and the compute gate refuses every account that is not
`exempt` with `subscription_required`. Until Polar is configured, either
set `BILLING_ENFORCE=false` on both api apps (starts go through, nothing
is charged) or mark the accounts that must keep working exempt:
`repose-admin users exempt <handle>`. Remove `WAITLIST_PERCENT` from both
apps at the same time; the api ignores it and logs a warning (I-290).

## 1. Polar (about an hour)

Steps 2 to 5 were done in the sandbox (2026-10-08, DECISIONS I-604), where
prod's api ran Polar's sandbox organization `repose` (sandbox.polar.sh) and
checkout took Stripe's test card. Step 6 replaced that on 2026-10-19: prod
runs the production organization.

1. Create a Polar account at https://polar.sh and an organization for
   repose (a sandbox organization is made separately at
   https://sandbox.polar.sh). Polar is the merchant of record: it reviews
   the organization before payouts and wants pricing, terms, privacy and
   a refund policy on the domain. All four are live after the web deploy:
   `/#pricing`, `/terms`, `/privacy`, `/refunds`.
2. In the sandbox first: Settings, Developers, create an organization
   access token (`polar_oat_...`). Paste it into the api's Coolify
   environment as `POLAR_ACCESS_TOKEN` with `POLAR_ENVIRONMENT=sandbox`
   (both `api` and `api-grpc` apps; the two must match, the grpc app runs
   the ticks).
3. Run the bootstrap from this checkout with the token in the environment:

   ```
   POLAR_ACCESS_TOKEN=... POLAR_ENVIRONMENT=sandbox ops/polar/bootstrap.sh > /tmp/polar.env
   ```

   It creates the meter "Egress overage", the three products (Solo $49,
   Plus $89, Pro $169, each with the metered overage price at 5 cents a GB
   and a seven-day trial), Solo's founding discount ($10 off the first
   twelve charges, I-672), and the webhook endpoint at
   `https://api.repose.herakraft.co/v1/billing/webhook`; it sets the
   organization (one subscription per customer, trial abuse prevention,
   plan changes off in the customer portal, prices exclusive of tax) and
   prints the env block: `POLAR_ENVIRONMENT`, `POLAR_PRODUCT_SOLO`,
   `POLAR_PRODUCT_PLUS`, `POLAR_PRODUCT_PRO`, `POLAR_DISCOUNT_INTRO`,
   `POLAR_WEBHOOK_SECRET`. Paste it into both api apps and redeploy, then
   delete the file. It is idempotent: run it again and it creates nothing
   and prints the same ids.
4. Check Settings > Notifications in Polar: the bootstrap turned off the
   customer emails repose sends itself (trial ending, payment failed,
   cancellation, revoked, plan updated; I-291, I-604) and left the
   receipts, the subscription confirmation and the card-expiring reminder
   on. Leave it that way.
5. Prove the gate in the sandbox with `docs/ops/M4-GATE.md`: a checkout
   with Stripe's test card `4242 4242 4242 4242` on a throwaway account,
   the subscription appearing in `repose-admin billing show <handle>`, the
   trial ended at once so Polar charges the first period, a renewal on
   the failing card `4000 0000 0000 0341`, and an overage line with
   `repose-admin billing overage-now <handle>`.
6. Going live: create the production organization at https://polar.sh
   and its access token, then
   `POLAR_ACCESS_TOKEN=... POLAR_ENVIRONMENT=production ops/polar/bootstrap.sh --production --webhook-url https://api.repose.herakraft.co/v1/billing/webhook > /tmp/polar.env`,
   and paste the block with the token and `POLAR_ENVIRONMENT=production`
   into Coolify for `api` and `api-grpc`. Tests refuse a production
   environment.

   Run 2026-10-19: the organization `repose` holds the meter, the three
   products, the founding discount and the webhook, a rerun creates
   nothing, and `api` and `api-grpc` carry the block with
   `POLAR_ENVIRONMENT=production`. Production takes a card from
   2026-10-22. What is left is the first real charge. Run `M4-GATE.md`
   sections 2 to 5 against production with your own card, on a day you
   choose.

A promotion needs a code change: the checkout turns Polar's discount
code field off (I-604). With it turned back on, a code made in Polar's
dashboard (Products, Discounts; for example 50% off the first three
months, limited to 20 redemptions) can go in the tweet.

## 2. Resend (ten minutes)

1. https://resend.com, add and verify the domain `repose.herakraft.co`
   (the DNS records are in `AZURE-SETUP.md` "Resend"), create an API key.
2. Set `RESEND_API_KEY` on both api apps and redeploy. `NOTIFY_FROM` stays
   `repose <notify@repose.herakraft.co>`.
3. Check: sign in with a fresh GitHub account; the `welcome` email arrives.
   The dashboard's Settings page has **Send test**.

## 3. Seats and the hosts (money)

**The launch adds host-02 beside host-01; it resizes nothing** (DECISIONS
I-786, amending I-14 and I-39). The earlier plan set `host_size` to
`Standard_D64s_v7` and applied it to the one host,
which is a deallocate, resize and start with every guest on it stopped,
including the conductor's own machine. host-02 is a second host:
`Standard_D64s_v7`, class `azure-d64s-v7`, a 2048 GB data disk, running
alongside host-01, which stays a `D16s_v7` with its 512 GB and is retired
later by moving its projects across (step 5).

Why that shape, from
[../proposals/2026-10-14-host-cpu-capacity.md](../proposals/2026-10-14-host-cpu-capacity.md):
measurement on prod on 2026-10-14 says a `D64s_v7` delivers about 24 seats,
not 30. Its 64 Azure vCPUs are 32 physical cores, and past roughly one busy
guest vCPU per core typing in every guest on the host goes from 3 ms to
17-45 ms. The proposal argues for the `D<n>ls_v7` line, where a host's
memory ceiling and its CPU ceiling nearly coincide, over the `D64s_v7`
(I-39); I-784 inverted that, and the `D64s_v7` now delivers all 24 seats
its cores can place while a `D64ls_v7` delivers 14 of the same 24. The api
counts that ceiling itself (I-739, I-784), so step 2 needs no hand-set
number.

Quota and capacity, checked 2026-10-25: `Standard_D64s_v7` is available in
East US zones 1, 2 and 3 with no restriction. `Total Regional vCPUs` in East
US was raised from 65 to **350** on 2026-10-14, and two hosts plus the
control VM and the edge are 86 vCPUs: the regional count goes from 40 to 104
and the `Dsv7` family from 32 to 96 against its own 350. Size is bounded by
the credit balance rather than by quota, so everything up to a `D248ls_v7`
is creatable and only a `D372s_v7` would need a further raise of both.

The sizes are per host. `host_shapes` in `prod.tfvars` is a map of
objects keyed by host name; every attribute is optional and an attribute
left out inherits the fleet value (`host_size`, `host_class`,
`host_data_disk_gb`, `host_data_disk_iops`, `host_data_disk_mbps`):

```
host_shapes = {
  host-02 = {
    size         = "Standard_D64s_v7"
    class        = "azure-d64s-v7"
    data_disk_gb = 2048
  }
}
```

host-02's IOPS and throughput inherit 16000 and 600 MB/s. Raising them
for the four-times-larger disk costs money (about $5 a month per 1,000
IOPS and $40 per 1,000 MB/s above the free 3,000 and 125,
`infra/README.md` "Cost") and is yours to decide. host-01 names no entry
at all, so every value it has resolves to what it has today and the plan
shows no change for it. Read the plan for that before applying: a plan
that changes host-01 reboots a host with tenants on it.

0. WireGuard addresses. host-02 registers as the second host
   (`nextval('host_seq')`), so the api hands it 10.255.0.4 and the guest
   CIDR 10.64.4.0/22. Sequence 2 would have taken 10.255.0.3, which your
   monitoring server has held since I-170 — Grafana would have lost
   every scrape and the hosts' Loki writes would have gone to the new
   host. `hostmgr.allocate` steps over that address (I-782, amending
   I-359), host-01 keeps .2, and nothing on your server changes.

   Check after the host registers: `repose-admin hosts list` shows
   host-02 at 10.255.0.4, `wg show wg0 allowed-ips` on the edge lists no
   address twice, and Grafana still shows host-01 `up`.

1. Add host-02.

   **This step needs the work on `main` first; it cannot be done from a
   worktree.** `flake_path` in `prod.local.tfvars` is
   `/home/azureuser/projects/factory/nix`, the main checkout, not whatever
   worktree you ran `make` from, and nix resolves a bare path inside a git
   repository as a git flakeref, so only what is committed there is
   visible. That checkout today has no `nix/installer`, no
   `nix/hosts/prod.nix` and no `host-02` attribute.

   Today that stops `tofu plan`, on the preflight build's `flake_path`
   precondition, with a message naming the path and saying the likely cause
   is work that has not landed yet. It is the one gate here that catches
   the pre-merge state by name, and you never reach nix.

   Once the installer is on `main` that precondition passes for good, and a
   missing **host** attribute is caught by nothing stronger than a warning:
   the plan succeeds and the apply fails inside nix with `does not provide
   attribute ... Did you mean host-01?`, which reads like a broken flake
   rather than like being too early. That is the shape whoever adds host-03
   from a worktree will hit.

   So: let the release queue land this on `main`, and check that the
   checkout `flake_path` names has pulled it. A merged `main` this checkout
   has not fetched is indistinguishable from the un-merged case, down to
   the same `Did you mean host-01?`.

   1. `repose-admin hosts add --name host-02 --provider azure` prints a
      single-use token that expires in 24 hours.
   2. Nothing to do about the installer image, but know what it is. A
      host installs through the kexec image this repository builds, not
      the upstream asset nixos-anywhere would otherwise have the target
      download: that asset matches interfaces by MAC, and on an Azure
      size with accelerated networking the synthetic NIC and the SR-IOV
      VF share one MAC, so it DHCPs the VF as if it were a second NIC.
      That is what hung host-02's first install. `kexec_image` is unset
      in `prod.tfvars`, and unset means ours, so the fix does not depend
      on anyone remembering a variable on the apply line.

      The apply builds the image before it creates anything and orders
      both the edge and the hosts behind that build, so a flake that
      does not build costs no VM. Each install then builds the same
      attribute again, which is a cache hit, and uploads the tarball:
      526 MB through the edge per install (`infra/README.md`
      "Adding a host" step 1). Setting `kexec_image` by hand pins one
      image, which is for bisecting a bad install rather than for normal
      use, and a URL in the same variable would be fetched by host-02
      directly instead of crossing the edge.
   3. `nix/hosts/host-02.nix` and `nixosConfigurations.host-02` in
      `nix/flake.nix` must exist **in the checkout `flake_path` names**,
      which is the main one: nixos-anywhere installs from that path on the
      machine running `tofu`, and because it is a bare path inside a git
      repository nix reads only what is committed there, so an attribute
      that is uncommitted or on somebody else's branch is not there. The
      file imports
      `nix/hosts/prod.nix`, which carries everything every production host
      shares (the api address and server name, the platform CA, the
      bootstrap key, the base repository, the Blob account and snapshot
      identity, the idle pause and the freeze vault), and names on top of
      it only host-02's own freeze write rate. `host_flake_attrs` must
      name the host. Without that entry the install is the generic `host`
      configuration, which knows no api address, so the host comes up and
      never registers; the plan and the apply warn and neither stops.
   4. `host-02 = "<token>"` in `prod.local.tfvars`, and the `host_shapes`
      entry and `"host-02"` in `hosts` in `prod.tfvars`.
   5. `make -C infra plan ENV=prod`, and read it: host-02's VM, data
      disk, NIC and install, and **no change to host-01**.
   6. `make -C infra apply ENV=prod`. About fifteen minutes: the image
      upload, the kexec and the closure copy. `build_on` defaults to
      `auto`, so the closure builds on the machine running the apply and
      is copied over the edge, and so is the 526 MB installer image.

      `install_deadline`, 45 minutes by default, bounds three things
      separately, and each exits 124 with its own message: the preflight
      image build, this install's own build of the same attribute, and
      the nixos-anywhere run. A build that hits it says plainly that
      nothing was installed and no machine was touched, which is how you
      tell it from the install hitting it. Nothing else bounds the
      install: a
      target that never comes back holds the apply and this
      environment's only state lock until it is killed by hand, which on
      host-02's first attempt was 16 hours 56 minutes with no emergency
      apply possible against the edge, the control plane, DNS or the
      vaults.

      While it runs, the two commands that tell you whether it is
      working are `az vm boot-diagnostics get-boot-log` and
      `az monitor metrics list --interval PT1M`. Both are read-only and
      neither needs SSH. `infra/README.md` "Watching an install" has
      them with the numbers from host-02's failure, and reading it before
      the apply is cheaper than reading it during one.
   7. On the monitoring server, once host-02 is `ready`: correct its
      address in `ops/prometheus/targets/hosts.yml` and copy that file
      to `/etc/prometheus/repose/targets/` (`ops/README.md` step 2). No
      apply does this, and every host alert is scoped to the targets in
      that file, so until the copy nothing watches host-02 and
      `HostNotInTargets` warns. Check `up{host_id="host-02"}` is 1 for
      the `hosts`, `hostd` and `fluent-bit` jobs.

2. `SEATS_TOTAL` stays `0`, which derives the count from the ready hosts:
   per host the smaller of its 8 GB memory blocks after the reserve and
   what its physical cores can serve (I-739, I-784), summed over every
   `ready`, undrained host. That is **28** while both hosts are ready: 4
   from host-01's `D16s_v7` and 24 from host-02. Check: `repose-admin
   seats` prints total, held, free, waiting and the two ceilings per
   host; the landing page's pricing section shows "N seats left".

   **24 of those seats are free the moment host-02's first heartbeat
   lands, and the waitlist will mail all of them in one tick.** The
   Inviter runs every minute and invites the oldest waiting user while
   `Free > 0`, one seat each, with no rate limit; each invitation holds a
   seat for 72 hours whether the user acts or not. So decide before the
   apply, not after:

   - Set `SEATS_TOTAL` on both api apps (`api` and `api-grpc`) to the
     number you want sold now — it overrides the derivation — and raise
     it in steps as invitations convert. Back to `0` when you want the
     hosts' own figure again.
   - Or invite by hand at your own pace: `repose-admin waitlist admit
     --next N`.
   - `repose-admin waitlist list` before and after says who was invited
     and when each hold runs out.

3. New projects land on host-02 by themselves: the scheduler orders ready
   hosts by free memory (I-736, I-734 bound it by cores too), and
   host-02 has 24 seats of it against host-01's 4. So host-01 takes
   nothing new while host-02 has room, and the way to empty it is
   attrition plus step 5, not filling it further. Watch
   `HostCPUSaturated` (I-738) on host-01 meanwhile: placement reads its
   two `xl` and one `large` as 10 committed vCPUs of the 12 its 8 physical
   cores allow, but each of them is still running with the vCPU count it
   was started with (I-784), so the host is carrying 20 until each one
   next stops.

4. Your own account and any tester's: `repose-admin users exempt <handle>`
   passes the gate without a plan and holds no seat.

5. Later: move host-01's projects to host-02 and retire it. Not part of
   the launch; do it on a day you choose, with the tenants told.

   1. `repose-admin hosts drain host-01`: nothing new is placed there.
      The fleet's seat total drops by host-01's 4 at once, because the
      count sums `ready`, undrained hosts only.
   2. For each project, `repose-admin projects move <id> --to host-02`.
      Three things make this sharp:
      - **A project that is not running is restored from whatever
        snapshot already exists**, which may be a day old. `move` only
        takes a fresh snapshot when the project is `running` (it stops it
        with `snapshot: true` first). Start it, or `repose-admin projects
        snapshot <id>`, before moving a stopped one.
      - **`--to` is not capacity-checked.** A named target
        short-circuits the scheduler, so a move onto a host without the
        memory, cores or pool for it is accepted and fails later, on the
        host. Read `repose-admin hosts list` and move in an order that
        fits.
      - **Every moved project emails its owner** that it "was restored
        onto a new host from its latest snapshot" (`host_moved`), which
        on a planned migration reads as data loss. Tell the tenants
        first, or expect the replies.
      - **A moved project comes up logged out of Claude Code.** The
        login share is one file per user per host (I-278), so each
        owner types `claude` and logs in once on host-02.
   3. `repose-admin hosts retire host-01`. It refuses while the host
      holds **any** project, stopped ones included: `host-01 still holds
      N project(s); move them first`. `repose-admin projects list --host
      host-01` is the list to empty.
   4. Remove host-01 from `hosts` in `prod.tfvars` and apply. Its data
      disk carries `prevent_destroy`, so that plan fails on purpose;
      `infra/README.md` "Draining and destroying a host" has the step.

**One known limit of the bigger host: a restart does not save all of its
memory.** The freeze writes each running guest's memory to a thin volume
on the data disk and admits snapshots only while they fit in
`writeRateMBs` times `deadline`, which is capped at 300 s
(`nix/hosts/freeze.nix`); the guests that do not fit are stopped by the
shutdown and lose what they were running. host-01's measured
single-stream rate is 400 MB/s, so its 240 s default saves 96 GB and the
300 s cap 120 GB; the disk's provisioned 600 MB/s would be 180 GB at the
cap. A full host-02 holds 192 GB of guest memory (24 seats of 8 GB).
host-01 never met this: 64 GB less its 8 GB reserve is 56 GB of guest
memory at most, under even the 240 s figure. Measure host-02's
own rate on its pool (RUNBOOK "Guest freeze" step 3) and set
`writeRateMBs` in `nix/hosts/host-02.nix` from that -- it is the one value
that file carries, and it currently holds host-01's number unmeasured. The
only lever that raises the ceiling is the disk's provisioned MB/s, which
costs money. `GuestLeftUnfrozen` counts
what was lost.

## 4. Deploy order

1. Push `main`; watch CI and the three Coolify deploys. The api migrates
   itself (`0008_plans`, and any later one on `main`).
2. Publish the base for `repose browser` (Xvnc, the viewer page):
   `repose-admin base publish --rev <sha of main> --changelog "repose
   browser: one command, the screen follows your tab"`. Machines pick it
   up at their next start or the 04:00 UTC rebuild.
3. Tag the CLI: `git tag v0.1.21 && git push origin v0.1.21`; the release
   workflow builds the binaries and `repose` self-updates. The new plan
   sentences, hours in `repose status` and `repose browser` need it.
4. Try it end to end on a throwaway account: sign in, choose Solo in the
   sandbox, `repose run` on a small repo, `repose browser`, stop, `repose
   rm`, cancel the plan from the dashboard.

## 5. The tweet

The link is `https://repose.herakraft.co`. The landing page shows the two
plans and the live seat count, "Start with GitHub" leads to the dashboard,
the dashboard's Billing page to checkout or the waitlist. Watch:

- `repose-admin seats` and `repose-admin waitlist list` for the reading.
- Grafana's billing board: subscriptions by plan and status, gate refusals
  by reason, webhook results, overage charges.
- `repose-admin abuse list` for the first miners.

## What is not in this round

- The edge relay for the browser view and a dashboard button for it: the
  view still needs the CLI (`repose browser`), by design (I-292).
- Teams, annual plans, currencies other than USD (R5-6).
- Hetzner hosts (`proposals/2026-09-26-subscription-paddle-hetzner.md`).
  The prices are set for them; the hosts are further off than that reads. A
  host outside this VNet cannot register at all today, because the api
  address hostd registers to is a private VNet address and `Register` comes
  before the tunnel exists. `docs/workstreams/11-infra-opentofu.md`, "The
  portability wall", is what has to be decided first, and none of it is
  decided.
