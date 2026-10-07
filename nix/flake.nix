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
        # The same nixpkgs as a locked github: input for the global
        # registry (DECISIONS I-531).
        repose.nixpkgsLocked = lib.mkDefault { inherit (nixpkgs) rev narHash lastModified; };
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
        # The keystroke path (DECISIONS I-576, I-494): the unit settings
        # that keep SSH, the tmux server and dev's user manager alive and
        # resident when memory runs out, and MGLRU's thrash prevention.
        # Reads the unit texts, so it builds no part of the guest system.
        # It also runs bash-env.sh's command-not-found handler with a PATH
        # that lacks repose-command-not-found, under a process limit: the
        # handler must not call itself (DECISIONS I-577).
        guest-keystroke-path =
          let
            cfg = self.guestSystem.config;
            text = units: name: pkgs.writeText (lib.replaceStrings [ "@" ] [ "-at-" ] name)
              (builtins.unsafeDiscardStringContext units.${name}.text);
            sys = text cfg.systemd.units;
            usr = text cfg.systemd.user.units;
          in
          pkgs.runCommand "guest-keystroke-path" {
            userManager = sys "user@.service";
            sshd = sys "sshd.service";
            session = sys "session-.scope";
            systemSlice = sys "system.slice";
            userSlice = sys "user.slice";
            userUidSlice = sys "user-.slice";
            guestd = sys "guestd.service";
            journald = sys "systemd-journald.service";
            logind = sys "systemd-logind.service";
            dbus = sys "dbus-broker.service";
            tmux = usr "repose-tmux-session.service";
            appSlice = usr "app.slice";
            userConf = pkgs.writeText "user.conf" (builtins.unsafeDiscardStringContext cfg.environment.etc."systemd/user.conf".text);
            tmpfiles = pkgs.writeText "tmpfiles" (lib.concatStringsSep "\n" cfg.systemd.tmpfiles.rules);
            bashrc = pkgs.writeText "bashrc" (builtins.unsafeDiscardStringContext cfg.programs.bash.interactiveShellInit);
            zshrc = pkgs.writeText "zshrc" (builtins.unsafeDiscardStringContext cfg.programs.zsh.interactiveShellInit);
            fishrc = pkgs.writeText "fishrc" (builtins.unsafeDiscardStringContext cfg.programs.fish.interactiveShellInit);
            bashEnv = ./guest/base/bash-env.sh;
            # Stands in for the helper at the store path the interactive
            # handlers name, so the check needs no nix-index database.
            stub = pkgs.writeShellScriptBin "repose-command-not-found" ''
              printf '%s: stub\n' "$1" >&2
              exit 127
            '';
          } ''
            fail=0
            need() { grep -qx "$2" "$1" || { echo "$1 lacks $2" >&2; fail=1; }; }
            need "$userManager" 'OOMScoreAdjust=-900'
            need "$userManager" 'MemoryLow=64M'
            need "$userConf" 'DefaultOOMScoreAdjust=0'
            need "$sshd" 'OOMScoreAdjust=-800'
            need "$sshd" 'MemoryLow=16M'
            need "$session" 'CPUWeight=1000'
            need "$session" 'MemoryLow=32M'
            need "$systemSlice" 'MemoryLow=176M'
            need "$userSlice" 'MemoryLow=128M'
            need "$userUidSlice" 'MemoryLow=128M'
            need "$guestd" 'MemoryLow=64M'
            need "$guestd" 'OOMScoreAdjust=-900'
            need "$journald" 'MemoryLow=64M'
            need "$logind" 'MemoryLow=16M'
            need "$dbus" 'MemoryLow=16M'
            need "$tmux" 'CPUWeight=1000'
            need "$tmux" 'MemoryLow=48M'
            need "$tmux" 'OOMPolicy=continue'
            need "$appSlice" 'MemoryLow=48M'
            need "$tmpfiles" 'w- /sys/kernel/mm/lru_gen/min_ttl_ms - - - - 1000'
            # Every not-found handler, with a PATH that lacks the helper and
            # under a process limit (DECISIONS I-577): it answers once and
            # the command's status is 127, not a fork per level until the
            # limit. bash-env.sh's runs as an agent's bash -c does; the
            # interactive bash, zsh and fish ones are cut from their init
            # text, with the helper's store path pointed at the stub. A
            # handler that called the helper by name would not reach it.
            handler() {
              sed -n "/$2/,/$3/p" "$1" \
                | sed "s#${builtins.storeDir}/[^ ]*/bin/repose-command-not-found#${stub}/bin/repose-command-not-found#"
            }
            bashFn=$(handler "$bashrc" '^command_not_found_handle() {$' '^}$')
            zshFn=$(handler "$zshrc" '^command_not_found_handler() {$' '^}$')
            fishFn=$(handler "$fishrc" '^function fish_command_not_found$' '^end$')
            for fn in "bash:$bashFn" "zsh:$zshFn" "fish:$fishFn"; do
              case "$fn" in
                *"${stub}/bin/repose-command-not-found"*) ;;
                *) echo "the interactive ''${fn%%:*} handler does not call repose-command-not-found by its store path" >&2; fail=1 ;;
              esac
            done
            try() {
              ( ulimit -u 128; HOME=$TMPDIR timeout 20 env PATH=${pkgs.coreutils}/bin "$@" 2>&1 ) || true
            }
            expect() {
              if [ "$2" != "$3" ]; then
                echo "the $1 handler with no helper on PATH printed:" >&2
                printf '%s\n' "$2" | head -5 >&2
                fail=1
              fi
            }
            expect bash-env.sh "$(try BASH_ENV="$bashEnv" ${pkgs.bash}/bin/bash -c 'cowsayzz hi; echo status=$?')" "$(printf 'cowsayzz: command not found\nstatus=127')"
            expect bash "$(try ${pkgs.bash}/bin/bash -c "$bashFn; cowsayzz hi; echo status=\$?")" "$(printf 'cowsayzz: stub\nstatus=127')"
            expect zsh "$(try ${pkgs.zsh}/bin/zsh -f -c "$zshFn; cowsayzz hi; echo status=\$?")" "$(printf 'cowsayzz: stub\nstatus=127')"
            # fish may add its own error lines after the handler's; the
            # status is 127 whatever the handler returns.
            got=$(try ${pkgs.fish}/bin/fish --no-config -c "$fishFn; cowsayzz hi; echo status=\$status")
            if [ "$(printf '%s\n' "$got" | grep -c '^cowsayzz: stub$')" != 1 ] || [ "$(printf '%s\n' "$got" | tail -1)" != status=127 ]; then
              expect fish "$got" "cowsayzz: stub, once, and status=127"
            fi
            [ "$fail" = 0 ] || exit 1
            echo "keystroke path: user manager -900, user units 0, sshd -800, MemoryLow on every slice and unit of it, tmux OOMPolicy=continue, min_ttl_ms 1000; bash-env.sh, bash, zsh and fish not-found handlers answer once with 127"
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
            python3 -c 'import sys,tomllib; c=tomllib.load(open(sys.argv[1],"rb")); sys.exit(c.get("ui",{}).get("toast",{}).get("delivery")!="herdr")' "$HOME/.config/herdr/config.toml" \
              || fail "the seed does not turn on herdr's toast delivery (I-564)"
            [ "$(grep -c 'server reload-config' "$HOME/herdr.log")" = 2 ] || fail "no reload after the file went away"
            # home-manager took the file over (moved it to .repose-bak) and
            # then let it go: the backup comes back and no backup is left to
            # make the next activation refuse.
            cfg="$HOME/.config/herdr/config.toml"
            echo 'theme = "mine"' > "$cfg"
            mv "$cfg" "$cfg.repose-bak"
            ln -s /dev/null "$cfg"
            $reload
            [ -L "$cfg" ] || fail "the seed replaced a file home-manager manages"
            rm "$cfg"
            $reload
            grep -qx 'theme = "mine"' "$cfg" || fail "the file home-manager moved aside did not come back"
            [ ! -e "$cfg.repose-bak" ] || fail "a .repose-bak is left to block the next activation"
            echo "login PATH guarded; herdr starts without guards or dropped names; reload on change only, seed restored with toasts on"
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

        # A base switch must never restart dockerd, the desktop or the
        # agents' browser (I-536); the session units are checked above. A unit nixpkgs ships
        # carries the line in its overrides.conf drop-in.
        guest-services-survive-switch = pkgs.runCommand "guest-services-survive-switch" { } ''
          etc=${self.guestSystem.config.system.build.etc}/etc/systemd
          check() {
            local files=("$1") f
            for f in "$1.d"/*.conf; do [ -e "$f" ] && files+=("$f"); done
            if ! cat "''${files[@]}" | grep -x 'X-RestartIfChanged=false' >/dev/null; then
              echo "$1 lacks X-RestartIfChanged=false" >&2; exit 1
            fi
          }
          for u in docker repose-xvnc repose-openbox repose-vncconfig repose-novnc \
            repose-novnc-proxy repose-browser repose-browser-proxy repose-browser-bridge-proxy; do
            check $etc/system/$u.service
          done
          touch $out
        '';
        # A switch that adds, changes or removes a tmux config reaches the
        # running server, and removing one leaves tmux's defaults: the
        # option, the binding and the array option the file touched
        # (DECISIONS I-552).
        guest-tmux-follows-config = pkgs.runCommand "guest-tmux-follows-config" {
          nativeBuildInputs = [ pkgs.tmux pkgs.gnugrep ];
        } ''
          unit=${self.guestSystem.config.system.build.etc}/etc/systemd/system/home-manager-dev.service
          grep -q '^ExecStartPost=-.*/bin/repose-tmux-reload$' "$unit" || { echo "$unit lacks the tmux reload" >&2; exit 1; }
          reload=${pkgs.callPackage ./guest/base/tmux-reload.nix { }}/bin/repose-tmux-reload
          export HOME=$TMPDIR/h TMUX_TMPDIR=$TMPDIR/t
          mkdir -p $HOME/.config/tmux $TMUX_TMPDIR
          conf=$HOME/.config/tmux/tmux.conf
          printf 'set -g status-position top\nbind M-z display-message reposecheck\nset -as terminal-features ",x:RGB"\n' > $conf
          tmux new-session -d -s w -x 80 -y 24
          tmux set-option -t =w: status-right forward
          features=$(tmux show -s terminal-features | wc -l)
          $reload
          [ "$(tmux show -gv status-position)" = top ] || { echo "first run lost the file's option" >&2; exit 1; }
          defaults=$(tmux -L probe -f /dev/null start-server \; list-keys | wc -l)
          keys_ok() { tmux list-keys -T prefix d >/dev/null && [ "$(tmux list-keys | wc -l)" -ge "$defaults" ]; }
          keys_ok || { echo "default bindings missing after the first run" >&2; exit 1; }
          [ "$(tmux show -s terminal-features | wc -l)" -eq "$features" ] || { echo "first run appended to terminal-features" >&2; exit 1; }
          rm $conf
          $reload
          [ "$(tmux show -gv status-position)" = bottom ] || { echo "status-position survived removal" >&2; exit 1; }
          ! tmux list-keys -T prefix | grep -q reposecheck || { echo "binding survived removal" >&2; exit 1; }
          [ "$(tmux show -s terminal-features | wc -l)" -lt "$features" ] || { echo "terminal-features kept the file's entry" >&2; exit 1; }
          [ "$(tmux show -v -t =w: status-right)" = forward ] || { echo "session option lost" >&2; exit 1; }
          [ "$(tmux list-keys | wc -l)" -eq "$defaults" ] && keys_ok || { echo "key tables are not tmux's defaults after removal" >&2; exit 1; }
          printf 'set -g status-position top\n' > $conf
          $reload
          [ "$(tmux show -gv status-position)" = top ] || { echo "added file not loaded" >&2; exit 1; }
          tmux kill-server
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
