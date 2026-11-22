# Baseline ("before" column): today's Nix guests on host-stg-01

Measured 2026-11-22, 07:57-08:11 UTC, by the staging spark.

**Surface.** Host host-stg-01 (Azure Standard_D8s_v7, 31.3 GB, Intel, staging VNet) running hostd **b2ce136** (the version on the host, not main). The guest base is BASE_REF 5fa82823 (pushed main): Nix guests, kernel 6.18.52, a 6.3 GB closure. The control plane is stg-api from main 5615455a plus stg-cp, on stg-edge. The CLI is the stg-cp build on the dev box woker-1 (20.102.97.100), going through the staging gateway on 172.206.225.242:22. The host was quiet: nothing else ran on it, and at most two projects were alive at once. Every create was `small`.

Sources. "CLI" means the line the CLI prints (`Booted`, `Ready in`, `ready in`), plus the shell's wall time around the command. "hostd" means the `boot timings` / `stop timings` lines in the host journal (`journalctl -u hostd`). "op" means the api's `ops` row, created_at to finished_at. Raw files are in /mnt/nixstore/tmp/stg-cp/bench/: phase1.log, phase1-host.jsonl, phase1-ops.csv, phase2.log and phase2-ops.txt.

## 1. Ten creates, ten stop/start cycles (p50 / p95, n=10 each)

The creates are ten consecutive `repose run --temp --size small -d` runs from a non-git directory, each removed before the next. The cycles are `repose stop -y --wait` then `repose start` of one named project, `stgbench`. Every stop took a snapshot (`snapshot_ok: true`).

| measure | n | p50 | p95 | min | max | unit |
|---|---|---|---|---|---|---|
| CLI create: Booted (temp, small) | 10 | 12.0 | 12.0 | 11.0 | 12.0 | s |
| CLI create: Ready in | 10 | 12.5 | 14.1 | 12.0 | 15.0 | s |
| CLI create: wall of `run --temp -d` | 10 | 12.7 | 14.4 | 12.426996839 | 15.060041522 | s |
| CLI start: Booted | 10 | 9.2 | 9.4 | 9.0 | 9.4 | s |
| CLI start: ready in | 10 | 9.2 | 9.4 | 9.1 | 9.5 | s |
| CLI start: wall of `start` | 10 | 9.3 | 9.5 | 9.236190398 | 9.624715893 | s |
| CLI stop --wait: wall | 10 | 4.0 | 6.4 | 3.801306052 | 6.532703514 | s |
| hostd boot timings (create) total_ms | 10 | 10836.0 | 11207.1 | 10737 | 11249 | ms |
| hostd boot timings (create) volume_ms | 10 | 748.0 | 1042.7 | 695 | 1049 | ms |
| hostd boot timings (create) prepare_ms | 10 | 169.0 | 181.6 | 155 | 182 | ms |
| hostd boot timings (create) ready_ms | 10 | 4494.0 | 4693.9 | 4412 | 4761 | ms |
| hostd boot timings (create) paths_ms | 10 | 3007.0 | 3078.5 | 2953 | 3101 | ms |
| hostd boot timings (create) setup_ms | 10 | 2439.5 | 2459.3 | 2265 | 2462 | ms |
| hostd boot timings (create) identity_ms | 10 | 0.0 | 0.0 | 0 | 0 | ms |
| hostd boot timings (start) total_ms | 10 | 8858.5 | 9038.7 | 8754 | 9117 | ms |
| hostd boot timings (start) volume_ms | 10 | 0.0 | 0.0 | 0 | 0 | ms |
| hostd boot timings (start) prepare_ms | 10 | 158.0 | 166.6 | 147 | 167 | ms |
| hostd boot timings (start) ready_ms | 10 | 4451.5 | 4544.9 | 4383 | 4575 | ms |
| hostd boot timings (start) paths_ms | 10 | 2731.0 | 2835.2 | 2649 | 2864 | ms |
| hostd boot timings (start) setup_ms | 10 | 1527.5 | 1557.5 | 1507 | 1571 | ms |
| hostd boot timings (start) identity_ms | 10 | 0.0 | 0.0 | 0 | 0 | ms |
| hostd stop timings total_ms | 10 | 3344.5 | 3353.0 | 3333 | 3353 | ms |
| api op create: created->finished | 10 | 11.6 | 12.0 | 11.54 | 12.05 | s |
| api op create: started->finished | 10 | 11.6 | 12.0 | 11.54 | 12.05 | s |
| api op destroy: created->finished | 10 | 3.4 | 3.6 | 3.41 | 3.68 | s |
| api op destroy: started->finished | 10 | 3.4 | 3.5 | 3.4 | 3.47 | s |
| api op start: created->finished | 10 | 9.2 | 9.3 | 9.06 | 9.42 | s |
| api op start: started->finished | 10 | 9.2 | 9.3 | 9.05 | 9.42 | s |
| api op stop: created->finished | 10 | 3.6 | 3.6 | 3.57 | 3.6 | s |
| api op stop: started->finished | 10 | 3.6 | 3.6 | 3.56 | 3.6 | s |

