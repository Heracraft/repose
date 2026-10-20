package undo

import (
	"bufio"
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

// HoldersOf is the scan of one live area named by an absolute,
// symlink-free path, for a caller that holds the path rather than the
// store. The agent runs it on itself under sudo, which is how a process
// of another user is seen at all (Btrfs.Scan).
func HoldersOf(root string) ([]Holder, error) { return holders(root) }

// holders returns the processes whose working directory, open files or
// memory mappings are under root, which must be absolute and free of
// symlinks because that is the shape /proc reports. It is the quiescence
// check a rewind makes first: a writer inside the live subvolume
// survives the swap and goes on writing where nothing can reach it, so
// its last writes are lost silently (docs/research/wrapper-undo.md).
//
// A process of another user is invisible to a reader that is not root:
// /proc/<pid>/cwd and /proc/<pid>/fd deny it. Btrfs.Holders runs a second
// scan through Btrfs.Scan for that case; this one sees the processes of
// the user it runs as.
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
// directory, any open file or any memory mapping under root.
func processUnder(proc, root string) bool {
	if link, err := os.Readlink(filepath.Join(proc, "cwd")); err == nil && pathUnder(link, root) {
		return true
	}
	if fdUnder(proc, root) {
		return true
	}
	return mappedUnder(proc, root)
}

func fdUnder(proc, root string) bool {
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

// mappedUnder reports whether the process has a file under root mapped
// into its memory. A mapping is not an open file: a process that mmaps
// a database and closes the descriptor has no entry in fd/ and still
// writes through the mapping after a swap, into a subvolume no path
// reaches. sqlite's WAL, a JVM and every dynamically linked binary run
// from the live area look like this.
func mappedUnder(proc, root string) bool {
	f, err := os.Open(filepath.Join(proc, "maps"))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		// address perms offset dev inode path — and only the path can
		// hold a slash, so the first one starts it. An anonymous or
		// pseudo mapping ("[heap]", "[stack]") has none.
		line := sc.Text()
		if i := strings.IndexByte(line, '/'); i >= 0 && pathUnder(line[i:], root) {
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
