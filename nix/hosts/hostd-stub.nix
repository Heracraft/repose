# Stand-in for hostd until workstream 03's `packages.hostd` is merged. It
# implements exactly the subcommands the host units call, so the host boots,
# the units can be inspected, and the VM tests run:
#
#   hostd                    serve: log once, sleep forever
#   hostd register           see registration.nix; consumes the join token
#                            only when HOSTD_STUB_FIXTURE names a host.json
#                            to install (VM tests), else leaves it in place
#   hostd audit-login        called from PAM; logs the login event
#   hostd snapshot-all       called by repose-snapshot.timer; logs and exits
#   hostd version
#
# Everything it logs is ids and states; never the token or key material.
{ writeShellApplication, coreutils, jq }:
writeShellApplication {
  name = "hostd";
  runtimeInputs = [ coreutils jq ];
  text = ''
    state=/var/lib/repose/hostd
    token=/run/repose/join-token
    cmd=serve
    # Flags are skipped, with or without a value. A flag takes its next
    # argument only when there is one and it is not itself a flag: a value
    # left behind would be read as the command word, and a `shift 2` on a
    # value-less flag is worse still. This used to name the boolean flags
    # instead, and when `--pause-idle` was added to the host unit and
    # `repose.host.pauseIdle` defaulted to true (I-818, and the release
    # that flipped it), every host unit ended its argv with a boolean this
    # list did not have. `shift 2` with one argument left fails, and under
    # `set -o errexit` -- which writeShellApplication sets -- the stub
    # exited 1 in about 15 ms having logged nothing at all, which read
    # from outside exactly like a daemon that will not start (I-1020).
    while [ $# -gt 0 ]; do
      case "$1" in
        --state) state="$2"; shift 2 ;;
        --token|--join-token) token="$2"; shift 2 ;;
        --*=*) shift ;;
        --*)
          if [ $# -ge 2 ] && [ "''${2#-}" = "$2" ]; then shift 2; else shift; fi
          ;;
        *) cmd="$1"; shift ;;
      esac
    done
    log() { echo "{\"component\":\"hostd\",\"event\":\"$1\",\"msg\":\"$2\",\"stub\":true}"; }

    case "$cmd" in
      version)
        echo "hostd stub (workstream 01 placeholder; 03 provides the real binary)"
        ;;
      register)
        if [ -s "$state/host.json" ]; then
          log register_skip "already registered"
          exit 0
        fi
        if [ ! -s "$token" ]; then
          log register_wait "waiting for join token"
          exit 1
        fi
        if [ -n "''${HOSTD_STUB_FIXTURE:-}" ]; then
          mkdir -p "$state"
          jq -e .host_id "$HOSTD_STUB_FIXTURE" >/dev/null
          install -m 0600 "$HOSTD_STUB_FIXTURE" "$state/host.json"
          rm -f "$token"
          log register_done "host_id=$(jq -r .host_id "$state/host.json") (stub: fixture installed, token consumed)"
          exit 0
        fi
        log register_stub "registration needs the real hostd (workstream 03); token left in place"
        exit 0
        ;;
      audit-login)
        log audit_login "pam type=''${PAM_TYPE:-?} user=''${PAM_USER:-?} service=''${PAM_SERVICE:-?}"
        ;;
      snapshot-all)
        log snapshot_skip "stub cannot snapshot; nothing done"
        ;;
      serve)
        log stub_start "hostd stub running; workstream 03 provides the daemon"
        exec sleep infinity
        ;;
      *)
        log unknown_command "$cmd"
        exit 2
        ;;
    esac
  '';
}
