# NixOS VM tests for the guest base (docs/workstreams/02-guest-base.md §7).
# They run on QEMU through the NixOS test driver, whose store layout is the
# same shape as the microvm one (/nix/.ro-store read-only, /nix/.rw-store
# overlay upper), so the overlay logic is tested without Cloud Hypervisor.
# guestd is replaced by a fake that owns /run/repose/hooks.sock and records
# every POST, because workstream 04's binary is not part of this base.
{ pkgs, lib, baseVersion, guestBase, guestd, reposeHook, nixpkgsSource, homeManagerModule }:
let
  fakeGuestd = pkgs.writers.writePython3Bin "fake-guestd" { } ''
    import json
    import os
    import socketserver
    import grp
    from http.server import BaseHTTPRequestHandler

    SOCK = "/run/repose/hooks.sock"
    LOG = "/run/repose/hooks.log"


    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            n = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(n)
            try:
                json.loads(body)
            except ValueError:
                self.send_response(400)
                self.end_headers()
                return
            with open(LOG, "ab") as f:
                f.write(body + b"\n")
            self.send_response(200)
            self.end_headers()

        def log_message(self, *a):
            pass


    class Server(socketserver.UnixStreamServer):
        allow_reuse_address = True


    if os.path.exists(SOCK):
        os.unlink(SOCK)
    srv = Server(SOCK, Handler)
    os.chown(SOCK, 0, grp.getgrnam("dev").gr_gid)
    os.chmod(SOCK, 0o660)
    print("fake guestd listening on " + SOCK, flush=True)
    srv.serve_forever()
  '';

  node = { config, pkgs, lib, ... }: {
    imports = [ guestBase ];
    repose.class = "large";
    repose.applyOverlay = false;
    repose.baseVersion = baseVersion;
    repose.guestd.package = guestd;
    repose.hookPackage = reposeHook;
    systemd.services.guestd.serviceConfig.ExecStart = lib.mkForce "${fakeGuestd}/bin/fake-guestd";
    # The test driver gives root a password file; the guest has none and the
    # test asserts that. The driver's backdoor shell needs no password.
    users.users.root.hashedPasswordFile = lib.mkForce null;
    # The test framework's own network config for eth1; the guest network
    # module expects eth0 from the runner, which does not exist here.
    virtualisation.memorySize = 3072;
    virtualisation.cores = 2;
    virtualisation.diskSize = 8192;
    # ssh-keygen and ssh client for the certificate test.
    environment.systemPackages = [ pkgs.openssh ];
    # A path in the host store but outside the system closure, registered
    # in the VM's database so the pin test can install it offline.
    # gcLowerProbe is registered and unrooted: a dead path in the lower
    # layer, which repose-store-gc must leave alone (I-529).
    virtualisation.additionalPaths = [ pkgs.hello gcLowerProbe ];
  };

  gcLowerProbe = pkgs.writeText "repose-gc-lower-probe" "in the host store only\n";

  helloImage = pkgs.dockerTools.buildImage {
    name = "repose-hello";
    tag = "test";
    copyToRoot = pkgs.buildEnv { name = "hello-root"; paths = [ pkgs.hello ]; pathsToLink = [ "/bin" ]; };
    config.Cmd = [ "/bin/hello" ];
  };

  claudeStopPayload = builtins.toJSON {
    session_id = "abc"; hook_event_name = "Stop"; transcript_path = "/tmp/transcript.jsonl"; stop_hook_active = false;
  };
  transcript = ''
    {"type":"user","message":{"role":"user","content":"do the thing"}}
    {"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"All tests pass and the change is committed."}]}}
  '';
  userHook = builtins.toJSON {
    hooks.Stop = [ { matcher = ""; hooks = [ { type = "command"; command = "echo user-hook"; } ]; } ];
    permissions.allow = [ "Bash(ls:*)" ];
  };

  # A dynamically linked program as a download would be: interpreter
  # /lib64/ld-linux-x86-64.so.2 and no rpath, so it finds neither the
  # loader nor libz without nix-ld (I-218).
  foreignElf = pkgs.runCommandCC "foreign-elf" {
    nativeBuildInputs = [ pkgs.patchelf ];
    buildInputs = [ pkgs.zlib ];
  } ''
    cat > m.c <<'EOF'
    #include <stdio.h>
    #include <zlib.h>
    int main(void) { printf("foreign ok zlib %s\n", zlibVersion()); return 0; }
    EOF
    $CC m.c -lz -o foreign
    patchelf --set-interpreter /lib64/ld-linux-x86-64.so.2 --remove-rpath foreign
    mkdir -p $out/bin
    cp foreign $out/bin/
  '';

  # Downloads the compat test runs offline (I-228): Prisma's
  # debian-openssl-3.0.x schema engine for 6.16's engines commit, which is
  # what the redirect sends a linux-nixos request to, and a manylinux numpy
  # wheel that needs libstdc++ from outside the nixpkgs python.
  prismaCommit = "1c57fdcd7e44b29b9313256c76699e91c3ac3c43";
  prismaSchemaEngineGz = pkgs.fetchurl {
    url = "https://binaries.prisma.sh/all_commits/${prismaCommit}/debian-openssl-3.0.x/schema-engine.gz";
    sha256 = "ee38b431ac7281e87cfbf3b940cdf40d76c6f0f9b06daa9948bb950de472cb56";
  };
  numpyWheel = pkgs.fetchurl {
    url = "https://files.pythonhosted.org/packages/51/64/7de3c91e821a2debf77c92962ea3fe6ac2bc45d0778c1cbe15d4fce2fd94/numpy-2.3.3-cp312-cp312-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl";
    sha256 = "d9192da52b9745f7f0766531dcfa978b7763916f158bb63bdb8a1eca0068ab20";
  };
  numpyWheelName = "numpy-2.3.3-cp312-cp312-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl";
  userBinDirsForTest = import ../base/user-bin-dirs.nix;

  # guest-devtools' stand-in agent (I-241): wrapped exactly like the five,
  # it records what environment the wrapper handed it. repose-probe-0 is
  # the first user bin dir's probe program from the I-227 subtest.
  envProbeAgent = (import ../../overlay/agents/wrap.nix { inherit pkgs; }) {
    name = "repose-env-probe";
    pkg = pkgs.writeShellScriptBin "repose-env-probe" ''
      {
        echo "project=''${REPOSE_PROJECT:-}"
        echo "secret=''${MY_TOKEN:-}"
        echo "agent=''${REPOSE_HOOK_AGENT:-}"
        if command -v repose-probe-0 >/dev/null 2>&1; then echo path=ok; fi
        echo done
      } > /tmp/out-agent 2>&1
      sleep 30
    '';
  };

  # guest-devshell's stand-in agent (I-259): wrapped like the five, it
  # records what of the checkout's dev environment it sees, in a file
  # named after its working directory.
  devshellProbeAgent = (import ../../overlay/agents/wrap.nix { inherit pkgs; }) {
    name = "repose-devshell-probe";
    pkg = pkgs.writeShellScriptBin "repose-devshell-probe" ''
      out=/tmp/out-$(basename "$PWD")
      {
        echo "flake=''${REPOSE_FLAKE_PROBE:-}"
        echo "envrc=''${ENVRC_ONLY:-}"
        echo "project=''${REPOSE_PROJECT:-}"
        if command -v flake-tool >/dev/null 2>&1; then echo "tool=$(flake-tool)"; fi
        # The fragment's session variable and path (I-488), in the agent
        # and in a command it runs (a non-interactive bash).
        echo "frag=''${REPOSE_FRAG_PROBE:-}"
        echo "fragchild=$(bash -c 'echo "''${REPOSE_FRAG_PROBE:-}"; command -v frag-tool')"
        echo done
      } > "$out.tmp" 2>&1
      mv "$out.tmp" "$out"
      sleep 60
    '';
  };
  # What only the test flake's dev shell puts on PATH.
  flakeTool = pkgs.writeShellScriptBin "flake-tool" "echo flake-tool-ok";
  # A flake with no inputs whose dev shell is a bare derivation (no
  # stdenv), so it evaluates and builds offline from paths the VM has.
  # Its paths are plain strings, which is why the test turns the build
  # sandbox off, as guest-tools-carry does.
  devshellFlake = pkgs.writeText "flake.nix" ''
    {
      outputs = { self }: {
        devShells.x86_64-linux.default = derivation {
          name = "probe-shell";
          system = "x86_64-linux";
          builder = "${pkgs.bash}/bin/bash";
          args = [ "-c" "echo > $out" ];
          # get-env.sh writes to each of $outputs, set only when named.
          outputs = [ "out" ];
          PATH = "${flakeTool}/bin:${pkgs.coreutils}/bin";
          REPOSE_FLAKE_PROBE = "from-the-flake";
        };
      };
    }
  '';

  mkTest = name: attrs: pkgs.testers.runNixOSTest ({ inherit name; } // attrs);

  # I-265: one call into each library Rails' native gems link.
  nativeGemProbe = pkgs.writeText "native-gems.c" ''
    #include <stdio.h>
    #include <yaml.h>
    #include <libpq-fe.h>
    #include <libxml/parser.h>
    #include <libxslt/xslt.h>
    #include <mysql.h>
    int main(void) {
      int a, b, c;
      yaml_get_version(&a, &b, &c);
      xmlCheckVersion(LIBXML_VERSION);
      printf("%d %d %d %s\n", a, PQlibVersion() > 0, xsltLibxsltVersion > 0, mysql_get_client_info());
      return 0;
    }
  '';

  # I-264: a tmux server on /etc/tmux.conf, a pane in raw mode that asks
  # for extended keys and records its input, then Shift+Enter and Enter.
  # Asks the running herdr server for a live handoff, the request
  # `herdr update --handoff` sends after it installs (I-560).
  herdrHandoff = pkgs.writeShellScript "herdr-handoff" ''
    echo '{"id":"t","method":"server.live_handoff","params":{}}' \
      | ${pkgs.socat}/bin/socat -t 20 - UNIX-CONNECT:/home/dev/.config/herdr/herdr.sock
  '';
  tmuxKeysProbe = pkgs.writeShellScript "tmux-keys-probe" ''
    set -eu
    t() { ${pkgs.tmux}/bin/tmux -L keysprobe -f /etc/tmux.conf "$@"; }
    rm -f /tmp/keys
    t new-session -d -s k -x 80 -y 24 "stty raw -echo; printf '\033[>4;1m'; od -An -c > /tmp/keys"
    sleep 1
    t send-keys -t k S-Enter
    t send-keys -t k Enter
    sleep 1
    t kill-server
    for _ in $(seq 50); do [ -s /tmp/keys ] && break; sleep 0.1; done
  '';

  # The exact scripts the CLI sends for I-198 and I-195, kept in step with
  # the code by internal/cli's TestGuestPartsGolden.
  guestParts = ../../../internal/cli/testdata/guest-parts;

  # guest-tools-carry's stand-ins. fakeNixpkgs is a flake with three
  # packages, each one script copied into $out/bin; nothing to download.
  fakeBin = name: text: pkgs.writeScript name "#!${pkgs.bash}/bin/bash\n${text}\n";
  fakeNode = fakeBin "node" ''if [ "''${1:-}" = --version ]; then echo v22.1.0; else exec /run/current-system/sw/bin/node "$@"; fi'';
  # I-265: what ruby --version and java -version (on stderr) print.
  fakeRuby = fakeBin "ruby" ''echo "ruby 3.3.9 (2025-07-24 revision f5c772fc7c) +PRISM [x86_64-linux]"'';
  fakeJava = fakeBin "java" ''echo 'openjdk version "21.0.8" 2025-07-15' >&2'';
  fakeNixpkgs = pkgs.writeTextDir "flake.nix" ''
    {
      outputs = { self }:
        let
          mk = name: bin: script: derivation {
            inherit name;
            system = "x86_64-linux";
            builder = "${pkgs.bash}/bin/bash";
            args = [ "-c" "${pkgs.coreutils}/bin/mkdir -p $out/bin && ${pkgs.coreutils}/bin/cp ''${script} $out/bin/''${bin}" ];
          };
        in {
          legacyPackages.x86_64-linux = {
            hello = mk "hello-2.12" "hello" "${fakeBin "hello" "echo Hello from the stand-in nixpkgs"}";
            greeter = mk "greeter-1.0" "greet" "${fakeBin "greet" "echo greetings"}";
            nodejs_22 = mk "nodejs-22.1.0" "node" "${fakeNode}";
            ruby_3_3 = mk "ruby-3.3.9" "ruby" "${fakeRuby}";
            jdk21_headless = mk "openjdk-headless-21.0.8" "java" "${fakeJava}";
          };
        };
    }
  '';
  # nix-locate over fakeNixpkgs (the real index is another worker's part
  # of the base): greeter provides greet, so the name differs.
  fakeNixLocate = pkgs.writeShellScriptBin "nix-locate" ''
    # the flags the installer passes (nix-index 0.1.11 has no --top-level)
    [ "$*" = "--minimal --no-group --type x --type s --whole-name --at-root ''${!#}" ] || exit 2
    case "$*" in
      */bin/hello) echo hello.out ;;
      */bin/greet) echo gr.eet.out; echo zz-greet-extra.out; echo greeter.out ;;
      */bin/node) echo nodejs_22.out; echo nodejs_20.out ;;
    esac
  '';
  # The registry document for one packed tarball.
  npmMeta = pkgs.writers.writePython3 "npm-meta" { } ''
    import base64
    import hashlib
    import json
    import os
    import sys

    tgz = sys.argv[1]
    b = open(tgz, "rb").read()
    name = "fake-tool"
    url = "http://127.0.0.1:4874/" + os.path.basename(tgz)
    sri = "sha512-" + base64.b64encode(hashlib.sha512(b).digest()).decode()
    v = {
        "name": name,
        "version": "1.0.0",
        "bin": {"fake-tool": "cli.sh"},
        "dist": {
            "tarball": url,
            "shasum": hashlib.sha1(b).hexdigest(),
            "integrity": sri,
        },
    }
    doc = {
        "name": name,
        "dist-tags": {"latest": "1.0.0"},
        "versions": {"1.0.0": v},
    }
    print(json.dumps(doc))
  '';
