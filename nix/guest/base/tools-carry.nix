# The guest half of the tools carry (DECISIONS I-221, I-222): the laptop's
# global tools and the commands the project's scripts run, installed in the
# background when the guest lacks them. The CLI sends the list in the
# carry's ssh (internal/cli/carry_tools.go, scan.go) and runs
# `repose-tools-install plan`, which answers in milliseconds and starts
# repose-tools-carry, a user unit of dev that does the installs at low
# priority. The unit is also wanted by default.target, so a pass a reboot
# cut short is finished at the next boot (the marker is written only when a
# pass ends). guest-conventions.md "Tools carry" has the file contract.
{ config, lib, pkgs, ... }:
let
  install = pkgs.writeShellApplication {
    name = "repose-tools-install";
    # nix, npm, go, cargo and uv are the login PATH's (the unit runs under
    # `bash -l`), so a user's own versions of them are the ones used.
    runtimeInputs = [ pkgs.jq pkgs.coreutils pkgs.gnugrep pkgs.gnused pkgs.systemd pkgs.bash ];
    text = builtins.readFile ./tools-carry.sh;
  };
  # The ruby series and java majors the CLI may ask for (I-265), the same
  # list as internal/cli/scan_runtimes.go: each must be an attribute of
  # this nixpkgs, or the base does not build.
  runtimeVersions = lib.importJSON ./runtime-versions.json;
  runtimeAttrs =
    map (v: "ruby_" + lib.replaceStrings [ "." ] [ "_" ] v) runtimeVersions.ruby
    ++ map (v: "jdk${v}_headless") runtimeVersions.java;
in
{
  assertions = map (a: {
    assertion = pkgs ? ${a};
    message = "runtime-versions.json names ${a}, which this nixpkgs does not have; update it and internal/cli/scan_runtimes.go (DECISIONS I-265)";
  }) runtimeAttrs;

  environment.systemPackages = [ install ];

  systemd.user.services.repose-tools-carry = {
    description = "repose: install the carried tools the guest lacks";
    wantedBy = [ "default.target" ];
    unitConfig = {
      ConditionUser = "dev";
      ConditionPathExists = "%h/.repose/tools-wanted.json";
    };
    serviceConfig = {
      Type = "exec";
      # A login shell: the PATH, NPM_CONFIG_PREFIX and ~/.npmrc registry
      # every terminal has.
      ExecStart = "${pkgs.bash}/bin/bash -lc 'exec ${install}/bin/repose-tools-install run'";
      Nice = 10;
      IOSchedulingClass = "idle";
      CPUWeight = 20;
      # A build (a crate, a native npm module) is throttled past 40
      # percent of the machine's memory rather than pushing the agent out
      # (DECISIONS I-1348): 1.6 GB on small, 3.2 GB on large. A temporary
      # or tiny machine never starts this unit (I-1346).
      MemoryHigh = "40%";
    };
  };
}
