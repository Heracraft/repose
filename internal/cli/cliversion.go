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
// X-Repose-CLI-Latest header (DECISIONS I-626). A CLI older than that
// says so on stderr once per newer release, at the end of the command or
// before an attach replaces it with ssh: an old CLI lacks flags the docs
// describe, and its `unknown flag` does not say why.

const latestCLIHeader = "X-Repose-CLI-Latest"

// cliNoticeFile records the newest release the CLI has already named.
const cliNoticeFile = "cli-latest-noticed"

// installCommand is the documented install, which also updates.
const installCommand = "curl -fsSL https://repose.herakraft.co/install.sh | sh"

var (
	latestMu  sync.Mutex
	latestCLI string
	noticed   bool
)

// noteLatestCLI keeps the latest release an api answer named.
func noteLatestCLI(h http.Header) {
	v := strings.TrimSpace(h.Get(latestCLIHeader))
	if v == "" {
		return
	}
	latestMu.Lock()
	latestCLI = v
	latestMu.Unlock()
}

// noteNewerCLI prints the notice when this CLI is older than the latest
// release an api answer named and it has not named that release before.
// A development build, and the CLI on a machine (which the machine's
// base pins), never print it.
func noteNewerCLI() { noteNewerCLITo(os.Stderr) }

func noteNewerCLITo(w io.Writer) {
	latestMu.Lock()
	defer latestMu.Unlock()
	if noticed || latestCLI == "" || os.Getenv(envInGuest) == "1" {
		return
	}
	if !versionOlder(cliVersion, latestCLI) {
		return
	}
	dir, err := configDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, cliNoticeFile)
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == latestCLI {
		return
	}
	noticed = true
	_, _ = fmt.Fprintf(w, "repose %s is older than %s, the latest release. `%s` updates it.\n", cliVersion, latestCLI, installCommand)
	_ = writeFileAtomic(path, []byte(latestCLI+"\n"), 0o600)
}

// versionOlder reports whether have is a release (vX.Y.Z) older than
// want. Anything that does not parse is never older.
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
