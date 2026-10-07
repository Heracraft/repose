# The guestd VM test of docs/workstreams/04-guestd.md §7: the real binary,
# inside a real NixOS guest, exercised over the protocol it serves.
#
# Two guestd instances run. The first serves the VM's own root and is what the
# secrets, principals, tmux and hook checks talk to. The second serves a
# loop-mounted ext4 under --root, and is where freeze and grow are exercised:
# FIFREEZE on the VM's own root would block the test driver's own writes, and a
# loop device is a block device that can actually be resized from inside.
{ pkgs, guestdPackage, hookPackage }:

let
  # A real compiled binary named claude, so a tmux window named "claude" has a
  # process tree whose comm is "claude" (docs/workstreams/04-guestd.md §5).
  # It has to be compiled rather than a copy of `sleep` or a shell script:
  # coreutils may be a multi-call binary that dispatches on argv[0], and a
  # script's comm is its interpreter's.
  fakeClaude = pkgs.runCommand "fake-claude" { nativeBuildInputs = [ pkgs.stdenv.cc ]; } ''
    mkdir -p $out/bin
    cat > claude.c <<'SRC'
    #include <unistd.h>
    int main(void) { for (;;) { sleep(60); } return 0; }
SRC
    $CC -O0 -o $out/bin/claude claude.c
  '';

  devUID = 1000;

  # An agent's shell parent (DECISIONS I-475): a bash that loaded the
  # secrets when it started, waits, then runs `bash -c printenv` children
  # the way an agent runs each command.
  agentParent = pkgs.writeShellScript "i475-agent-parent" ''
    printf '%s %s\n' "''${OLD_ONE-<unset>}" "''${NEW_ONE-<unset>}" > /tmp/i475.parent
    # What the project's .envrc does after the profile: its own value.
    export PLAIN=from-envrc
    while [ ! -e /tmp/i475.go ]; do ${pkgs.coreutils}/bin/sleep 0.1; done
    ${pkgs.bash}/bin/bash -c '${pkgs.coreutils}/bin/printenv NEW_ONE' > /tmp/i475.new || :
    if ${pkgs.bash}/bin/bash -c '${pkgs.coreutils}/bin/printenv OLD_ONE' > /tmp/i475.old; then
      echo present > /tmp/i475.oldstate
    else
      echo gone > /tmp/i475.oldstate
    fi
    ${pkgs.bash}/bin/bash -c 'echo "$PLAIN $ROT"' > /tmp/i475.kept
    : > /tmp/i475.done
  '';
