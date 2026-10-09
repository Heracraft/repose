package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// The home folder is where a terminal opens, not a project (DECISIONS
// I-601): `repose run` there never links the folder to a machine, never
// syncs it (a dotfiles repository included), and a command that acts on
// a project needs its name.

// isHomeFolder says path is the laptop's home directory or a folder
// above it (`/Users`, `/`, `C:\Users`). Its subfolders are ordinary
// folders.
func isHomeFolder(path string) bool {
	if path == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	home, path = canonicalDir(home), canonicalDir(path)
	if path == home {
		return true
	}
	rel, err := filepath.Rel(path, home)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// canonicalDir is path cleaned, with symlinks resolved when it exists
// (macOS's /var is /private/var).
func canonicalDir(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	return filepath.Clean(path)
}

// inHomeFolder says cwd is the home folder for repose: the home
// directory or a folder above it, or a folder inside a repository whose
// root is one of those (a dotfiles repository in ~).
func inHomeFolder(cwd string, deps resolveDeps) bool {
	if isHomeFolder(cwd) {
		return true
	}
	if deps.RootFor != nil {
		if root := deps.RootFor(cwd); root != "" {
			return isHomeFolder(root)
		}
	}
	return false
}

// inHome is inHomeFolder for this command's working directory.
func (e *Env) inHome() bool {
	if e.home == nil {
		v := inHomeFolder(e.Cwd, defaultResolveDeps())
		e.home = &v
	}
	return *e.home
}

// errHomeRun is a plain `repose run` in the home folder.
func errHomeRun() error {
	return exitf(ExitUsage, "Your home folder is not a project. cd into one, or run `repose run NAME` or `repose run --temp`.")
}

// errHomeSync is `repose sync` in the home folder.
func errHomeSync() error {
	return exitf(ExitUsage, "Your home folder is never synced. cd into a checkout.")
}

// errHomeNoProject is a command that acts on a project, run in the home
// folder without naming one. usage is "`repose rm PROJECT`" or the like.
func errHomeNoProject(usage string) error {
	return exitf(ExitProjectNotFound, "Your home folder is not a project. Name one: %s (`repose ls` lists them).", usage)
}
