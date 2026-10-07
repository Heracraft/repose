package guest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
)

// Every command in the Command oneof must have a handler; a new proto
// field without code fails here, not at integration.
func TestEveryCommandKindIsHandled(t *testing.T) {
	h := newHarness(t, nil)
	desc := (&hostdv1.Command{}).ProtoReflect().Descriptor()
	oneof := desc.Oneofs().ByName("cmd")
	if oneof == nil {
		t.Fatal("Command has no cmd oneof")
	}
	fields := oneof.Fields()
	seen := 0
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		c := &hostdv1.Command{CommandId: "kind-" + string(fd.Name())}
		msg := c.ProtoReflect()
		msg.Set(fd, protoreflect.ValueOfMessage(msg.NewField(fd).Message()))
		if Kind(c) == "" {
			t.Errorf("command field %s has no Kind", fd.Name())
			continue
		}
		res := h.m.Execute(context.Background(), c)
		if res == nil {
			t.Errorf("%s: no result", fd.Name())
			continue
		}
		if !res.Ok && res.Error.Message == "unknown command" {
			t.Errorf("%s: not handled", fd.Name())
		}
		seen++
	}
	if seen != 14 {
		t.Fatalf("expected 14 commands in the contract, saw %d", seen)
	}
}

func TestCreateReachesRunningWithEverythingWired(t *testing.T) {
	h := newHarness(t, nil)
	res := h.create(gid1)
	if res.GetCreate().GuestIp != "10.64.4.2" || res.GetCreate().VsockCid != 1000 {
		t.Fatalf("create result %v", res.GetCreate())
	}
	g := h.guest(gid1)
	wantTap, _ := TapName(gid1)
	wantMAC, _ := MACAddr(gid1)
	if g.State != StateRunning || g.Tap != wantTap || g.MAC != wantMAC {
		t.Fatalf("guest record %+v", g)
	}
	if !h.net.Taps[g.Tap] || h.net.Shaped[g.Tap] != 200 || h.net.Elements[gid1] != wantMAC+" . 10.64.4.2 . "+wantTap {
		t.Fatalf("network not wired: %+v", h.net)
	}
	if v := h.lvm.Volumes["g-"+gid1]; v == nil || !v.HasFS || v.Size != 40<<30 {
		t.Fatalf("volume: %+v", v)
	}
	if tgt, _ := h.roots.Get(gid1); tgt != h.closure {
		t.Fatalf("gc root %q", tgt)
	}
	u := h.sd.Units["guest@"+gid1]
	if u == nil || !u.Active || u.Props[0] != "MemoryMax=8704M" || u.Props[1] != "MemoryHigh=8576M" || u.Props[2] != "CPUQuota=400%" {
		t.Fatalf("unit %+v", u)
	}
	// H-2: the hypervisor is not root, and the unit is the sandbox
	// GuestUnitProps renders (its golden pins the full list).
	dir := filepath.Join(h.cfg.GuestsDir, gid1)
	for _, want := range []string{"User=hostd", "NoNewPrivileges=yes", "DevicePolicy=closed", "DeviceAllow=/dev/vg-guests/g-" + gid1 + " rw", "BindPaths=" + dir, "TemporaryFileSystem=" + h.cfg.GuestsDir} {
		if !slices.Contains(u.Props, want) {
			t.Fatalf("guest unit lacks %q: %v", want, u.Props)
		}
	}
	argv := strings.Join(u.Argv, " ")
	if !strings.Contains(argv, "--memory size=8192M,shared=on") || !strings.Contains(argv, "--seccomp true") {
		t.Fatalf("argv %v", u.Argv)
	}
	if v := h.sd.Units["virtiofsd@"+gid1]; v == nil || !v.Active || !strings.Contains(strings.Join(v.Argv, " "), "--socket-path "+filepath.Join(dir, "virtiofsd", "virtiofsd.sock")+" ") || !strings.Contains(strings.Join(v.Argv, " "), "--socket-group hostd") {
		t.Fatalf("virtiofsd unit %+v", v)
	}
	fg := h.guestd(gid1)
	sec := fg.Secrets()
	if string(sec["API_KEY"]) != "s3cret" || string(sec[SecretHostKey]) != "hostkey" || string(sec[SecretHostCert]) != "hostcert" || string(sec[SecretUserCA]) != "ssh-ed25519 AAAA ca" {
		t.Fatalf("delivered secrets %v", sec)
	}
	if p := fg.Principals(); len(p) != 1 || p[0] != "proj-0192f0a1" {
		t.Fatalf("principals %v", p)
	}
	var delivery []string
	for _, k := range fg.Kinds() {
		if k != "Ping" { // the readiness probe interleaves freely
			delivery = append(delivery, k)
		}
	}
	kinds := strings.Join(delivery, ",")
	if !strings.Contains(kinds, "WriteSecrets,SetPrincipals,SetupProject") {
		t.Fatalf("delivery order %s", kinds)
	}
	if st := h.rec.states(gid1); strings.Join(st, ">") != "creating>starting>running" {
		t.Fatalf("state events %v", st)
	}
	if b, err := os.ReadFile(filepath.Join(h.cfg.GuestsDir, gid1, "ch.args")); err != nil {
		t.Fatal("ch.args not written")
	} else if !strings.Contains(string(b), ":todo-app:eth0:off systemd.hostname=todo-app") {
		t.Fatalf("ch.args does not name the guest after its slug (I-550):\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(h.cfg.GuestsDir, gid1, "guest.json")); err != nil {
		t.Fatal("guest.json not written")
	}
	// Secrets never touch the host disk.
	files, _ := os.ReadDir(filepath.Join(h.cfg.GuestsDir, gid1))
	for _, f := range files {
		b, _ := os.ReadFile(filepath.Join(h.cfg.GuestsDir, gid1, f.Name()))
		if bytes.Contains(b, []byte("s3cret")) || bytes.Contains(b, []byte("hostkey")) {
			t.Fatalf("secret material found on disk in %s", f.Name())
		}
	}
}

func TestCreateValidation(t *testing.T) {
	h := newHarness(t, nil)
	req := createReq(gid1)
	req.SystemClosure = h.closure
	bad := func(mut func(*hostdv1.CreateGuest), code string) {
		t.Helper()
		r := createReq(gid1)
		r.SystemClosure = h.closure
		mut(r)
		h.mustFail(cmd(r), code)
	}
	bad(func(c *hostdv1.CreateGuest) { c.Class = "huge" }, CodeInvalidArgument)
	bad(func(c *hostdv1.CreateGuest) { c.VolumeBytes = 1 << 30 }, CodeInvalidArgument)
	bad(func(c *hostdv1.CreateGuest) { c.VolumeBytes = 3 << 40 }, CodeInvalidArgument)
	bad(func(c *hostdv1.CreateGuest) { c.GuestId = "zz" }, CodeInvalidArgument)
	bad(func(c *hostdv1.CreateGuest) { c.Secrets = []*hostdv1.Secret{{Name: SecretHostKey, Value: []byte("x")}} }, CodeInvalidArgument)
	h.nix.Exists = map[string]bool{}
	bad(func(c *hostdv1.CreateGuest) {}, CodeNotFound)
	h.nix.Exists = nil
	h.lvm.PoolFree = h.lvm.PoolSize / 20 // 95% used
	bad(func(c *hostdv1.CreateGuest) {}, CodeInsufficientCapacity)
	h.lvm.PoolFree = h.lvm.PoolSize
	// Memory: 64 GB total, 8 GB reserve = 56 GB; 6 large guests fit (8.5 each), the 7th does not.
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("0192f0b%d-0000-7000-8000-00000000000%d", i, i)
		h.create(id)
	}
	bad(func(c *hostdv1.CreateGuest) {}, CodeInsufficientCapacity)
	if g, err := h.st.GetGuest(gid1); err == nil {
		t.Fatalf("a refused create must leave no record, got %v", g)
	}
	_ = req
}

