package undo

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Holder is a process keeping a project's live area busy. Comm is the
// process name, which is all a log line or a message may carry: never the
// path it holds, never its arguments (docs/ops/OBSERVABILITY.md).
type Holder struct {
	PID  int    `json:"pid"`
	Comm string `json:"comm"`
}

// holders returns the processes whose working directory or open files are
// under root, which must be absolute and free of symlinks because that is
// the shape /proc reports. It is the quiescence check a rewind makes
// first: a writer inside the live subvolume survives the swap and goes on
// writing where nothing can reach it, so its last writes are lost
// silently (docs/research/wrapper-undo.md).
//
// A process of another user is invisible to a reader that is not root:
// /proc/<pid>/cwd and /proc/<pid>/fd deny it. The project's own processes
// belong to the login user, which is who the agent runs as, so those are
// the ones this sees.
func holders(root string) ([]Holder, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []Holder
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		proc := filepath.Join("/proc", e.Name())
		if !processUnder(proc, root) {
			continue
		}
		// A process that exits between the two reads has no comm left;
		// an unnamed holder still counts, so this is not an error.
		comm, _ := os.ReadFile(filepath.Join(proc, "comm"))
		out = append(out, Holder{PID: pid, Comm: strings.TrimSpace(string(comm))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// processUnder reports whether the process at proc has its working
// directory or any open file under root.
func processUnder(proc, root string) bool {
	if link, err := os.Readlink(filepath.Join(proc, "cwd")); err == nil && pathUnder(link, root) {
		return true
	}
	fds, err := os.ReadDir(filepath.Join(proc, "fd"))
	if err != nil {
		return false
	}
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(proc, "fd", fd.Name()))
		if err != nil {
			continue
		}
		if pathUnder(link, root) {
			return true
		}
	}
	return false
}

// pathUnder reports whether p is root or inside it. A deleted file reads
// back as "<path> (deleted)", which still holds the subvolume open and
// still matches on the prefix.
func pathUnder(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}
