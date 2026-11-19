# A stand-in for the platform flake, for the real-Nix tests of the build
# pipeline (realnix_test.go): the same `fragment` input and `guestSystem`
# output as nix/flake.nix, nixpkgs at the platform's locked revision, and
# no home-manager or base, so every case runs in seconds. The fragment is
# applied as a plain function of pkgs; the toplevel has the kernel, initrd,
# init and kernel-params files hostd reads (nix-build-contract.md).
{
  description = "repose nixbuild test flake";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/b1b875982b17dabde9b4a37f3e229e74913e6db3";
  inputs.fragment = { url = "path:./fragment-placeholder"; flake = false; };
  outputs = { self, nixpkgs, fragment }:
    let
      system = "x86_64-linux";
      real = import nixpkgs { inherit system; };
      # The test packages the canonical cases name, next to real nixpkgs.
      pkgs = real // {
        sleep-forever = real.runCommand "sleep-forever-1.0" { } "sleep 1860; echo done > $out";
        big = real.runCommand "big-1.0" { } "head -c 120000000 /dev/zero > $out";
        fails = real.runCommand "fails-1.0" { } "echo compiling; echo boom; exit 1";
      };
      frag = import "${fragment}/fragment.nix";
      cfg = if builtins.isFunction frag then frag { inherit pkgs; lib = real.lib; config = { }; } else frag;
      # home-manager forces every option a fragment sets; so does this,
      # stopping at derivations (which reference themselves).
      force = v:
        if builtins.isAttrs v then
          (if v ? type && v.type == "derivation" then builtins.seq v.outPath true
           else builtins.all (n: force v.${n}) (builtins.attrNames v))
        else if builtins.isList v then builtins.all force v
        else builtins.seq v true;
      packages = assert force cfg; cfg.home.packages or [ ];
      # The fragment may name a kernel version so kernel_changed can be
      # exercised: "kernel-<v>" is a different store path per version.
      kernelVersion = cfg.repose.testKernel or "6.17.4";
      kernel = real.runCommand "kernel-${kernelVersion}" { } "echo bzImage-${kernelVersion} > $out";
      initrd = real.runCommand "initrd-${kernelVersion}" { } "echo initrd > $out";
      toplevel = real.runCommand "nixos-system-repose-guest-test" { inherit packages; } ''
        mkdir -p $out
        for p in $packages; do echo $p >> $out/packages; done
        ln -s ${kernel} $out/kernel
        ln -s ${initrd} $out/initrd
        echo '#!/bin/sh' > $out/init
        chmod +x $out/init
        echo 'console=ttyS0' > $out/kernel-params
      '';
    in
    {
      guestSystem = { config.system.build.toplevel = toplevel; };
      # The per-architecture name hostd evaluates on aarch64 (I-1374).
      guestSystems.aarch64-linux = { config.system.build.toplevel = toplevel; };
    };
}
