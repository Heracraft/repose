package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

// `repose cp` (DECISIONS I-201): a thin wrapper over scp with the project
// resolved the way every other command resolves it. `<project>:<path>`
// names a file in that project's guest, `:<path>` one in the current
// project's; a relative guest path is taken from ~/<slug>, the checkout.
// Several sources go into one destination, as with scp, so a shell glob
// works (I-346).
//
//	repose cp :logs/x.log .
//	repose cp izma:/tmp/trace.json ./trace.json
//	repose cp ./report-*.pdf izma:/tmp/
//	repose cp -r ./fixtures :test/fixtures

// scpExtraArgs is added to every scp; tests set -O (the classic protocol)
// because the local sshd harness has no SFTP server. A real guest's sshd
// does, so modern scp's SFTP mode is what users get.
var scpExtraArgs []string

// cpSide is one argument of `repose cp`.
type cpSide struct {
	Remote  bool
	Project string // "" is the current project
	Path    string
}

// parseCpSide follows scp's rule: a colon before any slash makes it
// remote; a path that starts with / or . is always local, so ./a:b is a
// file. One letter before the colon is a Windows drive, C:\a.txt or
// C:/a.txt, never a project: no project name is one character (I-621).
func parseCpSide(arg string) cpSide {
	if strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, ".") {
		return cpSide{Path: arg}
	}
	i := strings.Index(arg, ":")
	if i < 0 || strings.ContainsAny(arg[:i], "/\\") || i == 1 {
		return cpSide{Path: arg}
	}
	return cpSide{Remote: true, Project: arg[:i], Path: arg[i+1:]}
}

// guestPath makes a guest path scp understands: relative to the
// checkout unless absolute or ~-based. checkout is the checkout's name
// under the home, "" when the machine has none and relative paths start
// at the home directory (I-368).
func (s cpSide) guestPath(checkout string) string {
	p := s.Path
	switch {
	case strings.HasPrefix(p, "/"), p == "~", strings.HasPrefix(p, "~/"):
		return p
	case checkout == "" && (p == "" || p == "."):
		return "."
	case checkout == "":
		return p
	case p == "" || p == ".":
		return checkout
	default:
		return checkout + "/" + p
	}
}

// relative reports whether the guest side needs the checkout's name.
func (s cpSide) relative() bool {
	p := s.Path
	return s.Remote && !strings.HasPrefix(p, "/") && p != "~" && !strings.HasPrefix(p, "~/")
}

