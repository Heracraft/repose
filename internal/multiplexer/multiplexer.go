// Package multiplexer holds the names every component uses for a
// project's terminal multiplexer (DECISIONS I-501..I-503): the api's
// field and column, project.json's key, the CLI's flag and config key,
// and guestd's choice of session unit. It has no dependencies so the
// api, hostd, guestd, repose-hook and the CLI can all import it.
//
// The values here are contract (docs/interfaces/guest-conventions.md,
// "herdr"; docs/interfaces/api.md, "Projects"). Change them only with
// the docs and a DECISIONS entry.
package multiplexer

const (
	// Tmux is the default multiplexer, and what an absent or unknown
	// value means everywhere.
	Tmux = "tmux"
	// Herdr is the other one (I-501).
	Herdr = "herdr"
)

// Names lists the accepted values in the order help text shows them.
var Names = []string{Tmux, Herdr}

// Valid reports whether s is one of Names. The empty string is not
// valid input to the api; callers that read stored state use Normalize.
func Valid(s string) bool {
	return s == Tmux || s == Herdr
}

// Normalize maps a stored or received value to a multiplexer: herdr
// stays herdr, anything else (absent, empty, unknown) is tmux, the
// one-release compatibility rule for project.json and the api.
func Normalize(s string) string {
	if s == Herdr {
		return Herdr
	}
	return Tmux
}

// Session units in dev's user manager (guest-conventions.md). guestd
// starts the one project.json names (I-503).
const (
	TmuxUnit  = "repose-tmux-session.service"
	HerdrUnit = "repose-herdr-server.service"
)

// Unit is the session unit for a multiplexer value, normalized first.
func Unit(s string) string {
	if Normalize(s) == Herdr {
		return HerdrUnit
	}
	return TmuxUnit
}

// herdr's socket and the protocol the guest's package is checked for
// (I-501, I-504).
const (
	HerdrSocket             = "/home/dev/.config/herdr/herdr.sock"
	HerdrEndpointGeneration = 1
	HerdrMinProtocol        = 22
)

// HerdrWindowPrefix starts a hook's window when it names a herdr pane
// ("herdr:" + $HERDR_PANE_ID, I-506).
const HerdrWindowPrefix = "herdr:"

// MaxKey is the longest agent key (tmux window name or herdr agent
// key) any component sends or keeps: hostd's capWindow.
const MaxKey = 64
