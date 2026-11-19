# Disk layout for nixos-anywhere; the shape lives in disko-layout.nix.
# Both devices are options so workstream 11 can pass what it attached. A
# missing data disk makes disko fail with `device ... not found` before it
# touches the OS disk (01-host-nixos §6).
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
in
{
  disko.devices = import ./disko-layout.nix {
    inherit lib;
    inherit (cfg) osDevice dataDevice;
    azureUdevRules = if cfg.provider == "azure" then "${pkgs.waagent}/etc/udev/rules.d" else null;
    # The Mac appliance has one disk (mac.nix, DECISIONS I-1376): the PV
    # of vg-guests is a third partition of the OS disk and there is no
    # data disk.
    oneDisk = cfg.provider == "mac";
    rootSize = cfg.mac.rootSize;
    poolMetadataFloor = if cfg.provider == "mac" then 64 * 1024 * 1024 else 1073741824;
  };
}
