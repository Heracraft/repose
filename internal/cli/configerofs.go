package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/heracraft/repose/internal/menu"
)

// An erofs machine (DECISIONS I-1462) boots the Ubuntu base and is
// configured by its menu alone: `repose config add/remove` work as on a
// Nix machine, and every Nix file (repose.nix, machine.nix) is retired
// there (I-1488).

// nixConfigRetired is the api's `nix_config_retired` sentence
// (docs/interfaces/api.md "PUT /projects/:id/config"). The CLI prints the
// api's own message when it has one; this copy is for the refusals the
// CLI makes before it sends anything (an editor it would otherwise open
// on an empty file, machine.nix on or off for one machine).
const nixConfigRetired = "This machine runs the Ubuntu base; custom Nix files are not used, and `repose config add <item>` installs packages and services."

// isErofs reports whether the project's machine boots the Ubuntu base.
// An api older than I-1462 sends no runtime, which is nix.
func isErofs(p *Project) bool { return p != nil && p.GuestRuntime == menu.RuntimeErofs }

// errNixConfigRetired is the refusal, exit 1 as every refusal of the
// api's is (docs/interfaces/cli-config.md "Exit codes").
func errNixConfigRetired(apiMessage string) *exitError {
	if apiMessage == "" {
		apiMessage = nixConfigRetired
	}
	return exitf(ExitGeneric, "%s", apiMessage)
}

// asNixConfigRetired turns the api's nix_config_retired answer into the
// refusal, and reports whether err was one.
func asNixConfigRetired(err error) (*exitError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code == "nix_config_retired" {
		return errNixConfigRetired(apiErr.Message), true
	}
	return nil, false
}

// validMenuPackage is the package rule for the project's runtime: apt
// names on erofs, nixpkgs attribute paths on nix (menu.ValidateFor).
func validMenuPackage(p *Project, name string) bool {
	if isErofs(p) {
		return menu.ValidAptPackage(name)
	}
	return menu.ValidPackage(name)
}

// badPackageName is the refusal of a name that is neither a menu entry
// nor a package name of the project's runtime.
func badPackageName(p *Project, arg string) *exitError {
	if isErofs(p) {
		if len(arg) > menu.MaxAptPackageLen {
			return exitf(ExitUsage, "%q is longer than an apt package name can be (%d characters at most).", arg, menu.MaxAptPackageLen)
		}
		return exitf(ExitUsage, "%q is not an apt package name (lower case letters, digits, + - and dots, like ffmpeg or libpq-dev).", arg)
	}
	return exitf(ExitUsage, "%q is not a nixpkgs attribute name (letters, digits, _ - + and dots, like gcc or python312Packages.black).", arg)
}

// printMenu is `repose config show` on an erofs machine, which has no
// repose.nix to print: its menu, one item per line, options after the
// name, as `repose config add` takes them.
func printMenu(w io.Writer, sel []MenuItem) {
	for _, it := range sel {
		line := it.name()
		keys := make([]string, 0, len(it.Options))
		for k := range it.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			line += " " + k + "=" + it.Options[k]
		}
		_, _ = fmt.Fprintln(w, line)
	}
}
