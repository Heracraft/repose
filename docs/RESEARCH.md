# Research

Facts the design rests on, with citations. Gathered 2026-09-16 and 2026-09-17
by research agents during the design interview; each section names what was
verified and where. Update a section when a fact changes, and date the change.

## 1. Azure bare metal and nested virtualization

**Bare metal with hypervisor control does not exist on Azure for general use.**

- Azure Dedicated Host rents a physical server, but only Azure VMs can be
  placed on it; there is no host OS or hypervisor access, VMs must be one
  size family per host, and billing is per host.
  https://learn.microsoft.com/en-us/azure/virtual-machines/dedicated-hosts
- Azure BareMetal Infrastructure (formerly Large Instances) is the only "no
  virtualization layer, full root" product. Pricing is workload-specific and
  custom-quoted through a CSA/GBB engagement, in a handful of regions
  (Microsoft Q&A answer, April 2025).
  https://learn.microsoft.com/en-us/answers/questions/2245321/how-can-i-create-a-dedicated-physical-server-not-v
  It targets certified enterprise apps (SAP HANA, Oracle, Nutanix NC2); SAP
  HANA Large Instances is being decommissioned by 31 Dec 2025.
  https://learn.microsoft.com/en-us/azure/sap/large-instances/hana-available-skus
- NC2 on Azure is BareMetal running Nutanix AHV (KVM-based), but AOS
  abstracts kvm, virsh, qemu and libvirt away; minimum 3 nodes plus Nutanix
  licences. https://learn.microsoft.com/en-us/azure/nutanix/about-nc2-on-azure
- "AKS on bare metal" (Build 2026, June preview) runs on customer-owned Azure
  Local edge hardware, not in Azure datacentres.
  https://blog.aks.azure.com/2026/06/02/aks-baremetal-public-preview

**Nested virtualization on ordinary Azure VMs (2026).**

- Supported series per official feature tables: Dv5/Dsv5, Ddsv5, Ev5, Dsv6
  (Intel Emerald Rapids), Dasv6 (AMD Genoa), Lsv3. Check each size page's
  "Feature support" row.
  https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/ddsv5-series
  https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/dsv6-series
  https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/dasv6-series
  https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/storage-optimized/lsv3-series
- ARM (Dpsv5/Dpsv6 Cobalt): not supported.
  https://learn.microsoft.com/en-us/answers/questions/1166616/nested-virtualization-for-arm-architecture
- Trusted Launch (the portal default) and Confidential VMs disable nested
  virtualization. Create hosts with `--security-type Standard`.
  https://learn.microsoft.com/en-us/azure/virtual-machines/trusted-launch-faq
  https://learn.microsoft.com/en-us/azure/confidential-computing/confidential-vm-faq
- KVM inside works (`/dev/kvm` appears; Firecracker and Cloud Hypervisor open
  it). Microsoft's position: "Non-Microsoft virtualization on Hyper-V
  virtualization isn't supported" and is untested.
  https://learn.microsoft.com/en-us/virtualization/hyper-v-on-windows/user-guide/nested-virtualization
  A February 2026 guide quotes 5 to 15 percent CPU overhead and "not meant
  for production" nested VMs.
  https://oneuptime.com/blog/post/2026-02-16-how-to-enable-nested-virtualization-on-an-azure-virtual-machine/view
- AMD penalty: Cloud Hypervisor issue #4827 measured on Azure AMD about 50
  percent CPU loss with Firecracker and 80 to 90 percent with Cloud
  Hypervisor and QEMU, versus about 10 percent on Intel; "Hyper-V not
  optimised for nested VMs on EPYC". An Azure fix was promised December 2022;
  the issue is still open. Prefer Intel SKUs and benchmark before committing.
  https://github.com/cloud-hypervisor/cloud-hypervisor/issues/4827