func TestCreateRollbackAtEveryStep(t *testing.T) {
	for step := stepVolume; step <= stepReady; step++ {
		t.Run(fmt.Sprintf("step%d", step), func(t *testing.T) {
			h := newHarness(t, func(c *Config) { c.FailAtStep = step; c.ReadyTimeout = 200 * time.Millisecond })
			req := createReq(gid1)
			req.SystemClosure = h.closure
			res := h.run(cmd(req))
			if res.Ok {
				t.Fatal("expected failure")
			}
			wantCode := CodeInternal
			if step == stepReady {
				wantCode = CodeGuestUnresponsive
			}
			if res.Error.Code != wantCode || !strings.Contains(res.Error.Message, fmt.Sprintf("create: step %d (%s) failed", step, stepNames[step])) {
				t.Fatalf("error %s: %s", res.Error.Code, res.Error.Message)
			}
			g := h.guest(gid1)
			if g.State != StateError {
				t.Fatalf("state %s", g.State)
			}
			if left := h.net.Leftovers(gid1, g.Tap); len(left) != 0 {
				t.Fatalf("leftovers after step %d: %v", step, left)
			}
			for _, u := range []string{"guest@" + gid1, "virtiofsd@" + gid1} {
				if un := h.sd.Units[u]; un != nil && un.Active {
					t.Fatalf("unit %s still active after step %d", u, step)
				}
			}
			if step > stepVolume {
				if _, ok := h.lvm.Volumes["g-"+gid1]; !ok {
					t.Fatalf("volume removed by rollback at step %d", step)
				}
			}
			// The api's retry (same guest id, new command) succeeds once the fault is gone.
			h.m.cfg.FailAtStep = 0
			h.create(gid1)
			if h.guest(gid1).State != StateRunning {
				t.Fatal("retry after rollback did not reach running")
			}
		})
	}
}

func TestGuestNeverReady(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.ReadyTimeout = 200 * time.Millisecond })
	h.noBoot[gid1] = true
	req := createReq(gid1)
	req.SystemClosure = h.closure
	res := h.mustFail(cmd(req), CodeGuestUnresponsive)
	if !strings.Contains(res.Error.Message, "guest did not become ready") {
		t.Fatalf("message %s", res.Error.Message)
	}
	if h.guest(gid1).State != StateError {
		t.Fatal("state should be error")
	}
}

func TestIdempotencyAndReplay(t *testing.T) {
	h := newHarness(t, nil)
	req := createReq(gid1)
	req.SystemClosure = h.closure
	c := cmd(req)
	first := h.mustOK(c)
	again := h.mustOK(clone(c))
	if again.GetCreate().GuestIp != first.GetCreate().GuestIp {
		t.Fatal("repeat must return the stored result")
	}
	if n := len(h.rec.states(gid1)); n != 3 {
		t.Fatalf("repeat re-executed: %d state events", n)
	}
	// A record left "started" (hostd died mid-command) is re-executed.
	stop := cmd(&hostdv1.StopGuest{GuestId: gid1})
	if _, err := h.st.StartCommand(stateCommandPtr(stop)); err != nil {
		t.Fatal(err)
	}
	h.mustOK(stop)
	if h.guest(gid1).State != StateStopped {
		t.Fatal("replayed stop did not run")
	}
	// Exec is not re-executed.
	ex := cmd(&hostdv1.Exec{GuestId: gid1, Argv: []string{"uptime"}, AuditId: "a1"})
	if _, err := h.st.StartCommand(stateCommandPtr(ex)); err != nil {
		t.Fatal(err)
	}
	res := h.mustFail(ex, CodeInternal)
	if res.Error.Message != "command interrupted" {
		t.Fatalf("exec replay: %s", res.Error.Message)
	}
	// Missing command_id and unknown guest.
	h.mustFail(&hostdv1.Command{Cmd: &hostdv1.Command_Drain{Drain: &hostdv1.Drain{}}}, CodeInvalidArgument)
	h.mustFail(cmd(&hostdv1.StopGuest{GuestId: gid2}), CodeNotFound)
}