How to read the hostd fields (internal/hostd/guest/create.go):
- `total_ms` is volume + prepare + ready + delivery.
- `ready_ms` runs from Cloud Hypervisor start to guestd ready.
- `paths_ms` is the Nix-specific **RegisterPaths** step: the closure's `nix-store --dump-db` loaded into the guest's Nix database. It is about 3.0 s on a create and 2.7 s on a start.
- `setup_ms` is SetupProject, which waits for **home-manager**'s first activation before the session starts. It is about 2.4 s on a create and 1.5 s on a start.
- `identity_ms` is 0 because hostd b2ce136 does not emit it; that field came in with I-1408.

Two extra one-off measurements:
- In the guest on a start, `home-manager-dev.service` took 515 ms from InactiveExit to ActiveEnter. `systemd-analyze` reported 1.642 s kernel + 7.103 s userspace. The slowest units: docker 4.79 s, repose-paths 3.23 s, dev-vda.device 2.85 s, repose-zram-swap 2.71 s.
- The first create ever on this host, with an empty store, before this run: 644 paths (1.7 GiB) were fetched in 20 s, 323 derivations built in 1m14s, and the op took 129.5 s. Every create in the table was warm.

## 2. Snapshot storage, and each flow once

**How the api authenticates to Blob, and the staging equivalent.**
- In production, the api uses `azidentity.NewDefaultAzureCredential` (internal/api/snapshots/expiry.go `NewAzureBlob`). It is fed by AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET, which belong to the api's Entra app registration (ops/coolify/README.md).
- The code accepts only a TokenCredential: no SAS and no account key. So a SAS or a key from `az storage` would not work without a code change.
- Staging has exactly one identity, `id-repose-host` (client 0fc60aaf-...). It already holds Storage Blob Data Contributor on reposesnapstaging3912/repose-snapshots.
- So I **assigned that existing identity to the edge VM**: `az vm identity assign -g repose-staging -n edge-01 --identities .../id-repose-host`. I then set `BLOB_ACCOUNT_URL=https://reposesnapstaging3912.blob.core.windows.net`, `BLOB_CONTAINER=repose-snapshots` and `AZURE_CLIENT_ID=0fc60aaf-0f6d-40c9-baa9-f58d20612580` in /var/lib/repose-staging/api.env. This creates no new resource and no new role assignment.

Side effects of the assignment:
- The staging tofu plan will show the identity as drift on edge-01 until it is removed. To remove it: `az vm identity remove -g repose-staging -n edge-01 --identities <same id>`.
- The edge now holds the host's identity, including whatever Key Vault access that identity has.
- Evidence it works: `repose_api_blob_configured 1`. At cleanup, the api's own expiry sweep deleted 17 snapshot blobs with 0 errors (`snapshot_expired` x17).
- The host uploads and downloads snapshots with its own identity, so snapshot, restore and fork did not depend on this setting. In the api, Blob is used only for deletes.

| flow | how | CLI | op row | hostd |
|---|---|---|---|---|
| snapshot (200 MB written) | `snapshots create stgbench` | "taken in 2.5s (192.9 MB)", wall 2.58 s | snapshot 2.49 s | snapshot_done 2110 ms (freeze 169 ms, read_wait 431 ms), 202,321,126 bytes, format extents |
| fork | `snapshots restore stgbench <id> --as stgfork -y` | "in 11s", wall 10.95 s; marker and 200 MB file present | restore 10.79 s | restore_done source **blob**, 1288 ms (write 1164, fsck 123) |
| destroy | `rm -y stgbench` | returns at once (0.07 s); gone from `ls` in about 7 s | destroy 6.03 s (stgfork 5.93 s; with no data, 3.4 s, table 1) | |
| restore | `restore stgbench` | "Restored 2.0s", "in 11s", wall 11.36 s; marker present | restore 11.14 s | restore_done source **blob**, 1153 ms (write 1044, fsck 108) |
| pause / wake (operator) | `hostd pause/wake <guest>` | | | pause refused `in_use` while the CLI's ssh ControlMaster (ControlPersist 10m) was open; after `ssh -O exit`, paused at once; operator wake 103 ms (wall 112 ms) |
| wake by the user | `repose exec` on a paused machine | wall 0.56 s, against 0.11 s on a running machine | | guest woken reason `connect`, wake_ms 68 |
| park / unpark (operator) | `hostd park` / `hostd unpark`, one small guest | | | park: guest 3541 ms (snapshot 371 ms), image 685,215,744 bytes, whole park 8658 ms (wall 8666 ms); unpark 1666 ms (wall 1836 ms): net 37, virtiofsd 29, store_view 53, image_read 643, resume 108, guestd 112. A `sleep` started before the park was still alive after the unpark. Key Vault unwrap ok. |
| resize disk +10 | `resize stgbench 30G` (running) | "Resized ... to 30G", wall 0.53 s; `df /` 20G to 30G with no restart | resize 0.50 s | |
| first run + sync | `run stgsync -d` in a 51-file, 208 KB git repo | "Synced ... 0.7s", "Ready in 13s", wall 13.03 s | create 12.00 s | |
| sync | 1 commit, 1 dirty file, 1 untracked file | "Synced: 1 modified, 1 untracked (1 new commit) 0.6s", wall 0.73 s | | |
| sync, nothing new | | "Nothing new to sync", wall 0.34 s | | |
| config add (Nix path) | `config add postgresql redis bun` | evaluated 5.6 s; fetched 11 paths (47.4 MiB) 0.5 s; built 42 derivations 4.5 s; switched 6.3 s; "Applied ... in 18s", wall 17.53 s. postgresql and redis active, bun 1.4.2 | build 17.05 s | |