- Evidence it is production-viable: Microsoft's own AKS Pod Sandboxing is
  Kata plus Cloud Hypervisor nested inside Azure VMs ("any Gen2 size that
  supports nested virtualization"), GA docs updated November 2025.
  https://learn.microsoft.com/en-us/azure/aks/use-pod-sandboxing
  Northflank runs Kata plus Cloud Hypervisor sandboxes, BYOC on Azure among
  others, falling back to gVisor where nested virtualization is unavailable
  (May 2026). https://northflank.com/blog/firecracker-vs-cloud-hypervisor
  actuated (Firecracker CI runners) lists Azure nested-virt VMs as a
  supported "lowest cost, mid-level performance" tier versus bare metal.
  https://docs.actuated.com/provision-server/
  General guidance: nested setups "can look healthy and be quietly slow";
  measure the workload.
  https://www.pandastack.ai/blog/bare-metal-vs-cloud-vms-for-firecracker/

## 2. Azure host pricing (East US, Linux, retail price API, September 2026)

| SKU | vCPU / RAM | On-demand /h | 1-yr RI /h equiv. | 3-yr RI /h equiv. |
|---|---|---|---|---|
| D64s_v5 (Intel) | 64 / 256 GB | $3.072 | $1.894 | $1.212 |
| D64s_v6 (Intel EMR) | 64 / 256 GB | $3.226 | $2.00 | $1.26 |
| D64ds_v5 (+2.4 TB local SSD) | 64 / 256 GB | $3.616 | not quoted | not quoted |
| E64s_v5 | 64 / 512 GB | $4.032 | $2.378 | $1.591 |
| L64s_v3 (8x1.92 TB NVMe) | 64 / 512 GB | $5.568 | ~$3.57 | ~$2.33 |

Sources:
https://prices.azure.com/api/retail/prices?$filter=armSkuName%20eq%20%27Standard_D64s_v5%27%20and%20armRegionName%20eq%20%27eastus%27
https://prices.azure.com/api/retail/prices?$filter=armSkuName%20eq%20%27Standard_E64s_v5%27%20and%20armRegionName%20eq%20%27eastus%27
https://prices.azure.com/api/retail/prices?$filter=armSkuName%20eq%20%27Standard_D64s_v6%27%20and%20armRegionName%20eq%20%27eastus%27

Notes: Ddsv5 has a 2,400 GiB local disk at 300k IOPS but it is ephemeral.
Dsv6 has no local disk. Lsv3 is Ice Lake with 8x NVMe and about 50 percent
more expensive. Spot for D64s_v5 was about $0.65/h. The design (DECISIONS
R2-17) chose D64s_v5 with a managed data disk; D64ds_v5 is the candidate if
hot overlay data moves to local SSD later. The dev box this repo is developed
on is a `Standard_D8alds_v7` (AMD) in `eastus`, which is the family to avoid
for hosts.

### 2a. What this subscription may actually deploy in East US (2026-09-20, DECISIONS I-39)

Checked with `az vm list-skus -l eastus --all` during the M1 bring-up
after the first apply failed with `SkuNotAvailable` on the edge:

- `Standard_D2s_v5`, `D4s_v5`, `D16s_v5`, `D2s_v6`, `D16s_v6` and
  `D2as_v5` all carry `NotAvailableForSubscription` restrictions of type
  `Location` and `Zone` (zones 1, 2, 3) in `eastus`. The quota for
  `standardDSv5Family` is 65 vCPUs, so this is a SKU restriction on the
  subscription, not a quota. The dev box's own family (`Dalsv7`) is
  unrestricted.
- Unrestricted Intel general-purpose families in `eastus`: `Dsv7`/`Ddsv7`
  (Xeon 6 Granite Rapids, all three zones, quota 350 vCPUs in
  `StandardDsv7Family`), and the network-optimised `Dnsv6`/`Dndsv6`.
  Confidential (`DC*`) and AMD (`Da*`) families are also open.
- `Standard_D16s_v5` is unrestricted for this subscription only in
  `swedencentral`, `koreacentral`, `southafricanorth`, `eastasia`,
  `israelcentral`, `denmarkeast`, `chilecentral`, `newzealandnorth`,
  `indiasouthcentral`, `saudiarabiaeast`, `taiwannorth` and a set of
  non-GA region codes.
- Dsv7 feature table: nested virtualization **Supported**, Premium SSD v2
  supported, no local temp disk, NVMe-only (the OS disk is `nvme0n1` on the
  cached controller; uncached data disks are on a second controller,
  `nvme1n1` first). Retail prices, `eastus`, Linux, on-demand
  (2026-09-20): `Standard_D16s_v7` $1.058/h (about $772/month against
  $561 for `D16s_v5`), `Standard_D2s_v7` $0.132/h (about $96 against $70).
  https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/dsv7-series
  https://learn.microsoft.com/en-us/azure/virtual-machines/enable-nvme-remote-faqs

## 3. AWS

**EC2 bare metal (.metal).** Ordinary on-demand types with no Nitro
hypervisor, direct `/dev/kvm`, no nested penalty; AWS recommends metal for
"performance sensitive or strict latency" nested workloads.
https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html
GA families: m6i/c6i/r6i.metal (128 vCPU), i4i.metal, and the October 2023
"half-size" metals c7i/m7i/r7i.metal-24xl (96 vCPU) and -48xl (192),
r7iz.metal-16xl (64) and -32xl. AMD metal is only m7a/c7a/r7a.metal-48xl
(192 vCPU). Smallest x86 metal is 64 to 96 vCPU.
https://aws.amazon.com/about-aws/whats-new/2023/10/new-amazon-ec2-bare-metal-instances

| Instance | vCPU / RAM | On-demand (us-east-1) | 1-yr RI no-upfront | Monthly OD |
|---|---|---|---|---|
| c7i.metal-24xl | 96 / 192 GB | $4.284/h | $2.834/h | $3,127 |
| m7i.metal-24xl | 96 / 384 GB | $4.838/h | $3.201/h ($2,336/mo) | $3,532 |
| m6i.metal | 128 / 512 GB | $6.144/h | $4.064/h | $4,485 |
| m7a.metal-48xl | 192 / 768 GB | $11.128/h | $7.361/h | $8,124 |

https://instances.vantage.sh/aws/ec2/c7i.metal-24xl
https://instances.vantage.sh/aws/ec2/m7i.metal-24xl
https://aws-pricing.com/m7i.metal-24xl.html
https://instances.vantage.sh/aws/ec2/m6i.metal
https://instances.vantage.sh/aws/ec2/m7a.metal-48xl

Spot m7i.metal-24xl was about $1.21/h. Metal on m7i/c7i is EBS-only (no local
NVMe).

**Quota gotcha.** Metal counts under "Running On-Demand Standard (A, C, D, H,
I, M, R, T, Z)" whose default for a new account is 5 vCPUs; a single 96-vCPU
metal instance needs a quota increase, and staged requests are approved
faster than large jumps.
https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-on-demand-instances.html
https://blog.ronin.cloud/how-to-request-an-ec2-quota-increase-on-aws-and-get-it-approved-faster/

**Nested virtualization on regular EC2 changed in February 2026.** A
`NestedVirtualization=enabled` CPU option (`--cpu-options`), no extra cost,
KVM and Hyper-V as L1, launched on C8i/M8i/R8i; docs list
M7i/M7i-flex/M8i(d)/C7i(-flex)/C8i(d)/R7i/R7iz/R8i/X8i/I7i/I7ie. Intel only,
no AMD or Graviton. Firecracker and Cloud Hypervisor work on it.
https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual
https://www.theregister.com/2026/02/17/nested_virtualization_aws_ec2/

**Credits.** AWS Activate: Founders $1k (up to $5k), Portfolio up to $200k;
credits cover EC2 broadly, expire in 1 to 2 years, exclude RI and Savings
Plan upfront fees, Marketplace and ProServe. No published exclusion of
.metal types.
https://aws.amazon.com/aws-startups/learn/everything-you-need-to-know-about-aws-activate-credits/

## 4. Other bare-metal providers and vendor statements

| Provider / plan | Hardware | Price | Notes |
|---|---|---|---|
| Hetzner AX162-2 (was AX162-R) | EPYC 9454P 48c/96t, 256 GB, 2x1.92 TB NVMe | €199/mo at launch (+€79 setup); €842.30/mo (+€419 setup), $992.10, for orders from 15 June 2026 (I-642) | Robot webservice API for ordering once enabled |
| Latitude.sh c3.large.x86 | 24c, 256 GB, 2x1.9 TB NVMe | $0.68/h, about $496/mo | hourly billing, API and Terraform |
| Latitude.sh c4.metal.large | 96c Zen 5, 384 GB | $4.00/h, about $2,920/mo | |
| OVHcloud Advance-5 | EPYC 8224P 24c/48t, up to 576 GB NVMe | about $504/mo | OVH API |
| Vultr Bare Metal | entry plans | from $120 to $185/mo | hourly, API; 256 GB price not verified |
| Equinix Metal | | | sunset 30 June 2026 |

https://www.hetzner.com/pressroom/new-ax162/
https://docs.hetzner.com/general/infrastructure-and-availability/price-adjustment/
https://robot.hetzner.com/doc/webservice/en.html
https://docs.hetzner.com/robot/dedicated-server/robot-interfaces/
https://www.latitude.sh/pricing
https://us.ovhcloud.com/bare-metal/prices/
https://blogs.vultr.com/introducing-a-new-vultr-bare-metal-plan-for-185-per-month
https://www.datacenterdynamics.com/en/news/equinix-to-kill-off-metal-by-june-2026/
https://www.latitude.sh/blog/equinix-metal-sunset-how-to-reduce-the-migration-burden

Vendor statements:

- Fly.io: "you need bare metal servers to efficiently do lightweight
  virtualization; you want KVM but without nested virtualization... You're
  probably not going to shell out for EC2 metal instances just to get some
  extra isolation." https://fly.io/blog/sandboxing-and-workload-isolation/
  "Our Workers are now bare-metal servers, not EC2 VMs."
  https://fly.io/blog/the-serverless-server/ Fleet is 8 to 32 core, 32 to
  256 GB physical boxes. https://fly.io/docs/reference/architecture/
- Northflank: Kata plus Cloud Hypervisor primary, Firecracker and gVisor
  where nested virtualization is unavailable; "On AWS, this means running on
  .metal instances or instances with nested virtualization enabled."
  https://northflank.com/blog/what-is-aws-firecracker
  https://northflank.com/blog/gpu-sandboxes
- Modal avoids KVM entirely (gVisor), so it runs on ordinary cloud VMs.
  https://northflank.com/blog/e2b-vs-modal
