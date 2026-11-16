package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// gitInitOutcome is what offerGitInit did with a folder that is not a git
// repository.
type gitInitOutcome int

const (
	gitInitMade     gitInitOutcome = iota // a repository with one commit
	gitInitDeclined                       // the user said no
	gitInitNotAsked                       // no terminal, no git, or a .git already there
	gitInitTooBig                         // past what one sync sends
)

// maxInitFiles is the most files a folder may hold for the offer: more
// is a folder that is not a project (a home's Documents, a photo
// library), and walking it all before the question would stall the run.
const maxInitFiles = 100_000

// askGitInit asks the question; tests replace it.
var askGitInit = func(ctx context.Context, prompt string) (bool, error) {
	return askYesNoFrom(ctx, os.Stdin, os.Stderr, prompt, true)
}

// canAskGitInit is whether there is a terminal to ask on; tests replace it.
var canAskGitInit = func() bool { return canPrompt(os.Stdin) && isatty(os.Stderr) }

// gitInitPrompt is the question about dir, in full.
func gitInitPrompt(dir string) string {
	return homeTilde(dir) + " isn't a git repository. Make it one and sync it? [Y/n] "
}

// homeTilde spells dir with ~ for the home folder, as a user typed it.
func homeTilde(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return dir
}

// gitInitDone is what a run says after a yes: the commit, and each
// repository inside the folder that was left out of it.
type gitInitDone struct {
	files  int
	nested []string
}

// offerGitInit is `repose run` and `repose sync` in a folder that is not a
// git repository (DECISIONS I-1065). The sync works through git, so a
// folder outside one synced nothing, and a first run from a plain folder
// started an empty machine. From a terminal they now ask, defaulting to
// yes, and a yes makes dir a repository: `git init`, a starter .gitignore
// when dir has none, and one commit of everything else. A folder past
// maxUntrackedBytes, the most one sync sends, or past maxInitFiles is not
// offered: committing it would copy all of it into .git on the laptop and
// then send it. Nothing is left behind when a step fails.
func offerGitInit(ctx context.Context, dir string) (gitInitOutcome, gitInitDone, error) {
	if !canAskGitInit() {
		return gitInitNotAsked, gitInitDone{}, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return gitInitNotAsked, gitInitDone{}, nil
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		// A .git git cannot read: not ours to replace.
		return gitInitNotAsked, gitInitDone{}, nil
	}
	over, nested := scanFolder(dir, maxUntrackedBytes, maxInitFiles)
	if over {
		return gitInitTooBig, gitInitDone{}, nil
	}
	ok, err := askGitInit(ctx, gitInitPrompt(dir))
	if err != nil {
		return gitInitDeclined, gitInitDone{}, err
	}
	if !ok {
		return gitInitDeclined, gitInitDone{}, nil
	}
	n, err := makeRepo(dir, nested)
	if err != nil {
		return gitInitDeclined, gitInitDone{}, err
	}
	return gitInitMade, gitInitDone{files: n, nested: nested}, nil
}

// lines is what the run prints after a yes.
func (d gitInitDone) lines() []string {
	out := []string{"Committed " + strconv.Itoa(d.files) + " " + plural(d.files, "file", "files") + " to a new git repository."}
	if len(d.nested) > 0 {
		out = append(out, "Left out "+strings.Join(d.nested, ", ")+": "+plural(len(d.nested), "it is a git repository", "each is a git repository")+" of its own.")
	}
	return out
}

