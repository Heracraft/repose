package hostdev

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/testca"
	"github.com/heracraft/repose/internal/obs"
)

// Usage is the subcommand list.
const Usage = `usage: hostdev [--state-dir DIR] <command> [flags]

  init      --listen ADDR --names host,ip,...   create the CA, server cert, join token
  serve                                          run the api stand-in (gRPC + control socket)
  status                                         host, guests, last heartbeat
  build     --project P --fragment F --base-ref R   build a fragment on the host
  create    --project P --class C (--fragment F --base-ref R | --closure PATH) [--volume 40G] [--secret K=V]...
  start     --project P
  stop      --project P [--snapshot] [--timeout 60]
  destroy   --project P [--keep-volume]
  apply     --project P --closure PATH [--force-reboot]
  resize    --project P --size 80G
  snapshot  --project P [--reason manual]
  restore   --project P --blob-path PATH [--class C] [--volume 40G] [--closure PATH]
  secrets   set --project P K=V...
  principals --project P PRINCIPAL...
  exec      --project P -- ARGV...
  drain
  ssh-cert  --project P --pubkey ~/.ssh/id_ed25519.pub   sign the operator's key for the guest
  logs      COMMAND-ID [--follow]
  samples                                        print the last Samples
  events                                         print recent events
`

// Main runs the CLI and returns the exit code.
func Main(args []string) int {
	fs := flag.NewFlagSet("hostdev", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home, _ := os.UserHomeDir() // an unknown home only affects the default state dir
	dir := fs.String("state-dir", filepath.Join(home, ".repose-hostdev"), "state directory")
	fs.Usage = func() { fmt.Fprint(os.Stderr, Usage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprint(os.Stderr, Usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cmd, rest := fs.Arg(0), fs.Args()[1:]
	err := run(ctx, *dir, cmd, rest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hostdev:", err)
		var ce *cmdError
		if errors.As(err, &ce) {
			return 10
		}
		return 1
	}
	return 0
}

type cmdError struct{ code, msg string }

func (e *cmdError) Error() string { return e.code + ": " + e.msg }

func parseSize(s string) (uint64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "T"):
		mult, s = 1<<40, strings.TrimSuffix(s, "T")
	case strings.HasSuffix(s, "G"):
		mult, s = 1<<30, strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, strings.TrimSuffix(s, "M")
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q: use e.g. 40G", s)
	}
	return n * mult, nil
}

func run(ctx context.Context, dir, cmd string, args []string) error {
	switch cmd {
	case "init":
		return initCmd(dir, args)
	case "serve":
		return serveCmd(ctx, dir)
	}
	c := NewClient(dir)
	switch cmd {
	case "status":
		return statusCmd(ctx, c)
	case "build":
		return buildCmd(ctx, c, args)
	case "create":
		return createCmd(ctx, dir, c, args)
	case "start":
		return simpleCmd(ctx, c, args, func(p *Project) *hostdv1.Command {
			return &hostdv1.Command{Cmd: &hostdv1.Command_StartGuest{StartGuest: &hostdv1.StartGuest{GuestId: p.GuestID, Secrets: secretsOf(p), HostKey: p.HostKey, HostCert: p.HostCert, SshCaPub: sshCAPub(dir), Principals: []string{p.ProjectID}, ProjectJson: projectJSON(p)}}}
		})
	case "stop":
		return stopCmd(ctx, c, args)
	case "destroy":
		return destroyCmd(ctx, c, args)
	case "apply":
		return applyCmd(ctx, c, args)
	case "resize":
		return resizeCmd(ctx, c, args)
	case "snapshot":
		return snapshotCmd(ctx, c, args)
	case "restore":
		return restoreCmd(ctx, dir, c, args)
	case "secrets":
		return secretsCmd(ctx, c, args)
	case "principals":
		return principalsCmd(ctx, c, args)
	case "exec":
		return execCmd(ctx, c, args)
	case "drain":
		_, err := submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_Drain{Drain: &hostdv1.Drain{}}}, "", false)
		return err
	case "ssh-cert":
		return sshCertCmd(ctx, dir, c, args)
	case "logs":
		return logsCmd(ctx, c, args)
	case "samples":
		_, st, err := c.Status(ctx)
		if err != nil {
			return err
		}
		if len(st.Samples) == 0 {
			fmt.Println("no samples yet")
			return nil
		}
		return printJSON(st.Samples[len(st.Samples)-1])
	case "events":
		_, st, err := c.Status(ctx)
		if err != nil {
			return err
		}
		for _, e := range st.Events {
			fmt.Println(string(e))
		}
		return nil
	}
	fmt.Fprint(os.Stderr, Usage)
	return errors.New("unknown command " + cmd)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func initCmd(dir string, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:8443", "gRPC listen address")
	names := fs.String("names", "", "comma-separated names and IPs the host will dial (server certificate SANs)")
	cidr := fs.String("guest-cidr", "10.64.4.0/22", "the host's guest /22")
	handle := fs.String("handle", "operator", "user handle for SSH host-certificate principals")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *names == "" {
		return errors.New("--names is required: every hostname or IP hostd will use in --api-addr")
	}
	if _, err := os.Stat(filepath.Join(dir, StateFile)); err == nil {
		return fmt.Errorf("%s already initialised; remove it to start over", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	ca, err := testca.New()
	if err != nil {
		return err
	}
	caKey, err := ca.KeyPEM()
	if err != nil {
		return err
	}
	srvCert, srvKey, err := ca.IssueServer(strings.Split(*names, ",")...)
	if err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{{CAFile, ca.PEM, 0o644}, {CAKeyFile, caKey, 0o600}, {ServerCert, srvCert, 0o644}, {ServerKey, srvKey, 0o600}} {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, f.mode); err != nil {
			return err
		}
	}
	if _, err := NewSSHCA(dir); err != nil {
		return err
	}
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	token := "rjt_" + hex.EncodeToString(tok)
	st := &State{Listen: *listen, GuestCIDR: *cidr, JoinToken: token, Projects: map[string]*Project{}, Commands: map[string]*CommandRecord{}, UserHandle: *handle, UserID: "user-" + newID()[:8]}
	if err := Write(dir, st); err != nil {
		return err
	}
	_, port, _ := strings.Cut(*listen, ":")
	first := strings.Split(*names, ",")[0]
	fmt.Printf("hostdev initialised in %s\n\njoin token (write it to /run/repose/join-token on the host):\n  %s\n\nhostd flags:\n  --api-addr %s:%s --api-ca <copy of %s>\n\nstart with: hostdev --state-dir %s serve\n",
		dir, token, first, port, filepath.Join(dir, CAFile), dir)
	fmt.Printf("\nSSH user CA (the guest sshd trusts it):\n  %s", mustRead(filepath.Join(dir, SSHCAPub)))
	return nil
}

func mustRead(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func serveCmd(ctx context.Context, dir string) error {
	log := obs.NewLogger(obs.LogOptions{Component: obs.ComponentHostdev})
	s, err := NewServer(dir, log)
	if err != nil {
		return err
	}
	return s.Serve(ctx)
}

func statusCmd(ctx context.Context, c *Client) error {
	connected, st, err := c.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("host: %s  connected: %v  registered: %s\n", st.Host.HostID, connected, st.Host.Registered.Format(time.RFC3339))
	if len(st.Host.LastHeartbeat) > 0 {
		fmt.Printf("heartbeat (%s ago): %s\n", time.Since(st.Host.HeartbeatAt).Round(time.Second), string(st.Host.LastHeartbeat))
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PROJECT\tGUEST\tCLASS\tSTATE\tIP\tCLOSURE\tLAST SNAPSHOT")
	for name, p := range st.Projects {
		state := st.Host.Guests[p.GuestID]
		if state == "" {
			state = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, p.GuestID, p.Class, state, p.GuestIP, filepath.Base(p.Closure), p.LastBlob)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	pending := 0
	for _, cr := range st.Commands {
		if cr.Result == nil {
			pending++
		}
	}
	fmt.Printf("commands: %d recorded, %d pending\n", len(st.Commands), pending)
	return nil
}

func projectOf(ctx context.Context, c *Client, name string) (*Project, *State, error) {
	if name == "" {
		return nil, nil, errors.New("--project is required")
	}
	_, st, err := c.Status(ctx)
	if err != nil {
		return nil, nil, err
	}
	p := st.Projects[name]
	if p == nil {
		return nil, st, fmt.Errorf("project %q unknown; create it first", name)
	}
	return p, st, nil
}

func secretsOf(p *Project) []*hostdv1.Secret {
	var out []*hostdv1.Secret
	for k, v := range p.Secrets {
		out = append(out, &hostdv1.Secret{Name: k, Value: v})
	}
	return out
}

func sshCAPub(dir string) string { return strings.TrimSpace(mustRead(filepath.Join(dir, SSHCAPub))) }

func projectJSON(p *Project) []byte {
	b, _ := json.Marshal(map[string]any{"project_id": p.ProjectID, "slug": p.Name, "name": p.Name, "class": p.Class}) // fixed shape, cannot fail
	return b
}

// submitAndWait sends, optionally tails the build log, waits, prints.
func submitAndWait(ctx context.Context, c *Client, cmd *hostdv1.Command, project string, tailLogs bool) (*hostdv1.Result, error) {
	id, err := c.Submit(ctx, cmd, project)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "command %s sent\n", id)
	if tailLogs {
		go func() { _ = c.Logs(ctx, id, true, os.Stdout) }() // the wait below is authoritative
	}
	res, err := c.Wait(ctx, id)
	if err != nil {
		return nil, err
	}
	if !res.Ok {
		msg := res.Error.Message
		if res.Error.FragmentLine > 0 {
			msg = fmt.Sprintf("(fragment line %d) %s", res.Error.FragmentLine, msg)
		}
		return res, &cmdError{res.Error.Code, msg}
	}
	if res.Payload != nil {
		fmt.Println(string(rawProto(res)))
	} else {
		fmt.Println("ok")
	}
	return res, nil
}

func buildCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	frag := fs.String("fragment", "", "fragment file")
	base := fs.String("base-ref", "", "git revision of the platform nix/ directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, st, err := c.Status(ctx)
	if err != nil {
		return err
	}
	p := st.Projects[*name]
	if p == nil {
		p = &Project{Name: *name, ProjectID: newID(), GuestID: newID(), CreatedAt: time.Now().UTC()}
		if err := c.PutProject(ctx, p); err != nil {
			return err
		}
	}
	_, err = buildFor(ctx, c, p, *frag, *base)
	return err
}

func buildFor(ctx context.Context, c *Client, p *Project, frag, base string) (string, error) {
	if frag == "" || base == "" {
		return "", errors.New("--fragment and --base-ref are required")
	}
	fb, err := os.ReadFile(frag)
	if err != nil {
		return "", err
	}
	p.Fragment, p.BaseRef = fb, base
	if err := c.PutProject(ctx, p); err != nil {
		return "", err
	}
	cmd := &hostdv1.Command{Cmd: &hostdv1.Command_Build{Build: &hostdv1.Build{ProjectId: p.ProjectID, RevisionId: newID(), Fragment: fb, BaseRef: base,
		Limits: &hostdv1.Limits{EvalS: 60, BuildS: 1800, Cores: 8, ClosureBytes: 20 << 30}}}}
	res, err := submitAndWait(ctx, c, cmd, p.Name, true)
	if err != nil {
		return "", err
	}
	return res.GetBuild().SystemClosure, nil
}

func createCmd(ctx context.Context, dir string, c *Client, args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	name := fs.String("project", "", "project name (becomes the slug)")
	class := fs.String("class", "large", "tiny|small|large|xl")
	frag := fs.String("fragment", "", "fragment file (built first)")
	base := fs.String("base-ref", "", "git revision of the platform nix/ directory")
	closure := fs.String("closure", "", "an already built system closure")
	volume := fs.String("volume", "", "volume size (default by class: 20G, 40G, 80G)")
	var secrets stringList
	fs.Var(&secrets, "secret", "K=V, repeatable")
	remote := fs.String("remote", "", "git remote url for the guest checkout")
	tz := fs.String("tz", "UTC", "TZ for the guest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("--project is required")
	}
	_, st, err := c.Status(ctx)
	if err != nil {
		return err
	}
	p := st.Projects[*name]
	if p == nil {
		p = &Project{Name: *name, ProjectID: newID(), GuestID: newID(), Class: *class, Secrets: map[string][]byte{}, CreatedAt: time.Now().UTC()}
	}
	p.Class = *class
	if *volume == "" {
		*volume = map[string]string{"tiny": "10G", "small": "20G", "large": "40G", "xl": "80G"}[*class]
	}
	if p.VolumeBytes, err = parseSize(*volume); err != nil {
		return err
	}
	for _, kv := range secrets {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--secret %q: want K=V", kv)
		}
		if p.Secrets == nil {
			p.Secrets = map[string][]byte{}
		}
		p.Secrets[k] = []byte(v)
	}
	if err := c.PutProject(ctx, p); err != nil {
		return err
	}
	if *closure == "" {
		if *frag == "" {
			return errors.New("give --fragment (with --base-ref) or --closure")
		}
		out, err := buildFor(ctx, c, p, *frag, *base)
		if err != nil {
			return err
		}
		*closure = out
	}
	p.Closure = *closure
	sshca, err := LoadSSHCA(dir)
	if err != nil {
		return err
	}
	// The guest IP is assigned by hostd; the host certificate carries the
	// name principal now and hostdev re-signs with the IP after create.
	if len(p.HostKey) == 0 {
		p.HostKey, p.HostCert, err = sshca.GuestHostKey("", p.Name, st.UserHandle)
		if err != nil {
			return err
		}
	}
	if err := c.PutProject(ctx, p); err != nil {
		return err
	}
	cmd := &hostdv1.Command{Cmd: &hostdv1.Command_CreateGuest{CreateGuest: &hostdv1.CreateGuest{
		ProjectId: p.ProjectID, GuestId: p.GuestID, Class: p.Class, VolumeBytes: p.VolumeBytes, SystemClosure: p.Closure,
		Secrets: secretsOf(p), Env: map[string]string{"TZ": *tz, "LANG": "C.UTF-8"}, SshCaPub: sshCAPub(dir), Principals: []string{p.ProjectID},
		HostKey: p.HostKey, HostCert: p.HostCert, UserId: st.UserID, ProjectSlug: p.Name, RemoteUrl: *remote, ProjectJson: projectJSON(p),
	}}}
	res, err := submitAndWait(ctx, c, cmd, p.Name, false)
	if err != nil {
		return err
	}
	fmt.Printf("guest %s running at %s (vsock cid %d)\n", p.GuestID, res.GetCreate().GuestIp, res.GetCreate().VsockCid)
	return nil
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func simpleCmd(ctx context.Context, c *Client, args []string, build func(*Project) *hostdv1.Command) error {
	fs := flag.NewFlagSet("cmd", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	_, err = submitAndWait(ctx, c, build(p), p.Name, false)
	return err
}

func stopCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	snap := fs.Bool("snapshot", false, "snapshot before stopping")
	timeout := fs.Uint("timeout", 60, "seconds before the hypervisor is asked to stop")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	_, err = submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_StopGuest{StopGuest: &hostdv1.StopGuest{GuestId: p.GuestID, SnapshotFirst: *snap, TimeoutS: uint32(*timeout)}}}, p.Name, false)
	return err
}

func destroyCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("destroy", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	keep := fs.Bool("keep-volume", false, "keep the thin volume")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	if _, err := submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_DestroyGuest{DestroyGuest: &hostdv1.DestroyGuest{GuestId: p.GuestID, KeepVolume: *keep}}}, p.Name, false); err != nil {
		return err
	}
	return c.DeleteProject(ctx, p.Name)
}

func applyCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	closure := fs.String("closure", "", "system closure to switch to")
	force := fs.Bool("force-reboot", false, "stop (with snapshot) and start when the kernel changed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	if *closure == "" {
		return errors.New("--closure is required (from build)")
	}
	res, err := submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_ApplyConfig{ApplyConfig: &hostdv1.ApplyConfig{GuestId: p.GuestID, SystemClosure: *closure, ForceReboot: *force}}}, p.Name, false)
	if err != nil {
		return err
	}
	if res.GetApply().RebootRequired {
		fmt.Println("the new closure changes the kernel or initrd; nothing was applied. Re-run with --force-reboot.")
		return nil
	}
	p.Closure = *closure
	return c.PutProject(ctx, p)
}

func resizeCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("resize", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	size := fs.String("size", "", "new size, e.g. 80G")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	n, err := parseSize(*size)
	if err != nil {
		return err
	}
	if _, err := submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_ResizeVolume{ResizeVolume: &hostdv1.ResizeVolume{GuestId: p.GuestID, NewBytes: n}}}, p.Name, false); err != nil {
		return err
	}
	p.VolumeBytes = n
	return c.PutProject(ctx, p)
}

func snapshotCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	reason := fs.String("reason", "manual", "scheduled|stop|manual")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	_, err = submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_Snapshot{Snapshot: &hostdv1.Snapshot{GuestId: p.GuestID, Reason: *reason}}}, p.Name, false)
	return err
}