func TestStopStartDestroy(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1, TimeoutS: 2}))
	g := h.guest(gid1)
	if g.State != StateStopped {
		t.Fatalf("state %s", g.State)
	}
	if left := h.net.Leftovers(gid1, g.Tap); len(left) != 0 {
		t.Fatalf("leftovers after stop: %v", left)
	}
	if _, ok := h.lvm.Volumes["g-"+gid1]; !ok {
		t.Fatal("stop removed the volume")
	}
	if _, ok := h.net.Counters[gid1]; !ok {
		t.Fatal("stop removed the counter")
	}
	if ok, _ := h.roots.Exists(gid1); !ok {
		t.Fatal("stop removed the gc root")
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1})) // idempotent
	// Start reuses the address.
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	g = h.guest(gid1)
	if g.State != StateRunning || g.IP != "10.64.4.2" {
		t.Fatalf("after start %+v", g)
	}
	if sec := h.guestd(gid1).Secrets(); string(sec["API_KEY"]) != "s3cret" {
		t.Fatal("cached secrets not redelivered at start")
	}
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1})) // idempotent
	// Start refuses when the closure root is gone.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	_ = h.roots.Remove(gid1)
	res := h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeNotFound)
	if res.Error.Message != "system closure missing; api must rebuild" {
		t.Fatalf("message %s", res.Error.Message)
	}
	_ = h.roots.Set(gid1, h.closure)
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	// Revision roots of this project and of another: destroy removes the
	// project's own and leaves the other's (I-115).
	revRoots := []string{"rev-proj-" + gid1[:8] + "-01", "rev-proj-" + gid1[:8] + "-02"}
	for _, name := range revRoots {
		if err := h.roots.Set(name, h.closure); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.roots.Set("rev-other-project-01", h.closure); err != nil {
		t.Fatal(err)
	}
	// Destroy from running, with a final egress sample (from the tap,
	// I-448) and nothing left.
	h.net.Counters[gid1] = 12345
	h.net.Send(h.guest(gid1).Tap, 0, 12345)
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
	if _, err := h.st.GetGuest(gid1); err == nil {
		t.Fatal("record still present after destroy")
	}
	if _, ok := h.lvm.Volumes["g-"+gid1]; ok {
		t.Fatal("volume still present after destroy")
	}
	if _, ok := h.net.Counters[gid1]; ok {
		t.Fatal("counter still present after destroy")
	}
	if ok, _ := h.roots.Exists(gid1); ok {
		t.Fatal("gc root still present after destroy")
	}
	for _, name := range revRoots {
		if ok, _ := h.roots.Exists(name); ok {
			t.Fatalf("revision root %s still present after destroy (I-115)", name)
		}
	}
	if ok, _ := h.roots.Exists("rev-other-project-01"); !ok {
		t.Fatal("another project's revision root was removed by the destroy")
	}
	// repose_host_guests publishes every state and class, at 0 when
	// empty, so a panel reads 0 rather than no data (I-116).
	if n := testutil.CollectAndCount(h.metrics.Guests); n != len(GuestStates)*len(Classes) {
		t.Fatalf("repose_host_guests has %d series, want %d", n, len(GuestStates)*len(Classes))
	}
	if v := testutil.ToFloat64(h.metrics.Guests.WithLabelValues(StateRunning, "large")); v != 0 {
		t.Fatalf("running/large gauge after destroy = %v, want 0", v)
	}
	if _, err := os.Stat(filepath.Join(h.cfg.GuestsDir, gid1)); err == nil {
		t.Fatal("guest dir still present after destroy")
	}
	h.rec.mu.Lock()
	var final *hostdv1.Samples
	if len(h.rec.samples) > 0 {
		final = h.rec.samples[len(h.rec.samples)-1]
	}
	h.rec.mu.Unlock()
	if final == nil || final.Guests[0].NetTxBytesDelta != 12345 {
		t.Fatalf("final egress sample %v", final)
	}
	st := h.rec.states(gid1)
	if st[len(st)-1] != StateDestroyed {
		t.Fatalf("states %v", st)
	}
	// The address is free again.
	h.create(gid2)
	if h.guest(gid2).IP != "10.64.4.2" {
		t.Fatal("released address not reused")
	}
	h.mustFail(cmd(&hostdv1.DestroyGuest{GuestId: gid1}), CodeNotFound)
}

// TestStartAppliesChangedClass: the api sends the project's class on every
// start (I-260), so a class changed while the project was stopped reaches
// the hypervisor, the unit's memory limit and the record the samples read.
func TestStartAppliesChangedClass(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1) // large
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	res := h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1, Class: "huge"}), CodeInvalidArgument)
	if !strings.Contains(res.Error.Message, "small, large or xl") {
		t.Fatalf("message %s", res.Error.Message)
	}
	if g := h.guest(gid1); g.Class != "large" || g.State != StateStopped {
		t.Fatalf("a refused start changed the record: %+v", g)
	}
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1, Class: "xl"}))
	if g := h.guest(gid1); g.Class != "xl" || g.State != StateRunning {
		t.Fatalf("after start at xl %+v", g)
	}
	u := h.sd.Units["guest@"+gid1]
	if u == nil || u.Props[0] != "MemoryMax=16896M" || u.Props[2] != "CPUQuota=800%" {
		t.Fatalf("unit %+v", u)
	}
	argv := strings.Join(u.Argv, " ")
	if !strings.Contains(argv, "--memory size=16384M,shared=on") || !strings.Contains(argv, "--cpus boot=8") {
		t.Fatalf("argv %v", u.Argv)
	}
	// An old-shape start (no class) keeps the recorded one.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if g := h.guest(gid1); g.Class != "xl" {
		t.Fatalf("class without a class in StartGuest: %s", g.Class)
	}
	// And back down.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1, Class: "small"}))
	if !strings.Contains(strings.Join(h.sd.Units["guest@"+gid1].Argv, " "), "--memory size=4096M,shared=on") {
		t.Fatalf("argv after small %v", h.sd.Units["guest@"+gid1].Argv)
	}
}

