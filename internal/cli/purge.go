package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const includeLine = "Include ~/.ssh/repose/config"
const includeComment = "# added by repose"

// purgeCLIFiles removes everything repose owns on the laptop (07-cli.md
// §8 "Rollback": `~/.config/repose/`, `~/.ssh/repose/` and the one
// `Include` line). machine.nix is the user's own file (DECISIONS I-490)
// and stays, with the directory around it.
func purgeCLIFiles(configDirPath, sshDirPath string) error {
	if fi, err := os.Stat(filepath.Join(configDirPath, machineNixFile)); err == nil && fi.Mode().IsRegular() {
		entries, err := os.ReadDir(configDirPath)
		if err != nil {
			return err
		}
		for _, en := range entries {
			if en.Name() == machineNixFile {
				continue
			}
			if err := os.RemoveAll(filepath.Join(configDirPath, en.Name())); err != nil {
				return err
			}
		}
	} else if err := os.RemoveAll(configDirPath); err != nil {
		return err
	}
	if err := os.RemoveAll(sshDirPath); err != nil {
		return err
	}
	sc, err := userSSHConfig()
	if err != nil {
		return err
	}
	return removeIncludeLine(sc)
}

// removeIncludeLine drops the "# added by repose" comment and the Include
// line that follows it, leaving every other line untouched.
func removeIncludeLine(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := splitLines(string(b))
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == includeComment && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == includeLine {
			i++
			continue
		}
		out = append(out, lines[i])
	}
	perm := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		perm = info.Mode().Perm()
	}
	return writeFileAtomic(path, []byte(strings.Join(out, "\n")), perm)
}

func splitLines(s string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// includeUnwritableError is a ~/.ssh/config the CLI will not or cannot
// edit: a link into a read-only store (home-manager, a dotfiles manager)
// or a file without write permission. The caller turns it into
// instructions rather than failing the command (I-151).
type includeUnwritableError struct {
	path, target string
	err          error
}

func (e *includeUnwritableError) Error() string { return e.err.Error() }
func (e *includeUnwritableError) Unwrap() error { return e.err }

// includeEffective reports whether content has the Include line where ssh
// honours it for every host: before the first Host or Match line. An
// Include inside a Host block applies only to that block, and a
// commented-out one to nothing.
func includeEffective(content string) bool {
	for _, l := range splitLines(content) {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		fields := strings.Fields(strings.ReplaceAll(l, "=", " "))
		switch strings.ToLower(fields[0]) {
		case "include":
			for _, f := range fields[1:] {
				if f == "~/.ssh/repose/config" || strings.HasSuffix(f, "/.ssh/repose/config") {
					return true
				}
			}
		case "host", "match":
			return false
		}
	}
	return false
}

// ensureIncludeLine inserts the comment and Include line as the first two
// lines of ~/.ssh/config, once, never touching any other line (07-cli.md
// §5.4 and its checklist "exactly one Include line, first line, and no
// other line changes on repeated runs"). A line that exists but sits
// after a Host or Match line does not count (I-151): the new one goes on
// top and the old one is left where the user put it. A symlinked config
// is edited at its target when that is writable and reported otherwise,
// never replaced by a regular file.
func ensureIncludeLine(path string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	existing := string(b)
	if includeEffective(existing) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	newContent := includeComment + "\n" + includeLine + "\n"
	if existing != "" {
		newContent += existing
	}
	writePath := path
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return &includeUnwritableError{path: path, err: err}
		}
		writePath = target
	}
	perm := os.FileMode(0o644)
	if info, statErr := os.Stat(writePath); statErr == nil {
		perm = info.Mode().Perm()
	}
	if err := writeFileAtomic(writePath, []byte(newContent), perm); err != nil {
		t := ""
		if writePath != path {
			t = writePath
		}
		return &includeUnwritableError{path: path, target: t, err: err}
	}
	return nil
}
