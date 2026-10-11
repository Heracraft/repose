# LVM on the data disk: the thin pool `vg-guests/thin` created by disko
# (disko-layout.nix), autoextend so the pool grows into the VG headroom at
# 80 percent instead of filling silently, dmeventd to act on it, the one
# unit that activates the pool after boot (repose-guest-pool, DECISIONS
# I-643), and a 5-minute timer exporting pool state for node_exporter's
# textfile collector.
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
  textfileDir = "/var/lib/node_exporter/textfile";
  # The lvm built with dmeventd support. pkgs.lvm2 is built without it, and
  # its `lvchange --monitor y` does nothing and says nothing: host-01's pool
  # stayed unmonitored after the 2026-10-10 reboot because that was the lvm
  # on PATH (I-643).
  lvm = lib.getBin config.services.lvm.package;

  poolMonitor = pkgs.writeShellApplication {
    name = "repose-pool-monitor";
    runtimeInputs = [ lvm pkgs.coreutils pkgs.gawk ];
    text = ''
      out=${textfileDir}/repose_lvm.prom
      tmp="$out.tmp"
      # A metric whose value lvs left empty (an inactive pool has no
      # percentages) is left out: node_exporter rejects the whole file for
      # one line without a value.
      metric() { # name type help value
        [ -n "$4" ] || return 0
        echo "# HELP $1 $3"
        echo "# TYPE $1 $2"
        echo "$1 $4"
      }
      {
        if line=$(lvs --noheadings --units b --nosuffix --separator '|' -o lv_size,data_percent,metadata_percent,lv_active,seg_monitor vg-guests/thin 2>/dev/null); then
          IFS='|' read -r size data meta active monitored <<< "$(echo "$line" | tr -d ' ')"
          metric repose_lvm_pool_present gauge "1 when vg-guests/thin exists" 1
          if [ "$active" = active ]; then a=1; else a=0; fi
          metric repose_lvm_pool_active gauge "1 when vg-guests/thin is active" "$a"
          if [ "$monitored" = monitored ]; then mon=1; else mon=0; fi
          metric repose_lvm_pool_monitored gauge "1 when dmeventd monitors vg-guests/thin (autoextend needs it)" "$mon"
          metric repose_lvm_pool_size_bytes gauge "thin pool data size" "$size"
          metric repose_lvm_pool_data_percent gauge "thin pool data used, percent" "$data"
          metric repose_lvm_pool_metadata_percent gauge "thin pool metadata used, percent" "$meta"
          metric repose_lvm_volumes gauge "number of guest thin volumes" \
            "$(lvs --noheadings -o lv_name -S 'lv_name=~^g-' vg-guests 2>/dev/null | wc -l)"
          if [ "$a" = 1 ]; then
            metric repose_lvm_volumes_inactive gauge "guest and login-share volumes left inactive with the pool active" \
              "$(lvs --noheadings -o lv_name,lv_active vg-guests 2>/dev/null | awk '$1 ~ /^(g|auth)-/ && $2 != "active" { n++ } END { print n + 0 }')"
          fi
        else
          metric repose_lvm_pool_present gauge "1 when vg-guests/thin exists" 0
          metric repose_lvm_pool_active gauge "1 when vg-guests/thin is active" 0
        fi
      } > "$tmp"
      mv "$tmp" "$out"
    '';
  };

  # repose-guest-pool (DECISIONS I-643). On 2026-10-10 the initrd's LVM
  # started checking the pool and was killed halfway through; its leftover
  # device-mapper tables blocked every later activation and nothing tried
  # again. Now the initrd has no LVM, nothing autoactivates vg-guests, and
  # this script is the one thing that activates the pool. Exit 0: pool and
  # repose-cache active. Exit 1: the VG never appeared, or three attempts
  # failed. Exit 2: thin_check says the metadata is damaged; never retried
  # and never repaired, the owner decides (RUNBOOK "PoolUnavailable").
  # Every failure leaves /run/repose/pool-hold: hostd's Wants= and the cache
  # volume's Requires= would otherwise start the failed unit again at every
  # hostd restart and every switch, so the unit's ExecCondition skips it
  # until an operator removes the file. It is on /run, so a reboot runs the
  # unit once more.
  # Log lines carry the VG, volume and dm device names and exit codes only.
  poolHold = "/run/repose/pool-hold";
  poolHeld = pkgs.writeShellScript "repose-guest-pool-held" ''
    if [ -e ${poolHold} ]; then
      echo "repose-guest-pool: event=pool_held marker=${poolHold}"
      exit 1
    fi
  '';
  poolScript = pkgs.writeShellApplication {
    name = "repose-guest-pool";
    runtimeInputs = [ lvm pkgs.coreutils pkgs.gnugrep pkgs.gawk pkgs.systemd pkgs.util-linux ];
    text = ''
      vg="vg-guests"
      pool=thin
      # dm names double the dashes of the VG and LV names.
      dmpool="vg--guests-thin"
      vg_wait=''${REPOSE_POOL_VG_WAIT:-300}
      say() { echo "repose-guest-pool: $*"; }
      hold() {
        mkdir -p "$(dirname ${poolHold})"
        : > ${poolHold}
        exit "$1"
      }
      rm -f ${poolHold}

      udevadm settle --timeout=60 || true
      waited=0
      until vgs "$vg" >/dev/null 2>&1; do
        if [ "$waited" -ge "$vg_wait" ]; then
          say "event=pool_vg_missing vg=$vg waited_s=$waited"
          hold 1
        fi
        sleep 1
        waited=$((waited + 1))
      done

      # Event activation (pvscan from udev, lvm-activate-vg-guests) must
      # never race this unit: the empty auto_activation_volume_list in
      # lvm.conf covers this boot, the VG flag any other config.
      if [ "$(vgs --noheadings -o autoactivation "$vg" | tr -d ' ')" = enabled ]; then
        vgchange --setautoactivation n "$vg"
        say "event=pool_autoactivation_off vg=$vg"
      fi

      lvm_says_active() {
        [ "$(lvs --noheadings -o lv_active "$vg/$pool" 2>/dev/null | tr -d ' ')" = active ]
      }
      # Active as lvm says and really a thin-pool target: `-tpool` once the
      # pool has thin volumes, the pool's own device while it has none.
      pool_active() {
        local table
        lvm_says_active || return 1
        table=$(dmsetup table "$dmpool-tpool" 2>/dev/null || dmsetup table "$dmpool" 2>/dev/null || true)
        grep -q ' thin-pool ' <<< "$table"
      }
      open_count() { dmsetup info -c --noheadings -o open "$1" 2>/dev/null | tr -d ' '; }

      # Leftovers of an interrupted activation, top down: tpool holds tmeta
      # and tdata, so the children can only go once their parent has. A
      # device held open by anything else ends the attempt untouched.
      clear_leftovers() {
        local d n names
        # Never take apart a pool lvm itself reports active.
        if lvm_says_active; then
          say "event=pool_leftover_held reason=pool_reported_active"
          return 1
        fi
        names=$(dmsetup info -c --noheadings -o name 2>/dev/null || true)
        if grep -qE "^vg--guests-(g--|auth--|repose--cache)" <<< "$names"; then
          say "event=pool_leftover_held reason=thin_volume_device_present"
          return 1
        fi
        for d in "$dmpool" "$dmpool-tpool" "$dmpool"_tdata "$dmpool"_tmeta; do
          dmsetup info "$d" >/dev/null 2>&1 || continue
          n=$(open_count "$d")
          if [ "$n" != 0 ]; then
            say "event=pool_leftover_held device=$d open_count=$n"
            return 1
          fi
          dmsetup remove "$d"
          say "event=pool_leftover_removed device=$d"
        done
      }

      attempt() {
        local err rc status
        if pool_active; then
          say "event=pool_already_active vg=$vg"
        else
          clear_leftovers || return 1
          err=$(mktemp)
          rc=0
          lvchange -ay "$vg/$pool" 2>"$err" || rc=$?
          if [ "$rc" != 0 ]; then
            # lvm reports thin_check's own exit as "(status:N)"; -1 is a
            # check that was killed, which is not damage.
            status=$(grep -oE 'Check of pool .* failed \(status:-?[0-9]+\)' "$err" | grep -oE -- '-?[0-9]+\)$' | tr -d ')' || true)
            rm -f "$err"
            if [ -n "$status" ] && [ "$status" -gt 0 ]; then
              say "event=pool_check_failed vg=$vg pool=$pool status=$status"
              hold 2
            fi
            say "event=pool_activate_failed vg=$vg pool=$pool exit=$rc"
            return 1
          fi
          rm -f "$err"
          say "event=pool_activated vg=$vg pool=$pool"
        fi
        # Every volume but the snapshots, which are created with the skip flag.
        vgchange -ay "$vg" >/dev/null || say "event=pool_volumes_partial vg=$vg"
        pool_active || return 1
        if lvs "$vg/repose-cache" >/dev/null 2>&1 &&
          [ "$(lvs --noheadings -o lv_active "$vg/repose-cache" | tr -d ' ')" != active ]; then
          say "event=pool_cache_inactive vg=$vg"
          return 1
        fi
        return 0
      }

      delays=(10 30 60)
      ok=0
      for i in 1 2 3; do
        if attempt; then ok=1; break; fi
        [ "$i" = 3 ] && break
        sleep "''${REPOSE_POOL_RETRY_DELAY:-''${delays[$((i - 1))]}}"
      done
      if [ "$ok" != 1 ]; then
        say "event=pool_unavailable vg=$vg attempts=3"
        hold 1
      fi

      # A tenant volume that will not activate fails its own guest's start
      # (hostd activates once and answers volume_unavailable), never the
      # pool, the caches or every other guest.
      # lvm's own selection regex refuses alternation; awk reads the list.
      inactive=$(lvs --noheadings -o lv_name,lv_active "$vg" | awk '$1 ~ /^(g|auth)-/ && $2 != "active" { n++ } END { print n + 0 }')
      if [ "$inactive" != 0 ]; then
        say "event=pool_volumes_inactive vg=$vg count=$inactive"
      fi

      # dmeventd: autoextend acts only on a monitored pool. lvm2-monitor
      # ran at boot while the pool was inactive and monitored nothing.
      systemctl start dm-event.service || true
      lvchange --monitor y "$vg/$pool" || true
      if [ "$(lvs --noheadings -o seg_monitor "$vg/$pool" | tr -d ' ')" = monitored ]; then
        say "event=pool_monitored vg=$vg pool=$pool"
      else
        say "event=pool_unmonitored vg=$vg pool=$pool"
      fi
      ${poolMonitor}/bin/repose-pool-monitor || true
    '';
  };