func TestDestroyKeepVolume(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1, KeepVolume: true}))
	if _, ok := h.lvm.Volumes["g-"+gid1]; !ok {
		t.Fatal("keep_volume removed the volume")
	}
}

func TestResize(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	res := h.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 20 << 30}), CodeInvalidArgument)
	if res.Error.Message != "volumes only grow" {
		t.Fatalf("message %s", res.Error.Message)
	}
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 80 << 30}))
	if h.lvm.Volumes["g-"+gid1].Size != 80<<30 || h.guest(gid1).VolumeBytes != 80<<30 {
		t.Fatal("volume not grown")
	}
	if kinds := h.guestd(gid1).Kinds(); kinds[len(kinds)-1] != "GrowFs" {
		t.Fatalf("GrowFs not sent: %v", kinds)
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 90 << 30})) // stopped: no GrowFs
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.lvm.SetData("g-"+gid1, []byte("the tenant's filesystem"))
	res := h.mustOK(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "manual"}))
	sr := res.GetSnapshot()
	if !strings.HasPrefix(sr.BlobPath, "user-1/proj-0192f0a1/") || !strings.HasSuffix(sr.BlobPath, ".img.zst") || sr.Bytes == 0 {
		t.Fatalf("snapshot result %v", sr)
	}
	if h.blob.Meta[sr.BlobPath]["reason"] != "manual" || h.blob.Meta[sr.BlobPath]["class"] != "large" {
		t.Fatalf("metadata %v", h.blob.Meta[sr.BlobPath])
	}
	kinds := strings.Join(h.guestd(gid1).Kinds(), ",")
	if !strings.Contains(kinds, "Freeze,Thaw") {
		t.Fatalf("freeze/thaw missing: %s", kinds)
	}
	if h.guestd(gid1).Frozen() {
		t.Fatal("guest left frozen")
	}
	for name := range h.lvm.Volumes {
		if strings.HasPrefix(name, "snap-") {
			t.Fatalf("lvm snapshot %s left behind", name)
		}
	}
	var done bool
	h.rec.mu.Lock()
	for _, e := range h.rec.events {
		if s := e.GetSnapshotDone(); s != nil && s.BlobPath == sr.BlobPath {
			done = true
		}
	}
	h.rec.mu.Unlock()
	if !done {
		t.Fatal("no snapshot_done event")
	}
	h.mustFail(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "bogus"}), CodeInvalidArgument)

	// Restore into a new guest ends stopped, identical bytes, fsck run.
	rq := &hostdv1.Restore{ProjectId: "proj-0192f0a1", GuestId: gid2, BlobPath: sr.BlobPath, Class: "large", VolumeBytes: 40 << 30,
		SystemClosure: h.closure, UserId: "user-1", ProjectSlug: "todo-app", Secrets: []*hostdv1.Secret{{Name: "API_KEY", Value: []byte("s3cret")}}, HostKey: []byte("hk")}
	rres := h.mustOK(cmd(rq))
	if rres.GetCreate().GuestIp != "10.64.4.3" {
		t.Fatalf("restore result %v", rres.GetCreate())
	}
	g2 := h.guest(gid2)
	if g2.State != StateStopped {
		t.Fatalf("restored state %s", g2.State)
	}
	if string(h.lvm.GetData("g-"+gid2)) != "the tenant's filesystem" {
		t.Fatal("restored bytes differ")
	}
	if !strings.Contains(strings.Join(h.lvm.Ops, ","), "fsck") {
		t.Fatal("fsck not run")
	}
	if left := h.net.Leftovers(gid2, g2.Tap); len(left) != 0 {
		t.Fatalf("restore wired the network: %v", left)
	}
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid2}))
	if b, _ := os.ReadFile(filepath.Join(h.cfg.GuestsDir, gid2, "ch.args")); !strings.Contains(string(b), "systemd.hostname=todo-app") {
		t.Fatalf("restored guest's ch.args lacks its slug (I-550):\n%s", b)
	}
	if sec := h.guestd(gid2).Secrets(); string(sec["API_KEY"]) != "s3cret" || string(sec[SecretHostKey]) != "hk" {
		t.Fatal("restore did not cache secrets for the start")
	}
	// Failure paths.
	h.mustFail(cmd(&hostdv1.Restore{ProjectId: "p", GuestId: "0192f0a3-3333-7000-8000-000000000003", BlobPath: "nope", Class: "large", VolumeBytes: 40 << 30}), CodeNotFound)
	h.lvm.FsckExit = 4
	res = h.mustFail(cmd(&hostdv1.Restore{ProjectId: "p", GuestId: "0192f0a3-3333-7000-8000-000000000003", BlobPath: sr.BlobPath, Class: "large", VolumeBytes: 40 << 30}), CodeInternal)
	if !strings.Contains(res.Error.Message, "filesystem check failed after restore") {
		t.Fatalf("fsck message %s", res.Error.Message)
	}
	h.lvm.FsckExit = 0
	if _, ok := h.lvm.Volumes["g-0192f0a3-3333-7000-8000-000000000003"]; ok {
		t.Fatal("a failed restore left its half-written volume for a start to boot (I-461)")
	}
	// I-462: the result carries the SHA-256 of the stored blob, and a
	// restore given it checks the whole blob before writing anything.
	sum := sha256.Sum256(h.blob.Blobs[sr.BlobPath])
	if sr.Sha256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("snapshot sha256 %q, blob's %x", sr.Sha256, sum)
	}
	h.rec.mu.Lock()
	for _, e := range h.rec.events {
		if s := e.GetSnapshotDone(); s != nil && s.BlobPath == sr.BlobPath && s.Sha256 != sr.Sha256 {
			t.Errorf("snapshot_done sha256 %q, result's %q", s.Sha256, sr.Sha256)
		}
	}
	h.rec.mu.Unlock()
	restoreReq := func(gid, sum string) *hostdv1.Restore {
		return &hostdv1.Restore{ProjectId: "proj-0192f0a1", GuestId: gid, BlobPath: sr.BlobPath, Class: "large", VolumeBytes: 40 << 30, SystemClosure: h.closure, Sha256: sum}
	}
	gid4 := "0192f0a3-4444-7000-8000-000000000004"
	h.mustOK(cmd(restoreReq(gid4, strings.ToUpper(sr.Sha256))))
	if string(h.lvm.GetData("g-"+gid4)) != "the tenant's filesystem" {
		t.Fatal("verified restore wrote different bytes")
	}
	gid5 := "0192f0a3-5555-7000-8000-000000000005"
	h.mustFail(cmd(restoreReq(gid5, "abc")), CodeInvalidArgument)
	lvmOps := len(h.lvm.Ops)
	res = h.mustFail(cmd(restoreReq(gid5, strings.Repeat("0", 64))), CodeInternal)
	if !strings.Contains(res.Error.Message, "checksum mismatch") {
		t.Fatalf("mismatch message %s", res.Error.Message)
	}
	if _, err := h.st.GetGuest(gid5); err == nil {
		t.Fatal("a restore whose blob failed its checksum recorded a guest")
	}
	if _, ok := h.lvm.Volumes["g-"+gid5]; ok || len(h.lvm.Ops) != lvmOps {
		t.Fatalf("a restore whose blob failed its checksum touched LVM: %v", h.lvm.Ops[lvmOps:])
	}
	// Bytes that change under an unchanged version between the check and
	// the write (a store breaking its promise) fail the restore after the
	// write, and its volume goes.
	downloads := 0
	h.blob.BeforeDownload = func(path string) {
		downloads++
		if downloads == 2 {
			h.blob.Blobs[path] = []byte("someone else's filesystem")
		}
	}
	gid6 := "0192f0a3-6666-7000-8000-000000000006"
	res = h.mustFail(cmd(restoreReq(gid6, sr.Sha256)), CodeInternal)
	h.blob.BeforeDownload = nil
	if !strings.Contains(res.Error.Message, "checksum mismatch") {
		t.Fatalf("mismatch during write: %s", res.Error.Message)
	}
	if _, ok := h.lvm.Volumes["g-"+gid6]; ok {
		t.Fatal("a restore that failed its checksum left its volume")
	}
	// Upload failure removes the LVM snapshot and reports internal.
	h.blob.Fail = fmt.Errorf("403 from blob")
	res = h.mustFail(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "scheduled"}), CodeInternal)
	if !strings.Contains(res.Error.Message, "snapshot upload failed") {
		t.Fatalf("upload failure message %s", res.Error.Message)
	}
	for name := range h.lvm.Volumes {
		if strings.HasPrefix(name, "snap-") {
			t.Fatalf("lvm snapshot %s left behind after failed upload", name)
		}
	}
	h.blob.Fail = nil
	// Stop with snapshot_first carries the blob path.
	res = h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1, SnapshotFirst: true}))
	if res.GetStop().BlobPath == "" || res.GetStop().Bytes == 0 {
		t.Fatalf("stop result %v", res.GetStop())
	}
	// A stopped guest snapshots without freeze.
	before := len(h.blob.Blobs)
	h.mustOK(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "manual"}))
	if len(h.blob.Blobs) != before+1 {
		t.Fatal("stopped guest snapshot not uploaded")
	}
}

