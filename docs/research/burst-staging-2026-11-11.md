# Burst cores on host-stg-01: the §6 measurement (2026-11-11)

**Status: a measurement, not a decision.** `docs/proposals/2026-11-10-burst-cores.md`
§6 asked for the keystroke-latency curve on a host before any host turns
`repose.host.burst.enable` on (DECISIONS I-1126). This is that run, by the
conductor session, on the staging host, with the flag on there and nowhere
else. The flag stays off on host-02.

## Setup

- host-stg-01, `Standard_D8s_v7`: 8 vCPUs, 2 threads per core, **4 physical
  cores**; hostd reports `repose_host_cpu_cores 4`, so the gate
  (`burst.gate = 0.75`) is **3 physical cores busy**. hostd `cc6b7d7`
  (main `b2ce136d` plus the one-line staging config), base guest closure built
  on the host from the same commit.
- Three `small` guests created and started through hostdev. Each booted with
  4 vCPUs (its class ceiling), `cpu.max 100000 100000` (its 1-core base) and
  `cpu.weight 50`.
- Load: `sh -c 'while :; do :; done'` loops started with `hostdev exec`.
- Typing lag: a Python probe run inside the idle guest (`hostdev exec`): a pty
  running `cat`, one byte written and its echo timed, 300 keys 20 ms apart,
  p50/p90/p99. It measures how often the guest's vCPU is scheduled, not the
  SSH path, so it is lower than I-738's end-to-end keystroke figures and is
  compared only with itself.
- Host busy share: `/proc/stat` idle delta over 5 s.

## Readings

| Tier | Load | Caps (millicores) | Host busy (of 8 vCPUs) | Idle guest c: p50 / p90 / p99 |
|---|---|---|---|---|
| 0 | none | a 1000, b 1000, c 1000 | 0.1–0.3 | 0.10 / 0.33–0.49 / 1.7–3.2 ms (3 runs) |
| 1 | a: 4 loops | a **2637–2914**, b 2000*, c 2000* | 3.0 | 0.30–0.33 / 0.93–1.35 / 3.3–6.9 ms (4 runs) |
| 2 | a and b: 4 loops each | a **1443**, b **1443**, c 2000* | 3.13 | 0.41–0.42 / 1.17–1.39 / 4.4–11.5 ms (3 runs) |
| 3 | a, b: 4 loops; c: 1 loop | a 1000, b 1000, c 1000 (gate closed) | ~3 | c used **952** of its 1000 millicores in 10 s |
| 4 | none (stopped) | a 1000, b 1481*, c 1481* | 0.16 | 0.11 / 0.40 / 1.45 ms |

\* a cap above base on a guest that is not hungry is the "keep what it holds"
branch of the controller (a guest between two bursts is not cut); it is
headroom nobody uses.

Metrics seen on the host (`:9101/metrics`): `repose_host_burst_cap_millicores`,
`repose_host_burst_cpu_seconds_total{class="small"}`,
`repose_host_burst_gate_open` (1 in tiers 1–2, 0 in tier 3).

## What it shows

- The arithmetic in `BurstTickOnce` holds to the digit: tier 1's cap is
  `base + (target − host base busy)` = 1000 + (3000 − ~360) ≈ 2640, bounded
  by `BurstRiseMillicores` per tick, never the 4000 ceiling, because the
  gate is 3 cores on this host and one hungry small guest alone cannot take
  them all while the host's own base use counts.
- Two hungry guests share the headroom equally at equal weight (1443 each)
  and the host is held at the gate (3.13 of 8 vCPUs ≈ 0.78 of 4 cores).
- **The base never loses.** When the third guest's paid core pushed the
  host's base use to the gate, the gate closed within a tick, every cap went
  to base, and that guest got 95 % of its core while the other two were
  still hungry.
- Typing lag at the gate: the idle guest's p50 went from 0.10 ms to
  0.3–0.4 ms and p90 from 0.4 ms to 1.2–1.4 ms, with p99 up to 11 ms once.
  No sample reached I-738's 17 ms p50 / 44.5 ms p90 of a saturated host. Caps
  returning to base brought the probe back to baseline.

## What it does not show

- Only `small` guests, on a 4-core host, with synthetic loops. host-02 is a
  `D64s_v7` (32 physical cores, gate 24): the same rule, a larger
  denominator, untested there.
- The gate value itself is untested against alternatives: 0.75 was not
  compared with 0.5 or 0.9. The curve §6.2 asks for (lag against busy share
  across several gate values) needs runs at other gates, each a host switch.
- The probe is in-guest echo, not an SSH keystroke from a laptop.
- Metering past the plan allowance and the overage switch (stages 4–5 of
  the proposal) are not built, so nothing here says what a user is charged.

## Decide next

Whether 0.75 of physical cores is the gate for host-02, and whether to run
the curve at 0.5 and 0.9 on staging first. Turning the flag on for host-02
is one line in `nix/hosts/host-02.nix` and a host switch, after that
decision (I-1126).
