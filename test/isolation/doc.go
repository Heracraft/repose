// Package isolation is the tenant-isolation test suite of
// docs/workstreams/14-security.md §5: one test per row of the boundary
// table, run against a real host carrying two guests owned by two users,
// plus the policy tests that run in ordinary CI without a host.
//
// The host-level tests never reach into the platform themselves. They run
// shell scripts through command prefixes given in the environment, so the
// same suite works through the gateway (a user's own certificate), through
// the operator path (edge, host, guest) before the gateway exists, or
// through `hostdev exec`. Every host test skips, naming the variable it
// needs, when that variable is unset; a test that cannot run is reported
// as skipped, never as passed.
//
// Environment (all prefixed REPOSE_ISOLATION_):
//
//	EXEC_A, EXEC_B   command prefix that runs its last argument as a shell
//	                 script inside guest A / guest B as user dev, for
//	                 example `ssh -F ~/.ssh/repose/config a.repose` or
//	                 `ssh -J root@<edge>,root@<host> dev@10.64.4.2` or
//	                 `hostdev --state-dir D exec --project a -- sh -c`
//	HOST_EXEC        the same for the host, as root
//	HOST_ID          the host id, printed with every result for the record
//	A_IP, B_IP       the guests' addresses on br-guests
//	A_MAC            guest A's MAC (52:54:...)
//	B_TAP            guest B's tap on the host (tap-<8hex>)
//	A_GUEST_ID       guest A's id (console.log path on the host)
//	A_SLUG           guest A's project slug (its tmux session name)
//	HOST_IP          the bridge address (.1 of the host's /22)
//	OTHER_GUEST_IP   an address in another host's guest /22
//	GATEWAY          ssh.repose.run:22 or a test gateway
//	KEY_A, CERT_A    user A's private key and certificate files
//	LOGIN_A, LOGIN_B <slug>.<handle> login names for A's and B's projects
//	API_URL          https://api.repose.run/v1 or a fake
//	TOKEN_A          a Logto access token for user A
//	PROJECT_A,       project ids
//	PROJECT_B
//	LOKI_URL         optional; the console-log test also queries Loki
//	DIRECT_SSH       set to 1 to run the direct-to-guest sshd test from the
//	                 host (needs the runbook's temporary input rule)
//
// Run everything that has what it needs:
//
//	go test ./test/isolation/ -run . -v -count=1
//
// Run only the CI-level policy tests (no host):
//
//	go test ./test/isolation/ -run 'Policy|ProcSample|WatchList|Credentials' -v
package isolation