func TestBuildAndApply(t *testing.T) {
	h := newHarness(t, nil)
	h.nix.Lines = []string{"evaluating", "building x.drv", "built"}
	res := h.mustOK(cmd(&hostdv1.Build{ProjectId: "proj-0192f0a1", RevisionId: "rev1", Fragment: []byte("{}"), BaseRef: "abc", Limits: &hostdv1.Limits{EvalS: 60, BuildS: 1800, Cores: 8, ClosureBytes: 20 << 30}}))
	if res.GetBuild().SystemClosure != h.closure || res.GetBuild().KernelChanged {
		t.Fatalf("build result %v", res.GetBuild())
	}
	h.rec.mu.Lock()
	logs := len(h.rec.logs)
	h.rec.mu.Unlock()
	if logs != 3 {
		t.Fatalf("build log lines %d", logs)
	}
	if h.nix.Calls[0].Limits.Cores != 8 || h.nix.Calls[0].BaseRef != "abc" {
		t.Fatalf("limits not passed: %+v", h.nix.Calls[0])
	}
	for _, e := range []*nixbuild.Error{
		{Code: "eval_failed", Message: "syntax error at fragment.nix:1:32\n\nerror: ...", FragmentLine: 1},
		{Code: "build_timeout", Message: "build exceeded 1800 s; last derivation: x"},
		{Code: "closure_too_large", Message: "closure is 31.2 GB, limit is 20 GB"},
		{Code: "build_failed", Message: "build of y failed"},
	} {
		h.nix.Fail = e
		r := h.mustFail(cmd(&hostdv1.Build{ProjectId: "p", RevisionId: "r", Fragment: []byte("{}"), BaseRef: "abc"}), e.Code)
		if r.Error.Message != e.Message || r.Error.FragmentLine != e.FragmentLine {
			t.Fatalf("error passthrough %v", r.Error)
		}
	}
	h.nix.Fail = nil
	h.mustFail(cmd(&hostdv1.Build{ProjectId: "p", RevisionId: "r"}), CodeInvalidArgument)

	// kernel_changed against a running guest.
	h.create(gid1)
	closure2 := fakeClosure(t, "nixos-system-v2")
	h.nix.Closure = closure2
	h.nix.Kernel = filepath.Join(filepath.Dir(closure2), "bzImage")
	res = h.mustOK(cmd(&hostdv1.Build{ProjectId: "proj-0192f0a1", RevisionId: "rev2", Fragment: []byte("{}"), BaseRef: "abc"}))
	if !res.GetBuild().KernelChanged {
		t.Fatal("kernel change not detected")
	}
	// Apply without kernel change: switch in place, root moves.
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: closure2}))
	if g := h.guest(gid1); g.SystemClosure != closure2 || g.State != StateRunning {
		t.Fatalf("after apply %+v", g)
	}
	if tgt, _ := h.roots.Get(gid1); tgt != closure2 {
		t.Fatal("gc root not moved")
	}
	// guestd goes away during the Switch (an activation restarting it):
	// hostd waits for it and asks again, once (I-148).
	h.gopts.DropOnce = map[string]bool{"Switch": true}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1})) // new fake guestd with the option
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: closure2}))
	if n := 0; true {
		for _, k := range h.guestds[gid1].Kinds() {
			if k == "Switch" {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("Switch asked %d times across the drop, want 2", n)
		}
	}
	h.gopts.DropOnce = nil
	// Apply with kernel change: reboot_required, nothing done.
	closure3 := fakeClosure(t, "nixos-system-v3")
	h.gopts.NeedsReboot = map[string]bool{closure3: true}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1})) // new fake guestd with the option
	res = h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: closure3}))
	if !res.GetApply().RebootRequired || res.GetApply().Rebooted {
		t.Fatalf("apply result %v", res.GetApply())
	}
	if h.guest(gid1).SystemClosure != closure2 {
		t.Fatal("closure changed without force_reboot")
	}
	// force_reboot: snapshot, stop, start on the new closure.
	blobs := len(h.blob.Blobs)
	res = h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: closure3, ForceReboot: true}))
	if !res.GetApply().Rebooted {
		t.Fatalf("apply result %v", res.GetApply())
	}
	if g := h.guest(gid1); g.SystemClosure != closure3 || g.State != StateRunning {
		t.Fatalf("after forced apply %+v", g)
	}
	if len(h.blob.Blobs) != blobs+1 {
		t.Fatal("forced reboot did not snapshot first")
	}
	// Apply to a stopped guest just roots the closure.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: closure2}))
	if tgt, _ := h.roots.Get(gid1); tgt != closure2 {
		t.Fatal("stopped apply did not move the root")
	}
	// A record that says stopped while the hypervisor runs is not a
	// stopped guest: the apply fails instead of moving the root and
	// reporting success with nothing switched (I-325).
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	g := h.guest(gid1)
	g.State = StateStopped
	if err := h.st.PutGuest(g); err != nil {
		t.Fatal(err)
	}
	closure4 := fakeClosure(t, "nixos-system-v4")
	h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: closure4}), CodeInternal)
	if tgt, _ := h.roots.Get(gid1); tgt == closure4 {
		t.Fatal("drifted apply moved the root")
	}
	h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid2, SystemClosure: closure2}), CodeNotFound)
}

