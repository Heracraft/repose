package appliance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// snapWorld runs host/bin/repose-snapshots-volume against stub LVM and
// mount commands on a scratch PATH: lv is what the volume holds, root the
// snapshot directory on the root partition (DECISIONS I-1636).
type snapWorld struct {
	t               *testing.T
	dir, lv, calls  string
	state, bin, tmp string
}

func newSnapWorld(t *testing.T) *snapWorld {
	t.Helper()
	w := t.TempDir()
	s := &snapWorld{t: t, dir: filepath.Join(w, "snapshots"), lv: filepath.Join(w, "lv"), calls: filepath.Join(w, "calls"),
		state: filepath.Join(w, "state"), bin: filepath.Join(w, "bin"), tmp: filepath.Join(w, "run")}
	for _, d := range []string{s.lv, s.state, s.bin, s.tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub := func(name, body string) {
		script := "#!/bin/bash\nS=" + s.state + "; LV=" + s.lv + "; DIR=" + s.dir + "\necho \"" + name + " $*\" >> " + s.calls + "\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(s.bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("lvs", `if [ "$1" = --noheadings ]; then echo "  active"; exit 0; fi; [ -e "$S/created" ]`)
	stub("vgs", `echo "  99857989632"`) // 93 GiB
	// LVM refuses LV names it reserves, as LVM 2.03.31 on the appliance
	// did to "snapshots" (the lab, 2026-11-26 00:22Z): a name starting
	// "snapshot" or "pvmove", or holding one of its internal suffixes.
	stub("lvcreate", `n=; while [ $# -gt 0 ]; do [ "$1" = -n ] && n=$2; shift; done
case "$n" in snapshot*|pvmove*) echo "  Names starting \"${n:0:8}\" are reserved. Please choose a different LV name." >&2; exit 3;; esac
case "$n" in *_cdata*|*_cmeta*|*_corig*|*_cpool*|*_cvol*|*_iorig*|*_mimage*|*_mlog*|*_pmspare*|*_rimage*|*_rmeta*|*_tdata*|*_tmeta*|*_vdata*|*_vorigin*|*_wcorig*) echo "  Names including \"_\" suffixes are reserved." >&2; exit 3;; esac
touch "$S/created"`)
	stub("udevadm", `:`)
	stub("mkfs.ext4", `:`)
	stub("lvchange", `:`)
	stub("mountpoint", `[ "$2" = "$DIR" ] && [ -e "$S/mounted" ]`)
	// A mount anywhere but DIR shows the volume's files there; an umount
	// puts what is there back on the volume and leaves the directory empty.
	stub("mount", `t=${@: -1}; if [ "$t" = "$DIR" ]; then touch "$S/mounted"; else cp -a "$LV"/. "$t"/; fi`)
	stub("umount", `cp -a "$1"/. "$LV"/; find "$1" -mindepth 1 -delete`)
	return s
}

func (s *snapWorld) run() string {
	s.t.Helper()
	cmd := exec.Command("bash", "host/bin/repose-snapshots-volume")
	cmd.Env = append(os.Environ(), "PATH="+s.bin+":"+os.Getenv("PATH"), "REPOSE_SNAPSHOTS_DIR="+s.dir, "REPOSE_SNAPSHOTS_TMP="+s.tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("repose-snapshots-volume: %v\n%s", err, out)
	}
	return string(out)
}

func (s *snapWorld) called() string {
	b, _ := os.ReadFile(s.calls)
	return string(b)
}

// A fresh host gets the volume, half the volume group, mounted on the
// directory; a second run changes nothing.
func TestSnapshotsVolumeFresh(t *testing.T) {
	s := newSnapWorld(t)
	out := s.run()
	if !strings.Contains(out, "event=volume_created size_gib=46") || !strings.Contains(out, "event=volume_mounted") {
		t.Fatalf("output %s", out)
	}
	if !strings.Contains(s.called(), "lvcreate -q -y -V 46G -T vg-guests/thin -n repose-snapshots") || !strings.Contains(s.called(), "mount -o noatime,discard /dev/vg-guests/repose-snapshots "+s.dir) {
		t.Fatalf("calls %s", s.called())
	}
	if out := s.run(); !strings.Contains(out, "event=volume_mounted_already") || strings.Count(s.called(), "lvcreate") != 1 {
		t.Fatalf("second run %s\ncalls %s", out, s.called())
	}
}

// An appliance that kept its snapshots on the root moves every one onto
// the volume before mounting it, so none is hidden under the mount.
func TestSnapshotsVolumeMovesWhatTheRootHolds(t *testing.T) {
	s := newSnapWorld(t)
	files := map[string]string{"acct/proj/1.img.zst": "one", "acct/proj/1.img.zst.meta": "reason=stop\n", "acct/other/2.img.zst": "two"}
	for p, body := range files {
		full := filepath.Join(s.dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := s.run()
	if !strings.Contains(out, "event=snapshots_moved files=3") {
		t.Fatalf("output %s", out)
	}
	for p, body := range files {
		b, err := os.ReadFile(filepath.Join(s.lv, p))
		if err != nil || string(b) != body {
			t.Fatalf("%s on the volume: %q, %v", p, b, err)
		}
	}
	left, _ := os.ReadDir(s.dir)
	if len(left) != 0 {
		t.Fatalf("root still holds %d entries", len(left))
	}
	if !strings.Contains(s.called(), "mount -o noatime,discard /dev/vg-guests/repose-snapshots "+s.dir) {
		t.Fatalf("not mounted: %s", s.called())
	}
}