// makeRepo makes dir a git repository with one commit, or leaves it as it
// was and says why. nested are folders inside dir that are repositories
// of their own: `git add` would record each as a bare commit pointer with
// no .gitmodules, which the sync cannot fill, so they are excluded.
func makeRepo(dir string, nested []string) (int, error) {
	ignore := filepath.Join(dir, ".gitignore")
	wroteIgnore := false
	undo := func() {
		_ = os.RemoveAll(filepath.Join(dir, ".git"))
		if wroteIgnore {
			_ = os.Remove(ignore)
		}
	}
	if _, err := gitCmd(dir, "init", "-q"); err != nil {
		undo()
		return 0, exitf(ExitGeneric, "git init failed: %s", oneLine(err.Error()))
	}
	if _, err := os.Lstat(ignore); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(ignore, []byte(starterGitignore()), 0o644); err != nil {
			undo()
			return 0, exitf(ExitGeneric, "Could not write .gitignore: %v", err)
		}
		wroteIgnore = true
	}
	if len(nested) > 0 {
		var b strings.Builder
		for _, n := range nested {
			b.WriteString("/" + n + "/\n")
		}
		f, err := os.OpenFile(filepath.Join(dir, ".git", "info", "exclude"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, err = f.WriteString(b.String())
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			undo()
			return 0, exitf(ExitGeneric, "Could not write .git/info/exclude: %v", err)
		}
	}
	if _, err := gitCmd(dir, "add", "-A"); err != nil {
		undo()
		return 0, exitf(ExitGeneric, "git add failed: %s", oneLine(err.Error()))
	}
	if _, err := gitCmd(dir, "commit", "-q", "-m", "Initial commit"); err != nil {
		undo()
		msg := err.Error()
		if strings.Contains(msg, "Please tell me who you are") || strings.Contains(msg, "Author identity unknown") {
			return 0, exitf(ExitGeneric, `Git has no name and email to commit with. Set them with git config --global user.name "Your Name" and git config --global user.email you@example.com, then run again.`)
		}
		return 0, exitf(ExitGeneric, "git commit failed: %s", oneLine(msg))
	}
	out, _ := gitCmd(dir, "ls-files")
	n := 0
	if out != "" {
		n = strings.Count(out, "\n") + 1
	}
	return n, nil
}

// starterGitignore keeps secrets out of the first commit and the folders
// a sync never sends (defaultSkipDirs) out of the repository. A commit is
// history, and history is pushed; an untracked key file only travels to
// the machine. The .env files still reach the machine on their own, over
// SSH (DECISIONS I-197).
func starterGitignore() string {
	dirs := make([]string, 0, len(defaultSkipDirs))
	for d := range defaultSkipDirs {
		dirs = append(dirs, d+"/")
	}
	sort.Strings(dirs)
	secrets := []string{
		".env", ".env.*", "!.env.example",
		"*.pem", "*.key", "*.p12", "*.pfx", "*.keystore",
		"id_rsa*", "id_ed25519*", "id_ecdsa*", ".netrc", "credentials.json",
	}
	return strings.Join(secrets, "\n") + "\n.DS_Store\n" + strings.Join(dirs, "\n") + "\n"
}

// starterIgnored reports whether the starter .gitignore leaves out a file
// of this name.
func starterIgnored(name string) bool {
	if name == ".env.example" {
		return false
	}
	for _, p := range []string{".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "*.keystore", "id_rsa*", "id_ed25519*", "id_ecdsa*", ".netrc", "credentials.json"} {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// scanFolder walks dir as a first commit would see it, skipping what the
// starter .gitignore skips, and reports whether it passes limit bytes or
// maxFiles files, stopping as soon as it does. nested is each folder
// below dir that is a repository of its own, relative and with slashes.
func scanFolder(dir string, limit int64, maxFiles int) (over bool, nested []string) {
	var total int64
	files := 0
	stop := errors.New("over")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable: git add would skip it or say so
		}
		if path == dir {
			return nil
		}
		if d.IsDir() {
			if defaultSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				rel, _ := filepath.Rel(dir, path)
				nested = append(nested, filepath.ToSlash(rel))
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || starterIgnored(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files++
		if total += info.Size(); total > limit || files > maxFiles {
			return stop
		}
		return nil
	})
	return errors.Is(err, stop), nested
}
