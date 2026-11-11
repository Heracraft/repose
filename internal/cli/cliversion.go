package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// The api names the latest CLI release in every answer's
// X-Repose-CLI-Latest header (DECISIONS I-626) and, in
// X-Repose-CLI-Oldest, the oldest release it still answers as the docs
// describe (I-1088). A CLI older than the latest says so on stderr once
// per newer release, at the end of the command or before an attach
// replaces it with ssh: an old CLI lacks flags the docs describe, and its
// `unknown flag` does not say why. A CLI older than the oldest says so on
// every run, attach and sync, because from there on an answer may not be
// the one the docs describe, and once per release is not enough warning
// for that.

const (
	latestCLIHeader = "X-Repose-CLI-Latest"
	oldestCLIHeader = "X-Repose-CLI-Oldest"
)

// cliNoticeFile records the newest release the CLI has already named.
const cliNoticeFile = "cli-latest-noticed"

// updateCommand replaces the binary (DECISIONS I-1086).
const updateCommand = "repose version update"

// skewCommands are the commands the too-old line prints after: the ones
// that move the user's work between laptop and machine, where an answer
// the docs do not describe costs them something. `ls` and `status` stay
// quiet.
var skewCommands = map[string]bool{"run": true, "attach": true, "sync": true}

var (
	latestMu  sync.Mutex
	latestCLI string
	oldestCLI string
	skewCmd   string
	noticed   bool
)

// noteCLIVersions keeps the releases an api answer named.
func noteCLIVersions(h http.Header) {
	latestMu.Lock()
	defer latestMu.Unlock()
	if v := strings.TrimSpace(h.Get(latestCLIHeader)); v != "" {
		latestCLI = v
	}
	if v := strings.TrimSpace(h.Get(oldestCLIHeader)); v != "" {
		oldestCLI = v
	}
}

// noteSkewCommand records the running command's name, which decides
// whether the too-old line prints. It is set before RunE, so an attach
// that hands over to ssh has it too.
func noteSkewCommand(name string) {
	latestMu.Lock()
	skewCmd = name
	latestMu.Unlock()
}

// noteNewerCLI prints the notice this CLI's age calls for: that the api
// no longer answers it as documented, or that a newer release exists.
// A development build, and the CLI on a machine (which the machine's
// base pins), never print either.
func noteNewerCLI() { noteNewerCLITo(os.Stderr) }

func noteNewerCLITo(w io.Writer) {
	latestMu.Lock()
	defer latestMu.Unlock()
	if noticed || os.Getenv(envInGuest) == "1" {
		return
	}
	behindLatest := latestCLI != "" && versionOlder(cliVersion, latestCLI)
	if oldestCLI != "" && skewCommands[skewCmd] && versionOlder(cliVersion, oldestCLI) {
		noticed = true
		_, _ = fmt.Fprintf(w, "repose %s is too old for some of repose's answers. %s\n", plainVersion(cliVersion), updateSentence())
		// One line, the stronger one. The newer release is written to the
		// notice file all the same, so the weaker line does not arrive
		// afterwards saying less about the same version.
		if behindLatest {
			rememberLatestCLI()
		}
		return
	}
	if !behindLatest || latestNoticed() {
		return
	}
	noticed = true
	_, _ = fmt.Fprintf(w, "repose %s is older than %s, the latest release. %s\n", plainVersion(cliVersion), plainVersion(latestCLI), updateSentence())
	rememberLatestCLI()
}

// updateSentence says how to replace this binary: the command, or the
// package manager that owns it, which `repose version update` refuses to
// touch.
func updateSentence() string {
	if owner := binaryOwner(executablePath()); owner != "" {
		return "Update it with " + owner + "."
	}
	return "`" + updateCommand + "` updates it."
}

// latestNoticed is whether this release has already been named once.
func latestNoticed() bool {
	dir, err := configDir()
	if err != nil {
		return true // nowhere to record it, so say it no more than once a process
	}
	b, err := os.ReadFile(filepath.Join(dir, cliNoticeFile))
	return err == nil && strings.TrimSpace(string(b)) == latestCLI
}

func rememberLatestCLI() {
	dir, err := configDir()
	if err != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(dir, cliNoticeFile), []byte(latestCLI+"\n"), 0o600)
}

// versionOlder reports whether have is a release (vX.Y.Z) older than
// want. Anything that does not parse is never older. The "v" is not part
// of the comparison: a release build carries 0.1.43 and a tag v0.1.44.
func versionOlder(have, want string) bool {
	h, ok1 := parseVersion(have)
	w, ok2 := parseVersion(want)
	if !ok1 || !ok2 {
		return false
	}
	for i := range h {
		if h[i] != w[i] {
			return h[i] < w[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		return out, false // a pre-release or a local build
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