func TestBuildQueueFull(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxBuildQueue = 2; c.MaxBuilds = 1 })
	h.nix.Delay = 300 * time.Millisecond
	for i := 0; i < 4; i++ {
		h.m.Dispatch(cmd(&hostdv1.Build{ProjectId: "p", RevisionId: fmt.Sprint("r", i), Fragment: []byte("{}"), BaseRef: "abc"}))
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.rec.mu.Lock()
		n := len(h.rec.results)
		h.rec.mu.Unlock()
		if n == 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	full := 0
	for _, r := range h.rec.results {
		if !r.Ok && r.Error.Code == CodeInsufficientCapacity && r.Error.Message == "build queue full" {
			full++
		}
	}
	if full < 1 || len(h.rec.results) != 4 {
		t.Fatalf("results: %d total, %d queue-full", len(h.rec.results), full)
	}
	// A build that waited behind another says so first, and its own lines
	// follow in the same sequence (I-320).
	queued := 0
	for id, ls := range h.rec.logSeq {
		if ls[0] != "1 waiting for a build slot" {
			continue
		}
		queued++
		if len(ls) > 1 && ls[1][:2] != "2 " {
			t.Fatalf("%s: lines after the queue line %q", id, ls)
		}
	}
	if queued < 1 {
		t.Fatalf("no build said it was waiting: %v", h.rec.logSeq)
	}
}

func TestDrainRejectsPlacementsOnly(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.Drain{}))
	if hb := h.m.Heartbeat(); !hb.Draining {
		t.Fatal("heartbeat does not report draining")
	}
	req := createReq(gid2)
	req.SystemClosure = h.closure
	res := h.mustFail(cmd(req), CodeInsufficientCapacity)
	if res.Error.Message != "host draining" {
		t.Fatalf("message %s", res.Error.Message)
	}
	h.mustFail(cmd(&hostdv1.Restore{ProjectId: "p", GuestId: gid2, BlobPath: "x", Class: "large", VolumeBytes: 40 << 30}), CodeInsufficientCapacity)
	h.mustOK(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "manual"}))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
}