func restoreCmd(ctx context.Context, dir string, c *Client, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	name := fs.String("project", "", "project name (new guest for it)")
	blob := fs.String("blob-path", "", "snapshot path in the store")
	class := fs.String("class", "", "size class (default: the project's)")
	volume := fs.String("volume", "", "volume size (default: the project's)")
	closure := fs.String("closure", "", "system closure to root (default: the project's)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, st, err := c.Status(ctx)
	if err != nil {
		return err
	}
	p := st.Projects[*name]
	if p == nil {
		p = &Project{Name: *name, ProjectID: newID(), Class: "large", VolumeBytes: 40 << 30, CreatedAt: time.Now().UTC()}
	}
	if *blob == "" {
		*blob = p.LastBlob
	}
	if *blob == "" {
		return errors.New("--blob-path is required")
	}
	if *class != "" {
		p.Class = *class
	}
	if *volume != "" {
		if p.VolumeBytes, err = parseSize(*volume); err != nil {
			return err
		}
	}
	if *closure != "" {
		p.Closure = *closure
	}
	p.GuestID = newID() // a restore is always a new guest
	sshca, err := LoadSSHCA(dir)
	if err != nil {
		return err
	}
	if len(p.HostKey) == 0 {
		if p.HostKey, p.HostCert, err = sshca.GuestHostKey("", p.Name, st.UserHandle); err != nil {
			return err
		}
	}
	if err := c.PutProject(ctx, p); err != nil {
		return err
	}
	cmd := &hostdv1.Command{Cmd: &hostdv1.Command_Restore{Restore: &hostdv1.Restore{
		ProjectId: p.ProjectID, GuestId: p.GuestID, BlobPath: *blob, Class: p.Class, VolumeBytes: p.VolumeBytes, SystemClosure: p.Closure,
		Secrets: secretsOf(p), Env: map[string]string{"TZ": "UTC", "LANG": "C.UTF-8"}, SshCaPub: sshCAPub(dir), Principals: []string{p.ProjectID},
		HostKey: p.HostKey, HostCert: p.HostCert, UserId: st.UserID, ProjectSlug: p.Name, ProjectJson: projectJSON(p),
	}}}
	_, err = submitAndWait(ctx, c, cmd, p.Name, false)
	if err != nil {
		return err
	}
	fmt.Println("restored and stopped; run `hostdev start --project", p.Name+"`")
	return nil
}

func secretsCmd(ctx context.Context, c *Client, args []string) error {
	if len(args) == 0 || args[0] != "set" {
		return errors.New("usage: secrets set --project P K=V")
	}
	fs := flag.NewFlagSet("secrets set", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	if p.Secrets == nil {
		p.Secrets = map[string][]byte{}
	}
	for _, kv := range fs.Args() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("%q: want K=V", kv)
		}
		p.Secrets[k] = []byte(v)
	}
	if err := c.PutProject(ctx, p); err != nil {
		return err
	}
	_, err = submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_UpdateSecrets{UpdateSecrets: &hostdv1.UpdateSecrets{GuestId: p.GuestID, Secrets: secretsOf(p)}}}, p.Name, false)
	return err
}

func principalsCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("principals", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	_, err = submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_SetPrincipals{SetPrincipals: &hostdv1.SetPrincipals{GuestId: p.GuestID, Principals: fs.Args()}}}, p.Name, false)
	return err
}

func execCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	timeout := fs.Uint("timeout", 60, "seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	argv := fs.Args()
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return errors.New("usage: exec --project P -- ARGV")
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	res, err := submitAndWait(ctx, c, &hostdv1.Command{Cmd: &hostdv1.Command_Exec{Exec: &hostdv1.Exec{GuestId: p.GuestID, Argv: argv, TimeoutS: uint32(*timeout), AuditId: "hostdev-" + newID()}}}, p.Name, false)
	if err != nil {
		return err
	}
	_, _ = os.Stdout.Write(res.GetExec().Stdout) // terminal gone
	_, _ = os.Stderr.Write(res.GetExec().Stderr)
	if code := res.GetExec().ExitCode; code != 0 {
		return fmt.Errorf("exit %d", code)
	}
	return nil
}

func sshCertCmd(ctx context.Context, dir string, c *Client, args []string) error {
	fs := flag.NewFlagSet("ssh-cert", flag.ContinueOnError)
	name := fs.String("project", "", "project name")
	pub := fs.String("pubkey", "", "public key file to sign")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, _, err := projectOf(ctx, c, *name)
	if err != nil {
		return err
	}
	pb, err := os.ReadFile(*pub)
	if err != nil {
		return err
	}
	sshca, err := LoadSSHCA(dir)
	if err != nil {
		return err
	}
	cert, err := sshca.UserCert(pb, p.ProjectID)
	if err != nil {
		return err
	}
	_, _ = os.Stdout.Write(cert) // terminal gone
	fmt.Fprintf(os.Stderr, "\nprincipal %s, 12 hours; save next to the key as <key>-cert.pub and ssh dev@%s\n", p.ProjectID, p.GuestIP)
	return nil
}

func logsCmd(ctx context.Context, c *Client, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "keep streaming until the command finishes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("logs COMMAND-ID [--follow]")
	}
	return c.Logs(ctx, fs.Arg(0), *follow, os.Stdout)
}

var _ io.Writer = os.Stdout
