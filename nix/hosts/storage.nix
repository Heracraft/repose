# LVM on the data disk: the thin pool `vg-guests/thin` created by disko
# (disko-layout.nix), autoextend so the pool grows into the VG headroom at
# 80 percent instead of filling silently, dmeventd to act on it, and a
# 5-minute timer exporting pool usage for node_exporter's textfile collector.
{ config, lib, pkgs, ... }:
let
  textfileDir = "/var/lib/node_exporter/textfile";
  poolMonitor = pkgs.writeShellApplication {
    name = "repose-pool-monitor";
    runtimeInputs = [ pkgs.lvm2 pkgs.coreutils pkgs.gawk ];
    text = ''
      out=${textfileDir}/repose_lvm.prom
      tmp="$out.tmp"
      {
        echo "# HELP repose_lvm_pool_present 1 when vg-guests/thin exists"
        echo "# TYPE repose_lvm_pool_present gauge"
        if line=$(lvs --noheadings --units b --nosuffix --separator ' ' -o lv_size,data_percent,metadata_percent vg-guests/thin 2>/dev/null); then
          read -r size data meta <<< "$line"
          echo "repose_lvm_pool_present 1"
          echo "# HELP repose_lvm_pool_size_bytes thin pool data size"
          echo "# TYPE repose_lvm_pool_size_bytes gauge"
          echo "repose_lvm_pool_size_bytes $size"
          echo "# HELP repose_lvm_pool_data_percent thin pool data used, percent"
          echo "# TYPE repose_lvm_pool_data_percent gauge"
          echo "repose_lvm_pool_data_percent $data"
          echo "# HELP repose_lvm_pool_metadata_percent thin pool metadata used, percent"
          echo "# TYPE repose_lvm_pool_metadata_percent gauge"
          echo "repose_lvm_pool_metadata_percent $meta"
          echo "# HELP repose_lvm_volumes number of guest thin volumes"
          echo "# TYPE repose_lvm_volumes gauge"
          echo "repose_lvm_volumes $(lvs --noheadings -o lv_name -S 'lv_name=~^g-' vg-guests 2>/dev/null | wc -l)"
        else
          echo "repose_lvm_pool_present 0"
        fi
      } > "$tmp"
      mv "$tmp" "$out"
    '';
  };
in
{
  services.lvm.enable = true;
  services.lvm.dmeventd.enable = true;
  services.lvm.boot.thin.enable = true;
  boot.initrd.systemd.enable = true;
  boot.initrd.services.lvm.enable = true;

  environment.etc."lvm/lvm.conf".text = lib.mkAfter ''
    activation {
      # Grow the pool by 10 percent of its size into the VG headroom every
      # time it passes 80 percent full. hostd's pool_high warning fires
      # earlier, at 70, where the api stops placing new projects on the
      # host and a human grows the disk (DECISIONS I-586).
      thin_pool_autoextend_threshold = 80
      thin_pool_autoextend_percent = 10
      monitoring = 1
    }
    devices {
      issue_discards = 1
    }
  '';

  environment.systemPackages = [
    pkgs.lvm2
    pkgs.thin-provisioning-tools
    poolMonitor
  ];

  systemd.tmpfiles.rules = [
    "d ${textfileDir} 0755 root root -"
    "d /var/lib/node_exporter 0755 root root -"
  ];

  systemd.services.repose-pool-monitor = {
    description = "Export vg-guests/thin usage for node_exporter";
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${poolMonitor}/bin/repose-pool-monitor";
    };
  };

  systemd.timers.repose-pool-monitor = {
    description = "Export thin pool usage every 5 minutes";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnBootSec = "1min";
      OnUnitActiveSec = "5min";
      Unit = "repose-pool-monitor.service";
    };
  };
}
