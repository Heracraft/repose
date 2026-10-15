// Package agentproc names the processes an agent runs as. It is a leaf on
// purpose: the hook socket needs this one predicate (DECISIONS I-690), and
// when it lived in internal/guestd/sample, `repose-hook` linked that
// package's generated protobuf and the whole gRPC stack with it — 7.29 MB
// of binary became 13.03 MB and the guest closure crossed its 6 GB limit
// (DECISIONS I-729). Nothing here may import anything but the standard
// library.
package agentproc

import "strings"

// Binaries maps an agent to the process names to look for in a pane's
// process tree (features/agents.md's "Binary" column).
var Binaries = map[string][]string{
	"claude":   {"claude"},
	"opencode": {"opencode"},
	"codex":    {"codex"},
	// Gemini CLI is a bundle the guest's node runs (DECISIONS I-46), so
	// its process is `node`, never `gemini`; without that name here the
	// window was never an agent window and the heuristic never fired on
	// host-01 (I-122).
	"gemini": {"gemini", "node"},
	"pi":     {"pi"},
}

// Matches reports whether a process name (comm, or the exe link's
// basename) is one of wants, directly or as nixpkgs' wrapper renames it:
// makeWrapper moves the real program to `.X-wrapped`, so the nix claude
// runs as `.claude-wrapped` (comm and exe alike), and comm is cut to 15
// bytes (`.opencode-wrapp`). Without this the guest's claude was never
// found and never OOM-protected (DECISIONS I-213).
func Matches(wants []string, name string) bool {
	for _, w := range wants {
		if name == w {
			return true
		}
		wrapped := "." + w + "-wrapped"
		if name == wrapped || (len(name) == 15 && len(name) > len(w)+1 && strings.HasPrefix(wrapped, name)) {
			return true
		}
	}
	return false
}

// IsAgentCommand reports whether comm is one of the process names the
// agent runs as.
func IsAgentCommand(agent, comm string) bool { return Matches(Binaries[agent], comm) }