func newCpCmd(env func() (*Env, error), g *globalFlags) *cobra.Command {
	var recursive bool
	cmd := &cobra.Command{
		Use:   "cp [-r] SRC... DST",
		Short: "Copy files to or from a project's machine (PROJECT:PATH, or :PATH for this checkout's)",
		Long: `Copy files between the laptop and a machine with scp. One side names the
machine: PROJECT:PATH for a project's, :PATH for this checkout's. A relative
path on the machine starts at the project's checkout. Several sources copy
into the destination directory, so a glob such as ./logs/* works.

  repose cp :logs/x.log .
  repose cp izma:/tmp/trace.json .
  repose cp ./report-*.pdf izma:/tmp/
  repose cp -r ./fixtures :test/fixtures`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return cobraUsageError{fmt.Errorf("repose cp takes SRC... DST, one side PROJECT:PATH or :PATH; got %s", gotArgs(args))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			return CpCmd(cmd.Context(), e, args[:len(args)-1], args[len(args)-1], recursive, g.project)
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "copy directories")
	cmd.ValidArgsFunction = completeCpArg(env)
	return cmd
}

// CpCmd resolves the one guest side, refreshes the certificate the way
// run does, and runs scp over the project's multiplexed connection. The
// sources are all on one side and the destination on the other.
func CpCmd(ctx context.Context, e *Env, srcArgs []string, dstArg string, recursive bool, projectFlag string) error {
	if len(srcArgs) == 0 {
		return exitf(ExitUsage, "repose cp takes SRC... DST; got no source.")
	}
	dst := parseCpSide(dstArg)
	srcs := make([]cpSide, len(srcArgs))
	for i, a := range srcArgs {
		srcs[i] = parseCpSide(a)
		if srcs[i].Remote != srcs[0].Remote {
			return exitf(ExitUsage, "%s and %s are on different sides; the sources of `repose cp` are all on the laptop or all on the machine.", srcArgs[0], a)
		}
		if srcs[i].Project != srcs[0].Project {
			return exitf(ExitUsage, "%s and %s name two projects; copy from one at a time.", srcArgs[0], a)
		}
	}
	if srcs[0].Remote == dst.Remote {
		side := "the laptop"
		if dst.Remote {
			side = "the machine"
		}
		all := strings.Join(append(append([]string{}, srcArgs...), dstArg), " ")
		return exitf(ExitUsage, "Every argument of `repose cp` is on %s: %s. One side names the machine (PROJECT:PATH, or :PATH for this checkout's project) and the other the laptop.", side, all)
	}
	remote := &dst
	if srcs[0].Remote {
		remote = &srcs[0]
	}
	// `:izma:/tmp` is this checkout's project and the relative path
	// izma:/tmp, which nobody means.
	if p := remote.Path; remote.Project == "" {
		if i := strings.Index(p, ":"); i > 0 && !strings.Contains(p[:i], "/") && (strings.HasPrefix(p[i+1:], "/") || strings.HasPrefix(p[i+1:], "~")) {
			return exitf(ExitUsage, "A leading : names this checkout's project. For the project %s, write %s.", p[:i], p)
		}
	}
	name := remote.Project
	if name != "" && projectFlag != "" && name != projectFlag {
		return exitf(ExitUsage, "Two projects named: %s and --project %s.", name, projectFlag)
	}
	if name == "" {
		name = projectFlag
	}
	project, err := requireRunningProject(ctx, e, name)
	if err != nil {
		return err
	}
	target, err := connect(ctx, e, project)
	if err != nil {
		return err
	}
	checkout := ""
	for _, s := range append(srcs, dst) {
		if s.relative() {
			if checkout, err = guestCheckoutName(ctx, target, project.Slug); err != nil {
				return err
			}
			break
		}
	}
	host, opts := scpTarget(target)
	args := append([]string{}, scpExtraArgs...)
	legacy := false
	for _, a := range scpExtraArgs {
		legacy = legacy || a == "-O"
	}
	if !legacy {
		if scpHasSFTPFlag() {
			// OpenSSH 8.8 and 8.9 have SFTP mode but default to the old
			// protocol; ask for it, since it takes the path as it is.
			args = append(args, "-s")
		} else {
			legacy = true
		}
	}
	args = append(args, opts...)
	if recursive {
		args = append(args, "-r")
	}
	for _, s := range append(srcs, dst) {
		if s.Remote {
			p := s.guestPath(checkout)
			if legacy {
				// The old protocol hands the path to the guest's shell,
				// which splits it on spaces and expands $.
				p = scpRemoteQuote(p)
			}
			args = append(args, host+":"+p)
		} else {
			args = append(args, s.Path)
		}
	}
	cmd := exec.CommandContext(ctx, "scp", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, e.Out, e.ErrOut
	if err := cmd.Run(); err != nil {
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			return silent(xe.ExitCode())
		}
		return exitf(ExitGeneric, "Could not run scp: %v. repose cp needs the OpenSSH client's scp on your PATH.", err)
	}
	return nil
}

// scpHasSFTPFlag reports whether the laptop's scp has -s (SFTP mode,
// OpenSSH 8.8 on), read once from its usage line. A variable for tests.
var scpHasSFTPFlag = sync.OnceValue(func() bool {
	out, _ := exec.Command("scp").CombinedOutput() // no arguments: usage, exit 1
	m := regexp.MustCompile(`\[-([0-9A-Za-z]+)\]`).FindSubmatch(out)
	return m != nil && strings.ContainsRune(string(m[1]), 's')
})

// scpRemoteQuote escapes a guest path for the remote shell of scp's old
// protocol with backslashes, not quotes: the laptop's scp also matches
// the file names the guest sends back against the path as a glob
// (OpenSSH's CVE-2019-6111 check), and a backslash means the same to the
// glob as to the shell, where quotes would make the names mismatch. A
// leading ~/ stays bare so it still expands, and * ? [ ] stay globs, as
// they are in SFTP mode.
func scpRemoteQuote(p string) string {
	var b strings.Builder
	if p == "~" || strings.HasPrefix(p, "~/") {
		b.WriteString("~")
		p = p[1:]
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("/._-+,=@%:*?[]", r), r > 127:
		default:
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// scpTarget splits an ssh target into scp's options and host: scp spells
// ssh's -p as -P, and takes everything else ssh does.
func scpTarget(t sshTarget) (host string, opts []string) {
	a := t.Args
	if len(a) == 0 {
		return "", nil
	}
	host = a[len(a)-1]
	for i := 0; i < len(a)-1; i++ {
		if a[i] == "-p" && i+1 < len(a)-1 {
			opts = append(opts, "-P", a[i+1])
			i++
			continue
		}
		opts = append(opts, a[i])
	}
	return host, opts
}