- E2B self-hosting requires hardware KVM (bare metal or nested-virt
  instances); community guides steer production to Hetzner dedicated.
  https://bex.co/blog/2026/07/12/daytona-vs-e2b-self-hosted-ai-sandbox
  https://openmetal.io/resources/blog/self-hosting-an-ai-agent-code-execution-sandbox-on-bare-metal/
- PandaStack: EC2 .metal is "genuinely bare metal that happens to bill
  hourly" at premium per-core pricing; put the floor on rented metal, the
  spike on cloud.
  https://www.pandastack.ai/blog/bare-metal-vs-cloud-vms-for-firecracker/

Bottom line for about 25 guests at 2 to 4 vCPU and 4 to 8 GB: AWS metal is
about $3.1k to $3.5k/mo on demand ($2.1k to $2.3k with a 1-year RI), Azure
D64s_v5 about $2.2k/mo, Hetzner €842.30 (after its 15 June 2026
repricing; about €230 before it) or Latitude about $500 for equivalent
hardware, a 2 to 7x gap.

## 5. Firecracker vs Cloud Hypervisor

| | Firecracker 1.17 | Cloud Hypervisor v53 |
|---|---|---|
| virtio-fs | No (devices: net, block, vsock, balloon, serial); host FS sharing issue #1180 closed 2020 unresolved | Yes via vhost-user virtiofsd, including DAX |
| vsock | Yes | Yes |
| Snapshot/restore | Full snapshots production, diff in dev preview; vsock reset on restore | Yes; virtio-fs snapshot/restore only filled out in v52 (#7937), earlier hung (#6931) |
| Memory hotplug | virtio-mem, added 1.14, developer preview | ACPI (grow only) or virtio-mem (grow/shrink), long-standing |
| Runs on MSHV | No, KVM only | Yes (KVM or MSHV) |

https://github.com/firecracker-microvm/firecracker/blob/main/FAQ.md
https://github.com/firecracker-microvm/firecracker/issues/1180
https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md
https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md
https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/release-notes.md
https://github.com/cloud-hypervisor/cloud-hypervisor/issues/6931
https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/memory.md

**microvm.nix** supports both. Its hypervisor table says Firecracker has "no
9p/virtiofs shares" and cloud-hypervisor "no 9p shares" (virtiofs OK). On
Firecracker the guest's closure is baked into a per-VM erofs/squashfs store
disk; on Cloud Hypervisor `/nix/store` is shared read-only through virtiofsd.
For a host-shared store, Cloud Hypervisor is the fit.
https://github.com/microvm-nix/microvm.nix
https://microvm-nix.github.io/microvm.nix/shares.html

## 6. Coding agents and supporting packages in nixpkgs (nixos-unstable, 2026-09-16)

| Agent | Attribute | Unfree | nixpkgs version | Upstream latest | Lag |
|---|---|---|---|---|---|
| Claude Code | `claude-code` | Yes | 2.1.245 | 2.1.273 (npm `@anthropic-ai/claude-code`) | about 28 patch releases, days to weeks |
| opencode | `opencode` | No (MIT) | 1.18.30 (package.nix; mynixos index showed 1.18.21) | v1.18.31 (14 Sep) | about 1 patch |
| OpenAI Codex CLI | `codex` | No (Apache-2.0), built from `codex-rs` via cargo | 0.154.0 | rust-v0.154.0 | current |
| Gemini CLI | `gemini-cli` | No (Apache-2.0) | 0.47.0 | v0.60.0 | about 13 minors |
| pi | `pi-coding-agent` | No (MIT) | 0.84.2 (0.84.4 bump PR open) | 0.85.1 (npm) | about 1 minor |

- Claude Code is packaged via `stdenv.mkDerivation` from Anthropic's binary
  manifest (`manifest.zst.json`), not `buildNpmPackage`; needs `allowUnfree`.
  Community flakes tracking upstream hourly: sadjow/claude-code-nix,
  ryoppippi/nix-claude-code.
- Codex: nixpkgs builds from source; community binary flakes:
  SecBear/codex-nix, sadjow/codex-cli-nix.
- pi is Mario Zechner's (badlogic) terminal coding agent from the
  `badlogic/pi-mono` monorepo (packages pi-ai, pi-agent-core, pi-tui,
  pi-coding-agent). The npm scope moved from `@mariozechner/pi-coding-agent`
  to `@earendil-works/pi-coding-agent`; homepage pi.dev. Other installs:
  `npm install -g --ignore-scripts @earendil-works/pi-coding-agent`, `curl
  -fsSL https://pi.dev/install.sh | sh`, or bun-compiled standalone binaries
  from GitHub releases. nixpkgs PR #558575 switched to repackaging upstream's
  bun binary. Home Manager has `programs.pi-coding-agent`; community flakes:
  sadjow/pi-nix, lukasl-dev/pi.nix, peedrr/nix-pi-coding-agent. Issue #701
  (missing lockfile) is closed.
- Supporting packages: `playwright-driver` 1.63.0 exposes
  `playwright-driver.browsers` (link farm of chromium,
  chromium-headless-shell, firefox, webkit; `withChromiumHeadlessShell`
  default true; `browsers-chromium` preset); `chromium` 152.0.7977.64
  (headless works with `--headless`); `virtiofsd` 1.14.0; `cloud-hypervisor`
  53.0.

Update 2026-09-20 (workstream 12): the overlay no longer takes any agent
from nixpkgs (DECISIONS I-46). Upstream release artefacts for
`x86_64-linux`, as pinned in `nix/overlay/agents/versions.json`:

| Agent | Source | Artefact | Linking |
|---|---|---|---|
| Claude Code 2.1.278 | `downloads.claude.ai/claude-code-releases/<v>/linux-x64/claude`, version from `.../latest`, sha256 in `<v>/manifest.json` | one bun-compiled ELF, 234 MB | dynamic (`autoPatchelfHook`, alsa-lib) |
| opencode 1.18.31 | GitHub `anomalyco/opencode` release `opencode-linux-x64.tar.gz` | one bun-compiled ELF | dynamic |
| Codex 0.155.1 | GitHub `openai/codex` release `rust-v<v>`, `codex-x86_64-unknown-linux-musl.tar.gz` | one static binary | static |
| Gemini CLI 0.60.0 | npm `@google/gemini-cli` tarball | `bundle/gemini.js` plus chunks, no dependencies | node 24 |
| pi 0.86.0 | GitHub `earendil-works/pi` release `pi-linux-x64.tar.gz` | bun-compiled ELF plus `package.json`, themes, docs, a wasm module it reads at run time (so the whole tree is installed; `pi --version` prints 0.0.0 without them) | dynamic |

Each printed its version from the built package on the dev box on
2026-09-20. nixpkgs's `gemini-cli` is marked for removal because Google
moved unpaid and AI Pro/Ultra accounts to "Antigravity CLI"; the CLI
itself still ships and works with an API key, which is how a guest
authenticates (`GEMINI_API_KEY` as a named secret).

https://search.nixos.org/packages?channel=unstable&show=claude-code
https://mynixos.com/nixpkgs/package/claude-code
https://github.com/NixOS/nixpkgs/blob/nixos-unstable/pkgs/by-name/cl/claude-code/package.nix
https://github.com/NixOS/nixpkgs/blob/nixos-unstable/pkgs/by-name/op/opencode/package.nix
https://github.com/anomalyco/opencode/releases/latest
https://github.com/NixOS/nixpkgs/blob/nixos-unstable/pkgs/by-name/co/codex/package.nix
https://github.com/openai/codex/releases/latest
https://github.com/NixOS/nixpkgs/blob/nixos-unstable/pkgs/by-name/ge/gemini-cli/package.nix
https://github.com/google-gemini/gemini-cli/releases/latest
https://mynixos.com/nixpkgs/package/pi-coding-agent
https://github.com/NixOS/nixpkgs/pull/558575
https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/README.md
https://github.com/badlogic/pi-mono/issues/701
https://github.com/nix-community/home-manager/blob/master/modules/programs/pi-coding-agent.nix
https://github.com/NixOS/nixpkgs/blob/nixos-unstable/pkgs/development/web/playwright/driver.nix
https://mynixos.com/nixpkgs/package/chromium
https://github.com/NixOS/nixpkgs/blob/nixos-unstable/pkgs/by-name/vi/virtiofsd/package.nix
https://github.com/NixOS/nixpkgs/blob/nixos-unstable/pkgs/by-name/cl/cloud-hypervisor/package.nix

## 7. What agents lose in a remote headless VM

### Browser use

- Playwright MCP (`@playwright/mcp`): headed by default, `--headless` for
  servers, `--cdp-endpoint` to attach to a browser elsewhere, `--port` to run
  as a standalone SSE/HTTP server on another host; the official Docker image
  is headless-Chromium only. https://github.com/microsoft/playwright-mcp
- chrome-devtools-mcp (Google, Puppeteer-based): `--headless`, or point at a
  pre-launched Chrome via `CHROME_CDP_URL` / `--browser-url`; `--isolated`
  for per-session profiles.
  https://github.com/ChromeDevTools/chrome-devtools-mcp/
- vercel-labs/agent-browser: CLI over CDP, `--connect` accepts a port, HTTP
  or WS URL, so it can drive a remote Chrome.
  https://github.com/vercel-labs/agent-browser
- Claude in Chrome / `claude --chrome`: needs the extension in a visible
  Chrome, `/login` subscription auth (refuses API key or setup-token), not
  supported in WSL; connects via a relay at `bridge.claudeusercontent.com`.
  Unusable on a headless VM unless the laptop's extension is bridged (open
  request https://github.com/anthropics/claude-code/issues/51844).
  https://code.claude.com/docs/en/chrome
- Cloud-browser alternative: Browserbase plugin for Claude Code.
  https://github.com/browserbase/claude-code-plugin

Headless Chromium on minimal Linux (Puppeteer's Debian list):
`ca-certificates fonts-liberation libasound2 libatk-bridge2.0-0 libatk1.0-0
libc6 libcairo2 libcups2 libdbus-1-3 libexpat1 libfontconfig1 libgbm1 libgcc1
libglib2.0-0 libgtk-3-0 libnspr4 libnss3 libpango-1.0-0 libpangocairo-1.0-0
libstdc++6 libx11-6 libx11-xcb1 libxcb1 libxcomposite1 libxcursor1
libxdamage1 libxext6 libxfixes3 libxi6 libxrandr2 libxrender1 libxss1
libxtst6 lsb-release wget xdg-utils`. Ubuntu 24.04 renames several
(`libasound2t64` and similar). Containers without user namespaces need
`--no-sandbox`. On Nix, use `pkgs.playwright-driver.browsers` or
`pkgs.chromium` instead of `npx playwright install`.
https://pptr.dev/troubleshooting
https://stevefenton.co.uk/blog/2025/09/playwright-insteall-github-actions/

Headed browsers: headless covers most agent work (screenshots, console,
DOM). People add a headed browser to watch or intervene (logins, CAPTCHAs):
`xvfb-run`, the devcontainer `desktop-lite` feature exposing noVNC on port
6080, or images like `xtr-dev/mcp-playwright-novnc`.
https://playwright.dev/docs/docker
https://github.com/xtr-dev/mcp-playwright-novnc

Remote-dev products:

- Codespaces and Gitpod: devcontainer plus port forwarding and preview URLs;
  Gitpod documents reverse-forwarding a laptop Playwright server into the
  workspace. https://github.com/gitpod-samples/playwright-local-server
- Coder: registry modules for Claude Code and Codex plus a `portabledesktop`
  lightweight desktop module. https://registry.coder.com/modules
- Daytona: Computer Use API (desktop, screenshot, click, type).
  https://www.daytona.io/docs/en/computer-use/
- E2B: separate desktop template with a noVNC stream. https://e2b.dev/
- Modal: sandboxes with Playwright and GPUs.
  https://modal.com/resources/best-sandboxes-browser-web-agent-rl-environments
- Claude Code cloud sessions: image ships `chromedriver`; network is
  allowlisted; the base image cannot be replaced, only extended via a setup
  script. https://code.claude.com/docs/en/cloud-environments
- Codex cloud: internet off during the agent phase by default, allowlist
  per environment. https://learn.chatgpt.com/docs/cloud/internet-access
- Managed Agents: browser via Browserbase integration.
  https://docs.browserbase.com/integrations/anthropic/managed-agents/introduction

### MCP servers

- Laptop-bound (stdio, OS-level): Apple Notes/AppleScript servers, Xcode
  (`xcrun mcpbridge`, XcodeMCP), iMessage, desktop automation, Claude in
  Chrome, filesystem servers pointed at laptop paths.
  https://github.com/lapfelix/xcodemcp
  https://www.usecarly.com/blog/claude-apple-notes-integration/
- Remote-friendly: HTTP servers (Notion, Stripe, Sentry, GitHub, Linear) and
  stdio wrappers around APIs (need `npx` plus a token).
- Claude Code storage: user and local scopes in `~/.claude.json` (local
  keyed under `projects["/abs/path"].mcpServers`, so paths must match on the
  VM); project scope in `.mcp.json`; `${VAR}` expansion; OAuth for remote
  servers via `claude mcp login <name> --no-browser`; headless `-p` runs
  cannot complete OAuth without tool search.
  https://code.claude.com/docs/en/mcp
- Forwarding a laptop stdio server: wrap with `mcp-proxy` (sparfenyuk Python
  or punkpeye TS) as HTTP on the laptop, `ssh -R 8123:localhost:8123 vm`,
  then `claude mcp add --transport http notes http://localhost:8123/mcp` on
  the VM. Playwright MCP's `--port` does this natively.
  https://github.com/sparfenyuk/mcp-proxy
  https://github.com/punkpeye/mcp-proxy

### Credentials

| Tool | Path | Copying works? |
|---|---|---|
| Claude Code | `~/.claude/.credentials.json` (0600; macOS uses Keychain; `CLAUDE_CONFIG_DIR` relocates) | Flaky: copied refresh tokens do not refresh (issue closed "not planned"). Use `claude` login (prints a paste code over SSH) or `claude setup-token` for `CLAUDE_CODE_OAUTH_TOKEN` (1 year; no Remote Control, connectors, or Chrome) |
| Codex CLI | `~/.codex/auth.json` (or keyring) | Yes, officially documented `ssh ... cat > ~/.codex/auth.json`; or `codex login --device-auth` (admin must enable) |
| Gemini CLI | `~/.gemini/oauth_creds.json` | OAuth over SSH is painful; prefer `GEMINI_API_KEY` or Vertex ADC |
| opencode | `~/.local/share/opencode/auth.json` (XDG) | Yes; `opencode auth login` |
| gh | `~/.config/gh/hosts.yml` (`GH_CONFIG_DIR`); `GH_TOKEN` overrides | Yes |

https://code.claude.com/docs/en/authentication
https://github.com/anthropics/claude-code/issues/21765
https://learn.chatgpt.com/docs/auth
https://github.com/openai/codex/issues/9253
https://github.com/google-gemini/gemini-cli/issues/1696
https://opencode.ai/docs/providers/
https://cli.github.com/manual/gh_help_environment

Policy: OAuth is for "ordinary use of Claude Code"; hosting Claude Code in
sandboxes requires Commercial Terms, an unmodified binary, and each user
authenticating with their own credentials; a user signing in with their own
subscription on such a platform is explicitly permitted. Third-party apps
using subscription OAuth were banned in February 2026.
https://code.claude.com/docs/en/legal-and-compliance

### Notifications and check-back

- Claude Code hooks: `Notification` (matchers `permission_prompt`,
  `idle_prompt`, `agent_needs_input`, `agent_completed`), `Stop`,
  `StopFailure`, `SessionEnd`; payload includes `session_id`, `cwd`,
  `transcript_path`. https://code.claude.com/docs/en/hooks
- Community: an ntfy one-line curl hook; `tap-to-tmux` (notification
  deep-links to a tmux window); `claude-notifications-go` (Slack, Telegram,
  ntfy, PagerDuty). https://github.com/flavio87/tap-to-tmux
  https://github.com/777genius/claude-notifications-go
- Remote Control (`claude --remote-control` inside tmux): phone or browser
  view, mobile push when done or blocked, permission approval from the
  phone; needs claude.ai `/login` (not a setup token); the process must
  stay alive. https://code.claude.com/docs/en/remote-control
- Channels: Telegram and Discord plugins push messages into a running
  session and can relay permission prompts.
  https://code.claude.com/docs/en/channels
- Cloud sessions: questions wait until environment expiry; idle VMs are
  reclaimed. https://code.claude.com/docs/en/claude-code-on-the-web

### Other gaps

- Docker in a microVM works (namespaces, no KVM needed) but the guest kernel
  must have overlayfs, cgroups v2 and netfilter; nested overlay on overlay
  fails; no `/dev/kvm` inside.
  https://www.pandastack.ai/blog/firecracker-nested-virtualization-explained/
  https://wundergraph.com/blog/the_builder_the_road_from_commit_to_production_in_13s
- inotify: raise `fs.inotify.max_user_watches` and `max_user_instances` in
  the VM's sysctl (cannot be set per container).
  https://www.suse.com/support/kb/doc/?id=000020048
- Git push: cloud products keep git credentials outside the sandbox via a
  proxy; on a VM you need `ssh -A` agent forwarding, a deploy key, or
  `GH_TOKEN` plus `gh auth setup-git`.
- Ports and previews: Codespaces and Gitpod auto-forward; on Tailscale use
  `tailscale serve` or Funnel, or SSH `-L`.
- GPU: absent in Firecracker; Modal and E2B offer GPU sandboxes.
- Root: Claude Code refuses `--dangerously-skip-permissions` as root; run as
  an unprivileged user with passwordless sudo.
- Clipboard: tmux OSC52 (`set -g set-clipboard on`) for copy-out.
- IDE: VS Code Remote-SSH, Cursor and Zed all attach over SSH.
- Repo clone time: Claude cloud caches environments; Codex uses setup
  scripts; a persistent VM sidesteps this.
- TZ and locale: set `TZ` and `LANG=C.UTF-8` so tmux and agent TUIs render
  and timestamps match the developer.

## 8. Coolify

Capabilities and limits as of September 2026:

- Zero-downtime deploys: yes for single-container apps (Nixpacks, Railpack,
  Static, Dockerfile, Docker Image). Coolify starts the new container
  alongside the old, waits for the health check, then removes the old one.
  It silently falls back to stop-then-start when there is no passing health
  check (Coolify's dashboard check or Dockerfile `HEALTHCHECK`; HTTP checks
  need curl or wget in the image), a host port mapping ("Ports Mappings"),
  Consistent Container Names or a custom container name, a custom `--ip`, or
  PR previews. https://coolify.io/docs/knowledge-base/rolling-updates
  https://coolify.io/docs/knowledge-base/health-checks
- Docker Compose resources are explicitly excluded: they go through `docker
  compose up -d` with a downtime window; a feature request has been open
  since October 2024.
  https://bex.co/blog/2026/09/04/coolify-docker-compose-zero-downtime-gap
  https://learnwithhasan.com/guide/coolify-zero-downtime-deployment/
- Raw TCP ports: yes, via "Ports Mappings" (`host:container`), plain Docker
  port publishing that bypasses Traefik and Caddy; the proxy only owns 80
  and 443. Mappings bind `0.0.0.0` (firewall yourself), and a mapped port
  disables rolling updates for that app.
  https://coolify.io/docs/knowledge-base/proxy/traefik/overview
  https://next.coolify.io/docs/core/networking-in-coolify
  https://github.com/coollabsio/coolify/issues/4749
- Multi-server: one instance manages many servers over SSH; an app can be
  built on a primary and deployed to attached servers, with an external load
  balancer supplied by you; apps with persistent storage or Compose cannot
  multi-server. No native N-replica-per-server setting; Docker Swarm is
  deprecated. https://coolify.io/docs/knowledge-base/internal/scalability
  https://github.com/coollabsio/coolify/discussions/3862
- Database backups: scheduled cron dumps (Postgres, MySQL, MariaDB, MongoDB,
  ClickHouse) with upload to S3-compatible storage and separate local and S3
  retention; v4.3 added volume backups. Azure Blob is not supported
  natively; the community workaround is an s3proxy container.
  https://coolify.io/docs/databases/backups
  https://coolify.io/changelog
  https://docs.vultr.com/how-to-back-up-and-restore-postgresql-databases-to-s3-compatible-storage-in-coolify
  https://github.com/coollabsio/coolify/discussions/7570
- v5: announced April 2025 as a PHP refactor plus Vue/Inertia UI, not a Go
  rewrite; no public release date as of September 2026. v4.0.0 stable
  shipped April 2026 and v4.4-rc.1 in August 2026.
  https://github.com/coollabsio/coolify/issues/5685
  https://bex.co/blog/2026/07/09/coolify-v5-rewrite-no-timeline

Coolify on NixOS is unsupported on both sides:

- No nixpkgs package or NixOS module; packaging requests closed
  (https://github.com/NixOS/nixpkgs/issues/291589 "not planned",
  https://github.com/NixOS/nixpkgs/issues/303482 duplicate). Only a Coolify
  CLI PR (#500295) is mentioned in
  https://github.com/coollabsio/coolify/discussions/1855.
- NixOS as a managed server is rejected by Coolify's validator: "Server OS
  type is not supported for automated installation" even with Docker and
  Compose running. Cause: OS detection from `/etc/os-release` plus a
  hardcoded `/usr/bin/docker`; NixOS has it at
  `/run/current-system/sw/bin/docker`, and a symlink did not fix it for the
  reporter. https://github.com/coollabsio/coolify/issues/1578
  https://github.com/coollabsio/coolify/discussions/4061 (open since October
  2024, no maintainer reply)
- NixOS as the Coolify host: the installer targets Debian/Ubuntu and expects
  `curl wget git jq jc`, Docker 24+, `/data/coolify/*`, and generated SSH
  keys. Running Coolify via docker-compose on NixOS
  (https://github.com/coollabsio/coolify/issues/2721) reintroduces the
  validator problem because Coolify SSHes into its own host as localhost.
  Coolify needs root SSH or a non-root user with `NOPASSWD: ALL` sudo.
  https://coolify.io/docs/knowledge-base/server/non-root-user

## 9. Logto

- Device Authorization Grant (RFC 8628): yes, shipped in v1.38.0 (OSS and
  Cloud), endpoint `/oidc/device/auth`, token at `/oidc/token`. The app must
  be a Native app ("Input-limited app / CLI" template), public client, no
  secret. https://github.com/logto-io/logto/releases/tag/v1.38.0
  https://docs.logto.io/quick-starts/device-flow
- Logto's own CLI auth guide (April 2026) recommends authorization code plus
  PKCE with a loopback redirect as the default and device code as the
  headless fallback. https://blog.logto.io/cli-authentication-methods
- GitHub social connector: yes. https://docs.logto.io/integrations/github
- JWT access tokens for custom API resources: yes, requested via the
  `resource` parameter; opaque tokens only when no resource is given.
  https://docs.logto.io/authorization/global-api-resources
- Refresh tokens with `offline_access`: yes, including in the device flow.
  https://docs.logto.io/quick-starts/device-flow

## 10. Benchmark results (M0)

Deferred (DECISIONS I-12): no plain-VM comparison was run. The first host
measured itself instead; see §11 to §14, and §15 for fragment timings on
the dev box.

---

Method: web research by sub-agents on 2026-09-16 and
2026-09-17, summarised in the design interview; every URL above is one those
agents cited. Prices are as quoted on those dates and will drift.

## 11. First host timings (M1, host-01, 2026-09-20)

Measured by the M1 integration session on the first real host, in place of
the deferred M0 benchmark (DECISIONS I-12). Host: `Standard_D16s_v7`
(Intel Xeon 6973P-C, 16 vCPU, 64 GB, NVMe-only, I-39), NixOS 26.11
`b1b8759`, kernel 6.18.52, Cloud Hypervisor 53.0, virtiofsd 1.14.0, thin
pool 476 GiB on a 512 GB Premium SSD v2 (16,000 IOPS, 600 MB/s). Guest:
class `large` (4 vCPU, 8 GB), base closure 6.35 GB, root on a thin volume,
store over virtio-fs with the writable overlay. No comparison VM was run;
these are the absolute numbers the design can be checked against.

| Operation | Measured |
|---|---|
| `nix copy` of a 6.35 GB guest closure into the host through the edge (daemon store) | 24 s once the store paths were mostly present; first transfers were interrupted by the `.links` bug (I-61) |
| `hostdev create` (thin volume, mkfs, tap, nft, virtiofsd, CH, Ready, secrets, project) | 20 s to `running` |
| Guest boot to `Ready` (serial console timestamp) | 17 s on first boot, 11.7 s on restart (multi-user target at 11.2 s) |
| `hostdev start` of a stopped guest | 12.5 s to `running` |
| `hostdev stop --snapshot` (fsfreeze, LVM snapshot, zstd, Blob upload of 558 MB, shutdown) | 62 s; the upload alone 57 s |
| Freeze window under a `dd conv=fsync` loop, 4 samples | all under 0.5 s (histogram sum 0.75 s) |
| Restore from Blob into a new 70 GB thin volume (download, zstd, e2fsck) | 75 s, then 12.5 s to start |
| `hostdev resize` 40 → 70 GB on a running guest (lvextend, CH resize-disk, resize2fs) | 0.17 s |
| `hostdev exec`, `secrets set`, `principals` round trip | under 0.2 s |
| `hostdev create` of a `small` guest under the I-49 sandbox (guest@ as `hostd`) | 15 s to `running` |
| `ApplyConfig` in place (new closure adds one package; `RegisterPaths` 480 KB, `switch-to-configuration switch`), tmux session kept | 2.2 s |
| `ApplyConfig` with a kernel change: refused without `force_reboot` | 0.3 s |
| `ApplyConfig --force-reboot` (snapshot to Blob, stop, start on the new kernel) | 75 s |
| `hostd` restart and `kill -9` under running guests: guests untouched, interrupted Snapshot replayed to completion | replay finished 57 s after the restart |
| `nix-collect-garbage` on the host with two guests running | 1.1 GiB freed, both rooted closures kept, guests unaffected |
| In-guest `docker pull node:24` (1.14 GB extracted), overlay2 | 37 s |
| In-guest `git clone` of NixOS/nix, full history (163 MB) | 11 s |
| In-guest `go build std` (CPU-bound) | 21 s wall, 28 s user on 4 vCPU |
| In-guest headless Chromium screenshot of example.com | 2.0 s |
| `nix profile install` of a cached package in the guest (cowsay, from cache.nixos.org) | 2.5 s |
| Install of the host with nixos-anywhere through the edge (owner's apply) | about ten minutes including the kexec and closure copy |

Reading: the 5 s start in DESIGN §5 is not met yet (12.5 s; the guest's own
systemd boot is 11 s of it, the hypervisor and virtiofsd start under a
second; `home-manager-dev.service` and Docker are the long poles in the
guest's boot and neither is needed before `Ready`). The host-side numbers (create, resize, freeze) are well inside the
design; the snapshot upload of a thin volume is bounded by zstd on one
core plus Blob throughput at about 10 MB/s of compressed output and is the
first thing to optimise if stop latency matters.


## 12. Edge, identity and CLI timings (M2, 2026-09-20)

Measured by the M2 integration session on host-01 (as in §11), the edge
(`Standard_D2s_v7`) and the control VM (`Standard_D4s_v7`, api and
api-grpc as Coolify applications). The table is filled in as the gate is
reached; a row without a number was not measured.

| Operation | Measured |
|---|---|
| `Build` of a one-line home-manager fragment on host-01 against main `abeed670` through `hostdev build` (the first Build ever run on a real host; store already held the M1 base closure) | 35.0 s: eval 13.0 s, build 21.9 s; closure 6.0 GB; `kernel_changed` against the M1 guest's closure |
| The same Build, first attempt, before DECISIONS I-93 | failed in 56 ms at `eval_failed`: libgit2 refused the root-owned checkout |
| host-01 `Register` with the real api over the VNet (token delivered by the apply, hostd's 30 s token poll, TLS 1.3 mTLS to 10.200.3.4:8443) to `hosts list` = ready | registered 18:15:12Z, first heartbeat within 15 s; `wg0` up on the host and the edge's `wgsync` had the peer and the /22 route at its next 30 s tick |
| Control VM ⇄ edge WireGuard (static peer both sides) | handshake within 3 s of `wg-quick up`; 1.0 ms RTT edge ⇄ control, 1.8 ms edge ⇄ host over the tunnel |
| `repose-admin hosts smoke host-01` through the api's ops engine and the real hostd: `Build` of the smoke fragment at main 49962ad (hostd's own clone, I-93) | build 18.8 s (eval 5.0 s, build 13.2 s), closure cached from the earlier build |
| the same, `create` (thin volume, taps, virtiofsd, Cloud Hypervisor, Ready, secrets, project setup) | 21.5 s to running |
| the same, `snapshot` (freeze, LVM snapshot, zstd, Blob) / `stop` / `start` / `destroy` | 16.5 s / 4.5 s / 14.5 s / 22.0 s; whole smoke 79 s |
| the same smoke, first attempt | failed at create step 8 in 21 s: `/run/repose` was 0700 after the token delivery (I-97) |
| The conductor's `repose run` (v0.1.3 build of main) for project `recruiting`, class small, base 2026.09.20 already in the store | build 5 s, create 14 s to running (22:38:36 → 22:38:50); first gateway relay into the guest 90 s later, exec sessions 105–267 ms each |
| Smoke against base 2026.09.20.2 (d315339: guestd and the guest ssh config changed, so a fresh guest closure) | hostd's own clone of the base, build 18.9 s, create → running 16 s, snapshot/stop/start/destroy all done within 2 min |
| Smoke against base 2026.09.20.3 (65d336e, another fresh closure) | build 38.6 s, create ok, snapshot 24.6 s (the client was cut by the api's automatic redeploy; the orphan was destroyed) |
| Edge switch under two running guests (2026-09-20 23:36Z, I-110 + m3's I-94) | gateway back in 20 s; the next relay into the same guest at 23:37:18 |
| The owner's gate run (v0.1.4, laptop, base 2026.09.20.3): `nuru-playground` (large) | create op 00:30 → build 5 s → guest running 00:31:42; first relay from the laptop 13 s later, exec round trips 201–441 ms |
| The owner's second project `age-calculator` (large), while three other guests ran | create op 00:32 → running 00:33 |

## 13. Guests through the api on host-01 (M3, 2026-09-20/21)

Measured by the M3 integration session on host-01 (as in §11, switched to
main `d3568d4` at 00:16Z) against the production api on the control VM
(`api` and `api-grpc` at `d90b31d`, then `d3568d4`), with the
`ops/checks/` scripts; every figure is from the evidence file the script
wrote (`ops/checks/out/`). Guests are class `small`; the store already
held the base closure.

| Operation | Measured |
|---|---|
| `repose run --name` from the dev box to `running` (create op: build of the empty fragment on a warm store, CreateGuest, boot to Ready), base 2026.09.20.3 | 84 s (23:42:08 to 23:43:32) |
| `repose-admin projects create` to `running`, one guest | 47 s (00:00:33 to 00:01:20) |
| Two `projects create` in the same second, both to `running` (I-120, distinct taps) | 31 s |
| `PUT /config {menu: bun}` to op done: eval and build of the bun addition as `nixbuild` in a scope (CPUQuota 8 s/s, MemoryMax 16 GiB, RuntimeMaxSec 30 min 30 s) | 5 s; 40 `BuildLog` lines over SSE; `bun` 1.4.2 on PATH in a new login shell with the same boot_id and tmux session |
| hostd `build_done` for four consecutive fragment builds of the same project (menu, takeover, package, fetch), warm store | 5.1 s each: eval 4.7 s, build 0.33 s; closure 6.0 GB |
| A fragment whose output is a 21 GB sparse file (`ops/checks/fragments/closure-cap.nix`) | refused after the build: `closure is 26.6 GB, limit is 20 GB`, the 21 GB path first among the ten largest, no GC root |
| `repose secrets set` to the file in the guest's tmpfs (`0400 dev`, exported in a login shell) | under 5 s (the script's first check after 5 s found it) |
| `repose secrets rm` to the file gone and `secrets.env` rewritten | under 5 s |
| A fragment carrying a current secret value, `repose config apply` | refused before evaluation (`fragment contains the value of secret M3_CHECK_SECRET`) |
| Hook event to `ntfy` and email delivery: Claude `Stop` replayed through `repose-hook` in the agent's tmux window | 1 s |
| The same for Codex and opencode | 7 s each (the outbox poll) |
| pi's pane-idle heuristic, the real binary idle in its window, to delivery | 101 s (the 90 s quiet window, the 5 s debounce, the outbox) |
| `POST /certs/revoke` to the gateway refusing that certificate | 7 s (the gateway's 30 s revocation poll) |
| `repose-admin projects destroy` of a running small guest (stop with snapshot, DestroyGuest) | about 60 s (23:45:48 to 23:46:19 for `stopping`, the row gone by 23:46:19) |
| `repose-admin hosts smoke host-01` on base 2026.09.21.1 (the api driving every op) | create 43.1 s, snapshot 17.0 s, stop 5.0 s (no snapshot), start 14.5 s, destroy 21.1 s |
| api-grpc SIGKILLed 8 s into a build (the crash row of 05 §9) | container back in 6 s (Docker's restart policy), hostd `stream_connect` 33 s after the kill, the build op done with no lost command; a `docker kill` instead left the container `Exited (137)` for as long as nobody started it (RUNBOOK) |
| api restarted 3 s into a snapshot op | op re-driven and done 15 s after the restart; an attached session saw 0 gaps |
| Snapshot expiry, first delete after the api's identity got Blob delete (I-131): four aged rows of one project | all four deleted in the same second (01:58:24Z), each `snapshot_expired` 1 s later; a snapshot under a running restore kept until the restore's op finished, then deleted at the next run |
| `repose snapshots restore` of a 1.5 MB snapshot over a stopped small volume, to `running` | 26 s (the op), 45 s to `running` through the CLI's poll |
| Security base publish to the sweep's first build op (basebump's ten-minute tick) | 4 min 21 s (02:08:16 to 02:12:37Z) and 9 min 13 s (02:20:09 to 02:29:22Z); a redeploy of api-grpc inside the window lost the sweep until 04:00 before I-138 |
| Sweep build of a kernel-changing base (38d1cbf, linux 7.2.6 already in the host store) per project, four projects | 5.2 s each: eval 4.8 s, build 0.35 s; `ApplyConfig` on the running guest: "apply needs reboot", revision `built` with `reboot_required`, nothing rebooted; ntfy delivery of the base_updated event 2 s after the op |
| `repose stop && repose start` of that project to boot the new kernel | 89 s (02:30:41 to 02:32:10Z; stop with a 1.5 MB snapshot, start onto 7.2.6, `uname -r` 7.2.6, revision `applied`) |
| The LTS republish's sweep (2026.09.21.3, 02:59Z, main 9d4cb40): builds of a base whose closure differs from the running one, four projects | 23 s to 24 s for the first two (eval 5.5 s, build 18 s: the new guestd and its dependents built on the host), 5 s for the rest (substituted from the first); the package-only switches on a running guest ended `guestd Switch: vsockrpc: EOF` (I-143) |
| First base with I-143 (2026.09.21.4, 04:32Z, main 0b68f82): publish to the sweep's first op | 1 min 57 s (the tick landed 2 min after the publish, across an api-grpc roll at 04:33Z, I-141); one clone of the new ref for five builds (I-144) |
| That sweep's package-only switch on a guest running a pre-I-143 base (nuru-playground, m3-held) | build 24 s / 5 s, switch 3 s; guestd never stopped (the new activation leaves it alone), no `guestd_lost`; the running guestd stays the old binary until the next boot |
| Base 2026.09.21.5 (I-147/I-148) onto guests already running an I-143 guestd (m3-held, m3-iso-c) | publish to first op 7 min 50 s (tick timing); switch through the transient unit 3 s, op `done`, guestd's self-restart onto the new binary 5 s after the answer, no `guestd_lost`; a `projects restart` of a recovered guest whose newest revision is applied sends no apply (stop 61 s, start 15 s) |
| `repose-admin projects restart` of a guest stranded with no guestd (m3-iso-c) | stop 60 s (no guestd to freeze; the unit's stop timeout), start 17 s, the built revision applied at boot, running |

Reading: on a warm host the api path adds nothing measurable over the
hostd numbers of §11; the 5 s menu apply is the eval of an already-built
system plus a substitution, and the create is dominated by the guest's
own boot (§11's 11.7 s) plus the api's two phases and the CLI's polling.
The notification path is the outbox's 2 s poll plus delivery; the 7 s of
Codex and opencode against Claude's 1 s is where in the poll the event
landed.


## 14. Provisioning time budget (host-01, 2026-09-23)

Measured by the provision-speed worker, read-only: hostd's and the host's
journal for the owner's `izma` create (large, default fragment, base
2026.09.21.5), the api and api-grpc container logs on the control VM, and
`systemd-analyze` plus `journalctl -b` inside `m3-check` (small, its last
boot a start at 2026-09-21 02:31:55Z) through `repose-admin exec`. Host as
in §11. The "after" column is an estimate until the api is deployed, the
next base is published and host-01 is switched; each row names the
decision that changes it.

| Phase | Before (measured) | After (expected) | Change |
|---|---|---|---|
| `POST /projects` (api) to the Build command sent (api-grpc drives the ops) | same second in both logs; up to 0.5 s of api-grpc's poll | at once (NOTIFY) | I-163, api |
| `Build` of the default fragment on a warm host | 6.08 s (eval 5.71 s, build 0.33 s, 6.2 s CPU, 872 MB peak) | 0 when another live guest on the host runs the same fragment on the same base, else unchanged | I-160, api |
| Build result to CreateGuest sent | 45 ms | 45 ms | |
| CreateGuest `creating` to `starting` (lvcreate, mkfs, GC root, tap, nft, tc, virtiofsd) | 1.50 s (StartGuest, no volume: 0.13 s) | about 0.5 s | I-162, hostd |
| Cloud Hypervisor start to guest kernel | about 0.3 s | same | |
| Guest kernel | 0.84 s | same | |
| Guest initrd | 4.21 s (two silent waits of 0.68 s and 0.83 s: serial console queries timing out, mount-monitor rate limit) | about 2.3 s | I-161, base |
| Switch root to stage-2 journald | 1.98 s (0.33 s of it the console query) | about 1.1 s | I-161, base |
| Stage 2 to `docker.service` start | 1.7 s | same | |
| `docker.service` (guestd and sshd ordered after it) | 2.37 s | 0 on the path | I-161, base |
| guestd start to Ready (0.5 s poll of `/proc/net/tcp`) | 0.65 s | about 0.35 s | I-161, base |
| Ready to paths registered (`nix-store --dump-db` 70 ms on the host, `--load-db` in the guest) | 0.45 s | same | |
| `repose-paths` noticing the stamp (0.5 s poll) | 0.51 s | under 0.1 s | I-161, base |
| home-manager activation | 1.07 s | same | |
| `systemd-user-sessions` and SetupProject (first login allowed; hostd says `running`) | 0.12 s | same | |
| CH start to hostd `running` | 14.1 s (m3-check start), 13.8 s (izma create) | about 8.5 s | I-161 |
| api `running` to op done | same second | same | |
| POST to op done, izma (default fragment, base in the store) | 22 s (00:06:47 to 00:07:09) | about 9.5 s with reuse, about 15.5 s without | I-160..I-163 |

The guest rows were reproduced on the dev box (AMD, so absolute numbers
are not a host's) by booting `nix build ./nix#guest-runner` with Cloud
Hypervisor 53 and virtiofsd as an unprivileged user, fresh 20 GB volume,
2 vCPU, 4 GB; seconds from the guest journal:

| Runner | fsck done | `/sysroot` mounted | switch root | stage 2 queued | guestd started | sshd | Ready |
|---|---|---|---|---|---|---|---|
| main at 5aa48d3 | 2.46 | 3.13 | 5.41 | 7.43 | 11.27 | 11.48 | 11.88 |
| this branch (I-161), two boots | 1.46 / 1.58 | 1.46 / 1.58 | 2.90 / 3.05 | 4.06 / 4.08 | 5.73 / 5.78 | 6.16 / 6.36 | 6.22 / 6.39 |
| plus virtiofsd `--cache always` (not adopted) | | | | | | | 5.4 |

Reading: the sshd socket listens 2.6 s before hostd reports `running`, but
an SSH login is refused until `systemd-user-sessions` removes
`/run/nologin`, which waits for home-manager, which waits for the path
registration hostd sends after Ready; so `running` is within 60 ms of the
first moment a login succeeds, and hostd's number is the right one to
optimise. The rest of the owner's 1 m 43 s was the CLI (SSH prompts and a
2 s op poll), which the cli-ux worker owns.

## 15. Fragment evaluation and build timings (dev box, 2026-09-20)

Measured by workstream 12 on the dev box (Azure AMD, `nix` 2.35.2,
single-user store, `eval-cache` off as in production), with the exact
command lines of `interfaces/nix-build-contract.md` and the platform
flake at commit `c863eff`. The base closure was already in the store, as
it is on a host after its first guest. Not a host measurement (CLAUDE.md);
the shape is what 05 needs to set expectations, the host numbers replace
these when the M1 session has them.

| Step | Fragment | Wall time | Note |
|---|---|---|---|
| `nix eval` of `guestSystem` | empty | 3.6 s | 880 MB RSS; the module system plus nixpkgs instantiation; the 60 s cap is a ceiling for pathological expressions, not a budget |
| `nix eval` of `guestSystem` | `zig`, `shellcheck`, `programs.direnv` | 4.1 s | |
| `nix eval` of `guestSystem` | the whole menu catalog (21 entries) | 3.6 s | |
| `nix build` of that system | `zig`, `shellcheck`, `programs.direnv` | 25 s | one path substituted from cache.nixos.org, the rest was local; a cold host substitutes tens of paths and is bound by its egress |
| `nix build` of three example systems | `docs/features/config-examples` | 36 s total | `checks.fragment-examples`, includes a fixed-output fetch and a jq rebuild through an overlay |
| build timeout case | a derivation sleeping 31 minutes, cap 5 s | 5 s to `build_timeout` | `internal/hostd/nixbuild` real-Nix corpus |
| closure cap case | 120 MB output, cap 100 MB | under 1 s after the build | `closure_too_large` with the ten largest paths |

A package-only change on a warm host is about 5 s of evaluation plus
the substitution of what is new, and the CLI's `Building ... 38s` in
`features/config.md` is the right order of magnitude. The first build on
a fresh host also pulls the base closure's build-time dependencies that
the guest itself never needs (home-manager's activation scripts and the
like), which is what the platform cache (DECISIONS I-46) removes.
