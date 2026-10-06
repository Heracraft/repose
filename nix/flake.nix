{
  description = "repose: hosts, edge, guest base, agent overlay, dev shell";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    microvm = {
      url = "github:microvm-nix/microvm.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    disko = {
      url = "github:nix-community/disko";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    # The prebuilt nix-index database the guest's command-not-found and
    # nix-locate read (DECISIONS I-219): the index is a fixed-output fetch,
    # so a lookup in the guest never touches the network.
    nix-index-database = {
      url = "github:nix-community/nix-index-database";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    # The user fragment for `guestSystem`. The placeholder keeps the lock
    # file valid; hostd overrides it per Build with
    # `--override-input fragment path:/var/lib/repose/builds/<rev>`
    # (docs/interfaces/nix-build-contract.md, DECISIONS I-28).
    fragment = {
      url = "path:./guest/fragment-placeholder";
      flake = false;
    };
  };

  outputs = { self, nixpkgs, home-manager, microvm, disko, nix-index-database, fragment }:
    let
      system = "x86_64-linux";
      lib = nixpkgs.lib;
      overlay = import ./overlay/agents;
      pkgs = import nixpkgs {
        inherit system;
        overlays = [ overlay ];
        config.allowUnfreePredicate = pkg: builtins.elem (lib.getName pkg) (import ./guest/unfree-allowlist.nix);
      };

      # The platform base version: the revision of this repository. The api's
      # base_versions row and /etc/repose/base-version carry the same string.
      baseVersion = self.shortRev or self.dirtyShortRev or "dirty";

      # Every Go binary in the repository (guestd, repose-hook, hostd,
      # hostdev), built from the repository root one level above this flake.
      # From `./nix` alone the parent is not in the flake source, so build with
      # `nix build '.?dir=nix#hostd'` from the repository root.
      goPkgs = import ./packages.nix { inherit pkgs lib; version = baseVersion; };
      guestd = goPkgs.guestd;
      reposeHook = goPkgs.repose-hook;

      mkGuestRunner = import ./guest/microvm.nix {
        inherit nixpkgs home-manager microvm system overlay self;
      };
      composeGuest = import ./guest/compose.nix { inherit mkGuestRunner; };
      # The fragment pipeline's checks: every example fragment composes and
      # builds, and the contract's refusals refuse (nix/guest/checks.nix).
      fragmentChecks = import ./guest/checks.nix {
        inherit pkgs lib composeGuest guestd baseVersion;
        hook = reposeHook;
        examplesDir = ../docs/features/config-examples;
      };

      guestTests = import ./guest/tests {
        inherit pkgs lib baseVersion;
        guestBase = self.nixosModules.guestBase;
        inherit guestd reposeHook;
        nixpkgsSource = nixpkgs.outPath;
        homeManagerModule = home-manager.nixosModules.home-manager;
      };

      hostModules = [
        disko.nixosModules.disko
        ./hosts
        { repose.host.hostdPackage = goPkgs.hostd; }
      ];
      mkHost = { hostName, provider ? "azure", modules ? [ ] }:
        lib.nixosSystem {
          inherit system;
          specialArgs = { inherit self; };
          modules = hostModules ++ [ { repose.host = { inherit hostName provider; }; } ] ++ modules;
        };
      hostChecks = import ./hosts/tests {
        inherit pkgs nixpkgs disko hostModules;
        # host-services registers against a real hostdev (DECISIONS I-17).
        hostdev = goPkgs.hostdev;
      };
    in {
      overlays.agents = overlay;

      # Hosts: nixos-anywhere targets. docs/workstreams/01-host-nixos.md.
      # `host` is the generic configuration; named hosts are instances of
      # lib.mkHost with their own hostName.
      nixosConfigurations.host = mkHost { hostName = "repose-host"; };
      nixosConfigurations.host-bench = mkHost { hostName = "host-bench"; };
      # Production hosts by name (infra `host_flake_attrs`); each names what
      # differs from `host`: today the api address, its CA and the Blob
      # account (DECISIONS I-40).
      nixosConfigurations.host-01 = mkHost { hostName = "host-01"; modules = [ ./hosts/host-01.nix ]; };

      # Edge: gateway + WireGuard hub. docs/workstreams/06-gateway-edge.md
      # The production values (operator addresses, the control plane's
      # WireGuard peer) are ./edge/edge-01.nix; the attribute keeps the name
      # `edge` because infra's installer keys its re-install trigger on it
      # (DECISIONS I-92).
      nixosConfigurations.edge = lib.nixosSystem {
        inherit system;
        specialArgs = { inherit self; };
        modules = [ disko.nixosModules.disko ./edge ./edge/edge-01.nix { repose.edge.gatewayPackage = goPkgs.gateway; } ];
      };

      # Guest base as a module, and the function hostd's build step calls with
      # a user fragment to produce a runner. docs/workstreams/02-guest-base.md
      # and docs/workstreams/12-nix-config-pipeline.md
      nixosModules.guestBase = {
        imports = [ ./guest/base ];
        # What lib.nixosSystem sets for the runner, for users of the module
        # outside it (the VM tests): `nixpkgs` in the guest's registry is
        # this flake's nixpkgs (DECISIONS I-218).
        nixpkgs.flake.source = lib.mkDefault nixpkgs.outPath;
        repose.nixIndexPackage = lib.mkDefault nix-index-database.packages.${system}.nix-index-with-small-db;
        repose.baseVersion = lib.mkDefault baseVersion;
        repose.guestd.package = lib.mkDefault guestd;
        repose.hookPackage = lib.mkDefault reposeHook;
      };

      lib = {
        inherit mkGuestRunner mkHost baseVersion composeGuest;
        # Name used by the scaffold; same function.
        mkGuest = mkGuestRunner;
      };

      # What hostd evaluates for a Build (docs/interfaces/nix-build-contract.md):
      # the base plus the fragment at "${fragment}/fragment.nix" (and the
      # personal layer at "${fragment}/personal.nix" when present) applied to
      # dev. `config.system.build.toplevel` is the system closure; the class
      # is not baked in (DECISIONS I-34, I-43).
      guestSystem = (composeGuest {
        fragmentPath = "${fragment}/fragment.nix";
        # The account's personal layer, when hostd wrote one beside the
        # fragment (Build.personal, DECISIONS I-490). hostd looks for the
        # string personal.nix in this file to know a base reads it.
        personalPath =
          if builtins.pathExists "${fragment}/personal.nix"
          then "${fragment}/personal.nix"
          else null;
        inherit guestd;
        # The label hostd writes next to the fragment (Build.base_version,
        # DECISIONS I-118): under `--override-input fragment` this flake
        # has no shortRev, so the stamp would read "dirty" for every guest.
        baseVersion =
          if builtins.pathExists "${fragment}/base-version"
          then lib.removeSuffix "\n" (builtins.readFile "${fragment}/base-version")
          else baseVersion;
        hook = reposeHook;
      }).guestSystem;

      packages.${system} = {
        inherit (goPkgs) guestd repose-hook hostd hostdev api repose-admin repose gateway;
        # Workstream 01's stand-in, kept for the host VM tests.
        hostd-stub = pkgs.callPackage ./hosts/hostd-stub.nix { };
        # A runner with an empty fragment: what `nix build .#guest-runner`
        # produces and what the local Cloud Hypervisor boot test uses.
        guest-runner = mkGuestRunner {
          inherit guestd;
          hook = reposeHook;
          baseVersion = baseVersion;
          class = "large";
        };
        guest-system = self.packages.${system}.guest-runner.toplevel;
        claude-code = pkgs.reposeAgents.claude-code;
        opencode = pkgs.reposeAgents.opencode;
        codex = pkgs.reposeAgents.codex;
        gemini-cli = pkgs.reposeAgents.gemini-cli;
        pi-coding-agent = pkgs.reposeAgents.pi-coding-agent;
        herdr = pkgs.reposeHerdr;
        playwright-mcp = pkgs.reposeMcp.playwright-mcp;
        chrome-devtools-mcp = pkgs.reposeMcp.chrome-devtools-mcp;
        default = self.packages.${system}.guest-runner;
      };

      # NixOS VM tests for the host (nix/hosts/tests) and the guest
      # (nix/guest/tests). They need KVM on the builder: `system-features =
      # kvm` in nix.conf.
      checks.${system} = hostChecks // guestTests // fragmentChecks // {
        # The base closure must stay under 6 GB (02 §7): every host's store
        # grows by it, and every first boot registers its metadata.
        guest-closure-size = pkgs.runCommand "guest-closure-size" {
          closureInfo = pkgs.closureInfo { rootPaths = [ self.packages.${system}.guest-system ]; };
        } ''
          size=$(cut -f2 $closureInfo/total-nar-size 2>/dev/null || true)
          [ -n "$size" ] || size=$(cat $closureInfo/total-nar-size)
          limit=$((6 * 1024 * 1024 * 1024))
          echo "guest system closure: $size bytes (limit $limit)"
          if [ "$size" -gt "$limit" ]; then
            echo "closure over 6 GB; largest paths:" >&2
            exit 1
          fi
          echo "$size" > $out
        '';
        # A base switch must never restart a session unit: the tmux or
        # herdr unit's cgroup holds every pane, so a restart ends every
        # agent on a running guest (DECISIONS I-496, I-503). It reads the
        # unit files' text, so it builds no part of the guest system. No
        # path unit may start a session before SetupProject writes this
        # boot's project.json (I-503); the tmux unit restarts its server
        # on a running machine instead (I-551).
        guest-session-survives-switch =
          let
            units = self.guestSystem.config.systemd.user.units;
            unitFile = name: pkgs.writeText name (builtins.unsafeDiscardStringContext units.${name}.text);
          in
          pkgs.runCommand "guest-session-survives-switch" {
            tmuxUnit = unitFile "repose-tmux-session.service";
            herdrUnit = unitFile "repose-herdr-server.service";
            userUnits = lib.concatStringsSep "\n" (builtins.attrNames units);
          } ''
            fail=0
            for pair in "tmux:$tmuxUnit" "herdr:$herdrUnit"; do
              m=''${pair%%:*} unit=''${pair#*:}
              grep -qx 'X-RestartIfChanged=false' "$unit" || { echo "the $m session unit lacks X-RestartIfChanged=false" >&2; fail=1; }
              grep -q "^ExecCondition=.*/bin/repose-multiplexer-is $m\$" "$unit" || { echo "the $m session unit lacks ExecCondition=repose-multiplexer-is $m" >&2; fail=1; }
              if grep -q '^WantedBy=' "$unit"; then echo "the $m session unit is wanted by a target" >&2; fail=1; fi
            done
            if printf '%s\n' "$userUnits" | grep -q '^repose-tmux-session\.path$\|^repose-herdr-server\.path$'; then
              echo "a path unit starts a session unit" >&2; fail=1
            fi
            # The tmux server exits with its last session; the unit brings
            # it back, after the wait I-352's temporary-machine check needs
            # (I-551), and a crashed or OOM-killed one too; a failed start
            # is never restarted, so a start that leaves no server cannot
            # loop (I-560).
            grep -qx 'Restart=on-success' "$tmuxUnit" || { echo "the tmux session unit lacks Restart=on-success" >&2; fail=1; }
            grep -qx 'RestartForceExitStatus=SIGKILL SIGSEGV SIGABRT SIGBUS' "$tmuxUnit" || { echo "the tmux session unit lacks RestartForceExitStatus for SIGKILL and crashes" >&2; fail=1; }
            for unit in "$tmuxUnit" "$herdrUnit"; do
              grep -qx 'RestartSec=5s' "$unit" || { echo "$unit lacks RestartSec=5s" >&2; fail=1; }
            done
            # herdr's panes share its unit: an OOM kill in a pane must not
            # stop it, a live handoff must not end it, and a watcher ends it
            # when no server is left, which Restart=always brings back,
            # at most five times a minute (I-560).
            grep -qx 'OOMPolicy=continue' "$herdrUnit" || { echo "the herdr session unit lacks OOMPolicy=continue" >&2; fail=1; }
            grep -qx 'ExitType=cgroup' "$herdrUnit" || { echo "the herdr session unit lacks ExitType=cgroup" >&2; fail=1; }
            grep -q '^ExecStartPost=.*/bin/repose-herdr-watch &' "$herdrUnit" || { echo "the herdr session unit does not start repose-herdr-watch" >&2; fail=1; }
            grep -qx 'Restart=always' "$herdrUnit" || { echo "the herdr session unit lacks Restart=always" >&2; fail=1; }
            grep -qx 'StartLimitBurst=5' "$herdrUnit" || { echo "the herdr session unit lacks StartLimitBurst=5" >&2; fail=1; }
            [ "$fail" = 0 ] || exit 1
            echo "both session units: X-RestartIfChanged=false, ExecCondition, no WantedBy, back after 5s; no path unit; tmux Restart=on-success; herdr OOMPolicy=continue, ExitType=cgroup, watcher, Restart=always"
            touch $out
          '';
        # The session units start their servers outside a login shell and
        # read the login PATH through repose-login-path, so a profile that
        # prints text or replaces the shell (machine.nix's
        # programs.bash.profileExtra) cannot break them; the herdr server's
        # environment lets each pane's shell load the current one; and a
        # running herdr rereads a config.toml a switch changed (DECISIONS
        # I-563). Runs the scripts against a scratch HOME and a fake herdr.
        guest-session-environment =
          let
            loginPath = import ./guest/base/login-path.nix { inherit pkgs; };
            names = pkgs.writeText "session-vars.names" "DROPPED_VAR\n";
            fakeHerdr = pkgs.writeShellScriptBin "herdr" ''
              echo "$*" >> "$HOME/herdr.log"
              if [ "$1" = server ] && [ $# = 1 ]; then ${pkgs.coreutils}/bin/env > "$HOME/server.env"; fi
            '';
            start = import ./guest/base/herdr-start.nix { inherit pkgs; herdr = fakeHerdr; namesFile = names; };
            herdrConfig = import ./guest/base/herdr-config.nix { inherit pkgs; herdr = fakeHerdr; };
            unit = pkgs.writeText "herdr-unit" (builtins.unsafeDiscardStringContext
              self.guestSystem.config.systemd.user.units."repose-herdr-server.service".text);
          in
          pkgs.runCommand "guest-session-environment" { nativeBuildInputs = [ pkgs.python3 ]; } ''
            fail() { echo "$*" >&2; exit 1; }
            export HOME=$PWD/home USER=dev
            mkdir -p "$HOME/bin"
            # A profile that prints text: the PATH comes back without it.
            printf 'echo hello from the profile\nexport PATH=$HOME/bin:$PATH\n' > "$HOME/.bash_profile"
            got=$(${loginPath}/bin/repose-login-path)
            case "$got" in
              "$HOME/bin:"*) ;;
              *) fail "repose-login-path printed '$got' for a profile that prints text" ;;
            esac
            # A profile that replaces the shell: nothing, exit 0.
            printf 'exec sh\n' > "$HOME/.bash_profile"
            got=$(${loginPath}/bin/repose-login-path) || fail "repose-login-path failed for a profile that runs exec"
            [ -z "$got" ] || fail "repose-login-path printed '$got' for a profile that runs exec"

            # The herdr server starts from the login PATH's herdr, without
            # the profile guards or a dropped session variable.
            ln -s ${fakeHerdr}/bin/herdr "$HOME/bin/herdr"
            printf 'echo noise\nexport PATH=$HOME/bin:$PATH\n' > "$HOME/.bash_profile"
            __NIXOS_SET_ENVIRONMENT_DONE=1 __ETC_PROFILE_DONE=1 __HM_SESS_VARS_SOURCED=1 DROPPED_VAR=old KEPT_VAR=yes \
              ${start}/bin/repose-herdr-start
            [ -s "$HOME/server.env" ] || fail "repose-herdr-start did not run herdr server"
            for v in __NIXOS_SET_ENVIRONMENT_DONE __ETC_PROFILE_DONE __HM_SESS_VARS_SOURCED DROPPED_VAR; do
              if grep -q "^$v=" "$HOME/server.env"; then fail "the herdr server kept $v"; fi
            done
            grep -qx 'KEPT_VAR=yes' "$HOME/server.env" || fail "the herdr server lost a variable outside the session list"
            grep -q "^PATH=$HOME/bin:" "$HOME/server.env" || fail "the herdr server did not get the login PATH"
            # The same with a profile that replaces the shell: the server
            # still starts, from the unit's PATH.
            printf 'exec sh\n' > "$HOME/.bash_profile"
            rm "$HOME/server.env"
            PATH=${fakeHerdr}/bin:$PATH ${start}/bin/repose-herdr-start
            [ -s "$HOME/server.env" ] || fail "repose-herdr-start did not start herdr when the profile runs exec"
            grep -q '^ExecStart=.*/bin/repose-herdr-start$' ${unit} || fail "the herdr unit does not start through repose-herdr-start"
            if grep -q 'bash -lc' ${unit}; then fail "the herdr unit starts a login shell"; fi

            # The reload: digest only with no server; a reload when the
            # file changed while one runs; none when it did not; the seed
            # back when the file went away.
            rm -f "$HOME/herdr.log" "$HOME/.bash_profile"
            reload=${herdrConfig.reload}/bin/repose-herdr-reload
            mkdir -p "$HOME/.config/herdr"
            echo 'theme = "a"' > "$HOME/.config/herdr/config.toml"
            $reload
            [ ! -e "$HOME/herdr.log" ] || fail "reloaded with no server"
            python3 -c 'import socket,sys; socket.socket(socket.AF_UNIX).bind(sys.argv[1])' "$HOME/.config/herdr/herdr.sock"
            $reload
            [ ! -e "$HOME/herdr.log" ] || fail "reloaded an unchanged config"
            echo 'theme = "b"' > "$HOME/.config/herdr/config.toml"
            $reload
            [ "$(grep -c 'server reload-config' "$HOME/herdr.log")" = 1 ] || fail "no reload after a change"
            rm "$HOME/.config/herdr/config.toml"
            $reload
            grep -q 'shell_mode = "login"' "$HOME/.config/herdr/config.toml" || fail "the seed did not come back"
            [ "$(grep -c 'server reload-config' "$HOME/herdr.log")" = 2 ] || fail "no reload after the file went away"
            echo "login PATH guarded; herdr starts without guards or dropped names; reload on change only, seed restored"
            touch $out
          '';
        # The herdr package's install check refuses a release whose socket
        # protocol guestd and a laptop herdr cannot speak (DECISIONS I-501):
        # generation 2, protocol 21, and unreadable output each fail it,
        # and generation 1 at protocol 22 passes.
        herdr-protocol-check =
          let
            check = pkgs.reposeHerdr.protocolCheck;
            fake = name: out: pkgs.writeShellScript "herdr-${name}" ''
              if [ "$*" = "status client --json" ]; then echo '${out}'; else exit 64; fi
            '';
          in
          pkgs.runCommand "herdr-protocol-check" { } ''
            ok() { "${check}" "$1" || { echo "refused $2, which it must accept" >&2; exit 1; }; }
            refused() {
              if "${check}" "$1"; then echo "accepted $2, which it must refuse" >&2; exit 1; fi
            }
            ok ${fake "good" ''{"version":"0.9.3","protocol":22,"endpoint_protocol_generation":1}''} "generation 1, protocol 22"
            ok ${fake "newer" ''{"version":"0.10.0","protocol":23,"endpoint_protocol_generation":1}''} "generation 1, protocol 23"
            refused ${fake "gen2" ''{"version":"1.0.0","protocol":22,"endpoint_protocol_generation":2}''} "generation 2"
            refused ${fake "old" ''{"version":"0.8.0","protocol":21,"endpoint_protocol_generation":1}''} "protocol 21"
            refused ${fake "nogen" ''{"version":"0.7.0","protocol":22}''} "no generation"
            refused ${fake "junk" "not json"} "unreadable output"
            touch $out
          '';
        guest-runner-builds = self.packages.${system}.guest-runner;
        # docs/workstreams/04-guestd.md §7: the real binary exercised inside a
        # real guest.
        guestd = import ./guest/tests/guestd.nix {
          inherit pkgs;
          guestdPackage = guestd;
          # The hook subtest runs repose-hook; without the package the node
          # has no such command and the test fails on PATH rather than on
          # anything it is checking.
          hookPackage = reposeHook;
        };
      };

      devShells.${system} = {
        default = pkgs.mkShell {
          packages = (import ./guest/base/tool-list.nix pkgs) ++ (with pkgs; [
            reposeAgents.claude-code reposeAgents.opencode
            go_1_26 gopls golangci-lint buf protoc-gen-go protoc-gen-go-grpc
            opentofu azure-cli just nixos-anywhere nixos-rebuild
            postgresql_16 sqlc wireguard-tools
            # Observability (docs/workstreams/10-observability.md): promtool
            # checks and tests ops/alerts.yaml, python3 generates and
            # validates the dashboards, docker compose runs the local stack.
            prometheus.cli python3 docker-compose
          ]);
        };

        # `nix develop ./nix#infra`. tfsec runs the policies in
        # infra/policy/tfsec; it is here rather than in the default shell
        # because only workstream 11 needs it.
        # docs/workstreams/11-infra-opentofu.md §7.
        infra = pkgs.mkShell {
          packages = with pkgs; [
            opentofu azure-cli tfsec just jq nixos-anywhere wireguard-tools
          ];
        };
      };

      formatter.${system} = pkgs.nixfmt;
    };
}