in
pkgs.testers.runNixOSTest {
  name = "guestd";

  nodes.guest = { config, pkgs, lib, ... }: {
    virtualisation.memorySize = 4096;
    virtualisation.cores = 4;
    virtualisation.diskSize = 4096;

    # guest-conventions.md gives the hook socket group `dev`, so the guest
    # declares that group; guestd resolves it at runtime either way.
    users.groups.dev.gid = devUID;
    users.users.dev = {
      isNormalUser = true;
      uid = devUID;
      group = "dev";
      extraGroups = [ "wheel" ];
    };
    security.sudo.wheelNeedsPassword = false;
    # The NixOS test framework panics the VM on any OOM so that a test
    # cannot pass by accident; the I-200 subtest needs the OOM killer to
    # run as it does in a real guest, which does not set this.
    boot.kernel.sysctl."vm.panic_on_oom" = lib.mkForce 0;
    users.users.dev.linger = true;

    programs.tmux.enable = true;

    services.openssh = {
      enable = true;
      settings = {
        PasswordAuthentication = false;
        PermitRootLogin = "no";
        AuthorizedPrincipalsFile = "/etc/ssh/principals/%u";
        TrustedUserCAKeys = "/run/repose/user_ca.pub";
      };
    };

    # What guestd shells out to, plus what the test script needs. The real
    # guest gets these from nix/guest/base/tools.nix (02); the test names them
    # so that a missing one fails here rather than on a tenant's guest.
    environment.systemPackages = with pkgs; [
      guestdPackage hookPackage fakeClaude
      git tmux openssh e2fsprogs util-linux procps
      jq curl coreutils python3
    ];

    # /run/repose and its secrets tmpfs, as nix/guest/base/guestd.nix (02)
    # declares them.
    # The BASH_ENV loader as nix/guest/base/env.nix installs it (I-475).
    environment.etc."repose/bash-env.sh".source = ../base/bash-env.sh;

    systemd.tmpfiles.rules = [
      "d /run/repose 0755 root root -"
      "d /run/repose/secrets 0700 dev dev -"
      "d /etc/ssh/principals 0755 root root -"
    ];

    systemd.services.guestd = {
      description = "repose guest daemon";
      wantedBy = [ "multi-user.target" ];
      before = [ "sshd.service" ];
      after = [ "network.target" ];
      serviceConfig = {
        ExecStart = "${guestdPackage}/bin/guestd --dev-socket /run/repose/guestd.sock --log-level debug";
        Restart = "always";
        User = "root";
      };
    };

    # The tmux session unit SetupProject starts
    # (docs/workstreams/02-guest-base.md owns it; the test provides the same
    # name so the contract is exercised).
    systemd.user.services.repose-tmux-session = {
      description = "project tmux session";
      serviceConfig = {
        Type = "forking";
        ExecStart = "${pkgs.writeShellScript "repose-tmux-session" ''
          slug=$(${pkgs.jq}/bin/jq -r .slug /home/dev/.repose/project.json)
          dir=/home/dev
          [ -d "/home/dev/$slug" ] && dir="/home/dev/$slug"
          exec ${pkgs.tmux}/bin/tmux new-session -d -s "$slug" -n shell -c "$dir"
        ''}";
        ExecStop = "${pkgs.tmux}/bin/tmux kill-server";
        RemainAfterExit = true;
      };
    };

    # A second system generation that adds a package and keeps the kernel, and
    # a third that changes the initrd. Specialisations are built with the
    # system, so no second evaluation is needed.
    specialisation.withHtop.configuration = {
      environment.systemPackages = [ pkgs.htop ];
    };
    specialisation.newInitrd.configuration = {
      boot.initrd.availableKernelModules = [ "nbd" "dm_mod" "raid0" ];
    };
  };

  testScript = ''
    import json

    guest.wait_for_unit("multi-user.target")
    guest.wait_for_unit("guestd.service")
    guest.wait_for_file("/run/repose/guestd.sock")
    guest.wait_for_file("/run/repose/hooks.sock")

    def call(request, body=None, extra="", socket="/run/repose/guestd.sock"):
        arg = "" if body is None else " '" + json.dumps(body) + "'"
        out = guest.succeed(f"guestd call {request}{arg} --dev-socket {socket} {extra}")
        return out

    def first_json(out):
        # `call` prints the response object, then any notification lines.
        decoder = json.JSONDecoder()
        return decoder.raw_decode(out.lstrip())[0]

    with subtest("guestd listens and answers Ping"):
        guest.succeed("journalctl -u guestd | grep -q '\"event\":\"ready\"'")
        guest.succeed("journalctl -u guestd | grep -q '\"transport\":\"unix\"'")
        ping = first_json(call("ping"))
        assert ping["ok"] is True, ping
        assert ping["ping"]["version"] == "1", ping
        assert ping["ping"]["bootId"], ping

    with subtest("the hook socket is 0660 root:dev"):
        mode = guest.succeed("stat -c '%a %U %G' /run/repose/hooks.sock").strip()
        assert mode == "660 root dev", mode

    with subtest("WriteSecrets: modes, ownership and shell quoting"):
        import base64
        tricky = "it's a\nmulti 'line' value\n"
        call("write-secrets", {"secrets": [
            {"name": "TRICKY", "value": base64.b64encode(tricky.encode()).decode()},
            {"name": "PLAIN", "value": base64.b64encode(b"simple").decode()},
        ]})
        for name in ["TRICKY", "PLAIN"]:
            out = guest.succeed(f"stat -c '%a %U' /run/repose/secrets/{name}").strip()
            assert out == "400 dev", f"{name}: {out}"
        out = guest.succeed("stat -c '%a %U' /run/repose/secrets.env").strip()
        assert out == "400 dev", out
        # The only check that matters for quoting is a shell sourcing it.
        got = guest.succeed(
            "sudo -u dev sh -c '. /run/repose/secrets.env; printf %s \"$TRICKY\" | base64 -w0'"
        ).strip()
        assert base64.b64decode(got).decode() == tricky, got

    with subtest("WriteSecrets: withdrawing a secret removes it"):
        call("write-secrets", {"secrets": [
            {"name": "PLAIN", "value": base64.b64encode(b"simple").decode()},
        ]})
        guest.fail("test -e /run/repose/secrets/TRICKY")
        guest.fail("grep -q TRICKY /run/repose/secrets.env")
        # I-475: the removed name is unset, guarded, for processes that
        # inherited it.
        guest.succeed("grep -q ') unset TRICKY ;; esac$' /run/repose/secrets.refresh")
        guest.succeed("stat -c '%a %U' /run/repose/secrets.refresh | grep -qx '400 dev'")
        guest.succeed("stat -c '%a %U' /run/repose/secrets.state | grep -qx '600 root'")

    with subtest("I-475: a long-running bash's next command sees a secret written after it started, not a removed one"):
        enc = lambda v: base64.b64encode(v.encode()).decode()
        call("write-secrets", {"secrets": [
            {"name": "PLAIN", "value": enc("simple")},
            {"name": "OLD_ONE", "value": enc("old-value")},
            {"name": "ROT", "value": enc("r1")},
        ]})
        guest.succeed("rm -f /tmp/i475.*")
        guest.succeed(
            "systemd-run --unit=i475-parent --uid=dev --gid=dev "
            "-p Environment=BASH_ENV=/etc/repose/bash-env.sh ${agentParent}"
        )
        guest.wait_for_file("/tmp/i475.parent")
        out = guest.succeed("cat /tmp/i475.parent").strip()
        assert out == "old-value <unset>", out

        call("write-secrets", {"secrets": [
            {"name": "PLAIN", "value": enc("simple")},
            {"name": "NEW_ONE", "value": enc("new;value")},
            {"name": "ROT", "value": enc("r2")},
        ]})
        guest.succeed("touch /tmp/i475.go")
        guest.wait_for_file("/tmp/i475.done")
        out = guest.succeed("cat /tmp/i475.new").strip()
        assert out == "new;value", out
        out = guest.succeed("cat /tmp/i475.oldstate").strip()
        assert out == "gone", out
        # PLAIN, which the parent set itself, stays; ROT, held as
        # delivered, is rotated.
        out = guest.succeed("cat /tmp/i475.kept").strip()
        assert out == "from-envrc r2", out
        # A user who cannot read the file gets nothing and no error, under
        # strict mode.
        out = guest.succeed(
            "sudo -u nobody env BASH_ENV=/etc/repose/bash-env.sh bash -euo pipefail "
            "-c 'printf %s \"''${NEW_ONE-unset}\"' 2>&1"
        )
        assert out == "unset", out
        call("write-secrets", {"secrets": [
            {"name": "PLAIN", "value": enc("simple")},
        ]})

    with subtest("SetPrincipals, then ssh with a matching certificate"):
        guest.succeed('ssh-keygen -t ed25519 -N "" -C repose-ca -f /tmp/ca')
        guest.succeed('sudo -u dev mkdir -p /home/dev/.ssh')
        guest.succeed('sudo -u dev ssh-keygen -t ed25519 -N "" -f /home/dev/.ssh/id_ed25519')
        guest.succeed(
            "ssh-keygen -s /tmp/ca -I tester -n project-abc -V +1h "
            "/home/dev/.ssh/id_ed25519.pub"
        )
        guest.succeed("chown dev:dev /home/dev/.ssh/id_ed25519-cert.pub")
        import base64 as b64
        ca_pub = guest.succeed("cat /tmp/ca.pub")
        call("write-secrets", {"secrets": [
            {"name": "user_ca.pub", "value": b64.b64encode(ca_pub.encode()).decode()},
            {"name": "PLAIN", "value": b64.b64encode(b"simple").decode()},
        ]})
        guest.succeed("stat -c '%a' /run/repose/user_ca.pub | grep -q 644")

        # Without the principal, sshd must refuse.
        call("set-principals", {"principals": ["someone-else"]})
        guest.fail(
            "sudo -u dev ssh -o StrictHostKeyChecking=no -o BatchMode=yes "
            "-i /home/dev/.ssh/id_ed25519 dev@127.0.0.1 true"
        )
        # With it, sshd must accept.
        call("set-principals", {"principals": ["project-abc", "todo-app.heracraft"]})
        guest.succeed("grep -q project-abc /etc/ssh/principals/dev")
        guest.succeed(
            "sudo -u dev ssh -o StrictHostKeyChecking=no -o BatchMode=yes "
            "-i /home/dev/.ssh/id_ed25519 dev@127.0.0.1 true"
        )

    with subtest("SetupProject creates the tree and the tmux session"):
        setup = {
            "projectSlug": "todo-app",
            "remoteUrl": "https://github.com/heracraft/todo-app",
            "tz": "Africa/Nairobi",
            "lang": "C.UTF-8",
        }
        call("setup-project", setup)
        # No checkout until the CLI's first sync records one (I-368).
        guest.succeed("test ! -e /home/dev/todo-app")
        guest.succeed("sudo -u dev sh -c 'mkdir -p ~/factory && echo factory > ~/.repose/checkout'")
        call("setup-project", setup)
        guest.succeed("test -d /home/dev/factory/.git")
        guest.succeed("sudo -u dev git -C /home/dev/factory remote get-url origin | grep -q heracraft/todo-app")
        guest.succeed("test ! -e /home/dev/todo-app")
        guest.succeed("grep -q 'REPOSE_PROJECT=todo-app' /etc/repose/env")
        guest.succeed("grep -q 'TZ=Africa/Nairobi' /etc/repose/env")
        guest.succeed("stat -c '%U' /home/dev/.repose/project.json | grep -q dev")
        guest.wait_until_succeeds("sudo -u dev tmux has-session -t todo-app", timeout=30)

    with subtest("I-475: WriteSecrets sets and unsets dev's tmux global environment"):
        enc = lambda v: base64.b64encode(v.encode()).decode()
        call("write-secrets", {"secrets": [
            {"name": "PLAIN", "value": enc("simple")},
            {"name": "TMUX_ONE", "value": enc("tmux;")},
            {"name": "TMUX_BIG", "value": enc("b" * 40000)},
        ]})
        out = guest.succeed("sudo -u dev tmux show-environment -g TMUX_ONE").strip()
        assert out == "TMUX_ONE=tmux;", out
        # Past tmux's 16 KiB command limit: the values go in on stdin.
        out = guest.succeed("sudo -u dev tmux show-environment -g TMUX_BIG").strip()
        assert out == "TMUX_BIG=" + "b" * 40000, len(out)
        gen = guest.succeed("head -1 /run/repose/secrets.env").strip()
        out = guest.succeed("sudo -u dev tmux show-environment -g REPOSE_ENV_GEN").strip()
        assert "export " + out == gen, (out, gen)
        call("write-secrets", {"secrets": [
            {"name": "PLAIN", "value": enc("simple")},
        ]})
        guest.fail("sudo -u dev tmux show-environment -g TMUX_ONE")
        guest.fail("sudo -u dev tmux show-environment -g TMUX_BIG")

    with subtest("Sample reports the tmux windows and agent states"):
        # The absolute path, because the tmux server's own PATH is the user
        # unit's, not a login shell's.
        # The binary runs and stays running, before blaming tmux for anything.
        guest.succeed(
            "sudo -u dev sh -c 'timeout 1 ${fakeClaude}/bin/claude; test $? -eq 124'"
        )
        guest.succeed(
            "sudo -u dev tmux new-window -t todo-app -n claude "
            "-c /home/dev/factory '${fakeClaude}/bin/claude'"
        )
        guest.sleep(2)
        print(guest.succeed("sudo -u dev tmux list-windows -t todo-app"))
        guest.succeed("sudo -u dev tmux list-windows -t todo-app | grep -q claude")
        guest.succeed("pgrep -x claude")

        def agents():
            sample = first_json(call("sample"))
            return sample["sample"]["signals"].get("agents", [])

        # If the agent never appears, this is what guestd's own tmux call looks
        # like from the outside, printed so the reason is in the log rather
        # than inferred.
        print(guest.execute(
            "setpriv --reuid=1000 --regid=1000 --init-groups -- "
            "env -i PATH=/run/current-system/sw/bin:/usr/bin:/bin HOME=/home/dev "
            "USER=dev LOGNAME=dev SHELL=/bin/sh TERM=dumb "
            "tmux list-windows -t todo-app -F '#{window_name} #{pane_pid} #{pane_current_command}'"
        )[1])
        # Wait by parsing the sample, not by grepping it: protojson varies its
        # whitespace between builds on purpose, so a grep for `"agent":
        # "claude"` matches only some of the time.
        found = []
        for _ in range(30):
            found = agents()
            if found:
                break
            guest.sleep(2)
        assert found, "no agent window appeared in a sample within 60 seconds"
        assert len(found) == 1, found
        assert found[0]["agent"] == "claude", found
        assert found[0]["tmuxWindow"] == "claude", found
        assert found[0]["state"] in ["working", "idle", "unknown", "needs_input"], found

        sample = first_json(call("sample"))
        signals = sample["sample"]["signals"]
        assert signals["guestdOk"] is True, signals
        assert int(signals.get("tmuxClients", 0)) >= 0, signals
        assert len(sample["sample"]["procs"]) > 0, sample

    with subtest("I-200: the agent and the tmux server are protected, what they start is not"):
        claude = guest.succeed("pgrep -x claude").strip()
        server = guest.succeed("pgrep -x 'tmux: server'").strip().split()[0]
        guest.wait_until_succeeds(f"test $(cat /proc/{claude}/oom_score_adj) = -800", timeout=30)
        guest.wait_until_succeeds(f"test $(cat /proc/{server}/oom_score_adj) = -800", timeout=30)
        # I-576: this node's user manager runs at upstream's 100; guestd
        # writes -900 to it, as to a manager a base update has not
        # restarted.
        manager = guest.succeed("systemctl show -p MainPID --value user@${toString devUID}.service").strip()
        guest.wait_until_succeeds(f"test $(cat /proc/{manager}/oom_score_adj) = -900", timeout=30)
        # A dev server started in a window forks from the tmux server and
        # inherits its -800; guestd puts it back to 0 within a refresh.
        guest.succeed(
            "sudo -u dev tmux new-window -d -t todo-app -n web "
            "'${pkgs.python3}/bin/python3 -m http.server 5173 --bind 127.0.0.1'"
        )
        guest.wait_for_open_port(5173)
        web = guest.succeed("ss -Hltnp 'sport = :5173' | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2").strip()
        guest.wait_until_succeeds(f"test $(cat /proc/{web}/oom_score_adj) = 0", timeout=30)
        print(guest.succeed(f"grep -H . /proc/{claude}/oom_score_adj /proc/{server}/oom_score_adj /proc/{web}/oom_score_adj"))

    with subtest("I-200: under memory pressure the kernel kills the hog, not the agent"):
        guest.succeed("echo 'a=[]' > /tmp/hog.py && echo 'while True: a.append(bytearray(64 << 20))' >> /tmp/hog.py")
        guest.succeed("sudo -u dev tmux new-window -d -t todo-app -n hog '${pkgs.python3}/bin/python3 /tmp/hog.py'")
        guest.wait_until_succeeds("journalctl -k --no-pager | grep -q 'Killed process [0-9]* (python3)'", timeout=120)
        print(guest.succeed("journalctl -k --no-pager | grep -E 'Out of memory|Killed process'"))
        guest.succeed(f"kill -0 {claude}")
        guest.succeed(f"test $(cat /proc/{claude}/oom_score_adj) = -800")

    with subtest("a sample costs under 20 ms"):
        line = guest.succeed(
            "journalctl -u guestd -o cat | grep '\"event\":\"sample\"' | tail -1"
        )
        entry = json.loads(line)
        print("sample timing:", line)
        assert entry["duration_ms"] < 20, entry

    with subtest("a hook reaches hostd as an AgentEvent"):
        guest.execute(
            "guestd call ping --dev-socket /run/repose/guestd.sock --watch 8s "
            ">/tmp/notifications.txt 2>&1 &"
        )
        guest.sleep(1)
        guest.succeed(
            "sudo -u dev repose-hook --agent claude --window claude "
            "--socket /run/repose/hooks.sock "
            "'{\"agent\":\"claude\",\"kind\":\"completed\",\"summary\":\"tests pass\"}'"
        )
        guest.wait_until_succeeds("grep -q agentEvent /tmp/notifications.txt", timeout=30)
        guest.succeed("grep -q 'tests pass' /tmp/notifications.txt")
        # A malformed hook is refused and nothing is logged of its body.
        guest.fail(
            "curl -sf --unix-socket /run/repose/hooks.sock -X POST "
            "-d '{\"agent\":\"aider\"}' http://x/"
        )
        guest.succeed("journalctl -u guestd | grep -q hook_bad_payload")
        guest.fail("journalctl -u guestd | grep -q aider")

    with subtest("repose-notify and repose-ask reach hostd, and an answer reaches the ask"):
        # DECISIONS I-244: the guest half of agent messages and questions,
        # with guestd call standing in for hostd.
        guest.execute(
            "guestd call ping --dev-socket /run/repose/guestd.sock --watch 20s "
            ">/tmp/ask-notes.txt 2>&1 &"
        )
        guest.sleep(1)
        guest.succeed("sudo -u dev repose-notify 'deploy is green'")
        guest.wait_until_succeeds("grep -q agent_message /tmp/ask-notes.txt", timeout=30)
        guest.execute(
            "sudo -u dev sh -c 'repose-ask --options yes,no --timeout 60 \"ship it?\" "
            ">/tmp/ask-out.txt 2>/tmp/ask-err.txt; echo $? >/tmp/ask-rc.txt' >/dev/null 2>&1 &"
        )
        guest.wait_until_succeeds("ls /run/repose/questions | grep -q json", timeout=30)
        guest.wait_until_succeeds("grep -q questionId /tmp/ask-notes.txt", timeout=30)
        qid = guest.succeed("ls /run/repose/questions | head -1").strip().removesuffix(".json")
        result = first_json(call("answer-question", {"questionId": qid, "status": "answered", "answer": "yes"}))
        assert result["ok"] is True, result
        guest.wait_until_succeeds("test -s /tmp/ask-rc.txt", timeout=40)
        rc = guest.succeed("cat /tmp/ask-rc.txt").strip()
        assert rc == "0", (rc, guest.succeed("cat /tmp/ask-err.txt"))
        guest.succeed("grep -qx yes /tmp/ask-out.txt")
        # An answer for a question the guest does not know is not_found.
        status, out = guest.execute(
            "guestd call answer-question "
            "'{\"questionId\": \"0199aaaa-0000-7000-8000-000000000000\", \"status\": \"answered\"}' "
            "--dev-socket /run/repose/guestd.sock"
        )
        assert status != 0 and "not_found" in out, (status, out)
        # Neither the message nor the question nor the answer is in a log.
        guest.fail("journalctl -u guestd | grep -q 'deploy is green'")
        guest.fail("journalctl -u guestd | grep -q 'ship it'")
        guest.succeed("journalctl -u guestd | grep -q agent_question")

    with subtest("Switch applies a new generation without a reboot"):
        guest.fail("test -e /run/current-system/sw/bin/htop")
        closure = guest.succeed(
            "readlink -f /run/booted-system/specialisation/withHtop"
        ).strip()
        boot_id_before = guest.succeed("cat /proc/sys/kernel/random/boot_id").strip()
        result = first_json(call("switch", {"systemClosure": closure}))
        assert result["ok"] is True, result
        assert not result.get("switch", {}).get("needsReboot", False), result
        assert not result.get("switch", {}).get("rebooted", False), result
        guest.succeed("test -e /run/current-system/sw/bin/htop")
        boot_id_after = guest.succeed("cat /proc/sys/kernel/random/boot_id").strip()
        assert boot_id_before == boot_id_after, "the guest rebooted"
        guest.succeed(f"test $(readlink -f /nix/var/nix/profiles/system) = {closure}")

    with subtest("Switch refuses a generation whose boot files changed"):
        closure = guest.succeed(
            "readlink -f /run/booted-system/specialisation/newInitrd"
        ).strip()
        result = first_json(call("switch", {"systemClosure": closure}))
        assert result["ok"] is True, result
        assert result["switch"]["needsReboot"] is True, result
        assert not result["switch"].get("rebooted", False), result

    with subtest("Switch refuses a closure the store does not have"):
        guest.fail(
            "guestd call switch '{\"systemClosure\":\"/nix/store/"
            "0000000000000000000000000000000-collected\"}' "
            "--dev-socket /run/repose/guestd.sock"
        )

    # Freeze and GrowFs run against a loop-mounted ext4 served by a second
    # guestd under --root, so the VM's own root is never frozen and the block
    # device is one the test can actually resize.
    with subtest("Freeze blocks a write and Thaw releases it"):
        guest.succeed("mkdir -p /srv/vol && truncate -s 64M /srv/disk.img")
        guest.succeed("mkfs.ext4 -q /srv/disk.img")
        loop = guest.succeed("losetup --find --show /srv/disk.img").strip()
        guest.succeed(f"mount {loop} /srv/vol")
        guest.succeed("mkdir -p /srv/vol/proc /srv/vol/run/repose")
        guest.succeed(f"echo '{loop} / ext4 rw,relatime 0 0' > /srv/vol/proc/mounts")
        guest.succeed(
            "systemd-run --unit=guestd-vol --collect "
            "${guestdPackage}/bin/guestd --root /srv/vol "
            "--dev-socket /srv/vol/run/repose/guestd.sock "
            "--hook-socket /srv/vol/run/repose/hooks.sock --log-level debug"
        )
        guest.wait_for_file("/srv/vol/run/repose/guestd.sock")
        vol_sock = "/srv/vol/run/repose/guestd.sock"

        call("freeze", socket=vol_sock)
        # A write to the frozen filesystem must not finish.
        guest.execute(
            "(dd if=/dev/zero of=/srv/vol/blocked bs=1M count=8 conv=fsync "
            "&& touch /srv/vol/.written) >/dev/null 2>&1 &"
        )
        guest.sleep(3)
        guest.fail("test -e /srv/vol/.written")
        call("thaw", socket=vol_sock)
        guest.wait_until_succeeds("test -e /srv/vol/.written", timeout=30)
        guest.succeed("rm -f /srv/vol/.written /srv/vol/blocked")

    with subtest("the freeze watchdog thaws and warns when Thaw is withheld"):
        out = call("freeze", extra="--watch 15s", socket=vol_sock)
        assert "freeze_timeout" in out, out
        # And the filesystem is usable again without a Thaw ever being sent.
        guest.succeed("touch /srv/vol/after-watchdog")
        guest.succeed("journalctl -u guestd-vol | grep -q '\"event\":\"freeze_timeout\"'")

    with subtest("GrowFs after a block device resize"):
        before = int(first_json(call("grow-fs", socket=vol_sock))["growFs"]["newBytes"])
        loop = guest.succeed("losetup -j /srv/disk.img -O NAME -n").strip()
        guest.succeed("truncate -s 256M /srv/disk.img")
        guest.succeed(f"losetup -c {loop}")
        after = int(first_json(call("grow-fs", socket=vol_sock))["growFs"]["newBytes"])
        assert after > before, (before, after)
        df = int(guest.succeed("df -B1 --output=size /srv/vol | tail -1").strip())
        assert abs(df - after) < 1024 * 1024, (df, after)

    with subtest("Exec runs as dev and as root"):
        result = first_json(call("exec", {"argv": ["id", "-un"], "asUser": "dev", "timeoutS": 10}))
        import base64 as b64
        assert b64.b64decode(result["exec"]["stdout"]).decode().strip() == "dev", result
        result = first_json(call("exec", {"argv": ["id", "-un"], "timeoutS": 10}))
        assert b64.b64decode(result["exec"]["stdout"]).decode().strip() == "root", result
        result = first_json(call("exec", {"argv": ["false"], "timeoutS": 10}))
        assert result["ok"] is True, result
        assert result["exec"]["exitCode"] == 1, result
        # The argv never reaches a log field.
        guest.fail("journalctl -u guestd | grep -q '\"argv\"'")
        guest.succeed("journalctl -u guestd | grep -q '\"event\":\"exec\"'")

    with subtest("no secret value is ever in a log line"):
        guest.fail("journalctl -u guestd | grep -q 'simple'")
        guest.fail("journalctl -u guestd | grep -q 'multi'")

    with subtest("guestd is small and light"):
        size = int(guest.succeed("stat -c %s $(readlink -f $(which guestd))").strip())
        print(f"guestd binary: {size} bytes")
        assert size < 15 * 1024 * 1024, size
        rss_kb = int(guest.succeed(
            "ps -o rss= -p $(systemctl show -p MainPID --value guestd)"
        ).strip())
        print(f"guestd RSS: {rss_kb} KB")
        assert rss_kb < 20 * 1024, rss_kb

    with subtest("Shutdown powers the guest off"):
        guest.execute("guestd call shutdown '{\"timeoutS\":10}' --dev-socket /run/repose/guestd.sock")
        guest.wait_for_shutdown()
  '';
}