in
{
  guest-base = mkTest "guest-base" {
    nodes.guest = node;
    testScript = ''
      guest.start()
      guest.wait_for_unit("multi-user.target")
      guest.wait_for_unit("guestd.service")
      guest.wait_for_file("/run/repose/hooks.sock")

      with subtest("dev user, sudo, locked root"):
          out = guest.succeed("id dev")
          assert "uid=1000(dev)" in out and "wheel" in out and "docker" in out, out
          guest.succeed("sudo -u dev sudo -n true")
          guest.succeed("sudo -u dev sudo -n -i true")
          status = guest.succeed("passwd -S root")
          assert " L " in status or status.split()[1] in ("L", "LK"), status

      with subtest("sysctls"):
          assert guest.succeed("sysctl -n fs.inotify.max_user_watches").strip() == "1048576"
          assert guest.succeed("sysctl -n fs.inotify.max_user_instances").strip() == "1024"
          # I-539: dev may attach a debugger to its own processes.
          assert guest.succeed("sysctl -n kernel.yama.ptrace_scope").strip() == "0"

      with subtest("environment in a login shell"):
          guest.succeed("printf 'TZ=Europe/Berlin\\nREPOSE_PROJECT=todo-app\\n' > /etc/repose/env")
          guest.succeed("install -m 0400 -o dev -g dev /dev/null /run/repose/secrets.env && echo \"export MY_TOKEN='s3cr3t'\" > /run/repose/secrets.env")
          env = guest.succeed("sudo -u dev bash -lc 'echo $TZ $LANG $REPOSE $REPOSE_PROJECT $MY_TOKEN $EDITOR'").strip()
          assert env == "Europe/Berlin C.UTF-8 1 todo-app s3cr3t nvim", env
          path = guest.succeed("sudo -u dev bash -lc 'echo $PATH'")
          assert "/home/dev/.npm-global/bin" in path and "/home/dev/.local/bin" in path, path
          assert guest.succeed("cat /etc/repose/base-version").strip() == "${baseVersion}"

      with subtest("tmux session from project.json"):
          guest.succeed("install -d -o dev -g dev -m 0700 /home/dev/.repose")
          guest.succeed("""echo '{"project_id":"0192e4b0-0000-7000-8000-000000000001","slug":"todo-app","name":"todo-app","tz":"Europe/Berlin","class":"large"}' > /home/dev/.repose/project.json && chown dev:dev /home/dev/.repose/project.json""")
          # Nothing starts a session on its own (no path unit, I-503):
          # SetupProject starts the unit project.json names, as here.
          guest.succeed("sleep 2; ! sudo -u dev tmux ls")
          guest.succeed("test ! -e /etc/systemd/user/repose-tmux-session.path")
          guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-tmux-session.service")
          guest.wait_until_succeeds("sudo -u dev tmux ls | grep -q '^todo-app:'", timeout=60)
          # No checkout yet (I-368): the session works in the home directory,
          # and nothing is made under the slug.
          win = guest.succeed("sudo -u dev tmux list-windows -t todo-app -F '#{window_name} #{pane_current_path}'").strip()
          assert win == "shell /home/dev", win
          guest.succeed("test ! -e /home/dev/todo-app")
          assert guest.succeed("sudo -u dev repose-checkout").strip() == "/home/dev"
          guest.succeed("grep -Eq 'set-clipboard +on' /etc/tmux.conf && grep -Eq 'mouse +off' /etc/tmux.conf && grep -Eq 'history-limit +50000' /etc/tmux.conf")

      with subtest("I-551: the session comes back after the tmux server exits"):
          # `exit` in the last window ends the server, as kill-server does.
          guest.succeed("sudo -u dev tmux kill-server")
          # Gone for the 5 s I-352's check after an attach needs.
          guest.succeed("! sudo -u dev tmux has-session -t =todo-app")
          guest.wait_until_succeeds("sudo -u dev tmux has-session -t =todo-app", timeout=30)
          # The attach the CLI runs finds it (a client needs a terminal:
          # script gives it one, and the detach ends it).
          guest.succeed("sudo -u dev env TERM=xterm-256color script -qec 'tmux attach -t todo-app \\; detach-client' /dev/null")
          # A stop is not undone.
          guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user stop repose-tmux-session.service")
          guest.succeed("sleep 7; ! sudo -u dev tmux has-session -t =todo-app")
          guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-tmux-session.service")
          guest.wait_until_succeeds("sudo -u dev tmux has-session -t =todo-app", timeout=60)

      with subtest("I-560: a session on a tmux server outside the unit is not restarted"):
          def user_q(cmd):
              return guest.succeed(f"sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 {cmd}").strip()
          guest.succeed("sudo -u dev tmux kill-server")
          # In the 5 s wait, a tmux started outside the unit (over ssh,
          # as here from the test's shell) takes the slug.
          guest.succeed("sudo -u dev tmux new-session -d -s todo-app")
          guest.wait_until_succeeds("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user is-failed repose-tmux-session.service", timeout=30)
          n = user_q("systemctl --user show -p NRestarts --value repose-tmux-session.service")
          guest.succeed("sleep 12")
          assert user_q("systemctl --user show -p NRestarts --value repose-tmux-session.service") == n, "the unit kept restarting"
          guest.succeed("sudo -u dev tmux has-session -t =todo-app")
          # A server that dies by a signal (SIGKILL is the OOM kill) comes
          # back as an exit does.
          guest.succeed("sudo -u dev tmux kill-server")
          guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-tmux-session.service")
          guest.wait_until_succeeds("sudo -u dev tmux has-session -t =todo-app", timeout=60)
          guest.succeed("kill -KILL $(sudo -u dev tmux display-message -p '#{pid}')")
          guest.wait_until_succeeds("sudo -u dev tmux has-session -t =todo-app", timeout=30)
          assert user_q("systemctl --user is-active repose-tmux-session.service") == "active"

      with subtest("I-538: the tmux server, its panes and a dev login get 524288 open files"):
          pid = guest.succeed("sudo -u dev tmux display -p -t todo-app '#{pid}'").strip()
          limits = guest.succeed(f"awk '/^Max open files/ {{print $4, $5}}' /proc/{pid}/limits").strip()
          assert limits == "524288 524288", limits
          pane = guest.succeed("sudo -u dev tmux display -p -t todo-app '#{pane_pid}'").strip()
          limits = guest.succeed(f"awk '/^Max open files/ {{print $4, $5}}' /proc/{pane}/limits").strip()
          assert limits == "524288 524288", limits
          # su runs dev's PAM session, as sshd does for SSH, exec and code.
          soft = guest.succeed("su - dev -c 'ulimit -Sn'").strip().splitlines()[-1]
          assert soft == "524288", soft

      with subtest("I-576: the keystroke path outlives a memory hog in a pane"):
          def adj(pid):
              return int(guest.succeed(f"cat /proc/{pid}/oom_score_adj").strip())
          manager = guest.succeed("systemctl show -p MainPID --value user@1000.service").strip()
          assert adj(manager) == -900, adj(manager)
          # The manager's units start at 0, not upstream's manager + 100.
          unit = guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemd-run --user --wait --pipe --quiet cat /proc/self/oom_score_adj").strip()
          assert unit == "0", unit
          assert guest.succeed("systemctl show -p OOMScoreAdjust --value sshd.service").strip() == "-800"
          # This node runs fake-guestd, so the tmux server and its panes
          # keep the 0 their unit gives them (upstream's was 200); guestd's
          # -800 on the server is the guestd test's I-200 subtest.
          server = guest.succeed("sudo -u dev tmux display -p -t todo-app '#{pid}'").strip()
          assert adj(server) == 0, adj(server)
          pane = guest.succeed("sudo -u dev tmux display -p -t todo-app '#{pane_pid}'").strip()
          assert adj(pane) == 0, adj(pane)
          # A child's protection is capped by its parent's; the slices'
          # values rely on memory_recursiveprot (systemd's mount option).
          guest.succeed("grep -E '^cgroup2 /sys/fs/cgroup .*memory_recursiveprot' /proc/mounts")
          # MemoryLow, in bytes, on each slice and unit of the path.
          def low(path):
              return int(guest.succeed(f"cat /sys/fs/cgroup/{path}/memory.low").strip())
          M = 1024 * 1024
          assert low("system.slice") == 176 * M
          assert low("system.slice/sshd.service") == 16 * M
          assert low("system.slice/guestd.service") == 64 * M
          assert low("system.slice/systemd-journald.service") == 64 * M
          assert low("user.slice") == 128 * M
          assert low("user.slice/user-1000.slice") == 128 * M
          assert low("user.slice/user-1000.slice/user@1000.service") == 64 * M
          assert low("user.slice/user-1000.slice/user@1000.service/app.slice") == 48 * M
          assert low("user.slice/user-1000.slice/user@1000.service/app.slice/repose-tmux-session.service") == 48 * M
          assert guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user show -p OOMPolicy --value repose-tmux-session.service").strip() == "continue"
          # nixpkgs' kernels have CONFIG_LRU_GEN and turn it on.
          assert guest.succeed("cat /sys/kernel/mm/lru_gen/min_ttl_ms").strip() == "1000"
          # The test framework panics the VM on an OOM (panic_on_oom=2);
          # a real guest does not.
          guest.succeed("sysctl -w vm.panic_on_oom=0")
          # A pane that takes every byte: the kernel kills it, and the
          # manager, the tmux server and the session are still there.
          guest.succeed("echo 'b = []' > /tmp/hog.py && echo 'while True: b.append(b\"x\" * (64 << 20))' >> /tmp/hog.py && chmod 644 /tmp/hog.py")
          guest.succeed("sudo -u dev tmux new-window -d -t todo-app -n hog 'python3 /tmp/hog.py'")
          guest.wait_until_succeeds("dmesg | grep -q 'Killed process [0-9]* (python3'", timeout=180)
          print(guest.succeed("dmesg | grep -E 'Killed process|invoked oom-killer'"))
          assert guest.succeed("systemctl show -p MainPID --value user@1000.service").strip() == manager
          assert guest.succeed("sudo -u dev tmux display -p -t todo-app '#{pid}'").strip() == server
          guest.succeed("sudo -u dev tmux has-session -t =todo-app")
          guest.succeed("! dmesg | grep -E 'Killed process [0-9]* \\((systemd|tmux: server|tmux: client|sshd|sshd-session|guestd|fake-guestd|systemd-journal)\\)'")
          guest.execute("sudo -u dev tmux kill-window -t todo-app:hog")
          guest.succeed("sysctl -w vm.panic_on_oom=2")

      with subtest("I-368: the session starts in the checkout the first sync recorded"):
          guest.succeed("sudo -u dev sh -c 'mkdir -p ~/factory && echo factory > ~/.repose/checkout'")
          assert guest.succeed("sudo -u dev repose-checkout").strip() == "/home/dev/factory"
          guest.succeed("sudo -u dev tmux kill-session -t todo-app")
          guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user restart repose-tmux-session.service")
          guest.wait_until_succeeds("sudo -u dev tmux ls | grep -q '^todo-app:'", timeout=60)
          win = guest.succeed("sudo -u dev tmux list-windows -t todo-app -F '#{window_name} #{pane_current_path}'").strip()
          assert win == "shell /home/dev/factory", win
          # A name that leads out of the home is ignored.
          guest.succeed("sudo -u dev sh -c 'echo ../etc > ~/.repose/checkout'")
          assert guest.succeed("sudo -u dev repose-checkout").strip() == "/home/dev"
          guest.succeed("sudo -u dev sh -c 'echo factory > ~/.repose/checkout'")

      with subtest("I-264: the running server has extended keys, passthrough and hyperlinks"):
          def opt(scope, name):
              return guest.succeed(f"sudo -H -u dev tmux show-options {scope} {name}").strip()
          assert opt("-sv", "extended-keys") == "on", opt("-s", "extended-keys")
          assert opt("-sv", "extended-keys-format") == "csi-u", opt("-s", "extended-keys-format")
          assert opt("-gv", "allow-passthrough") == "on", opt("-g", "allow-passthrough")
          features = guest.succeed("sudo -H -u dev tmux show-options -s terminal-features")
          print(features)
          assert "*:extkeys" in features and "*:hyperlinks" in features, features
          # Shift+Enter reaches a program that asked for extended keys
          # (mode 1, as Claude Code does) as CSI 13;2 u, and Enter stays CR.
          # A server of its own on /etc/tmux.conf, so the session is not
          # touched.
          guest.succeed("${tmuxKeysProbe}")
          keys = guest.succeed("cat /tmp/keys")
          print(keys)
          assert keys.split()[:8] == ["033", "[", "1", "3", ";", "2", "u", "\\r"], keys

      with subtest("I-501, I-503: one session unit, chosen by project.json"):
          import json
          def mux_is(m):
              return guest.execute(f"sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 repose-multiplexer-is {m}")[0]
          def unit_state(u):
              return guest.execute(f"sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user is-active {u}")[1].strip()
          def project(extra):
              guest.succeed(f"""echo '{{"project_id":"0192e4b0-0000-7000-8000-000000000001","slug":"todo-app","name":"todo-app","tz":"Europe/Berlin","class":"large"{extra}}}' > /home/dev/.repose/project.json && chown dev:dev /home/dev/.repose/project.json""")
          def user(cmd):
              return guest.succeed(f"sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 {cmd}")
          guest.succeed("test -x /run/current-system/sw/bin/herdr")
          print(guest.succeed("herdr status client --json"))
          # tmux runs: the herdr unit's condition refuses while it does.
          assert mux_is("tmux") == 0 and mux_is("herdr") != 0
          project(',"multiplexer":"herdr"')
          assert mux_is("herdr") != 0, "herdr allowed while the tmux unit runs"
          user("systemctl --user start repose-herdr-server.service")
          assert unit_state("repose-herdr-server.service") == "inactive"
          # The next start: tmux gone, herdr chosen.
          user("systemctl --user stop repose-tmux-session.service")
          assert mux_is("tmux") != 0 and mux_is("herdr") == 0
          user("systemctl --user start repose-tmux-session.service")
          assert unit_state("repose-tmux-session.service") == "inactive"
          user("systemctl --user start repose-herdr-server.service")
          guest.wait_until_succeeds("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user is-active repose-herdr-server.service", timeout=30)
          guest.succeed("test -S /home/dev/.config/herdr/herdr.sock")
          assert guest.execute("pgrep -u dev -c tmux")[1].strip() == "0"
          assert guest.succeed("cat /home/dev/.config/herdr/config.toml") == '[terminal]\nshell_mode = "login"\n\n[update]\nversion_check = false\n\n[ui.toast]\ndelivery = "herdr"\n'
          assert guest.succeed("stat -c %U /home/dev/.config/herdr/config.toml").strip() == "dev"
          # The workspace sits in the checkout (factory, from I-368 above),
          # once, however often the step runs.
          user("repose-herdr-workspace")
          ws = json.loads(user("herdr workspace list"))["result"]["workspaces"]
          assert [w["label"] for w in ws] == ["factory"], ws
          panes = json.loads(user("herdr pane list"))["result"]["panes"]
          assert any(p.get("cwd") == "/home/dev/factory" for p in panes), panes
          assert unit_state("repose-tmux-session.service") == "inactive"
          # A changed config.toml is the user's.
          guest.succeed("echo '# mine' >> /home/dev/.config/herdr/config.toml")
          user("systemctl --user restart repose-herdr-server.service")
          guest.wait_until_succeeds("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user is-active repose-herdr-server.service", timeout=30)
          guest.succeed("grep -qx '# mine' /home/dev/.config/herdr/config.toml")
          # The server starts without the profile guards, so each pane's
          # shell loads the current environment (I-563).
          main = user("systemctl --user show -p MainPID --value repose-herdr-server.service").strip()
          environ = guest.succeed(f"tr '\\0' '\\n' < /proc/{main}/environ")
          assert "__NIXOS_SET_ENVIRONMENT_DONE=" not in environ and "__ETC_PROFILE_DONE=" not in environ, environ
          # I-560: a live handoff keeps the unit and every pane; a server
          # that stops comes back with its workspaces.
          pane = json.loads(user("herdr pane list"))["result"]["panes"][0]["pane_id"]
          user(f"herdr pane run {pane} 'sleep 4242'")
          guest.wait_until_succeeds("pgrep -u dev -fx 'sleep 4242'", timeout=10)
          old = user("systemctl --user show -p MainPID --value repose-herdr-server.service").strip()
          print(user("${herdrHandoff}"))
          guest.succeed("sleep 8")
          assert unit_state("repose-herdr-server.service") == "active"
          guest.succeed("pgrep -u dev -fx 'sleep 4242'")
          new = guest.succeed("pgrep -u dev -x herdr").split()
          assert old not in new and len(new) == 1, (old, new)
          user("herdr server stop")
          guest.wait_until_succeeds("! pgrep -u dev -fx 'sleep 4242'", timeout=20)
          guest.wait_until_succeeds("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 herdr workspace list | grep -q factory", timeout=40)
          assert unit_state("repose-herdr-server.service") == "active"
          # Back to tmux for the subtests below.
          user("systemctl --user stop repose-herdr-server.service")
          project("")
          assert mux_is("tmux") == 0
          guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-tmux-session.service")
          guest.wait_until_succeeds("sudo -u dev tmux ls | grep -q '^todo-app:'", timeout=60)
          # Every wrapped agent tells herdr who it is (I-501).
          for cmd in ["claude", "opencode", "codex", "gemini", "pi"]:
              guest.succeed(f"grep -q 'HERDR_AGENT=' /run/current-system/sw/bin/{cmd}")

      with subtest("agent binaries and wrappers"):
          for cmd in ["claude", "opencode", "codex", "gemini", "pi"]:
              guest.succeed(f"test -x /run/current-system/sw/bin/{cmd}")
              guest.succeed(f"grep -q repose-agent-setup /run/current-system/sw/bin/{cmd}")
          for tool in ["node", "pnpm", "python3", "uv", "go", "rustup", "just", "rg", "jq", "gh", "git", "direnv", "starship", "zoxide", "eza", "nvim", "docker", "docker-compose", "chromium", "playwright-mcp", "chrome-devtools-mcp", "repose-hook", "repose-guest-profile", "repose-pin-profile"]:
              guest.succeed(f"test -x /run/current-system/sw/bin/{tool}")

      with subtest("claude hooks merged without clobbering a user hook"):
          guest.succeed("install -d -o dev -g dev /home/dev/.claude")
          guest.succeed("""cat > /home/dev/.claude/settings.json <<'EOF'
      ${userHook}
      EOF
      chown dev:dev /home/dev/.claude/settings.json""")
          guest.succeed("sudo -u dev repose-agent-setup claude")
          settings = guest.succeed("cat /home/dev/.claude/settings.json")
          import json
          s = json.loads(settings)
          stop_cmds = [h["command"] for e in s["hooks"]["Stop"] for h in e["hooks"]]
          notif_cmds = [h["command"] for e in s["hooks"]["Notification"] for h in e["hooks"]]
          assert "echo user-hook" in stop_cmds and "repose-hook" in stop_cmds, stop_cmds
          assert "repose-hook" in notif_cmds, notif_cmds
          assert s["permissions"]["allow"] == ["Bash(ls:*)"], s
          # idempotent
          guest.succeed("sudo -u dev repose-agent-setup claude")
          s2 = json.loads(guest.succeed("cat /home/dev/.claude/settings.json"))
          assert s2 == s, (s, s2)
          # I-250: bypassPermissions (and no warning dialog) where the user
          # set no defaultMode; a user's own defaultMode is kept.
          assert s["permissions"]["defaultMode"] == "bypassPermissions" and s["skipDangerousModePermissionPrompt"] is True, s
          guest.succeed("sudo -u dev sh -c 'mkdir -p /tmp/fresh && HOME=/tmp/fresh repose-agent-setup claude'")
          fresh = json.loads(guest.succeed("cat /tmp/fresh/.claude/settings.json"))
          assert fresh["permissions"]["defaultMode"] == "bypassPermissions" and fresh["skipDangerousModePermissionPrompt"] is True, fresh
          guest.succeed("sudo -u dev sh -c 'mkdir -p /tmp/plan/.claude && echo {\\\"permissions\\\":{\\\"defaultMode\\\":\\\"plan\\\"}} > /tmp/plan/.claude/settings.json && HOME=/tmp/plan repose-agent-setup claude && HOME=/tmp/plan repose-agent-setup claude'")
          plan = json.loads(guest.succeed("cat /tmp/plan/.claude/settings.json"))
          assert plan["permissions"] == {"defaultMode": "plan"} and "skipDangerousModePermissionPrompt" not in plan and "repose-hook" in json.dumps(plan["hooks"]), plan
          # I-306: a bypass mode the user set (carried from the laptop)
          # gets the skip flag too, so the warning cannot take a sent
          # prompt; the user's own false is kept.
          guest.succeed("sudo -u dev sh -c 'mkdir -p /tmp/byp/.claude && echo {\\\"permissions\\\":{\\\"defaultMode\\\":\\\"bypassPermissions\\\"}} > /tmp/byp/.claude/settings.json && HOME=/tmp/byp repose-agent-setup claude && HOME=/tmp/byp repose-agent-setup claude'")
          byp = json.loads(guest.succeed("cat /tmp/byp/.claude/settings.json"))
          assert byp["permissions"] == {"defaultMode": "bypassPermissions"} and byp["skipDangerousModePermissionPrompt"] is True, byp
          guest.succeed("sudo -u dev sh -c 'mkdir -p /tmp/bypf/.claude && echo {\\\"permissions\\\":{\\\"defaultMode\\\":\\\"bypassPermissions\\\"}\\,\\\"skipDangerousModePermissionPrompt\\\":false} > /tmp/bypf/.claude/settings.json && HOME=/tmp/bypf repose-agent-setup claude'")
          bypf = json.loads(guest.succeed("cat /tmp/bypf/.claude/settings.json"))
          assert bypf["skipDangerousModePermissionPrompt"] is False, bypf
          # I-425: the fullscreen renderer where the user set no tui; a
          # user's own tui is kept.
          assert s["tui"] == "fullscreen" and fresh["tui"] == "fullscreen" and plan["tui"] == "fullscreen", (s, fresh, plan)
          guest.succeed("sudo -u dev sh -c 'mkdir -p /tmp/tui/.claude && echo {\\\"tui\\\":\\\"default\\\"} > /tmp/tui/.claude/settings.json && HOME=/tmp/tui repose-agent-setup claude'")
          assert json.loads(guest.succeed("cat /tmp/tui/.claude/settings.json"))["tui"] == "default"
          mcp = json.loads(guest.succeed("cat /home/dev/.claude.json"))
          assert set(mcp["mcpServers"]) >= {"playwright", "chrome-devtools"}, mcp
          # I-283: no auto-mode offer to catch a sent prompt under bypass;
          # a machine in another mode keeps Claude Code's own behaviour.
          assert mcp["hasSeenAutoDefaultNudge"] is True, mcp
          assert "hasSeenAutoDefaultNudge" not in guest.succeed("cat /tmp/plan/.claude.json 2>/dev/null || true")
          guest.succeed("sudo -u dev sh -c 'jq \".hasSeenAutoDefaultNudge=false\" ~/.claude.json > /tmp/cj && cat /tmp/cj > ~/.claude.json' && sudo -u dev repose-agent-setup claude")
          assert json.loads(guest.succeed("cat /home/dev/.claude.json"))["hasSeenAutoDefaultNudge"] is False
          guest.succeed("sudo -u dev repose-agent-setup codex && grep -q 'notify = \\[\"repose-hook\"\\]' /home/dev/.codex/config.toml")
          guest.succeed("sudo -u dev repose-agent-setup opencode && grep -q 'id: \"repose\"' /home/dev/.config/opencode/plugins/repose.js")
          # I-481: a repose.js the user changed is kept.
          guest.succeed("sudo -u dev sh -c 'echo // mine >> ~/.config/opencode/plugins/repose.js' && sudo -u dev repose-agent-setup opencode && grep -q '// mine' /home/dev/.config/opencode/plugins/repose.js")

      with subtest("repose-hook posts to the socket"):
          guest.succeed("""cat > /tmp/transcript.jsonl <<'EOF'
      ${transcript}
      EOF
      chmod 644 /tmp/transcript.jsonl""")
          guest.succeed("""echo '${claudeStopPayload}' | sudo -u dev REPOSE_HOOK_AGENT=claude repose-hook""")
          hooklog = guest.wait_until_succeeds("cat /run/repose/hooks.log")
          ev = json.loads(hooklog.strip().splitlines()[-1])
          assert ev["agent"] == "claude" and ev["kind"] == "completed", ev
          assert "committed" in ev["summary"], ev
          guest.succeed("""echo '{"agent":"claude","kind":"completed","summary":"t"}' | curl -sf --unix-socket /run/repose/hooks.sock -d @- http://x/ -o /dev/null -w '%{http_code}' | grep -q 200""")
          # a broken payload never fails the caller
          guest.succeed("echo 'not json' | sudo -u dev repose-hook")

      with subtest("sshd accepts the right principal and refuses the wrong one"):
          guest.succeed("ssh-keygen -q -t ed25519 -N ''' -f /root/ca && ssh-keygen -q -t ed25519 -N ''' -f /root/user")
          guest.succeed("install -m 0644 /root/ca.pub /run/repose/user_ca.pub")
          guest.succeed("echo 0192e4b0-0000-7000-8000-000000000001 > /etc/ssh/principals/dev && systemctl reload sshd")
          guest.succeed("ssh-keygen -q -s /root/ca -I 'user:heracraft' -n 0192e4b0-0000-7000-8000-000000000001 -V -1m:+12h /root/user.pub")
          out = guest.succeed("ssh -n -F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o CertificateFile=/root/user-cert.pub -i /root/user dev@127.0.0.1 id")
          assert "uid=1000(dev)" in out, out
          guest.succeed("ssh-keygen -q -s /root/ca -I 'user:other' -n 0192e4b0-ffff-7000-8000-00000000beef -V -1m:+12h /root/user.pub")
          err = guest.fail("ssh -n -F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o CertificateFile=/root/user-cert.pub -i /root/user dev@127.0.0.1 id 2>&1")
          assert "Permission denied" in err, err
          guest.fail("ssh -n -F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -i /root/user root@127.0.0.1 id")

      with subtest("I-512: terminfo for the TERMs laptops attach with"):
          for term in ["xterm-ghostty", "xterm-kitty", "alacritty", "wezterm", "foot"]:
              guest.succeed(f"sudo -u dev bash -lc 'infocmp {term}' >/dev/null")
          guest.succeed("sudo -u dev env TERM=xterm-kitty bash -lc clear")

      with subtest("I-515: 24-bit colour only to terminals that have it; titles name the machine"):
          overrides = guest.succeed("sudo -H -u dev tmux show-options -s terminal-overrides")
          assert ":Tc" not in overrides, overrides
          features = guest.succeed("sudo -H -u dev tmux show-options -s terminal-features")
          for f in ["xterm-ghostty:RGB", "xterm-kitty:RGB", "alacritty:RGB", "wezterm:RGB", "foot*:RGB", "*-direct:RGB"]:
              assert f in features, features
          assert guest.succeed("sudo -H -u dev tmux show-options -gv set-titles").strip() == "on"
          assert guest.succeed("sudo -H -u dev tmux show-options -gv set-titles-string").strip() == "#h: #S"

      with subtest("I-513, I-514: login shells read ~/.bashrc last; ls, vi, history and fzf"):
          guest.succeed("cat > /home/dev/.bashrc <<'EOF'\nexport REPOSE_RC_MARK=read\nalias rcprobe='echo alias-ok'\nalias ll='echo user-ll'\nEOF\nchown dev:dev /home/dev/.bashrc")
          # A new tmux window: a login bash, as every pane is.
          guest.succeed("sudo -H -u dev tmux new-window -d -t todo-app -n rcprobe")
          guest.succeed("sudo -H -u dev tmux send-keys -t todo-app:rcprobe 'echo mark=$REPOSE_RC_MARK; rcprobe; ll' Enter")
          guest.wait_until_succeeds("sudo -H -u dev tmux capture-pane -p -t todo-app:rcprobe | grep -q '^alias-ok' && sudo -H -u dev tmux capture-pane -p -t todo-app:rcprobe | grep -q '^user-ll'", timeout=30)
          pane = guest.succeed("sudo -H -u dev tmux capture-pane -p -t todo-app:rcprobe")
          assert "mark=read" in pane, pane
          guest.succeed("sudo -H -u dev tmux kill-window -t todo-app:rcprobe")
          # An SSH shell.
          guest.succeed("ssh-keygen -q -s /root/ca -I 'user:heracraft' -n 0192e4b0-0000-7000-8000-000000000001 -V -1m:+12h /root/user.pub")
          ssh = "ssh -n -F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o CertificateFile=/root/user-cert.pub -i /root/user dev@127.0.0.1"
          out = guest.succeed(ssh + " \"bash -lic 'echo mark=\\$REPOSE_RC_MARK; rcprobe; ll; alias ls; echo hist=\\$HISTSIZE; shopt -q histappend && echo histappend; type -t __fzf_history__; command -v vi vim'\" 2>/dev/null")
          print(out)
          for want in ["mark=read", "alias-ok", "user-ll", "alias ls='ls --color=tty'", "hist=100000", "histappend", "function"]:
              assert want in out, out
          assert "/bin/vi" in out and "/bin/vim" in out, out
          # A user's ~/.bash_profile decides for itself.
          guest.succeed("install -o dev -g dev /dev/null /home/dev/.bash_profile")
          out = guest.succeed(ssh + " \"bash -lic 'echo mark=\\$REPOSE_RC_MARK'\" 2>/dev/null")
          assert "mark=read" not in out, out
          guest.succeed("rm /home/dev/.bash_profile /home/dev/.bashrc")

      with subtest("store overlay and profile pinning"):
          mounts = guest.succeed("mount | grep -E 'ro-store|rw-store|/nix/store'")
          assert "overlay" in mounts, mounts
          # hello is in the host store (lower). Install it into dev's profile
          # and assert the pin copies its closure into the upper dir.
          hello = "${pkgs.hello}"
          guest.succeed(f"sudo -u dev nix profile install --offline {hello}")
          guest.succeed("systemctl start repose-pin-profile.service")
          upper = guest.succeed("awk '$2 == \"/nix/store\" { print $4 }' /proc/mounts | tr , '\\n' | grep '^upperdir=' | cut -d= -f2").strip()
          # the overlay appears twice (initrd and stage 2 views); one line is enough.
          # Mounted by stage 1, it names /sysroot (systemd initrd) or /mnt-root (scripted, I-231).
          upper = upper.splitlines()[0].removeprefix("/sysroot").removeprefix("/mnt-root")
          import os
          name = os.path.basename(hello)
          guest.succeed(f"test -d {upper}/{name}/bin && cmp {upper}/{name}/bin/hello {hello}/bin/hello")
          guest.succeed(f"diff -r {upper}/{name} {hello}")
          guest.succeed("sudo -u dev bash -lc 'hello' | grep -q Hello")

      with subtest("I-533: what a guest-built binary links is copied up and rooted"):
          guest.succeed("install -d -o dev -g dev /tmp/link")
          guest.succeed("""cat > /tmp/link/t.c <<'EOF'
      #include <stdio.h>
      #include <openssl/crypto.h>
      int main(void) { printf("%s\\n", OpenSSL_version(OPENSSL_VERSION)); return 0; }
      EOF
      chown dev:dev /tmp/link/t.c""")
          guest.succeed("sudo -H -u dev bash -lc 'cd /tmp/link && cc t.c -o t $(pkg-config --cflags --libs openssl)'")
          interp = guest.succeed("readelf -l /tmp/link/t | sed -n 's/.*interpreter: \\(.*\\)]/\\1/p'").strip()
          runpath = guest.succeed("readelf -d /tmp/link/t | sed -n 's/.*runpath: \\[\\(.*\\)\\]/\\1/p'").strip()
          # The store paths the binary names: the interpreter's glibc and
          # every RUNPATH entry (openssl, glibc, gcc-lib).
          linked = sorted({"/".join(p.split("/")[:4]) for p in [interp] + runpath.split(":") if p.startswith("/nix/store/")})
          assert any("-glibc-" in p for p in linked) and any("-openssl-" in p for p in linked), linked
          guest.succeed("systemctl start repose-pin-profile.service")
          for p in linked:
              base = os.path.basename(p)
              guest.succeed(f"diff -r {upper}/{base} {p}")
              guest.succeed(f"test \"$(readlink /nix/var/nix/gcroots/repose-link-targets/{base})\" = {p}")
          # The lower layer is the host's read-only store and cannot lose a
          # path here, so run the binary from the upper copies alone: what
          # it would load once the host collected the base's paths.
          glibc = [p for p in linked if "-glibc-" in p][0]
          libpath = ":".join(f"{upper}/{os.path.basename(p)}/lib" for p in linked)
          out = guest.succeed(f"{upper}/{os.path.basename(glibc)}/lib/ld-linux-x86-64.so.2 --library-path {libpath} /tmp/link/t")
          assert out.startswith("OpenSSL "), out

      with subtest("I-529: repose-store-gc deletes dead paths only the upper dir holds"):
          lower_probe = "${gcLowerProbe}"
          lname = os.path.basename(lower_probe)
          guest.succeed(f"test -e /nix/.ro-store/{lname}")
          guest.fail(f"test -e {upper}/{lname}")
          # A path the guest's daemon adds lands in the upper dir alone.
          own = guest.succeed("echo only in the overlay > /tmp/own && nix-store --add /tmp/own").strip()
          oname = os.path.basename(own)
          guest.succeed(f"test -f {upper}/{oname}")
          guest.succeed("systemctl list-timers repose-store-gc.timer | grep -q repose-store-gc")
          guest.succeed("systemctl start repose-store-gc.service")
          guest.fail(f"test -e {own}")
          guest.fail(f"test -e {upper}/{oname}")
          # The dead lower path stays, with no whiteout hiding it.
          guest.succeed(f"test -f {lower_probe}")
          guest.fail(f"test -c {upper}/{lname}")
          # Live paths stay: the profile's hello and the link targets.
          guest.succeed(f"test -d {upper}/{name}")
          guest.succeed(f"test -x {glibc}/lib/ld-linux-x86-64.so.2")
          guest.succeed("/tmp/link/t")

      with subtest("guest profile script"):
          prof = json.loads(guest.succeed("sudo -u dev repose-guest-profile"))
          assert prof["slug"] == "todo-app" and prof["dir"] == "/home/dev/factory", prof
          assert prof["base_version"] == "${baseVersion}", prof
          assert prof["desktop"]["running"] is False, prof
    '';
  };

  # Workstream 15 on a real guest base: the timezone part through sudo
  # (I-198), the carried git config and its include precedence (I-195),
  # and the caches' guest side (I-202, I-208): Docker's mirror and the
  # one-time ~/.npmrc line, added only when the cache answers.
  guest-parity = mkTest "guest-parity" {
    nodes.guest = { ... }: {
      imports = [ node ];
      environment.systemPackages = [ pkgs.python3 ];
      # What hostd puts on the kernel line for project todo-app (I-550).
      boot.kernelParams = [ "systemd.hostname=todo-app" ];
    };
    testScript = ''
      guest.start()
      guest.wait_for_unit("multi-user.target")
      guest.succeed("printf 'TZ=Europe/Berlin\nREPOSE_PROJECT=todo-app\n' > /etc/repose/env")
      guest.succeed("install -d -o dev -g dev -m 0700 /home/dev/.repose")
      guest.succeed("""echo '{"project_id":"0192e4b0-0000-7000-8000-000000000001","slug":"todo-app","name":"todo-app","tz":"Europe/Berlin","class":"large"}' > /home/dev/.repose/project.json && chown dev:dev /home/dev/.repose/project.json""")
      guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-tmux-session.service")
      guest.wait_until_succeeds("sudo -H -u dev tmux ls | grep -q '^todo-app:'", timeout=60)
      guest.succeed("mkdir -p /tmp/p && cp -r ${guestParts}/. /tmp/p && chmod -R u+w /tmp/p && chown -R dev:dev /tmp/p")

      with subtest("I-550: systemd.hostname= on the kernel line names the guest over /etc/hostname"):
          assert guest.succeed("hostname").strip() == "todo-app"
          assert guest.succeed("cat /etc/hostname").strip() != "todo-app"

      with subtest("I-215: nothing of the system listens where auto-forward would pick it up"):
          listeners = guest.succeed("ss -Hltn")
          print(listeners)
          assert ":5355 " not in listeners, "resolved's LLMNR responder is listening"
          udp = guest.succeed("ss -Hlun")
          print(udp)
          assert ":5353 " not in udp, "resolved's mDNS responder is listening"
          assert ":5355 " not in udp, "resolved's LLMNR responder is listening"

      with subtest("the session starts in the project's zone; tmux does not take TZ from the client"):
          assert guest.succeed("sudo -H -u dev tmux show-environment -g TZ").strip() == "TZ=Europe/Berlin"
          assert "TZ" not in guest.succeed("sudo -H -u dev tmux show-options -gv update-environment")

      with subtest("I-198: the carry moves /etc/repose/env and tmux to the laptop's zone"):
          out = guest.succeed("sudo -H -u dev sh -e /tmp/p/tz.sh /tmp/p")
          assert "#tz Asia/Tokyo" in out, out
          env = guest.succeed("cat /etc/repose/env")
          assert "TZ=Asia/Tokyo" in env and "REPOSE_PROJECT=todo-app" in env and "Europe" not in env, env
          assert guest.succeed("stat -c '%U %a' /etc/repose/env").strip() == "root 644"
          assert guest.succeed("sudo -H -u dev tmux show-environment -g TZ").strip() == "TZ=Asia/Tokyo"
          guest.succeed("sudo -H -u dev tmux new-window -d -t todo-app -n clock 'date +%Z > /tmp/zone; sleep 30'")
          guest.wait_until_succeeds("grep -qx JST /tmp/zone")
          # the status clock follows too: its #() job runs with the global
          # environment (I-215); the server's own strftime stays in its
          # start zone
          assert "#(date" in guest.succeed("sudo -H -u dev tmux show-options -gv status-right")
          # a #() job runs only for an attached client: attach one on a pty,
          # and have a job on this session's status line record its zone
          guest.succeed("sudo -H -u dev tmux set-option -t todo-app status-left '#(date +%%Z > /tmp/jobzone)'")
          guest.succeed("sudo -H -u dev env TERM=xterm script -qfc 'tmux attach -t todo-app' /dev/null >/dev/null 2>&1 &")
          guest.wait_until_succeeds("grep -qx JST /tmp/jobzone", timeout=30)
          guest.succeed("sudo -H -u dev tmux set-option -u -t todo-app status-left")
          guest.succeed("sudo -H -u dev tmux detach-client -s todo-app || true")
          assert guest.succeed("sudo -H -u dev bash -lc 'date +%Z'").strip() == "JST"
          # the same zone again changes nothing
          assert "#tz" not in guest.succeed("sudo -H -u dev sh -e /tmp/p/tz.sh /tmp/p")

      with subtest("I-195: the carried config applies and a key set in the guest wins"):
          guest.succeed("sudo -H -u dev git config --global alias.st 'status --short'")
          out = guest.succeed("sudo -H -u dev sh -e /tmp/p/git.sh /tmp/p")
          assert "#dropped git core.pager" in out, out
          listing = guest.succeed("sudo -H -u dev git config --list --show-origin")
          print(listing)
          assert guest.succeed("sudo -H -u dev git config alias.st").strip() == "status --short"
          assert guest.succeed("sudo -H -u dev git config alias.lg").strip() == "log --oneline"
          assert guest.succeed("sudo -H -u dev git config user.email").strip() == "work@corp.example"
          assert "core.pager" not in listing, listing
          head = guest.succeed("head -2 /home/dev/.gitconfig")
          assert head == "[include]\n\tpath = ~/.config/git/repose-carried\n", head
          guest.succeed("sudo -H -u dev sh -e /tmp/p/git.sh /tmp/p")
          assert guest.succeed("grep -c repose-carried /home/dev/.gitconfig").strip() == "1"

      with subtest("I-202: Docker uses the host's mirror"):
          guest.wait_for_unit("docker.service")
          info = guest.succeed("docker info")
          assert "http://10.63.255.254:5000/" in info, info

      with subtest("I-208: ~/.npmrc is left alone while no cache answers"):
          guest.wait_until_succeeds("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user show -p ActiveState --value repose-npm-registry.service | grep -qx inactive", timeout=120)
          guest.fail("test -e /home/dev/.npmrc")
          guest.fail("test -e /home/dev/.repose/npm-registry")

      with subtest("I-208: once the cache answers, one registry line, once"):
          guest.succeed("ip addr add 10.63.255.254/32 dev lo")
          guest.succeed("systemd-run --unit fake-front python3 -m http.server 4873 --bind 10.63.255.254")
          guest.wait_for_open_port(4873, "10.63.255.254")
          guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-npm-registry.service")
          assert guest.succeed("grep -c '^registry=http://10.63.255.254:4873/$' /home/dev/.npmrc").strip() == "1"
          assert guest.succeed("sudo -H -u dev bash -lc 'npm config get registry'").strip() == "http://10.63.255.254:4873/"
          guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-npm-registry.service")
          assert guest.succeed("grep -c '^registry=' /home/dev/.npmrc").strip() == "1"
          # a project's own registry still wins
          guest.succeed("sudo -H -u dev sh -c 'mkdir -p /home/dev/proj && echo registry=https://corp.example/npm/ > /home/dev/proj/.npmrc'")
          assert guest.succeed("sudo -H -u dev bash -lc 'cd /home/dev/proj && npm config get registry'").strip() == "https://corp.example/npm/"

      with subtest("I-208: a ~/.npmrc with its own registry is never touched"):
          guest.succeed("rm -f /home/dev/.repose/npm-registry")
          guest.succeed("sudo -H -u dev sh -c 'echo registry=https://user.example/ > /home/dev/.npmrc'")
          guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-npm-registry.service")
          assert guest.succeed("cat /home/dev/.npmrc").strip() == "registry=https://user.example/"
          assert guest.succeed("cat /home/dev/.repose/npm-registry").strip() == "own"
    '';
  };

  # I-218 and I-219: the C toolchain (cc, cgo), the everyday CLIs, nix-ld
  # running a foreign ELF, the pinned nixpkgs registry, and
  # command-not-found with its install hints.
  guest-devtools = mkTest "guest-devtools" {
    nodes.guest = { ... }: {
      imports = [ node ];
      environment.systemPackages = [ envProbeAgent ];
    };
    testScript = ''
      guest.start()
      guest.wait_for_unit("multi-user.target")

      import json
      import shlex

      def dev(cmd):
          return guest.succeed(f"sudo -H -u dev bash -lc {shlex.quote(cmd)}")

      with subtest("I-218: C toolchain, cc links, cgo builds"):
          for tool in ["cc", "gcc", "g++", "c++", "ld", "ar", "make", "cmake", "pkg-config"]:
              dev(f"command -v {tool}")
          guest.succeed("install -d -o dev -g dev /tmp/c /tmp/cgo")
          guest.succeed("""cat > /tmp/c/hello.c <<'EOF'
      #include <stdio.h>
      int main(void) { printf("hello from cc\\n"); return 0; }
      EOF
      cat > /tmp/c/hello.cc <<'EOF'
      #include <iostream>
      int main() { std::cout << "hello from c++" << std::endl; }
      EOF
      cat > /tmp/cgo/main.go <<'EOF'
      package main

      // int add(int a, int b) { return a + b; }
      import "C"
      import "fmt"

      func main() { fmt.Println("cgo says", C.add(40, 2)) }
      EOF
      chown -R dev:dev /tmp/c /tmp/cgo""")
          assert dev("cd /tmp/c && cc -o hello hello.c && ./hello").strip() == "hello from cc"
          assert dev("cd /tmp/c && g++ -o hellocc hello.cc && ./hellocc").strip() == "hello from c++"
          out = dev("cd /tmp/cgo && CGO_ENABLED=1 GOTOOLCHAIN=local GOFLAGS=-mod=mod GOCACHE=/tmp/cgo/cache go build -o cgo main.go && ./cgo")
          assert out.strip() == "cgo says 42", out
          # rustup cannot fetch a toolchain in the sandbox; what it needs
          # from the base is a linker driver named cc.
          dev("command -v cc && command -v rustup")

      with subtest("I-520: pnpm's global bin dir is on PATH, yarn is corepack's"):
          assert dev("pnpm bin -g").strip() == "/home/dev/.local/share/pnpm/bin"
          # yarn --version downloads yarn from registry.yarnpkg.com, which
          # the test VM cannot reach; the command and its target are what
          # the base provides.
          target = dev("readlink -f $(command -v yarn)").strip()
          assert target.endswith("/lib/node_modules/corepack/dist/yarn.js"), target
          assert target.rsplit("/dist/", 1)[0] == dev("readlink -f $(command -v corepack)").strip().rsplit("/dist/", 1)[0], target
          dev("command -v yarnpkg")
          assert dev("echo -n $COREPACK_ENABLE_DOWNLOAD_PROMPT") == "0"

      with subtest("I-218: everyday CLIs"):
          for tool in ["file", "lsof", "zip", "unzip", "dig", "nslookup", "nc", "sqlite3", "psql", "pg_dump", "openssl", "gpg", "patch", "less", "strace", "rsync", "killall", "readelf"]:
              dev(f"command -v {tool}")
          # psql only: no server binaries on PATH.
          guest.fail("sudo -H -u dev bash -lc 'command -v postgres'")
          guest.fail("sudo -H -u dev bash -lc 'command -v initdb'")

      with subtest("I-525: git-lfs is installed and its filter is in the system config"):
          assert dev("git lfs version").startswith("git-lfs/"), "git lfs version"
          assert dev("git config --system filter.lfs.process").strip() == "git-lfs filter-process"
          assert dev("git config --system filter.lfs.required").strip() == "true"
          # A carried filter.lfs.required=true no longer fails the add.
          dev("rm -rf /tmp/lfs && git init -q /tmp/lfs && cd /tmp/lfs && echo '*.bin filter=lfs diff=lfs merge=lfs -text' > .gitattributes && head -c 64 /dev/urandom > a.bin && git add . && git -c user.name=t -c user.email=t@e commit -qm lfs")
          assert "oid sha256:" in dev("cd /tmp/lfs && git cat-file -p HEAD:a.bin")

      with subtest("I-526: gh is the system credential helper by name, and store-path helpers go"):
          for h in ["https://github.com", "https://gist.github.com"]:
              assert dev(f"git config --system credential.{h}.helper").strip() == "!gh auth git-credential", h
          act = guest.succeed("grep -o '/nix/store/[^ ]*nixos-activation-start' /etc/systemd/user/nixos-activation.service").strip()
          cleanup = guest.succeed(f"grep -o '/nix/store/[^ ]*/bin/repose-gh-helper-cleanup' {act}").strip()
          dev("cp ~/.gitconfig /tmp/gitconfig.keep 2>/dev/null || :; for h in https://github.com https://gist.github.com; do git config --global --add credential.$h.helper \"\"; git config --global --add credential.$h.helper '!/nix/store/0000-gh-2.100.0/bin/.gh-wrapped auth git-credential'; done; git config --global --add credential.https://example.com.helper store")
          dev(cleanup)
          dev(cleanup)
          left = dev("git config --global --get-regexp '^credential' || true")
          assert left.strip() == "credential.https://example.com.helper store", left
          dev("if [ -f /tmp/gitconfig.keep ]; then cp /tmp/gitconfig.keep ~/.gitconfig; else git config --global --unset credential.https://example.com.helper; fi")

      with subtest("I-527: git defaults for a fresh HOME"):
          assert dev("git config --system init.defaultBranch").strip() == "main"
          assert dev("git config --system push.autoSetupRemote").strip() == "true"
          assert dev("git config --system pull.rebase").strip() == "false"
          assert dev("rm -rf /tmp/br && git init -q /tmp/br && git -C /tmp/br symbolic-ref --short HEAD").strip() == "main"

      with subtest("I-528: gpg-agent has a pinentry that exists"):
          conf = guest.succeed("cat /etc/gnupg/gpg-agent.conf")
          prog = [l.split(None, 1)[1] for l in conf.splitlines() if l.startswith("pinentry-program ")]
          assert len(prog) == 1, conf
          guest.succeed(f"test -x {prog[0]}")
          dev("rm -rf /tmp/gnupg-t && install -d -m 700 /tmp/gnupg-t && echo pw | GNUPGHOME=/tmp/gnupg-t gpg --batch --pinentry-mode loopback --passphrase-fd 0 --symmetric -o /tmp/gnupg-t/x.gpg /etc/hostname && GNUPGHOME=/tmp/gnupg-t gpgconf --kill gpg-agent")
          assert "GPG_TTY=/dev/" in guest.succeed("script -qc 'sudo -H -u dev bash -ic \"env | grep ^GPG_TTY=\"' /dev/null")

      with subtest("I-218: nix-ld runs a prebuilt foreign ELF"):
          interp = guest.succeed("readelf -l ${foreignElf}/bin/foreign | grep 'program interpreter'")
          assert "/lib64/ld-linux-x86-64.so.2" in interp, interp
          guest.succeed("test -e /lib64/ld-linux-x86-64.so.2")
          out = dev("${foreignElf}/bin/foreign")
          assert out.startswith("foreign ok zlib "), out

      with subtest("I-218: nixpkgs is the base's own, offline"):
          reg = dev("nix registry list")
          print(reg)
          line = [l for l in reg.splitlines() if l.startswith("system flake:nixpkgs ")]
          assert line == ["system flake:nixpkgs path:${nixpkgsSource}"], reg
          nix_path = dev("echo $NIX_PATH").strip()
          assert nix_path == "nixpkgs=flake:nixpkgs", nix_path
          # I-532: no channels anywhere, so nix-shell has nothing to warn about.
          assert "channels" not in nix_path, nix_path
          guest.fail("sudo -H -u dev bash -lc 'command -v nix-channel'")
          ver = dev("timeout 120 nix eval --raw nixpkgs#hello.version")
          assert ver.strip() == "${pkgs.hello.version}", ver

      with subtest("I-531: a flake that names nixpkgs without a URL locks offline"):
          glob = [l for l in reg.splitlines() if l.startswith("global flake:nixpkgs ")]
          assert len(glob) == 1 and glob[0].split()[2].startswith("github:NixOS/nixpkgs/"), reg
          guest.succeed("install -d -o dev -g dev /tmp/fl")
          guest.succeed("echo '{ outputs = { self, nixpkgs }: { v = nixpkgs.lib.version; }; }' > /tmp/fl/flake.nix && chown dev:dev /tmp/fl/flake.nix")
          dev("cd /tmp/fl && timeout 120 nix --offline flake lock")
          locked = json.loads(dev("cat /tmp/fl/flake.lock"))["nodes"]["nixpkgs"]["locked"]
          assert locked["type"] == "github" and locked["owner"] == "NixOS" and locked["repo"] == "nixpkgs", locked
          assert glob[0].split()[2].startswith("github:NixOS/nixpkgs/" + locked["rev"]), (glob, locked)

      with subtest("I-530: dev is a trusted nix user"):
          info = dev("nix store info")
          assert "Trusted: 1" in info, info

      with subtest("I-534: man pages for what is installed"):
          for page in ["ls", "git", "tmux", "nix"]:
              dev(f"man -w {page}")

      with subtest("I-219: nix-locate maps a binary to its attribute"):
          attrs = dev("nix-locate --minimal --no-group --type x --type s --whole-name --at-root /bin/cowsay")
          assert "cowsay.out" in attrs.split(), attrs
          assert dev("nix-locate --minimal --no-group --type x --type s --whole-name --at-root /bin/reposenosuchcommand").strip() == ""

      with subtest("I-219: an unknown command that nixpkgs has prints the hint"):
          out = guest.succeed("sudo -H -u dev bash -ic 'cowsay hi; echo status=$?' 2>&1 || true")
          print(out)
          # I-249: the plain not-found line, then the two commands aligned.
          lines = [l for l in out.splitlines() if l.strip()]
          i = lines.index("cowsay: command not found")
          assert lines[i + 1] == "  nix profile add nixpkgs#cowsay  install it on this machine", out
          assert lines[i + 2] == "  repose config add cowsay        keep it on every rebuild (run this on your laptop)", out
          assert "is not installed" not in out, out
          assert "status=127" in out, out

      with subtest("I-410: a command only one package has prints the hint"):
          # az is only in azure-cli; the "other packages" grep found
          # nothing and ended the handler with no output at all.
          out = guest.succeed("sudo -H -u dev bash -ic 'az; echo status=$?' 2>&1 || true")
          print(out)
          lines = [l for l in out.splitlines() if l.strip()]
          i = lines.index("az: command not found")
          assert lines[i + 1] == "  nix profile add nixpkgs#azure-cli  install it on this machine", out
          assert "Other packages" not in out, out
          assert "status=127" in out, out

      with subtest("I-219: a truly unknown command prints the plain not-found"):
          out = guest.succeed("sudo -H -u dev bash -ic 'reposenosuchcommand; echo status=$?' 2>&1 || true")
          print(out)
          assert "reposenosuchcommand: command not found" in out, out
          assert "nixpkgs#" not in out, out
          assert "status=127" in out, out

      with subtest("I-516: an agent's bash -c gets the hint; a script file and sh do not"):
          # BASH_ENV is what an agent's environment carries (env.nix); sudo
          # drops it, so it is set again here.
          agent = "sudo -H -u dev env BASH_ENV=/etc/repose/bash-env.sh"
          out = guest.succeed(agent + " bash -c 'cowsay hi; echo status=$?' 2>&1")
          print(out)
          assert "  nix profile add nixpkgs#cowsay  install it on this machine" in out, out
          assert "status=127" in out, out
          guest.succeed("printf 'cowsay hi\\necho status=$?\\n' > /tmp/cnf-script.sh && chmod 0755 /tmp/cnf-script.sh")
          out = guest.succeed(agent + " bash /tmp/cnf-script.sh 2>&1")
          assert "nixpkgs#" not in out and "status=127" in out, out
          out = guest.succeed(agent + " sh -c 'cowsay hi; echo status=$?' 2>&1")
          assert "nixpkgs#" not in out and "status=127" in out, out
          # I-577: with a PATH that lacks the helper, the handler still
          # answers once; it used to call itself, one bash deeper each time.
          out = guest.succeed(agent + " PATH=${pkgs.coreutils}/bin timeout 60 ${pkgs.bash}/bin/bash -c 'cowsay hi; echo status=$?' 2>&1")
          print(out)
          assert "  nix profile add nixpkgs#cowsay  install it on this machine" in out, out
          assert "status=127" in out and "fork" not in out, out
          interactive = guest.succeed("grep -c 'bin/repose-command-not-found' /etc/bashrc").strip()
          assert int(interactive) >= 1, interactive

      with subtest("I-517: package managers, pip and cron get their own hint; no test attributes"):
          out = guest.succeed("sudo -H -u dev bash -ic 'apt-get install jq' 2>&1 || true")
          print(out)
          lines = [l for l in out.splitlines() if l.strip()]
          i = lines.index("apt-get: command not found")
          assert lines[i + 1] == "  nix profile add nixpkgs#NAME  install a package on this machine", out
          assert lines[i + 2] == "  repose config add NAME        keep it on every rebuild (run this on your laptop)", out
          assert "nixpkgs#apt" not in out, out
          out = guest.succeed("sudo -H -u dev bash -ic 'pip install requests' 2>&1 || true")
          print(out)
          assert "python3 -m venv .venv" in out and "uv tool install NAME" in out, out
          assert "Packages.pip" not in out, out
          out = guest.succeed("sudo -H -u dev bash -ic crontab 2>&1 || true")
          assert "/docs/machine#scheduled-jobs" in out and "mcron" not in out, out
          # vim is neovim now (I-514), so the handler is asked directly.
          out = guest.succeed("sudo -H -u dev bash -lc 'repose-command-not-found vim' 2>&1 || true")
          print(out)
          assert "tests." not in out, out

      with subtest("I-219: a command being installed says so"):
          guest.succeed("echo cowsay > /run/user/1000/repose-installing && chown dev:dev /run/user/1000/repose-installing")
          out = guest.succeed("sudo -H -u dev bash -ic 'cowsay hi' 2>&1 || true")
          assert "cowsay is still being installed; try again in a moment" in out, out
          assert "nixpkgs#" not in out, out
          guest.succeed("rm /run/user/1000/repose-installing")

      # I-227: every package manager's user bin dir resolves from a login
      # shell, a non-login child of one, `sh -c` in a tmux window (how
      # `repose run` starts an agent), an interactive tmux pane, and a user
      # unit; and a base applied without reboot refreshes the running tmux
      # server and user manager.
      bin_dirs = ${builtins.toJSON userBinDirsForTest}
      probes = {}
      for i, d in enumerate(bin_dirs):
          name = f"repose-probe-{i}"
          probes[name] = d
          guest.succeed(f"install -d -o dev -g dev /home/dev/{d} && printf '#!/bin/sh\\necho ok\\n' > /home/dev/{d}/{name} && chmod 755 /home/dev/{d}/{name} && chown dev:dev /home/dev/{d}/{name}")
      names = " ".join(probes)
      guest.succeed(f"""cat > /tmp/check-path <<'EOF'
      #!/bin/sh
      for n in {names}; do
        command -v "$n" >/dev/null 2>&1 || echo "missing $n"
      done
      echo done
      EOF
      chmod 755 /tmp/check-path""")

      def check(where, out):
          out = out.strip()
          missing = [probes[l.split()[1]] for l in out.splitlines() if l.startswith("missing ")]
          assert out.endswith("done") and not missing, f"{where}: not on PATH: {missing}\n{out}"

      guest.succeed("install -d -o dev -g dev -m 0700 /home/dev/.repose")
      guest.succeed("""echo '{"project_id":"0192e4b0-0000-7000-8000-000000000001","slug":"todo-app","name":"todo-app","tz":"UTC","class":"large"}' > /home/dev/.repose/project.json && chown dev:dev /home/dev/.repose/project.json""")
      guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-tmux-session.service")
      guest.wait_until_succeeds("sudo -H -u dev tmux ls | grep -q '^todo-app:'", timeout=60)

      # `repose run` reaches the guest over SSH and starts an agent with
      # `tmux new-window <cmd>` from that SSH command; tmux gives a window
      # started by a client outside tmux the client's PATH. So the agent
      # path is tested through a real sshd, as the CLI does it.
      guest.succeed("ssh-keygen -q -t ed25519 -N ''' -f /root/ca && ssh-keygen -q -t ed25519 -N ''' -f /root/user")
      guest.succeed("install -m 0644 /root/ca.pub /run/repose/user_ca.pub")
      guest.succeed("echo 0192e4b0-0000-7000-8000-000000000001 > /etc/ssh/principals/dev && systemctl reload sshd")
      guest.succeed("ssh-keygen -q -s /root/ca -I 'user:t' -n 0192e4b0-0000-7000-8000-000000000001 -V -1m:+12h /root/user.pub")

      def ssh(cmd):
          return guest.succeed("ssh -n -F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o CertificateFile=/root/user-cert.pub -i /root/user dev@127.0.0.1 " + shlex.quote(cmd))

      def wait_out(tag):
          return guest.wait_until_succeeds(f"grep -q done /tmp/out-{tag} && cat /tmp/out-{tag}", timeout=30)

      def via_tmux_command(tag):
          guest.succeed(f"rm -f /tmp/out-{tag}")
          ssh(f"tmux new-window -d -t todo-app 'sh -c /tmp/check-path > /tmp/out-{tag} 2>&1'")
          return wait_out(tag)

      # What runs with the tmux server's own environment: run-shell, #()
      # status jobs, and windows opened from inside tmux with a command.
      def via_tmux_server(tag):
          guest.succeed(f"rm -f /tmp/out-{tag}")
          guest.succeed(f"sudo -H -u dev timeout 30 tmux run-shell -t todo-app '/tmp/check-path > /tmp/out-{tag} 2>&1' < /dev/null")
          return wait_out(tag)

      def via_user_unit():
          return guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 systemd-run --user --quiet --wait --pipe /tmp/check-path")

      with subtest("I-227: user bin dirs on PATH everywhere"):
          check("bash -lc", dev("/tmp/check-path"))
          check("bash -c child of a login shell", dev("bash -c /tmp/check-path"))
          check("sh -c child of a login shell", dev("sh -c /tmp/check-path"))
          check("ssh command (non-login bash -c)", ssh("/tmp/check-path"))
          check("ssh command, sh -c child", ssh("sh -c /tmp/check-path"))
          check("tmux new-window sh -c over ssh (repose run)", via_tmux_command("cmd"))
          check("tmux server environment (run-shell)", via_tmux_server("server"))
          guest.succeed("rm -f /tmp/out-pane")
          # The session's own first window: the interactive login shell a
          # user attaches to.
          guest.succeed("sudo -H -u dev tmux send-keys -t todo-app:shell '/tmp/check-path > /tmp/out-pane 2>&1' Enter")
          check("tmux interactive pane", wait_out("pane"))
          check("systemd-run --user", via_user_unit())

      # I-241: an agent `repose run "prompt"` starts is tmux new-window's
      # command over SSH, a bash that is neither login nor interactive, in
      # a tmux server started by a user unit before the project's env and
      # any secret existed. The agent wrapper sources
      # /etc/profile.d/repose.sh, so it sees both. The probe is wrapped by
      # the same wrap.nix as the five agents and started the way
      # startAgentWindow starts one.
      with subtest("I-241: an agent started by tmux new-window over ssh sees REPOSE_PROJECT and named secrets"):
          guest.succeed("printf 'TZ=UTC\\nREPOSE_PROJECT=todo-app\\n' > /etc/repose/env")
          guest.succeed("install -m 0400 -o dev -g dev /dev/null /run/repose/secrets.env && echo \"export MY_TOKEN='s3cr3t'\" > /run/repose/secrets.env")
          guest.succeed("rm -f /tmp/out-raw /tmp/out-agent")
          # Without the wrapper (evidence only: what a bare command gets).
          ssh("tmux new-window -t todo-app -n raw -c ~/todo-app -d 'sh -c \"echo project=$REPOSE_PROJECT secret=$MY_TOKEN; echo done\" > /tmp/out-raw 2>&1'")
          print("bare new-window command: " + wait_out("raw"))
          ssh("tmux new-window -t todo-app -n probe -c ~/todo-app -d 'repose-env-probe'")
          out = wait_out("agent")
          print(out)
          assert "project=todo-app" in out, out
          assert "secret=s3cr3t" in out, out
          assert "path=ok" in out, out
          assert "agent=repose-env-probe" in out, out

      # I-475: the agent's commands are `bash -c` children of a process that
      # loaded the secrets when it started. BASH_ENV, from the PAM
      # environment over ssh, makes each child source secrets.refresh when
      # its REPOSE_ENV_GEN is not the file's. The second file is what guestd
      # writes for the second generation: a value the parent holds as
      # guestd exported it is rotated or unset, one it set itself (MY_LOCAL,
      # as a .envrc would) stays.
      with subtest("I-475: a tmux window's bash -c child sees a secret rotated after the window started, and keeps its own value"):
          guest.succeed("""cat > /tmp/be-parent <<'EOF'
      echo "bash_env=$BASH_ENV start=$MY_TOKEN local=$MY_LOCAL"
      export MY_LOCAL=from-envrc
      while [ ! -e /tmp/be-go ]; do sleep 0.1; done
      bash -c 'echo "child=$MY_TOKEN gone=$GONE_TOKEN. local=$MY_LOCAL"'
      echo done
      EOF
      cat > /tmp/refresh-aa <<'EOF'
      # repose-env-gen 00000000000000aa
      export MY_TOKEN='s3cr3t' GONE_TOKEN='g' MY_LOCAL='prod' REPOSE_ENV_GEN=00000000000000aa
      EOF
      cat > /tmp/refresh-bb <<'EOF'
      # repose-env-gen 00000000000000bb
      case ''${GONE_TOKEN+s$GONE_TOKEN} in s'g') unset GONE_TOKEN ;; esac
      case ''${MY_LOCAL+s$MY_LOCAL} in ''') export MY_LOCAL='prod' ;; esac
      case ''${MY_TOKEN+s$MY_TOKEN} in '''|s's3cr3t') export MY_TOKEN='rotated' ;; esac
      export REPOSE_ENV_GEN=00000000000000bb
      EOF
      chmod 0755 /tmp/be-parent; rm -f /tmp/be-go /tmp/out-be""")
          guest.succeed("install -m 0400 -o dev -g dev /tmp/refresh-aa /run/repose/secrets.refresh")
          ssh("tmux new-window -t todo-app -n be -d 'bash /tmp/be-parent > /tmp/out-be 2>&1'")
          guest.wait_until_succeeds("grep -q start= /tmp/out-be", timeout=30)
          guest.succeed("install -m 0400 -o dev -g dev /tmp/refresh-bb /run/repose/secrets.refresh")
          guest.succeed("touch /tmp/be-go")
          out = wait_out("be")
          print(out)
          assert "bash_env=/etc/repose/bash-env.sh start=s3cr3t local=prod" in out, out
          assert "child=rotated gone=. local=from-envrc" in out, out
          # A login shell goes through the same file: no generation, so it
          # gets every secret.
          env = guest.succeed("sudo -u dev bash -lc 'echo $MY_TOKEN $MY_LOCAL $REPOSE_ENV_GEN'").strip()
          assert env == "rotated prod 00000000000000bb", env
          guest.succeed("rm -f /run/repose/secrets.refresh")

      with subtest("I-227: installs land on PATH (go install, npm i -g)"):
          guest.succeed("""install -d -o dev -g dev /tmp/gi /tmp/npmpkg/bin && cat > /tmp/gi/go.mod <<'EOF'
      module example.com/gi

      go 1.22
      EOF
      cat > /tmp/gi/main.go <<'EOF'
      package main

      import "fmt"

      func main() { fmt.Println("gi ok") }
      EOF
      cat > /tmp/npmpkg/package.json <<'EOF'
      {"name": "repose-npm-probe", "version": "1.0.0", "bin": {"repose-npm-probe": "bin/probe.js"}}
      EOF
      printf '#!/usr/bin/env node\\nconsole.log("npm ok")\\n' > /tmp/npmpkg/bin/probe.js
      chmod 755 /tmp/npmpkg/bin/probe.js
      chown -R dev:dev /tmp/gi /tmp/npmpkg""")
          dev("cd /tmp/gi && GOTOOLCHAIN=local GOFLAGS=-mod=mod GOCACHE=/tmp/gi/cache go install .")
          assert dev("gi").strip() == "gi ok"
          dev("npm i -g --offline /tmp/npmpkg")
          assert dev("repose-npm-probe").strip() == "npm ok"
          assert guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 systemd-run --user --quiet --wait --pipe sh -c gi").strip() == "gi ok"

      with subtest("I-227: activation refreshes a running tmux server and user manager"):
          guest.succeed("sudo -H -u dev tmux set-environment -g PATH /run/current-system/sw/bin")
          guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user set-environment PATH=/run/current-system/sw/bin")
          out = via_tmux_server("stale")
          assert "missing" in out, out
          out = via_user_unit()
          assert "missing" in out, out
          guest.succeed("/run/current-system/activate")
          check("tmux server environment after activation", via_tmux_server("fresh"))
          check("systemd-run --user after activation", via_user_unit())
    '';
  };

  # I-221, I-222 on a real base: the CLI's tools part (the golden list from
  # internal/cli's TestToolsGuestPartsGolden) plans, the user unit installs
  # in the background, and every tool resolves in a new login shell.
  # Offline: "nixpkgs" is a stand-in flake (fakeNixpkgs) whose packages
  # build from what the guest's store already has, nix-locate is a stand-in
  # index over it, and npm installs from a registry on 127.0.0.1. go,
  # cargo and uv installs need the network and are not exercised here.
  guest-tools-carry = mkTest "guest-tools-carry" {
    nodes.guest = { lib, ... }: {
      imports = [ node ];
      environment.systemPackages = [ pkgs.python3 fakeNixLocate ];
      # The stand-in index, not the base's real one (I-219), answers
      # nix-locate here: the real index knows nothing of the fake tools.
      repose.nixIndexPackage = lib.mkForce null;
      virtualisation.additionalPaths = [ fakeNixpkgs pkgs.bash pkgs.coreutils ];
      nix.registry.nixpkgs.to = lib.mkForce { type = "path"; path = "${fakeNixpkgs}"; };
      nix.settings.flake-registry = lib.mkForce "";
      # The stand-in packages name their builder's paths as plain strings.
      nix.settings.sandbox = lib.mkForce false;
      nix.settings.substituters = lib.mkForce [ ];
      # The store overlay's upper on the disk, as on a real guest's thin
      # volume, so what dev installed survives the reboot subtest.
      virtualisation.writableStoreUseTmpfs = false;
    };
    testScript = ''
      import json
      guest.start()
      guest.wait_for_unit("multi-user.target")
      guest.wait_for_unit("user@1000.service")
      guest.succeed("install -d -o dev -g dev -m 0700 /home/dev/.repose")
      guest.succeed("mkdir -p /tmp/p && cp -r ${guestParts}/. /tmp/p && chmod -R u+w /tmp/p && chown -R dev:dev /tmp/p")
      wanted = json.loads(guest.succeed("cat /tmp/p/tools/wanted.json"))

      # The npm registry: fake-tool@1.0.0, packed here, served by python.
      guest.succeed("mkdir -p /tmp/fake-tool /srv/npm")
      guest.succeed("""printf '%s\n' '{"name":"fake-tool","version":"1.0.0","bin":{"fake-tool":"cli.sh"}}' > /tmp/fake-tool/package.json""")
      guest.succeed("printf '#!/bin/sh\\necho fake-tool ok\\n' > /tmp/fake-tool/cli.sh && chmod +x /tmp/fake-tool/cli.sh")
      guest.succeed("cd /srv/npm && HOME=/tmp npm pack /tmp/fake-tool")
      guest.succeed("${npmMeta} /srv/npm/fake-tool-1.0.0.tgz > /srv/npm/fake-tool")
      guest.succeed("systemd-run --unit fake-npm python3 -m http.server 4874 --bind 127.0.0.1 --directory /srv/npm")
      guest.wait_for_open_port(4874, "127.0.0.1")
      guest.succeed("sudo -H -u dev sh -c 'echo registry=http://127.0.0.1:4874/ > /home/dev/.npmrc'")
      assert guest.succeed("sudo -H -u dev bash -lc 'node --version'").strip().startswith("v24."), "the base's node is not 24"

      with subtest("plan: the one line, the installing file, no install yet"):
          out = guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 sh -e /tmp/p/tools.sh /tmp/p")
          print(out)
          assert "#installing fake-tool greet hello nonexistent-cmd nodejs_22 ruby_3_3 jdk21_headless" in out, out
          listed = guest.succeed("cat /run/user/1000/repose-installing").split()
          assert sorted(listed) == ["fake-tool", "greet", "hello", "nonexistent-cmd"], listed
          assert guest.succeed("cat /home/dev/.repose/tools-wanted.json").strip() == json.dumps(wanted, separators=(",", ":"))

      with subtest("run: every tool resolves in a new login shell, the installing file is gone"):
          guest.wait_until_succeeds(f"grep -qx {wanted['hash']} /home/dev/.repose/carry/tools", timeout=900)
          print(guest.succeed("cat /home/dev/.repose/tools-install.log"))
          assert "stand-in" in guest.succeed("sudo -H -u dev bash -lc 'hello'")
          assert guest.succeed("sudo -H -u dev bash -lc 'greet'").strip() == "greetings"
          assert guest.succeed("sudo -H -u dev bash -lc 'fake-tool'").strip() == "fake-tool ok"
          assert guest.succeed("sudo -H -u dev bash -lc 'node --version'").strip() == "v22.1.0"
          guest.fail("test -e /run/user/1000/repose-installing")
          ilog = guest.succeed("cat /home/dev/.repose/tools-install.log")
          assert "hello: installed nixpkgs#hello" in ilog, ilog
          assert "greet: installed nixpkgs#greeter" in ilog, ilog
          assert "fake-tool: installed with npm" in ilog, ilog
          assert "node: nodejs_22 is the node of new shells" in ilog, ilog
          assert "ruby: ruby_3_3 is the ruby of new shells" in ilog, ilog
          assert "java: jdk21_headless is the java of new shells" in ilog, ilog
          assert guest.succeed("sudo -H -u dev bash -lc ruby").startswith("ruby 3.3.9")
          assert "21.0.8" in guest.succeed("sudo -H -u dev bash -lc 'java -version 2>&1'")
          # dev's profile holds them, so the store overlay pins them
          profile = guest.succeed("sudo -H -u dev nix profile list")
          assert "hello" in profile and "greeter" in profile and "nodejs_22" in profile and "ruby_3_3" in profile and "jdk21_headless" in profile, profile

      with subtest("what could not be installed is said once"):
          notices = guest.succeed("cat /home/dev/.repose/tools-notices")
          assert "Could not install nonexistent-cmd: no nixpkgs package has bin/nonexistent-cmd" in notices, notices
          out = guest.succeed("sudo -H -u dev sh -e /tmp/p/tools-notices.sh /tmp/p")
          assert "#warn Could not install nonexistent-cmd" in out, out
          assert guest.succeed("sudo -H -u dev sh -e /tmp/p/tools-notices.sh /tmp/p").strip() == ""

      with subtest("the same list again installs nothing and says nothing"):
          out = guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 sh -e /tmp/p/tools.sh /tmp/p")
          assert "#installing" not in out and "#warn" not in out, out
          guest.fail("test -e /run/user/1000/repose-installing")

      with subtest("a pass cut short is finished by the unit at the next boot"):
          guest.succeed("rm /home/dev/.repose/carry/tools")
          guest.succeed("sudo -H -u dev nix profile remove greeter")
          guest.fail("sudo -H -u dev bash -lc 'command -v greet'")
          guest.shutdown()
          guest.start()
          guest.wait_for_unit("user@1000.service")
          guest.wait_until_succeeds(f"grep -qx {wanted['hash']} /home/dev/.repose/carry/tools", timeout=900)
          print(guest.succeed("tail -n 12 /home/dev/.repose/tools-install.log"))
          assert guest.succeed("sudo -H -u dev bash -lc 'greet'").strip() == "greetings"

      with subtest("I-522: a cargo tool gets rustup a default toolchain first"):
          # Stand-ins ahead of rustup's proxies on the login PATH: rustup
          # has no default until `default stable`, and cargo fails without
          # one, as the real ones do.
          guest.succeed("""sudo -H -u dev sh -c 'mkdir -p /home/dev/.local/bin && cd /home/dev/.local/bin && cat > rustup <<"EOF"
      #!/bin/sh
      echo "rustup $*" >> /tmp/rustup-calls
      case "$1 $2" in
        "show active-toolchain") test -f /tmp/rustup-default ;;
        "toolchain install") : > /tmp/rustup-stable ;;
        "default stable") test -f /tmp/rustup-stable && : > /tmp/rustup-default ;;
      esac
      EOF
      cat > cargo <<"EOF"
      #!/bin/sh
      test -f /tmp/rustup-default || { echo "error: no default is configured" >&2; exit 1; }
      printf "#!/bin/sh\\necho crate ok\\n" > /home/dev/.local/bin/fake-crate && chmod +x /home/dev/.local/bin/fake-crate
      EOF
      chmod +x rustup cargo'""")
          guest.succeed("""sudo -H -u dev sh -c 'printf "%s" "{\\"hash\\":\\"cargo-1\\",\\"items\\":[{\\"name\\":\\"fake-crate\\",\\"manager\\":\\"cargo\\",\\"pkg\\":\\"fake-crate\\",\\"bins\\":[\\"fake-crate\\"]}]}" > /home/dev/.repose/tools-wanted.json'""")
          guest.succeed("sudo -H -u dev XDG_RUNTIME_DIR=/run/user/1000 REPOSE_TOOLS_DELAY=0 bash -lc 'repose-tools-install run'")
          calls = guest.succeed("cat /tmp/rustup-calls")
          assert "rustup toolchain install stable --profile minimal\nrustup default stable" in calls, calls
          assert guest.succeed("sudo -H -u dev bash -lc fake-crate").strip() == "crate ok"
          assert "rustup: stable (minimal profile) is the default toolchain" in guest.succeed("cat /home/dev/.repose/tools-install.log")
          guest.succeed("rm /home/dev/.local/bin/rustup /home/dev/.local/bin/cargo /home/dev/.local/bin/fake-crate /home/dev/.repose/tools-wanted.json")

      with subtest("I-417: a boot sets the old /tmp aside in one rename and deletes it after"):
          guest.succeed("sudo -u dev mkdir -p /tmp/stale && sudo -u dev sh -c 'for i in $(seq 2000); do : > /tmp/stale/f$i; done'")
          guest.shutdown()
          guest.start()
          guest.wait_for_unit("multi-user.target")
          guest.fail("test -e /tmp/stale")
          assert guest.succeed("stat -c %a /tmp").strip() == "1777"
          guest.succeed("systemctl show -p Result --value repose-tmp-rotate | grep -qx success")
          guest.succeed("test -d /var/lib/repose/tmp-old/*/tmp/stale")
          # Off the boot: tmpfiles no longer deletes anything, and the purge
          # is a timer, not a dependency of anything a boot waits for.
          guest.fail("systemd-tmpfiles --cat-config | grep -q '^D! /tmp '")
          # What a base switch without a reboot does to it: nothing.
          guest.succeed("touch /tmp/live && systemctl start repose-tmp-rotate && test -e /tmp/live")
          guest.succeed("systemctl start repose-tmp-purge")
          assert guest.succeed("ls -A /var/lib/repose/tmp-old").strip() == ""
    '';
  };

  guest-compat = mkTest "guest-compat" {
    nodes.guest = node;
    testScript = ''
      guest.start()
      guest.wait_for_unit("multi-user.target")

      import shlex

      def dev(cmd):
          return guest.succeed(f"sudo -H -u dev bash -lc {shlex.quote(cmd)}")

      def fetch(path, method="GET"):
          return guest.succeed(f"curl -s --path-as-is -X {method} -o /dev/null -w '%{{http_code}} %{{redirect_url}}' 'http://127.0.0.1:850{path}'").strip()

      with subtest("I-228: Prisma's mirror redirects linux-nixos to the debian build"):
          guest.wait_for_unit("repose-prisma-engines.socket")
          mirror = dev("echo -n $PRISMA_ENGINES_MIRROR")
          assert mirror == "http://127.0.0.1:850", mirror
          up = "https://binaries.prisma.sh/all_commits/${prismaCommit}"
          out = fetch("/all_commits/${prismaCommit}/linux-nixos/schema-engine.gz")
          assert out == f"302 {up}/debian-openssl-3.0.x/schema-engine.gz", out
          out = fetch("/all_commits/${prismaCommit}/linux-nixos/libquery_engine.so.node.gz.sha256")
          assert out == f"302 {up}/debian-openssl-3.0.x/libquery_engine.so.node.gz.sha256", out
          # Any other target (a deploy target in binaryTargets) is untouched.
          out = fetch("/all_commits/${prismaCommit}/rhel-openssl-3.0.x/libquery_engine.so.node.gz")
          assert out == f"302 {up}/rhel-openssl-3.0.x/libquery_engine.so.node.gz", out
          assert fetch("/all_commits/x/linux-nixos/a", "HEAD").startswith("302 "), "HEAD"
          assert fetch("/all_commits/x/linux-nixos/a", "POST").startswith("405"), "POST"
          assert fetch("/a/../etc/passwd").startswith("400"), "dotdot"
          assert fetch("/a%20b").startswith("400"), "escape"
          # Never auto-forwarded: loopback, under 1024 (I-199).
          guest.succeed("ss -Hltn | grep -q '127.0.0.1:850 '")

      with subtest("I-228: the debian engine the redirect names runs through nix-ld"):
          guest.succeed("install -d -o dev -g dev /tmp/prisma")
          guest.succeed("gzip -dc ${prismaSchemaEngineGz} > /tmp/prisma/schema-engine-linux-nixos && chmod +x /tmp/prisma/schema-engine-linux-nixos && chown dev:dev /tmp/prisma/schema-engine-linux-nixos")
          out = dev("/tmp/prisma/schema-engine-linux-nixos --version")
          assert out.strip() == "schema-engine-cli ${prismaCommit}", out

      with subtest("I-228: Playwright's directory is writable and seeded"):
          d = "/home/dev/.cache/ms-playwright"
          path = dev("echo -n $PLAYWRIGHT_BROWSERS_PATH")
          assert path == d, path
          guest.wait_until_succeeds("systemctl show -p Result repose-playwright-seed.service | grep -q success && test -L /home/dev/.cache/ms-playwright/chromium_headless_shell-*")
          names = guest.succeed("ls ${pkgs.reposePlaywrightBrowsers}").split()
          chromium = [n for n in names if n.startswith("chromium-")][0]
          for n in names:
              dev(f"test -L {d}/{n} && test -d {d}/{n}/")
          dev(f"touch {d}/probe && rm {d}/probe")
          # A user's own download is left alone, a stale store link goes,
          # a missing link comes back.
          dev(f"rm {d}/{chromium} && mkdir {d}/{chromium} && ln -s /nix/store/00000000000000000000000000000000-gone {d}/chromium-1 && rm {d}/ffmpeg-*")
          guest.succeed("systemctl restart repose-playwright-seed.service")
          dev(f"test -d {d}/{chromium} && ! test -L {d}/{chromium}")
          dev(f"! test -e {d}/chromium-1 && ! test -L {d}/chromium-1")
          dev(f"test -L {d}/ffmpeg-* || ! ls ${pkgs.reposePlaywrightBrowsers} | grep -q ffmpeg")
          guest.succeed("systemctl restart repose-playwright-seed.service")
          # The MCP server still reads the store path from its own wrapper.
          guest.succeed("grep -q 'PLAYWRIGHT_BROWSERS_PATH=.*${pkgs.reposePlaywrightBrowsers}' ${pkgs.reposeMcp.playwright-mcp}/bin/playwright-mcp")

      with subtest("I-228: a manylinux wheel imports under the system python and its venvs"):
          guest.succeed("install -d -o dev -g dev /tmp/py && cp ${numpyWheel} /tmp/py/${numpyWheelName} && chown dev:dev /tmp/py/*")
          dev("cd /tmp/py && python3 -m venv v && v/bin/pip install -q --no-index /tmp/py/${numpyWheelName}")
          out = dev("env -u LD_LIBRARY_PATH /tmp/py/v/bin/python -c 'import numpy, sys; print(numpy.__version__, sys.prefix)'")
          assert out.strip() == "2.3.3 /tmp/py/v", out
          out = dev("cd /tmp/py && . v/bin/activate && python -c 'import numpy; print(numpy.ones(3).sum())'")
          assert out.strip() == "3.0", out
          dev("cd /tmp/py && UV_OFFLINE=1 uv venv -q -p python3 u && UV_OFFLINE=1 VIRTUAL_ENV=/tmp/py/u uv pip install -q --no-index /tmp/py/${numpyWheelName}")
          out = dev("env -u LD_LIBRARY_PATH /tmp/py/u/bin/python -c 'import numpy; print(numpy.__version__)'")
          assert out.strip() == "2.3.3", out
          out = dev("python3 -c 'import ssl, sqlite3, sys; print(sys.executable)'")
          assert out.strip() == "/run/current-system/sw/bin/python3", out

      with subtest("I-523: pipx uses the nix-ld python3"):
          assert dev("echo -n $PIPX_DEFAULT_PYTHON") == "/run/current-system/sw/bin/python3"

      with subtest("I-521: /bin/bash and /usr/bin/python3 scripts run, /etc/ssl/cert.pem is the CA bundle"):
          guest.succeed("install -d -o dev -g dev /tmp/sb")
          guest.succeed("printf '#!/bin/bash\\necho \"bash $BASH_VERSION\"\\n' > /tmp/sb/x.sh")
          guest.succeed("printf '#!/usr/bin/python3\\nimport sys\\nprint(sys.executable)\\n' > /tmp/sb/p.py")
          guest.succeed("printf 'SHELL := /bin/bash\\nall:\\n\\t@echo make $$BASH_VERSION\\n' > /tmp/sb/Makefile")
          guest.succeed("chmod +x /tmp/sb/x.sh /tmp/sb/p.py && chown -R dev:dev /tmp/sb")
          assert dev("/tmp/sb/x.sh").startswith("bash 5."), "#!/bin/bash"
          assert dev("/tmp/sb/p.py").strip() == "/usr/bin/python3", "#!/usr/bin/python3"
          assert dev("cd /tmp/sb && make -s").startswith("make 5."), "SHELL := /bin/bash"
          dev("/usr/bin/python -c 'import ssl' && /usr/bin/bash -c true")
          dev("test -x /usr/bin/perl || ! test -e /run/current-system/sw/bin/perl")
          guest.succeed("test -e /etc/ssl/cert.pem && cmp /etc/ssl/cert.pem /etc/ssl/certs/ca-certificates.crt")

      with subtest("I-228: pkg-config finds the common system libraries"):
          out = dev("pkg-config --modversion openssl zlib sqlite3 libffi")
          assert len(out.split()) == 4, out

      with subtest("I-265: Rails' native gem libraries build and link, and their configs are on PATH"):
          libs = "yaml-0.1 libpq libxml-2.0 libxslt mysqlclient"
          print(dev(f"pkg-config --modversion {libs}"))
          dev(f"install -d /tmp/gems && cp ${nativeGemProbe} /tmp/gems/t.c && cd /tmp/gems && cc t.c $(pkg-config --cflags --libs {libs}) -o t")
          out = dev("/tmp/gems/t")
          assert out.startswith("0 1 1 "), out
          assert dev("pg_config --libdir").strip() == dev("pkg-config --variable=libdir libpq").strip()
          assert "-lmariadb" in dev("mysql_config --libs") or "-lmysqlclient" in dev("mysql_config --libs")
    '';
  };

  guest-docker = mkTest "guest-docker" {
    nodes.guest = node;
    testScript = ''
      guest.start()
      guest.wait_for_unit("multi-user.target")
      guest.wait_for_unit("docker.service")
      with subtest("docker runs with overlay2 and the pinned address pool"):
          info = guest.succeed("docker info")
          assert "Storage Driver: overlay2" in info, info
          guest.succeed("docker load < ${helloImage}")
          out = guest.succeed("sudo -u dev docker run --rm repose-hello:test")
          assert "Hello, world!" in out, out
          guest.succeed("docker network create t")
          subnet = guest.succeed("docker network inspect t -f '{{(index .IPAM.Config 0).Subnet}}'").strip()
          assert subnet.startswith("172.2") and int(subnet.split(".")[1]) in range(20, 24), subnet
          guest.succeed("docker compose version")
      with subtest("I-536: containers outlive dockerd"):
          assert guest.succeed("docker info -f '{{.LiveRestoreEnabled}}'").strip() == "true"
      with subtest("I-537: containers resolve through resolved on the bridge's gateway"):
          guest.succeed("docker run --name dns repose-hello:test")
          path = guest.succeed("docker inspect -f '{{.ResolvConfPath}}' dns").strip()
          conf = guest.succeed(f"cat {path}")
          assert "nameserver 172.20.0.1" in conf, conf
          assert "172.20.0.1:53 " in guest.succeed("ss -Hlun"), "no resolved stub on 172.20.0.1"
          guest.succeed("docker rm dns")
    '';
  };

  # The agents' browser on the desktop (DECISIONS I-33, I-246, I-292): an
  # MCP server driven over stdio JSON-RPC navigates the shared headed
  # browser, the X display shows the page, the other MCP server sees the
  # same tab, the browser comes back after a crash; the viewer page is
  # served, a real RFB client authenticates with the boot's password and
  # gets the framebuffer, and its SetDesktopSize resizes the screen with
  # the browser's window following.
  guest-desktop = mkTest "guest-desktop" {
    nodes.guest = { pkgs, ... }: {
      imports = [ node ];
      environment.systemPackages = [ pkgs.imagemagick pkgs.python3 pkgs.xdotool pkgs.xorg.xdpyinfo pkgs.xorg.xrandr ];
    };
    testScript = ''
      import json, shlex

      guest.start()
      guest.wait_for_unit("multi-user.target")
      guest.wait_for_unit("repose-novnc.socket")
      guest.wait_for_unit("repose-browser.socket")

      reg = json.loads(guest.succeed("cat /etc/repose/mcp.json"))["mcpServers"]

      def mcp(server, *calls):
          """Start the registered MCP server as dev and make the calls."""
          e = reg[server]
          argv = [e["command"]] + e["args"] + ["--"]
          for name, args in calls:
              argv += [name, json.dumps(args)]
          cmd = "cd /home/dev && python3 ${./mcp-client.py} " + " ".join(shlex.quote(a) for a in argv)
          # The MCP servers give the browser 30 s to answer their first
          # connection, a limit of theirs. On a loaded test box (2026-09-27:
          # four workers on four cores, the guest's load at 5 on 2 vCPUs)
          # a cold Chromium took over two minutes on Xvfb and Xvnc alike,
          # so a connect timeout is tried again; a fast box never retries.
          for attempt in range(4):
              status, out = guest.execute(f"sudo -u dev bash -lc {shlex.quote(cmd)} 2>/tmp/mcp-{server}.err")
              if status == 0:
                  return out
              err = guest.execute(f"tail -20 /tmp/mcp-{server}.err")[1]
              if "initializeServer: Timeout" not in out + err or attempt == 3:
                  raise Exception(f"{server} failed ({status}):\n{out}\n{err}")
              print(f"{server}: the browser did not answer within the server's 30 s; trying again ({attempt + 2}/4)")

      def magenta_share(path, crop=""):
          """Share of the screen (or of a crop of it) that is the page's magenta."""
          out = guest.succeed(f"convert {path} {crop} -fuzz 5% -fill white -opaque '#ff00ff' -fill black +opaque white -colorspace gray -format '%[fx:mean]' info:")
          return float(out.strip())

      def memory(unit):
          cg = guest.succeed(f"systemctl show -P ControlGroup {unit}").strip()
          total = int(guest.succeed(f"cat /sys/fs/cgroup{cg}/memory.current").strip()) // (1024 * 1024)
          stat = guest.succeed(f"cat /sys/fs/cgroup{cg}/memory.stat")
          anon = int(dict(l.split() for l in stat.splitlines())["anon"]) // (1024 * 1024)
          # anon is the process's own memory; the rest is page cache,
          # charged to whichever cgroup read a file first.
          return f"{anon} MiB anon / {total} MiB charged"

      def cpu_usec(unit):
          cg = guest.succeed(f"systemctl show -P ControlGroup {unit}").strip()
          stat = guest.succeed(f"cat /sys/fs/cgroup{cg}/cpu.stat")
          return int(stat.split("usage_usec ")[1].split()[0])

      def rfb(*args):
          """The RFB client (rfb-client.py) against the guest's Xvnc."""
          out = guest.succeed("python3 ${./rfb-client.py} " + " ".join(shlex.quote(a) for a in args))
          return json.loads(out)

      def screen_size():
          out = guest.succeed("xdpyinfo -display :99 | sed -n 's/.*dimensions: *\\([0-9]*x[0-9]*\\) pixels.*/\\1/p'").strip()
          return out

      def browser_window():
          """Geometry of the agents' browser's visible top-level window, WxH."""
          wid = guest.succeed("DISPLAY=:99 xdotool search --onlyvisible --classname '^[Cc]hromium' | head -1").strip()
          assert wid, "no chromium window"
          out = guest.succeed(f"DISPLAY=:99 xdotool getwindowgeometry --shell {wid}")
          g = dict(l.split("=") for l in out.split())
          return f"{g['WIDTH']}x{g['HEIGHT']}"

      with subtest("nothing runs until asked for"):
          for u in ["repose-xvnc", "repose-openbox", "repose-browser", "repose-novnc"]:
              guest.fail(f"systemctl is-active {u}.service")
          assert reg["playwright"]["args"] == ["--cdp-endpoint", "http://127.0.0.1:9224"], reg
          assert reg["chrome-devtools"]["args"] == ["--browserUrl", "http://127.0.0.1:9224"], reg

      guest.succeed("install -d -o dev -g dev /home/dev/site")
      guest.succeed("""cat > /home/dev/site/magenta.html <<'EOF'
      <html><head><title>repose magenta</title></head><body style="margin:0;background:#ff00ff"><h1>repose says hello</h1></body></html>
      EOF""")
      guest.succeed("systemd-run --unit site -p User=dev python3 -m http.server 8123 --bind 127.0.0.1 --directory /home/dev/site")
      guest.wait_for_open_port(8123)
      page = "http://127.0.0.1:8123/magenta.html"

      with subtest("the first connection to 9224 starts the display, the window manager and the browser"):
          # A plain connection first, with time to spare: the MCP
          # server's own connect timeout is 30 s, and a cold Chromium on
          # a loaded 2-vCPU test VM has taken over a minute (2026-09-27),
          # which is the test box, not the guest.
          guest.succeed("curl -s -m 300 -o /dev/null http://127.0.0.1:9224/json/version")
          for u in ["repose-xvnc", "repose-openbox", "repose-browser"]:
              guest.succeed(f"systemctl is-active {u}.service")

      with subtest("playwright MCP drives the browser, headed on :99, and the page shows there"):
          out = mcp("playwright", ("browser_navigate", {"url": page}))
          assert "magenta.html" in out, out
          # The viewer is not needed for the browser to draw.
          guest.fail("systemctl is-active repose-novnc.service")
          assert "repose-browser.slice" in guest.succeed("systemctl show -P Slice repose-browser.service")
          assert guest.succeed("systemctl show -P MemoryMax repose-browser.slice").strip() != "infinity"
          assert screen_size() == "1440x900", screen_size()
          guest.succeed("DISPLAY=:99 import -window root /tmp/screen.png")
          guest.copy_from_machine("/tmp/screen.png", "")
          share = magenta_share("/tmp/screen.png")
          print(f"magenta share of the 1440x900 screen: {share:.3f}")
          # The page fills the maximised window under the tab strip.
          assert share > 0.8, share
          # ...and reaches the screen's bottom-right corner.
          corner = magenta_share("/tmp/screen.png", "-crop 20x20+1420+880 +repage")
          assert corner > 0.95, corner
          # The tab outlives the MCP server that opened it.
          out = mcp("playwright", ("browser_tabs", {"action": "list"}))
          assert "magenta.html" in out, out

      with subtest("chrome-devtools MCP sees the same tab"):
          out = mcp("chrome-devtools", ("list_pages", {}))
          assert "magenta.html" in out, out

      with subtest("software rendering: WebGL works, no GPU process relaunch loop"):
          out = mcp("playwright", ("browser_evaluate", {"function": "() => 'webgl=' + !!document.createElement('canvas').getContext('webgl')"}))
          assert "webgl=true" in out, out
          fails = guest.succeed("journalctl -u repose-browser.service | grep -c 'GLDisplayEGL::Initialize failed' || true").strip()
          print(f"MEASURE EGL init failures in the browser's journal: {fails}")
          assert fails == "0", fails

      with subtest("new shells get DISPLAY while the display is up"):
          disp = guest.succeed("sudo -u dev bash -lc 'echo $DISPLAY'").strip()
          assert disp == ":99", disp

      with subtest("memory and CPU, headed on Xvnc against headless"):
          guest.sleep(5)
          headed = memory("repose-browser.service")
          xvnc = memory("repose-xvnc.service")
          openbox = memory("repose-openbox.service")
          c0 = cpu_usec("repose-browser.service") + cpu_usec("repose-xvnc.service")
          guest.sleep(30)
          c1 = cpu_usec("repose-browser.service") + cpu_usec("repose-xvnc.service")
          guest.succeed("systemd-run --unit headless-measure -p User=dev -p Environment=HOME=/home/dev ${pkgs.chromium}/bin/chromium --headless --user-data-dir=/tmp/headless-measure --remote-debugging-port=9333 --no-first-run " + page)
          guest.wait_for_open_port(9333)
          guest.sleep(5)
          headless = memory("headless-measure.service")
          h0 = cpu_usec("headless-measure.service")
          guest.sleep(30)
          h1 = cpu_usec("headless-measure.service")
          guest.succeed("systemctl stop headless-measure.service")
          print(f"MEASURE headed chromium {headed}; Xvnc {xvnc}; openbox {openbox}; headless chromium {headless}")
          print(f"MEASURE idle CPU over 30 s: headed+Xvnc {(c1 - c0) / 1e6:.2f} s, headless {(h1 - h0) / 1e6:.2f} s")

      with subtest("the browser comes back after a crash, and a running MCP server reconnects"):
          # The MCP server keeps running across the kill; the socket
          # restarts the browser on the next connection (the curl gives
          # a cold start on a slow test VM the time it needs, as above).
          out = mcp("playwright",
                    ("browser_navigate", {"url": page + "?before"}),
                    ("!sh", {"cmd": "pkill -KILL -o -f user-data-dir=/home/dev/.local/share/repos[e]/browser; sleep 3; curl -s -m 300 -o /dev/null http://127.0.0.1:9224/json/version"}),
                    ("browser_navigate", {"url": page + "?after"}))
          assert "?after" in out, out
          guest.succeed("systemctl is-active repose-browser.service")
          guest.succeed("DISPLAY=:99 import -window root /tmp/screen2.png")
          share = magenta_share("/tmp/screen2.png")
          assert share > 0.8, share
          out = mcp("chrome-devtools", ("list_pages", {}))
          assert "?after" in out, out

      with subtest("a connection to 6080 starts the viewer and serves repose's page"):
          out = guest.succeed("curl -s -m 20 -o /tmp/index.html -w '%{http_code}' http://127.0.0.1:6080/")
          assert out.strip() == "200", out
          guest.wait_for_unit("repose-novnc.service")
          index = guest.succeed("cat /tmp/index.html")
          assert "the agent's browser" in index and "viewer.js" in index, index
          assert guest.succeed("curl -sf http://127.0.0.1:6080/healthz").strip() == "repose desktop viewer ok"
          ctype = guest.succeed("curl -sf -o /dev/null -w '%{content_type}' http://127.0.0.1:6080/viewer.js").strip()
          assert "javascript" in ctype, ctype
          guest.succeed("curl -sf http://127.0.0.1:6080/core/rfb.js -o /tmp/rfb.js && grep -q 'export default class RFB' /tmp/rfb.js")  # not piped: grep -q closes the pipe early and curl exits 23
          guest.succeed("curl -sf http://127.0.0.1:6080/vendor/pako/lib/zlib/inflate.js -o /dev/null")
          proj = json.loads(guest.succeed("curl -sf http://127.0.0.1:6080/project.json"))
          assert proj["name"] and proj["idle_minutes"] == 30, proj
          # Stock noVNC's page is not shipped; ours is the index.
          assert guest.succeed("curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:6080/vnc.html").strip() == "404"
          pw = guest.succeed("cat /run/repose/desktop/vnc-password").strip()
          assert len(pw) == 8, pw
          assert guest.succeed("stat -c '%U %a' /run/repose/desktop/vnc-password").strip() == "dev 600"
          prof = guest.succeed("sudo -u dev repose-guest-profile desktop status").strip()
          assert prof == "running", prof
          prof = json.loads(guest.succeed("sudo -u dev repose-guest-profile"))
          assert prof["desktop"]["running"] is True and prof["desktop"]["novnc_port"] == 6080, prof
          out = mcp("chrome-devtools", ("list_pages", {}))
          assert "?after" in out, out

      with subtest("a real RFB client authenticates with the password and receives the framebuffer"):
          guest.fail(f"python3 ${./rfb-client.py} --password wrong{pw[:3]}")
          r = rfb("--password", pw)
          print("RFB:", json.dumps({k: r[k] for k in ("server_version", "security_types", "auth", "width", "height", "bpp", "name", "first_update")}))
          assert r["auth"] == "ok" and r["security_types"] == [2], r
          assert (r["width"], r["height"]) == (1440, 900), r
          assert r["first_update"]["raw_bytes"] == 1440 * 900 * 4, r["first_update"]
          assert len(r["screens"]) == 1, r["screens"]

      with subtest("SetDesktopSize resizes the screen, and the browser's window follows"):
          # A large tab and a small one (Chromium's own minimum window
          # width is around 500 px, so a phone-width screen gets a window
          # wider than the screen, which the viewer scales).
          for size in ["2560x1440", "800x600"]:
              w, h = (int(v) for v in size.split("x"))
              r = rfb("--password", pw, "--resize", size)
              print(f"RFB resize to {size}:", json.dumps(r["resize"]))
              assert r["resize"]["result"] == 0 and (r["resize"]["w"], r["resize"]["h"]) == (w, h), r["resize"]
              assert r["resize"]["update_raw_bytes"] == w * h * 4, r["resize"]
              assert screen_size() == size, screen_size()
              xr = guest.succeed("xrandr -display :99 | head -1")
              assert f"current {w} x {h}," in xr, xr  # xrandr spaces it: "current 2560 x 1440,"
              # openbox re-maximises the browser to the new screen.
              guest.wait_until_succeeds(f"export DISPLAY=:99; xdotool search --onlyvisible --classname '^[Cc]hromium' | head -1 | xargs -I W xdotool getwindowgeometry --shell W | grep -q 'WIDTH={w}'", timeout=30)
              geo = browser_window()
              print(f"MEASURE browser window after resize to {size}: {geo}")
              assert geo == size, geo
              guest.sleep(2)
              guest.succeed(f"DISPLAY=:99 import -window root /tmp/screen-{size}.png")
              guest.copy_from_machine(f"/tmp/screen-{size}.png", "")
              share = magenta_share(f"/tmp/screen-{size}.png")
              corner = magenta_share(f"/tmp/screen-{size}.png", f"-crop 20x20+{w - 20}+{h - 20} +repage")
              print(f"magenta share of the {size} screen: {share:.3f}, bottom-right corner {corner:.3f}")
              assert share > 0.7 and corner > 0.95, (share, corner)
          rfb("--password", pw, "--resize", "1440x900")
          assert screen_size() == "1440x900", screen_size()

      with subtest("stopping the desktop stops the viewer, not the agents' browser or the display"):
          guest.succeed("sudo -u dev repose-guest-profile desktop stop")
          for u in ["repose-novnc", "repose-novnc-proxy", "repose-vncconfig"]:
              guest.wait_until_fails(f"systemctl is-active {u}.service")
          for u in ["repose-browser", "repose-xvnc"]:
              guest.succeed(f"systemctl is-active {u}.service")
          prof = guest.succeed("sudo -u dev repose-guest-profile desktop status").strip()
          assert prof == "stopped", prof

      # `repose browser bridge` (DECISIONS I-296): the laptop's Chrome at
      # the tunnel's end is a headless Chromium on 9226, where sshd's
      # reverse-forward listener would be. The MCP servers keep their one
      # endpoint; what changes is which browser answers it.
      def bridge(op):
          return guest.succeed(f"sudo -u dev repose-guest-profile browser bridge {op}").strip()

      def tabs(port):
          return guest.succeed(f"curl -sf http://127.0.0.1:{port}/json/list")

      with subtest("browser bridge: the endpoint switches to the laptop's Chrome, and back"):
          guest.succeed("systemd-run --unit laptop-chrome -p User=dev -p Environment=HOME=/home/dev ${pkgs.chromium}/bin/chromium --headless --user-data-dir=/tmp/laptop-chrome --remote-debugging-port=9226 --no-first-run about:blank")
          guest.wait_for_open_port(9226)
          assert bridge("status") == "off"
          bridge("start")
          assert bridge("status") == "on"
          guest.succeed("systemctl is-active repose-browser-bridge.socket")
          guest.fail("systemctl is-active repose-browser.socket")
          out = mcp("playwright", ("browser_navigate", {"url": page + "?bridged"}))
          assert "?bridged" in out, out
          out = mcp("chrome-devtools", ("list_pages", {}))
          assert "?bridged" in out and "?after" not in out, out
          # The laptop's browser has the new tab; the machine's still has its own.
          assert "?bridged" in tabs(9226) and "?bridged" not in tabs(9225), (tabs(9226), tabs(9225))
          bridge("stop")
          assert bridge("status") == "off"
          guest.succeed("systemctl is-active repose-browser.socket")
          guest.fail("systemctl is-active repose-browser-bridge.socket")
          out = mcp("playwright", ("browser_tabs", {"action": "list"}))
          assert "?after" in out and "?bridged" not in out, out

      with subtest("browser bridge: a running MCP server follows the switch on its next call"):
          out = mcp("playwright",
                    ("browser_tabs", {"action": "list"}),
                    ("!sh", {"cmd": "sudo -u dev repose-guest-profile browser bridge start"}),
                    ("browser_tabs", {"action": "list"}),
                    ("!sh", {"cmd": "sudo -u dev repose-guest-profile browser bridge stop"}),
                    ("browser_tabs", {"action": "list"}))
          blocks = out.split("=== browser_tabs")[1:]
          assert len(blocks) == 3, out
          assert "?after" in blocks[0] and "?bridged" not in blocks[0], blocks[0]
          assert "?bridged" in blocks[1] and "?after" not in blocks[1], blocks[1]
          assert "?after" in blocks[2] and "?bridged" not in blocks[2], blocks[2]

      with subtest("browser bridge: hold lasts as long as its stdin, then switches back"):
          guest.succeed("systemd-run --unit hold1 -p User=dev -p Environment=HOME=/home/dev bash -lc 'sleep 4 | repose-guest-profile browser bridge hold > /tmp/hold.out'")
          guest.wait_until_succeeds("grep -q on /tmp/hold.out", timeout=20)
          assert bridge("status") == "on"
          guest.wait_until_succeeds("test \"$(sudo -u dev repose-guest-profile browser bridge status)\" = off", timeout=30)

      with subtest("browser bridge: hold ends when the tunnel's listener is gone"):
          guest.succeed("systemd-run --unit hold2 -p User=dev -p Environment=HOME=/home/dev bash -lc 'sleep 60 | repose-guest-profile browser bridge hold > /tmp/hold2.out'")
          guest.wait_until_succeeds("grep -q on /tmp/hold2.out", timeout=20)
          guest.succeed("systemctl stop laptop-chrome.service")
          guest.wait_until_succeeds("test \"$(sudo -u dev repose-guest-profile browser bridge status)\" = off", timeout=30)
          guest.succeed("systemctl stop hold2.service 2>/dev/null || true")

      with subtest("browser bridge: the idle check switches back a bridge whose tunnel is gone"):
          bridge("start")
          assert bridge("status") == "on"
          guest.succeed("systemctl start repose-desktop-idle-check.service")
          assert bridge("status") == "off"
          guest.succeed("systemctl is-active repose-browser.socket")
          out = mcp("chrome-devtools", ("list_pages", {}))
          assert "?after" in out, out

      with subtest("the idle check stops the unused browser, and the display follows"):
          guest.succeed("touch -d '-31 minutes' /run/repose/desktop/last-client /run/repose/desktop/last-cdp")
          guest.succeed("systemctl start repose-desktop-idle-check.service")
          for u in ["repose-browser", "repose-xvnc", "repose-openbox"]:
              guest.wait_until_fails(f"systemctl is-active {u}.service")
          guest.succeed("systemctl is-active repose-browser.socket repose-novnc.socket")
          disp = guest.succeed("sudo -u dev bash -lc 'echo -n $DISPLAY'")
          assert disp == "", disp

      with subtest("desktop start starts the browser with the boot's password; the viewer's idle stop leaves a used browser"):
          pw2 = guest.succeed("sudo -u dev repose-guest-profile desktop start").strip().splitlines()[-1]
          assert pw2 == pw, (pw, pw2)
          guest.succeed("systemctl is-active repose-browser.service repose-novnc.service repose-xvnc.service")
          # The same link still opens the desktop after the display was
          # stopped and started again.
          r = rfb("--password", pw)
          assert r["auth"] == "ok" and r["first_update"]["raw_bytes"] == 1440 * 900 * 4, r
          guest.succeed("touch -d '-31 minutes' /run/repose/desktop/last-client")
          guest.succeed("systemctl start repose-desktop-idle-check.service")
          guest.fail("systemctl is-active repose-novnc.service")
          guest.succeed("systemctl is-active repose-browser.service repose-xvnc.service")
          guest.succeed("systemctl stop repose-browser.service")
          guest.wait_until_fails("systemctl is-active repose-xvnc.service")

      with subtest("a guest's old headless registration gives way; the user's own entries stay"):
          guest.succeed("""cat > /home/dev/.claude.json <<'EOF'
      {"mcpServers": {
        "playwright": {"type": "stdio", "command": "playwright-mcp", "args": ["--headless"]},
        "chrome-devtools": {"type": "stdio", "command": "chrome-devtools-mcp", "args": ["--headless", "--isolated"]},
        "mine": {"type": "stdio", "command": "my-mcp", "args": []}
      }, "other": 1}
      EOF
      chown dev:dev /home/dev/.claude.json""")
          guest.succeed("sudo -u dev repose-agent-setup claude")
          u = json.loads(guest.succeed("cat /home/dev/.claude.json"))
          assert u["mcpServers"]["playwright"] == reg["playwright"], u
          assert u["mcpServers"]["chrome-devtools"]["args"] == ["--headless", "--isolated"], u
          assert u["mcpServers"]["mine"]["command"] == "my-mcp", u
          assert u["other"] == 1, u
          guest.succeed("sudo -u dev repose-agent-setup claude")
          assert json.loads(guest.succeed("cat /home/dev/.claude.json")) == u

      with subtest("headless chromium renders a page"):
          guest.succeed("sudo -u dev bash -lc 'cd /home/dev && chromium --headless --disable-gpu --no-first-run --screenshot=/home/dev/a.png --window-size=800,600 file:///home/dev/site/magenta.html' 2>&1 | tail -5")
          size = int(guest.succeed("stat -c %s /home/dev/a.png").strip())
          assert size > 2000, size
          guest.copy_from_machine("/home/dev/a.png", "")

      with subtest("MCP servers run from the packaged versions, offline"):
          out = guest.succeed("sudo -u dev bash -lc 'playwright-mcp --version'")
          assert out.strip(), out
          out = guest.succeed("sudo -u dev bash -lc 'chrome-devtools-mcp --version'")
          assert out.strip(), out
          # playwright's browsers are the packaged ones, linked into the
          # writable directory at boot (I-228), not a download.
          guest.succeed("sudo -u dev bash -lc 'test -d \"$PLAYWRIGHT_BROWSERS_PATH\" && ls \"$PLAYWRIGHT_BROWSERS_PATH\" | grep -q chromium'")
    '';
  };

  # I-243: the machine guide is installed where each of the five agents
  # reads global instructions, each agent really sends it to its model
  # (a stand-in API records the first request), the user's own instruction
  # files are left byte for byte, and every command the guide names is on
  # the machine. A stand-in repose-notify shows a `needs:` line rendered
  # when its command exists; one whose command is missing is dropped.
  guest-agent-guide = mkTest "guest-agent-guide" {
    nodes.guest = { ... }: {
      imports = [ node ];
      environment.systemPackages = [ (pkgs.writeShellScriptBin "repose-notify" "exit 0") ];
    };
    testScript = ''
      import json
      import re
      import shlex
      import tomllib

      guest.start()
      guest.wait_for_unit("multi-user.target")

      def dev(cmd, env=""):
          return guest.succeed(f"sudo -H -u dev {env} bash -lc {shlex.quote(cmd)}")

      def has(cmd):
          return guest.execute(f"sudo -H -u dev bash -lc {shlex.quote('command -v ' + cmd)}")[0] == 0

      source = open("${../base/agent-guide.md}").read()
      commands = [l.split() for l in open("${../base/agent-guide.commands}") if l.strip() and not l.startswith("#")]

      with subtest("rendered from the source: comments dropped, needs lines follow the guest"):
          guide = guest.succeed("cat /etc/repose/agent-guide.md")
          out, in_comment = [], False
          for line in source.splitlines():
              if in_comment:
                  in_comment = "-->" not in line
                  continue
              if line.startswith("<!--") and "-->" not in line:
                  in_comment = True
                  continue
              m = re.search(r"<!-- needs: ([A-Za-z0-9._-]+) -->", line)
              if m and not has(m.group(1)):
                  continue
              out.append(re.sub(r"\s*<!--.*?-->", "", line).rstrip())
          while out and out[0] == "":
              out.pop(0)
          assert guide == "\n".join(out) + "\n", guide
          assert "<!--" not in guide, guide
          assert "`repose-notify " in guide, "a needs line whose command exists is rendered"
          for c in [c[0] for c in commands if c[1:] == ["needs"] and not has(c[0])]:
              assert f"`{c} " not in guide, f"{c} is missing but the guide tells agents to run it"

      with subtest("installed where each agent reads it"):
          assert guest.succeed("cat /etc/claude-code/CLAUDE.md") == guide
          assert guest.succeed("cat /etc/repose/gemini-extension/GEMINI.md") == guide
          codex = tomllib.loads(guest.succeed("cat /etc/codex/config.toml"))
          assert codex == {"developer_instructions": guide}, codex
          oc = json.loads(guest.succeed("cat /etc/opencode/opencode.json"))
          assert oc["instructions"] == ["/etc/repose/agent-guide.md"], oc
          ext = json.loads(guest.succeed("cat /etc/repose/gemini-extension/gemini-extension.json"))
          assert ext["contextFileName"] == "GEMINI.md", ext
          guest.succeed("grep -q /etc/repose/agent-guide.md /etc/repose/pi-extension.js")

      with subtest("every command the guide names is on the machine"):
          for c in commands:
              if c[1:] != ["needs"]:
                  dev(f"command -v {c[0]}")

      with subtest("each agent sends the guide and the user's own instructions"):
          guest.succeed("systemd-run --unit capture-llm ${pkgs.python3}/bin/python3 ${./capture-llm.py} 18777 /tmp/caps")
          guest.wait_until_succeeds("curl -s -o /dev/null http://127.0.0.1:18777/")
          files = {
              ".claude/CLAUDE.md": "USER-CLAUDE-MARK",
              ".codex/AGENTS.md": "USER-CODEX-MARK",
              ".config/opencode/AGENTS.md": "USER-OPENCODE-MARK",
              ".gemini/GEMINI.md": "USER-GEMINI-MARK",
              ".pi/agent/AGENTS.md": "USER-PI-MARK",
          }
          for f, mark in files.items():
              dev(f"mkdir -p $(dirname ~/{f}) && printf '# mine\\n{mark}\\n' > ~/{f}")
          dev("""printf 'model_provider = "fake"\\n[model_providers.fake]\\nname = "fake"\\nbase_url = "http://127.0.0.1:18777/v1"\\nenv_key = "FAKE_KEY"\\nwire_api = "responses"\\n' > ~/.codex/config.toml""")
          dev("""echo '{"provider":{"fake":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://127.0.0.1:18777/v1","apiKey":"x"},"models":{"m":{}}}},"model":"fake/m"}' > ~/.config/opencode/opencode.json""")
          dev("""echo '{"security":{"auth":{"selectedType":"gemini-api-key"}}}' > ~/.gemini/settings.json""")
          dev("""echo '{"providers":{"fake":{"baseUrl":"http://127.0.0.1:18777/v1","api":"openai-completions","apiKey":"x","models":[{"id":"m"}]}}}' > ~/.pi/agent/models.json""")
          dev("mkdir -p ~/proj")
          before = dev("cd ~ && sha256sum " + " ".join(files))
          runs = {
              "claude": ("USER-CLAUDE-MARK", "ANTHROPIC_BASE_URL=http://127.0.0.1:18777 ANTHROPIC_API_KEY=sk-x CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "claude -p hi --max-turns 1"),
              "codex": ("USER-CODEX-MARK", "FAKE_KEY=x", "codex exec --skip-git-repo-check hi"),
              "opencode": ("USER-OPENCODE-MARK", "OPENCODE_DISABLE_MODELS_FETCH=1 OPENCODE_DISABLE_AUTOUPDATE=1", "opencode run hi"),
              "gemini": ("USER-GEMINI-MARK", "GEMINI_CLI_TRUST_WORKSPACE=true GEMINI_API_KEY=x GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:18777", "gemini -p hi"),
              "pi": ("USER-PI-MARK", "PI_OFFLINE=1", "pi --provider fake --model m -p hi"),
          }
          sentinel = "This is a repose machine"
          for agent, (mark, env, cmd) in runs.items():
              guest.succeed("rm -rf /tmp/caps/*")
              print(guest.execute(f"sudo -H -u dev {env} bash -lc {shlex.quote('cd ~/proj && timeout 180 ' + cmd)} 2>&1 | tail -5")[1])
              bodies = guest.succeed("cat /tmp/caps/* 2>/dev/null || true")
              assert sentinel in bodies, f"{agent}: the guide is not in what it sent: {bodies[:3000]}"
              assert mark in bodies, f"{agent}: the user's own instructions are not in what it sent"
          after = dev("cd ~ && sha256sum " + " ".join(files))
          assert before == after, (before, after)

      with subtest("gemini and pi links: idempotent, a user's file at the path is left alone"):
          assert dev("readlink ~/.gemini/extensions/repose-machine-guide").strip() == "/etc/repose/gemini-extension"
          assert dev("readlink ~/.pi/agent/extensions/repose-machine-guide.js").strip() == "/etc/repose/pi-extension.js"
          dev("mkdir -p ~/.gemini/extensions/mine && echo '{}' > ~/.gemini/extensions/mine/gemini-extension.json")
          listing = dev("ls -la ~/.gemini/extensions ~/.pi/agent/extensions")
          for _ in range(2):
              dev("repose-agent-setup gemini && repose-agent-setup pi")
          assert dev("ls -la ~/.gemini/extensions ~/.pi/agent/extensions") == listing
          # a stale link of ours is repointed
          dev("ln -sfn /nonexistent ~/.pi/agent/extensions/repose-machine-guide.js && repose-agent-setup pi")
          assert dev("readlink ~/.pi/agent/extensions/repose-machine-guide.js").strip() == "/etc/repose/pi-extension.js"
          # something that is not a link stays
          dev("mkdir -p /tmp/h2/.pi/agent/extensions && echo mine > /tmp/h2/.pi/agent/extensions/repose-machine-guide.js")
          dev("HOME=/tmp/h2 repose-agent-setup pi")
          assert dev("cat /tmp/h2/.pi/agent/extensions/repose-machine-guide.js").strip() == "mine"
    '';
  };

  # I-259: an agent started the repose way (tmux new-window <agent> from
  # an SSH command) runs inside the checkout's dev environment: its
  # .envrc, allowed for it when never allowed on this machine and left out
  # when denied, or the flake's dev shell when there is no .envrc. A
  # worktree window gets the same; a broken flake still starts the agent
  # with a message; a slow load marks the pane for `repose run`.
  #
  # I-488: the fragment's home.sessionVariables and home.sessionPath reach
  # the agent, the commands it runs, an SSH command and an interactive
  # shell; and the user's own shell in the checkout (the tmux shell window)
  # loads the flake's dev shell the agent got.
  guest-devshell = mkTest "guest-devshell" {
    nodes.guest = { lib, ... }: {
      imports = [ node homeManagerModule ../contract.nix ];
      home-manager.useGlobalPkgs = true;
      home-manager.useUserPackages = true;
      home-manager.users.dev = {
        home.username = "dev";
        home.homeDirectory = "/home/dev";
        home.stateVersion = "26.11";
      };
      # contract.nix appends the fragment's overlays (none here) to
      # nixpkgs.overlays, which the driver's shared, read-only pkgs refuse.
      nixpkgs.overlays = lib.mkForce [ ];
      repose.fragment = {
        home.sessionVariables.REPOSE_FRAG_PROBE = "$HOME/frag";
        home.sessionPath = [ "$HOME/frag-bin" ];
      };
      environment.systemPackages = [ devshellProbeAgent ];
      virtualisation.additionalPaths = [ flakeTool pkgs.bash pkgs.coreutils ];
      nix.settings.sandbox = lib.mkForce false;
      nix.settings.substituters = lib.mkForce [ ];
    };
    testScript = ''
      import shlex

      guest.start()
      guest.wait_for_unit("multi-user.target")

      def dev(cmd):
          return guest.succeed(f"sudo -H -u dev bash -lc {shlex.quote(cmd)}")

      guest.succeed("printf 'TZ=UTC\\nREPOSE_PROJECT=todo-app\\n' > /etc/repose/env")
      guest.succeed("install -d -o dev -g dev -m 0700 /home/dev/.repose")
      guest.succeed("""echo '{"project_id":"0192e4b0-0000-7000-8000-000000000001","slug":"todo-app","name":"todo-app","tz":"UTC","class":"large"}' > /home/dev/.repose/project.json && chown dev:dev /home/dev/.repose/project.json""")
      guest.succeed("sudo -u dev XDG_RUNTIME_DIR=/run/user/1000 systemctl --user start repose-tmux-session.service")
      guest.wait_until_succeeds("sudo -H -u dev tmux ls | grep -q '^todo-app:'", timeout=60)
      guest.succeed("ssh-keygen -q -t ed25519 -N ''' -f /root/ca && ssh-keygen -q -t ed25519 -N ''' -f /root/user")
      guest.succeed("install -m 0644 /root/ca.pub /run/repose/user_ca.pub")
      guest.succeed("echo 0192e4b0-0000-7000-8000-000000000001 > /etc/ssh/principals/dev && systemctl reload sshd")
      guest.succeed("ssh-keygen -q -s /root/ca -I 'user:t' -n 0192e4b0-0000-7000-8000-000000000001 -V -1m:+12h /root/user.pub")

      def ssh(cmd):
          return guest.succeed("ssh -n -F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o CertificateFile=/root/user-cert.pub -i /root/user dev@127.0.0.1 " + shlex.quote(cmd))

      # How startAgentWindow starts an agent.
      def launch(window, d):
          tag = d.rsplit("/", 1)[-1]
          guest.succeed(f"rm -f /tmp/out-{tag}")
          ssh(f"tmux new-window -t todo-app -n {window} -c {d} -d repose-devshell-probe")
          out = guest.wait_until_succeeds(f"grep -q done /tmp/out-{tag} && cat /tmp/out-{tag}", timeout=180)
          pane = guest.succeed(f"sudo -H -u dev tmux capture-pane -p -J -t todo-app:{window}")
          print(f"{window} in {d}:\n{out}\npane:\n{pane}")
          return out, pane

      def git_repo(d, files):
          dev(f"mkdir -p {d} && cd {d} && git init -q -b main . && git add {files} && git -c user.email=t@t -c user.name=t commit -q -m init")

      dev("cp ${devshellFlake} ~/todo-app/flake.nix && chmod 644 ~/todo-app/flake.nix")
      git_repo("~/todo-app", "flake.nix")

      dev("mkdir -p ~/frag-bin && printf '#!/bin/sh\\necho frag-tool-ok\\n' > ~/frag-bin/frag-tool && chmod +x ~/frag-bin/frag-tool")

      with subtest("the fragment's session variables reach SSH commands and login shells"):
          assert ssh("echo $REPOSE_FRAG_PROBE").strip() == "/home/dev/frag"
          assert ssh("command -v frag-tool").strip() == "/home/dev/frag-bin/frag-tool"
          assert dev("echo $REPOSE_FRAG_PROBE").strip() == "/home/dev/frag"
          assert "REPOSE_FRAG_PROBE" in guest.succeed("cat /etc/repose/session-vars.names")

      with subtest("a flake without .envrc: the agent runs in its dev shell"):
          ssh("tmux new-window -t todo-app -n raw -c ~/todo-app -d 'sh -c \"echo flake=$REPOSE_FLAKE_PROBE; command -v flake-tool; echo done\" > /tmp/out-raw 2>&1'")
          raw = guest.wait_until_succeeds("grep -q done /tmp/out-raw && cat /tmp/out-raw", timeout=30)
          print("a bare command in a new window: " + raw)
          assert "from-the-flake" not in raw, raw
          out, pane = launch("bare", "/home/dev/todo-app")
          assert "flake=from-the-flake" in out, out
          assert "tool=flake-tool-ok" in out, out
          assert "project=todo-app" in out, out
          assert "repose: loading the dev shell from /home/dev/todo-app/flake.nix" in pane, pane
          # The generated .envrc lives outside the checkout, which stays clean.
          guest.succeed("test ! -e /home/dev/todo-app/.envrc && test ! -e /home/dev/todo-app/.direnv")
          shadow_envrc = dev("cat ~/.cache/repose/devshell/*/.envrc")
          assert "use flake /home/dev/todo-app" in shadow_envrc
          # No flake.lock in the repository: Nix's lock goes next to the
          # generated .envrc, never into the checkout (I-483).
          assert "--output-lock-file /home/dev/.cache/repose/devshell/" in shadow_envrc, shadow_envrc
          guest.succeed("test ! -e /home/dev/todo-app/flake.lock")
          assert dev("cd ~/todo-app && git status --porcelain").strip() == ""
          # I-488: the agent window and the bash its commands run in have
          # the fragment's variable, expanded, and its sessionPath.
          assert "frag=/home/dev/frag\n" in out, out
          assert "fragchild=/home/dev/frag\n/home/dev/frag-bin/frag-tool" in out, out

      # I-488: the user's own interactive shell, here the tmux shell window
      # opened in the checkout, loads the same dev shell from its cache,
      # has the fragment's variable, and unloads the dev shell on leaving.
      with subtest("an interactive shell in the checkout gets the flake's dev shell"):
          ssh("tmux new-window -t todo-app -n mine -c ~/todo-app")
          guest.wait_until_succeeds("sudo -H -u dev tmux capture-pane -p -t todo-app:mine | grep -q 'repose: loading the dev shell from /home/dev/todo-app/flake.nix'", timeout=60)
          def in_shell(cmd, tag):
              guest.succeed(f"rm -f /tmp/{tag}")
              guest.succeed("sudo -H -u dev tmux send-keys -t todo-app:mine " + shlex.quote(f"{{ {cmd}; }} > /tmp/{tag}.tmp 2>&1; mv /tmp/{tag}.tmp /tmp/{tag}") + " Enter")
              return guest.wait_until_succeeds(f"cat /tmp/{tag}", timeout=60)
          got = in_shell("echo flake=$REPOSE_FLAKE_PROBE frag=$REPOSE_FRAG_PROBE; flake-tool; command -v frag-tool", "shell-in")
          print("interactive shell in the checkout: " + got)
          assert "flake=from-the-flake frag=/home/dev/frag" in got, got
          assert "flake-tool-ok" in got and "/home/dev/frag-bin/frag-tool" in got, got
          # Leaving the folder unloads it, as direnv does for an .envrc.
          in_shell("cd /tmp", "shell-cd")
          got = in_shell("echo flake=$REPOSE_FLAKE_PROBE frag=$REPOSE_FRAG_PROBE; command -v flake-tool || echo no-flake-tool", "shell-out")
          print("after cd /tmp: " + got)
          assert "flake= frag=/home/dev/frag" in got and "no-flake-tool" in got, got
          pane = guest.succeed("sudo -H -u dev tmux capture-pane -p -J -t todo-app:mine")
          assert "direnv: unloading" in pane, pane
          guest.succeed("sudo -H -u dev tmux kill-window -t todo-app:mine")

      # I-275: `repose exec todo-app -- sh -c '...'` sends this command line
      # (internal/cli/testdata/exec-script.sh, which TestExecScriptGolden
      # holds to the CLI's execScript); it gets what the agent got.
      with subtest("repose exec runs its command in the agent's dev environment"):
          guest.succeed("test -r /etc/repose/devshell.sh")
          out = ssh(open("${../../../internal/cli/testdata/exec-script.sh}").read())
          print("repose exec: " + out)
          assert "flake=from-the-flake" in out, out
          assert "project=todo-app" in out, out
          assert "pwd=/home/dev/todo-app" in out, out
          assert "flake-tool-ok" in out, out
          # A dev shell that loads from its cache says nothing: stderr is
          # the user's command's own (found live on v0.1.18: six lines of
          # direnv on every exec).
          err = ssh("{ " + open("${../../../internal/cli/testdata/exec-script.sh}").read() + "\n} 2>&1 >/dev/null")
          print("repose exec stderr, cached: " + repr(err))
          assert "repose:" not in err and "direnv:" not in err, err

      with subtest("an .envrc never allowed here is allowed, and the agent gets all of it"):
          dev("cd ~/todo-app && printf 'use flake\\nexport ENVRC_ONLY=yes\\n' > .envrc && git add .envrc && git -c user.email=t@t -c user.name=t commit -q -m envrc")
          assert '"allowed": 1' in dev("cd ~/todo-app && direnv status --json")
          out, pane = launch("envrc", "/home/dev/todo-app")
          assert "flake=from-the-flake" in out and "tool=flake-tool-ok" in out, out
          assert "envrc=yes" in out, out
          assert "repose: allowed /home/dev/todo-app/.envrc" in pane, pane
          assert '"allowed": 0' in dev("cd ~/todo-app && direnv status --json")

      with subtest("a --worktree window, in another directory, gets the same environment"):
          dev("cd ~/todo-app && git worktree add -q -b repose/probe-2 ~/todo-app-probe-2")
          out, pane = launch("probe-2", "/home/dev/todo-app-probe-2")
          assert "flake=from-the-flake" in out and "envrc=yes" in out, out
          assert "repose: allowed /home/dev/todo-app-probe-2/.envrc" in pane, pane

      with subtest("a denied .envrc is left out and the agent still starts"):
          dev("direnv deny ~/todo-app")
          out, pane = launch("denied", "/home/dev/todo-app")
          assert "project=todo-app" in out, out
          assert "flake=\n" in out and "envrc=\n" in out, out
          assert "/home/dev/todo-app/.envrc is denied" in pane, pane

      with subtest("a broken flake: the agent starts without it and the pane says why"):
          dev("mkdir -p ~/broken && printf '{ outputs = { self }: { devShells = ; }; }\\n' > ~/broken/flake.nix")
          git_repo("~/broken", "flake.nix")
          out, pane = launch("broken", "/home/dev/broken")
          assert "project=todo-app" in out and "flake=\n" in out, out
          assert "repose: the dev shell from /home/dev/broken/flake.nix did not load" in pane, pane

      with subtest("the pane is marked while the environment loads, for repose run"):
          dev("mkdir -p ~/slow && printf 'sleep 8\\nexport ENVRC_ONLY=slow\\n' > ~/slow/.envrc")
          guest.succeed("rm -f /tmp/out-slow")
          ssh("tmux new-window -t todo-app -n slow -c ~/slow -d repose-devshell-probe")
          guest.wait_until_succeeds("sudo -H -u dev tmux show-options -p -v -t todo-app:slow @repose-devshell | grep -qx loading", timeout=30)
          out = guest.wait_until_succeeds("grep -q done /tmp/out-slow && cat /tmp/out-slow", timeout=60)
          assert "envrc=slow" in out, out
          assert guest.succeed("sudo -H -u dev tmux show-options -p -v -t todo-app:slow @repose-devshell || true").strip() == ""

      with subtest("an agent started from a shell that has the environment loads nothing again"):
          guest.succeed("rm -f /tmp/out-slow")
          out = dev("cd ~/slow && eval \"$(direnv export bash 2>/dev/null)\" && timeout 5 repose-devshell-probe 2>&1 || true")
          assert "loading the dev shell" not in out, out
          assert "envrc=slow" in guest.succeed("cat /tmp/out-slow")
    '';
  };

  # I-278: the Claude login share. The test VM has no virtio-fs device, so
  # a tmpfs mounted at the share's mount point before the unit runs stands
  # in for the host's claude-auth tag (the unit then skips its own mount).
  # The real tag was checked on host-01 (experiment B and the production
  # virtiofsd shape, docs/proposals/2026-09-24-claude-login-shared-folder.md).
  guest-claude-auth = mkTest "guest-claude-auth" {
    nodes.guest = { pkgs, ... }: {
      imports = [ node ];
      systemd.services.test-fake-auth-share = {
        wantedBy = [ "repose-claude-auth.service" ];
        before = [ "repose-claude-auth.service" ];
        unitConfig.DefaultDependencies = false;
        serviceConfig = { Type = "oneshot"; RemainAfterExit = true; };
        path = [ pkgs.util-linux ];
        script = "mkdir -p /run/repose/claude-auth && mount -t tmpfs -o mode=0700,uid=1000,gid=1000 fake /run/repose/claude-auth";
      };
    };
    testScript = ''
      import json
      import shlex

      guest.start()
      guest.wait_for_unit("multi-user.target")
      guest.wait_for_unit("repose-claude-auth.service")
      print(guest.succeed("systemctl status --no-pager test-fake-auth-share repose-claude-auth || true"))
      target = "/home/dev/.claude/.credentials.json"
      share = "/run/repose/claude-auth"

      def dev(cmd):
          return guest.succeed(f"sudo -H -u dev sh -c {shlex.quote(cmd)}")

      with subtest("one file bind-mounted, owned by dev, 0600"):
          guest.succeed(f"findmnt -n --mountpoint {target}")
          assert guest.succeed(f"stat -c '%U %a' {share}/.credentials.json").strip() == "dev 600"
          assert guest.succeed(f"ls -A {share}").split() == [".credentials.json"]

      with subtest("Claude Code's write (temp file, rename refused, rewrite in place) lands in the share"):
          dev(f"echo '{{\"claudeAiOauth\":{{\"expiresAt\":1}}}}' > {target}.tmp.x")
          code, out = guest.execute(f"sudo -H -u dev mv -T {target}.tmp.x {target} 2>&1")
          assert code != 0 and "busy" in out, out
          dev(f"echo '{{\"claudeAiOauth\":{{\"expiresAt\":2}}}}' > {target} && rm {target}.tmp.x")
          assert json.loads(guest.succeed(f"cat {share}/.credentials.json"))["claudeAiOauth"]["expiresAt"] == 2

      with subtest("the rest of ~/.claude stays in the machine"):
          dev("repose-agent-setup claude")
          guest.succeed("test -s /home/dev/.claude/settings.json")
          assert guest.succeed(f"ls -A {share}").split() == [".credentials.json"]
          guest.fail("findmnt -n --mountpoint /home/dev/.claude")
          guest.fail("findmnt -n --mountpoint /home/dev/.claude/settings.json")

      with subtest("onboarding is marked done where the share is mounted, and a user value wins"):
          assert json.loads(guest.succeed("cat /home/dev/.claude.json"))["hasCompletedOnboarding"] is True
          guest.succeed("sudo -H -u dev sh -c 'jq \".hasCompletedOnboarding=false\" ~/.claude.json > /tmp/cj && cat /tmp/cj > ~/.claude.json'")
          dev("repose-agent-setup claude")
          assert json.loads(guest.succeed("cat /home/dev/.claude.json"))["hasCompletedOnboarding"] is False

      with subtest("idempotent: a restart adds no second mount"):
          guest.succeed("systemctl restart repose-claude-auth && systemctl restart repose-claude-auth")
          assert guest.succeed(f"findmnt -n --mountpoint {target} | wc -l").strip() == "1"

      with subtest("no share on the host: the machine keeps its own login"):
          guest.succeed(f"umount {target} && umount {share}")
          guest.succeed("systemctl restart repose-claude-auth")
          guest.fail(f"findmnt -n --mountpoint {target}")
          guest.succeed(f"test -f {target}")
          out = guest.succeed("journalctl -u repose-claude-auth -b --no-pager")
          assert "keeps its login in this machine" in out, out
          guest.succeed("rm -f /home/dev/.claude.json && sudo -H -u dev repose-agent-setup claude")
          assert "hasCompletedOnboarding" not in guest.succeed("cat /home/dev/.claude.json")
    '';
  };
}