## 3. Isolation: ops/checks/isolation-host01.sh against host-stg-01

**How it ran.** The script and lib.sh were copied to /mnt/nixstore/tmp/stg-cp/checks/ and adapted for staging; the repo was not changed. Staging has no control VM, so `admin`, `psql_q` and exec-b go to the edge's stg-admin and psql. The ssh hops use the staging ssh config and known_hosts. EXEC_A uses `ssh -F $HOME/.ssh/repose/config`. GATEWAY and LOGIN_A are read from `~/.ssh/repose/hosts`.

**Tenants.**
- A is `isoa`, owned by stgdev, created through the CLI.
- B is `isob`, owned by stgb, created with `admin projects create --create-user`.
- Both were on host-stg-01, at 10.64.0.2 and 10.64.0.3.
- REVOKE_A=1, because stgdev is a throwaway staging account.

Script exit 0. Evidence: /mnt/nixstore/tmp/stg-cp/checks/out/isolation-20261122T080856Z.txt; full Go output in isolation-go-20261122T080856Z.txt. The 18 rows are the 16 host-level tests, the password row and the host X/Y row.

| # | row | result |
|---|---|---|
| 1 | TestOtherUsersProjectIs404NotForbidden | PASS (0.02 s) |
| 2 | TestSecretsListHasNoValues | PASS ("0 secrets listed, no value field") |
| 3 | TestCertificateForACannotOpenBAtGateway | PASS (refused, exit 255) |
| 4 | TestCertificateForACannotOpenBDirect | SKIP (DIRECT_SSH unset; it would put A's key on the host) |
| 5 | TestConsoleLogCarriesNoTerminalContents | PASS |
| 6 | TestForgedHookPayloadCannotNameAnotherProject | PASS |
| 7 | TestHypervisorRunsAsHostdUser | PASS |
| 8 | TestGuestCannotWriteStore | PASS |
| 9 | TestGuestSeesOneDisk | PASS |
| 10 | TestGuestACannotReachGuestB | PASS (11.5 s) |
| 11 | TestGuestCannotReachHost | PASS (17.7 s) |
| 12 | TestGuestCannotReachIMDS | PASS (10.1 s) |
| 13 | TestGuestCannotReachOtherHostsGuests | PASS (probe 10.64.4.1, 14.4 s) |
| 14 | TestGuestCannotSpoofAnotherAddress | PASS |
| 15 | TestForkBombAndMemoryHogLeaveNeighbourWithinTenPercent | PASS (18.5 s) |
| 16 | TestRevokedCertificateRejected | PASS ("rejected 12s after revocation") |
| 17 | operator password attempt refused and logged | PASS: host and edge both exit 255 "Permission denied (publickey)". Host journal: "Connection closed by authenticating user root 10.255.0.1 ... [preauth]"; edge journal: the same for 20.102.97.100. PasswordAuthentication no on both. The host listens on 10.255.0.2 only. |
| 18 | hostd for host X cannot act on host Y | NOT RUN: one host is registered |

The 4 CI-level policy tests also passed: PolicyText, ProcSample, WatchList, ClaudeCredentials.

## Clean-up
- Every project created here is destroyed. `admin projects list` is empty, and `hostd guests` on the host is empty.
- The 17 snapshots these runs left (stops, manual and final ones) were expired through the api. I set expires_at to the past for destroyed projects only, restarted stg-api, and its sweep deleted the 17 blobs. Every snapshot row now has deleted_at set.
- Still in place on purpose: the id-repose-host assignment on edge-01 and the BLOB_* lines in api.env. The removal command is in section 2.
