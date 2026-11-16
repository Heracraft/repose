# The host's WireGuard tunnel to the edge repairs itself when it goes one
# way, and publishes what it saw (DECISIONS I-1068).
#
# Why this exists. The tunnel carries everything an operator and a tenant
# need from a host: the gateway relays `repose ssh`, `run`, `attach` and
# `open` to guests over it, Prometheus scrapes node_exporter and hostd
# through it, Fluent Bit ships logs through it, and the host's sshd binds
# the WireGuard address and nothing else (network.nix, `write_sshd`). The
# host's heartbeat to the api does NOT: that is gRPC over the provider NIC.
# So a dead tunnel leaves a host that still looks `ready` to the control
# plane and is unreachable by every other means, the only shell into it
# included.
#
# Twice a tunnel has gone one way and stayed there. The control VM on
# 2026-10-26 (I-866): nine hours, packets leaving every 5.6 s and none
# arriving, fixed by rebinding its socket to a new port. host-02 on
# 2026-11-10 after an Azure live migration: its initiations reached the
# edge and authenticated, the edge's answers never came back, and the
# repair needed a VM restart because nothing could reach the host to run
# one command. Both times both ends kept retrying the same dead
# five-tuple for as long as it took a person to notice, and the
# 2026-10-26 incident file closed on exactly this gap: "neither end
# rebinds its socket when it has sent for minutes and received nothing."
#
# What it does. Every 60 s it reads each peer's handshake age and byte
# counters. When the handshake is older than `staleAfter`, the host has
# sent since the previous sample and received nothing in that time, it
# rebinds the WireGuard socket to a fresh source port with
# `wg set <iface> listen-port 0`. That is the entire repair: it changes
# the five-tuple, which is what a stale NAT mapping or a blackholed flow
# is keyed on, while keeping the interface, its keys, its peers and its
# routes. The host's wg0.conf carries no ListenPort, so a random port is
# what it already had and nothing else has to agree with the choice.
#
# What it deliberately does not do:
#
#   - It never restarts wg-quick. That tears down the routes a guest's
#     traffic is on, for a repair that does not need them touched.
#   - It does nothing on a quiet tunnel. "No handshake" alone is also
#     true of a host whose edge is genuinely down and of a tunnel nobody
#     is using; without `tx` rising and `rx` flat there is no evidence of
#     one-wayness, and a rebind would be churn.
#
#     This couples the detector to one line in network.nix: the host's
#     peer carries `PersistentKeepalive = 25`, which is what makes a
#     dead tunnel keep sending and so what makes `tx` rise between two
#     samples. Take the keepalive away and a one-way tunnel with no
#     traffic on it becomes invisible here. Hence the sampling interval
#     (60 s) must stay comfortably longer than the keepalive, and
#     `staleAfter` longer again.
#   - It does nothing on its first sample after a boot, having no
#     previous counters to compare.
#   - It cannot fix a break that is not keyed on the five-tuple: wrong
#     keys, a firewall or routing change, an edge that is down. Hence the
#     rate limit, and hence HostWgDown still pages (I-1070). A rebind
#     loop is the signal that the cause is not this one.
#
# NEVER run `wg show <iface> dump` here. Its first field is the
# interface's PRIVATE KEY, and anything this script reads can reach a log
# or a journal. `transfer` and `latest-handshakes` carry what is needed
# and no secret.
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host.wgWatchdog;
  textfileDir = "/var/lib/node_exporter/textfile";

  watchdog = pkgs.writeShellApplication {
    name = "repose-wg-watchdog";
    runtimeInputs = [ pkgs.wireguard-tools pkgs.coreutils pkgs.gawk pkgs.systemd ];
    text = ''
      iface=${cfg.interface}
      stale=${toString cfg.staleAfter}
      minInterval=${toString cfg.minInterval}
      state=/run/repose/wg-watchdog.state
      stamp=/run/repose/wg-watchdog.last-rebind
      count=/run/repose/wg-watchdog.rebinds
      out=${textfileDir}/repose_wg_watchdog.prom

      mkdir -p /run/repose "${textfileDir}"
      now=$(date -u +%s)

      # No interface yet (the host has not registered, or wg0 is down for
      # its own reasons): publish nothing rather than a zero that reads as
      # a healthy tunnel, and leave the judgement to HostWgDown.
      if ! wg show "$iface" >/dev/null 2>&1; then
        rm -f "$out"
        exit 0
      fi

      # pubkey -> handshake unix seconds, and pubkey -> rx tx. Never `dump`.
      handshakes=$(wg show "$iface" latest-handshakes)
      transfer=$(wg show "$iface" transfer)

      one_way=0
      worst_age=0
      next="$state.next"
      : > "$next"
      while IFS=$'\t' read -r peer hs; do
        [ -n "$peer" ] || continue
        rxtx=$(echo "$transfer" | awk -F'\t' -v p="$peer" '$1 == p { print $2 + 0, $3 + 0 }')
        rx=$(echo "$rxtx" | awk '{print $1 + 0}')
        tx=$(echo "$rxtx" | awk '{print $2 + 0}')

        # A peer that has never handshaked reports 0, which is not an age;
        # treat it as stale so a tunnel that never came up still counts.
        if [ "$hs" -gt 0 ] 2>/dev/null; then age=$((now - hs)); else age=$stale; fi
        if [ "$age" -gt "$worst_age" ]; then worst_age=$age; fi

        prev=$(awk -v p="$peer" '$1 == p { print $2, $3 }' "$state" 2>/dev/null || true)
        if [ -n "$prev" ]; then
          prx=$(echo "$prev" | awk '{print $1 + 0}')
          ptx=$(echo "$prev" | awk '{print $2 + 0}')
          # Sending and hearing nothing, with no handshake to show for it.
          if [ "$age" -ge "$stale" ] && [ "$tx" -gt "$ptx" ] && [ "$rx" -le "$prx" ]; then
            one_way=1
          fi
        fi
        printf '%s\t%s\t%s\n' "$peer" "$rx" "$tx" >> "$next"
      done <<< "$handshakes"

      mv "$next" "$state"

      rebinds=$(cat "$count" 2>/dev/null || echo 0)
      last=$(cat "$stamp" 2>/dev/null || echo 0)
      rebound=0
      failed=0
      if [ "$one_way" = 1 ] && [ $((now - last)) -ge "$minInterval" ]; then
        # The repair. One command, no interface teardown, no key change.
        if wg set "$iface" listen-port 0; then
          rebinds=$((rebinds + 1))
          rebound=1
          echo "$now" > "$stamp"
          echo "$rebinds" > "$count"
          echo "wg-watchdog: $iface sent with no reply for ''${worst_age}s; rebound the socket (rebind $rebinds)"
        else
          # Published, not just logged. "One-way and not rebound" is
          # otherwise indistinguishable from "one-way and rate limited",
          # and those call for different next steps.
          failed=1
          echo "wg-watchdog: $iface looks one-way and the rebind failed" >&2
        fi
      elif [ "$one_way" = 1 ]; then
        echo "wg-watchdog: $iface still looks one-way ''${worst_age}s; rate limited, $((minInterval - (now - last)))s to the next rebind"
      fi

      tmp="$out.tmp"
      {
        echo "# HELP repose_host_wg_one_way 1 when the host is sending on the tunnel and receiving nothing (DECISIONS I-1068)."
        echo "# TYPE repose_host_wg_one_way gauge"
        echo "repose_host_wg_one_way $one_way"
        echo "# HELP repose_host_wg_handshake_age_seconds Seconds since the oldest peer handshake on the tunnel."
        echo "# TYPE repose_host_wg_handshake_age_seconds gauge"
        echo "repose_host_wg_handshake_age_seconds $worst_age"
        echo "# HELP repose_host_wg_rebinds_total Times this boot the watchdog rebound the WireGuard socket."
        echo "# TYPE repose_host_wg_rebinds_total counter"
        echo "repose_host_wg_rebinds_total $rebinds"
        echo "# HELP repose_host_wg_rebound 1 on the run that rebound the socket, 0 otherwise."
        echo "# TYPE repose_host_wg_rebound gauge"
        echo "repose_host_wg_rebound $rebound"
        echo "# HELP repose_host_wg_rebind_failed 1 when the tunnel looked one-way and the rebind itself failed."
        echo "# TYPE repose_host_wg_rebind_failed gauge"
        echo "repose_host_wg_rebind_failed $failed"
        # node_exporter serves whatever is on disk, so a dead timer keeps
        # publishing its last numbers for ever and a stopped watchdog reads
        # exactly like a healthy tunnel. This is the freshness any reader
        # has to check first (the trap I-1010 names).
        echo "# HELP repose_host_wg_watchdog_run_seconds Unix time of this watchdog run; stale means the timer stopped, not that the tunnel is well."
        echo "# TYPE repose_host_wg_watchdog_run_seconds gauge"
        echo "repose_host_wg_watchdog_run_seconds $now"
      } > "$tmp"
      mv "$tmp" "$out"
    '';
  };