in
{
  options.repose.host.dataDisk.enable = lib.mkOption {
    type = lib.types.bool;
    default = true;
    description = ''
      The host has the data disk that holds vg-guests. It turns on
      repose-guest-pool, the one unit that activates the guest pool after
      boot, and the cache volume's dependency on it (DECISIONS I-643). The
      VM tests without a data disk turn it off.
    '';
  };

  config = lib.mkMerge [
    {
      services.lvm.enable = true;
      services.lvm.dmeventd.enable = true;
      services.lvm.boot.thin.enable = true;
      boot.initrd.systemd.enable = true;
      # The root filesystem is ext4 on the OS disk: the initrd needs no LVM
      # and must never touch vg-guests. Its default is on whenever the
      # systemd initrd is; on 2026-10-10 it began a thin_check of the pool
      # that the switch to the real root killed (DECISIONS I-643).
      boot.initrd.services.lvm.enable = false;

      environment.etc."lvm/lvm.conf".text = lib.mkAfter ''
        activation {
          # Grow the pool by 10 percent of its size into the VG headroom every
          # time it passes 80 percent full. hostd's pool_high warning fires
          # earlier, at 70, where the api stops placing new projects on the
          # host and a human grows the disk (DECISIONS I-586).
          thin_pool_autoextend_threshold = 80
          thin_pool_autoextend_percent = 10
          monitoring = 1
          # Nothing is activated by udev events or at boot by lvm itself:
          # repose-guest-pool owns activation (DECISIONS I-643).
          auto_activation_volume_list = [ ]
        }
        devices {
          issue_discards = 1
        }
      '';

      # The dmeventd-capable lvm is the one on PATH (the lvm module adds it);
      # pkgs.lvm2 next to it put the other one first.
      environment.systemPackages = [
        pkgs.thin-provisioning-tools
        poolMonitor
      ];

      systemd.tmpfiles.rules = [
        "d ${textfileDir} 0755 root root -"
        "d /var/lib/node_exporter 0755 root root -"
      ];

      systemd.services.repose-pool-monitor = {
        description = "Export vg-guests/thin state for node_exporter";
        serviceConfig = {
          Type = "oneshot";
          ExecStart = "${poolMonitor}/bin/repose-pool-monitor";
        };
      };

      systemd.timers.repose-pool-monitor = {
        description = "Export thin pool state every 5 minutes";
        wantedBy = [ "timers.target" ];
        timerConfig = {
          OnBootSec = "1min";
          OnUnitActiveSec = "5min";
          Unit = "repose-pool-monitor.service";
        };
      };
    }

    (lib.mkIf cfg.dataDisk.enable {
      systemd.services.repose-guest-pool = {
        description = "Activate the guest pool vg-guests/thin (DECISIONS I-643)";
        wantedBy = [ "multi-user.target" ];
        after = [ "systemd-udevd.service" "dm-event.socket" ];
        wants = [ "dm-event.socket" ];
        before = [ "hostd.service" "repose-cache-volume.service" ];
        # A switch never re-runs it: on a live host the pool is active and a
        # second run would find nothing to do, and a changed script is
        # proven at the next boot instead.
        restartIfChanged = false;
        # Its retries are its own and a failure holds it; systemd's start
        # limit would only refuse the operator's start after a few held
        # skips (start-limit-hit, seen in host-reboot).
        startLimitIntervalSec = 0;
        serviceConfig = {
          Type = "oneshot";
          RemainAfterExit = true;
          # thin_check of a large pool after a crash takes minutes.
          TimeoutStartSec = "30min";
          # A failed run held the unit (see poolScript): skipped, not failed.
          ExecCondition = "${poolHeld}";
          ExecStart = "${poolScript}/bin/repose-guest-pool";
        };
      };
    })
  ];
}