func TestSecretsPrincipalsExec(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.UpdateSecrets{GuestId: gid1, Secrets: []*hostdv1.Secret{{Name: "TOKEN", Value: []byte("t")}}}))
	sec := h.guestd(gid1).Secrets()
	if string(sec["TOKEN"]) != "t" || string(sec[SecretHostKey]) != "hostkey" {
		t.Fatalf("secrets after update %v", sec)
	}
	h.mustFail(cmd(&hostdv1.UpdateSecrets{GuestId: gid1, Secrets: []*hostdv1.Secret{{Name: SecretUserCA, Value: []byte("t")}}}), CodeInvalidArgument)
	h.mustOK(cmd(&hostdv1.SetPrincipals{GuestId: gid1, Principals: []string{"a", "b"}}))
	if p := h.guestd(gid1).Principals(); len(p) != 2 || h.guest(gid1).Principals[1] != "b" {
		t.Fatalf("principals %v", p)
	}
	h.mustFail(cmd(&hostdv1.Exec{GuestId: gid1, Argv: []string{"uptime"}}), CodeInvalidArgument)
	res := h.mustOK(cmd(&hostdv1.Exec{GuestId: gid1, Argv: []string{"uptime"}, AuditId: "audit-1", TimeoutS: 5}))
	if !strings.Contains(string(res.GetExec().Stdout), "fake exec: uptime") {
		t.Fatalf("exec result %v", res.GetExec())
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustFail(cmd(&hostdv1.Exec{GuestId: gid1, Argv: []string{"uptime"}, AuditId: "audit-2"}), CodeInvalidArgument)
	h.mustOK(cmd(&hostdv1.UpdateSecrets{GuestId: gid1, Secrets: []*hostdv1.Secret{{Name: "X", Value: []byte("1")}}})) // cached for next start
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if sec := h.guestd(gid1).Secrets(); string(sec["X"]) != "1" {
		t.Fatal("secret set while stopped not delivered at start")
	}
}