in
{
  options.repose.host.wgWatchdog = {
    enable = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Watch the host's WireGuard tunnel and rebind its socket when it
        goes one way (DECISIONS I-1068). On means a one-way tunnel repairs
        itself in about `staleAfter` plus a minute instead of waiting for a
        person; the VM tests that have no tunnel turn it off.
      '';
    };

    interface = lib.mkOption {
      type = lib.types.str;
      default = "wg0";
      description = "The WireGuard interface to watch.";
    };

    staleAfter = lib.mkOption {
      type = lib.types.int;
      default = 180;
      description = ''
        Seconds without a handshake before a tunnel that is sending and
        hearing nothing counts as one-way. WireGuard retries a handshake
        every 5 s and a peer with a 25 s keepalive handshakes at least
        every two minutes, so 180 is past anything healthy and well short
        of the hour host-02 spent down on 2026-11-10.
      '';
    };

    minInterval = lib.mkOption {
      type = lib.types.int;
      default = 600;
      description = ''
        Seconds between rebinds. A rebind that does not fix the tunnel
        means the cause is not the five-tuple, and repeating it faster
        helps nothing; `repose_host_wg_rebinds_total` climbing is how that
        case shows up.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.repose-wg-watchdog = {
      description = "Rebind the WireGuard socket when the tunnel to the edge goes one way";
      # Reads the interface only; it does not want it, so a host with no
      # tunnel yet runs it harmlessly and publishes nothing.
      after = [ "wg-quick-${cfg.interface}.service" ];
      serviceConfig = {
        Type = "oneshot";
        ExecStart = "${watchdog}/bin/repose-wg-watchdog";
      };
    };

    systemd.timers.repose-wg-watchdog = {
      description = "Check the tunnel to the edge every minute";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "2min";
        OnUnitActiveSec = "1min";
        Unit = "repose-wg-watchdog.service";
      };
    };

    systemd.tmpfiles.rules = [ "d ${textfileDir} 0755 root root -" ];
  };
}