func TestSamplesMergeHostAndGuestd(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.sd.Set("guest@"+gid1, 1_000_000_000, 500<<20)
	h.net.Stats[h.guest(gid1).Tap] = [2]uint64{1000, 2000}
	h.net.Counters[gid1] = 2000
	h.lvm.Volumes["g-"+gid1].Used = 3 << 30
	ctx := context.Background()
	// The first sample counts from boot: the cursor starts at zero when
	// the tap is created (I-448).
	s := h.m.CollectSamples(ctx)
	if len(s.Guests) != 1 || s.Guests[0].CpuNsDelta != 1_000_000_000 || s.Guests[0].NetRxBytesDelta != 1000 || s.Guests[0].NetTxBytesDelta != 2000 {
		t.Fatalf("first sample does not count from boot: %v", s.Guests)
	}
	h.sd.Set("guest@"+gid1, 3_000_000_000, 600<<20)
	h.net.Stats[h.guest(gid1).Tap] = [2]uint64{1500, 2600}
	s = h.m.CollectSamples(ctx)
	gs := s.Guests[0]
	if gs.CpuNsDelta != 2_000_000_000 || gs.NetRxBytesDelta != 500 || gs.NetTxBytesDelta != 600 || gs.MemRssBytes != 600<<20 || gs.DiskUsedBytes != 3<<30 || gs.DiskAllocBytes != 40<<30 {
		t.Fatalf("sample %v", gs)
	}
	if !gs.Signals.GuestdOk || gs.Signals.SshSessions != 1 || len(gs.Procs) != 1 || gs.Procs[0].Comm != "claude" {
		t.Fatalf("guestd part %v %v", gs.Signals, gs.Procs)
	}
	if s.Host.MemFree != 40<<30 || s.Host.Load1 != 0.5 {
		t.Fatalf("host sample %v", s.Host)
	}
	// guestd gone: guestd_ok=false, empty lists, guest keeps running, guestd_lost warning after the grace.
	_ = h.guestd(gid1).Close()
	time.Sleep(50 * time.Millisecond)
	s = h.m.CollectSamples(ctx)
	if s.Guests[0].Signals.GuestdOk || len(s.Guests[0].Procs) != 0 || s.Guests[0].State != StateRunning {
		t.Fatalf("sample with guestd down %v", s.Guests[0])
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, w := range h.rec.warnings() {
			if w == "guestd_lost" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no guestd_lost warning")
}

func TestHypervisorExitMarksError(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.sd.Exit("guest@"+gid1, 137)
	h.waitState(gid1, StateError)
	g := h.guest(gid1)
	if g.Reason != "hypervisor exited 137" {
		t.Fatalf("reason %q", g.Reason)
	}
	if left := h.net.Leftovers(gid1, g.Tap); len(left) != 0 {
		t.Fatalf("leftovers %v", left)
	}
	// The api restarts it.
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if h.guest(gid1).State != StateRunning {
		t.Fatal("restart after crash failed")
	}
	h.sd.Exit("virtiofsd@"+gid1, 1)
	h.waitState(gid1, StateError)
	if h.guest(gid1).Reason != "virtiofsd exited" {
		t.Fatalf("reason %q", h.guest(gid1).Reason)
	}
}

func TestGuestdNotificationsBecomeEvents(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	fg := h.guestd(gid1)
	if err := fg.Notify(agentEvent("claude", "completed", "done")); err != nil {
		t.Fatal(err)
	}
	if err := fg.Notify(warning("disk_high", "91%")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.rec.mu.Lock()
		var gotAgent, gotWarn bool
		for _, e := range h.rec.events {
			if a := e.GetAgentEvent(); a != nil && a.GuestId == gid1 && a.Kind == "completed" {
				// The window guestd resolved travels to the api (I-121).
				if a.TmuxWindow != "claude" || a.Summary != "done" {
					t.Errorf("agent event lost fields: window %q summary %q", a.TmuxWindow, a.Summary)
				}
				gotAgent = true
			}
			if w := e.GetHostWarning(); w != nil && w.Kind == "disk_high" && strings.Contains(w.Detail, gid1) {
				gotWarn = true
			}
		}
		h.rec.mu.Unlock()
		if gotAgent && gotWarn {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("events not forwarded")
}

func TestReconcileAfterRestart(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.create(gid2)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid2}))
	// Simulate hostd dying: mark gid1 mid-transition and leave a tap for gid2.
	_, _ = h.st.SetGuestState(gid1, StateStarting, "")
	h.net.Taps[h.guest(gid2).Tap] = true
	// and gid1's tap shaped the way an older hostd left it (I-217), with
	// its rules as a hostd before I-238 left them (none of the new ones:
	// the fake models them as the one element)
	delete(h.net.Shaped, h.guest(gid1).Tap)
	delete(h.net.Elements, gid1)
	h.m.Close()
	m2, err := New(h.cfg, h.m.d)
	if err != nil {
		t.Fatal(err)
	}
	m2.Run()
	defer m2.Close()
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.guest(gid1).State != StateRunning {
		t.Fatalf("running guest with a live unit should be running, got %s", h.guest(gid1).State)
	}
	if got := h.net.Shaped[h.guest(gid1).Tap]; got != m2.cfg.EgressMbit || got == 0 {
		t.Fatalf("running guest's shape not re-applied by reconcile: %d mbit", got)
	}
	if h.net.Elements[gid1] == "" {
		t.Fatal("running guest's nft rules not re-applied by reconcile (I-238)")
	}
	if h.net.Taps[h.guest(gid2).Tap] {
		t.Fatal("leftover tap of a stopped guest not removed")
	}
	hello := m2.Hello()
	if len(hello.Guests) != 2 || hello.HostId != "host-1" {
		t.Fatalf("hello %v", hello)
	}
	// Host rebooted: unit gone, guest recorded running -> stopped.
	h.sd.Exit("guest@"+gid1, 0)
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := h.guest(gid1); g.State != StateStopped || !strings.Contains(g.Reason, "not running") {
		t.Fatalf("after reboot %+v", g)
	}
}

func TestRebuildFromDisk(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.m.Close()
	// state.db lost.
	_ = h.st.Close()
	_ = os.Remove(h.st.Path())
	st2, err := stateOpen(h.st.Path())
	if err != nil {
		t.Fatal(err)
	}
	d := h.m.d
	d.State = st2
	m2, err := New(h.cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	m2.Run()
	defer m2.Close()
	ids, err := m2.Rebuild(context.Background())
	if err != nil || len(ids) != 1 || ids[0] != gid1 {
		t.Fatalf("rebuild %v %v", ids, err)
	}
	g, err := st2.GetGuest(gid1)
	if err != nil || g.State != StateStopped || g.IP != "10.64.4.2" {
		t.Fatalf("rebuilt %+v %v", g, err)
	}
	if i, _ := st2.AllocIndex("other", 1020); i != 1 {
		t.Fatal("rebuilt guest's address not reserved")
	}
}

func TestStoreHighWarnsAndRefusesBuild(t *testing.T) {
	h := newHarness(t, nil)
	used := uint64(50 << 30)
	h.m.d.StoreStat = func() (uint64, uint64, error) { return 100 << 30, used, nil }
	h.mustOK(cmd(&hostdv1.Build{ProjectId: "p", RevisionId: "r1", Fragment: []byte("{}"), BaseRef: "abc"}))
	used = 85 << 30
	res := h.mustFail(cmd(&hostdv1.Build{ProjectId: "p", RevisionId: "r2", Fragment: []byte("{}"), BaseRef: "abc"}), CodeInsufficientCapacity)
	if res.Error.Message != "host store full" {
		t.Fatalf("message %s", res.Error.Message)
	}
	h.m.CollectSamples(context.Background())
	for _, w := range h.rec.warnings() {
		if w == "store_high" {
			return
		}
	}
	t.Fatal("no store_high warning")
}

func TestPoolHighWarning(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.PoolFree = h.lvm.PoolSize / 10
	h.m.CollectSamples(context.Background())
	for _, w := range h.rec.warnings() {
		if w == "pool_high" {
			return
		}
	}
	t.Fatal("no pool_high warning")
}

// Two guest ids minted in the same window share their first eight hex
// (the UUIDv7 timestamp); their taps and MACs must still differ, and a
// create whose tap another guest holds is refused (I-120).
func TestTapCollision(t *testing.T) {
	// Same first eight hex, as two UUIDv7 ids minted in one minute have.
	a := "0192f0a1-1111-7000-8000-000000000001"
	b := "0192f0a1-2222-7000-8000-000000000002"
	ta, err := TapName(a)
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := TapName(b)
	ma, _ := MACAddr(a)
	mb, _ := MACAddr(b)
	if ta == tb || ma == mb {
		t.Fatalf("collision: %s/%s %s/%s", ta, tb, ma, mb)
	}
	if !regexp.MustCompile(`^tap-[0-9a-f]{8}$`).MatchString(ta) || !regexp.MustCompile(`^52:54(:[0-9a-f]{2}){4}$`).MatchString(ma) {
		t.Fatalf("shape: %s %s", ta, ma)
	}
	if again, _ := TapName(a); again != ta {
		t.Fatal("tap name is not deterministic")
	}
	if _, err := TapName("not-an-id"); err == nil {
		t.Fatal("a non-id was accepted")
	}

	h := newHarness(t, nil)
	h.create(a)
	// Plant a record whose tap is what b would get: the create must refuse.
	tbName, _ := TapName(b)
	if err := h.st.PutGuest(&state.Guest{GuestID: "0192f0a1-2222-7000-8000-000000000009", ProjectID: "proj-x", Tap: tbName, MAC: "52:54:00:00:00:09", Class: "small", State: StateStopped}); err != nil {
		t.Fatal(err)
	}
	h.mustFail(cmd(&hostdv1.CreateGuest{ProjectId: "proj-b", GuestId: b, Class: "small", VolumeBytes: 20 << 30, SystemClosure: h.closure}), CodeAlreadyExists)
}
