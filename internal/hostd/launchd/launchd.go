// Package launchd is the systemd.Systemd of a Mac host (design 2,
// DECISIONS I-1664): every unit hostd starts is a launchd job of its own in
// the user's GUI domain (gui/<uid>), so a VM outlives hostd's restarts and
// upgrades as a guest@ unit does on Linux, and runs in the login session
// that VZ's save file needs (DECISIONS I-1662).
//
// A job is bootstrapped from a plist hostd writes under Dir and never
// installed in ~/Library/LaunchAgents: after a Mac restart no VM starts
// cold behind hostd's back; hostd reconciles and restores what it parked.
//
// Unit names are the Linux ones (guest@<id>); the label is Prefix+unit.
// The Cloud Hypervisor argv hostd renders is run by repose-vm (Launcher),
// which takes the same flags, with the host's own flags (ExtraArgs)
// appended. systemd properties (cgroup caps, sandboxing) have no launchd
// equivalent and are not applied: each VM is its own process under macOS's
// own memory management.
package launchd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// Prefix is the label prefix of every job hostd starts.
const Prefix = "run.repose."

// Launchd implements systemd.Systemd over launchctl.
type Launchd struct {
	R shell.Runner
	// UID is the user every VM runs as: its GUI domain (gui/<uid>) holds
	// the jobs started while the user's session exists.
	UID int
	// UserName, when set, lets a job start with nobody logged in: in the
	// system domain with UserName set, so it still runs as the user, never
	// as root (DECISIONS I-1664). Only a root hostd can do that. Such a
	// VM cannot park (VZ's save needs the session, I-1662).
	UserName string
	// HasSession reports whether the user's GUI session exists now; nil
	// is always.
	HasSession func(ctx context.Context) bool
	// ChownLog gives a new job log to the user, who writes it.
	ChownLog func(path string) error
	// Dir holds the jobs' plists and, unless LogFor names another place,
	// their stdout/stderr logs.
	Dir string
	// LogFor is where a unit's stdout and stderr go when not in Dir. A
	// job's log is opened by launchd as the job's user, so it must sit
	// under directories that user can reach: a root hostd keeps Dir under
	// its 0700 state directory, and there every VM's job ended with status
	// 78 before repose-vm ran (lab Mac, 2026-11-27). Nil keeps Dir.
	LogFor func(unit string) string
	// Launcher replaces argv[0] "cloud-hypervisor".
	Launcher string
	// ExtraArgs returns flags appended to a launcher argv for a unit
	// (the switch's socket directory, the NIC's MTU, the park
	// directory); nil adds none.
	ExtraArgs func(unit string, argv []string) []string
	// ExitTimeOut is the time launchd gives a job between SIGTERM and
	// SIGKILL: a VM parked on SIGTERM needs its save to finish.
	ExitTimeOut time.Duration
	Poll        time.Duration
}

func (l *Launchd) domain() string         { return "gui/" + strconv.Itoa(l.UID) }
func (l *Launchd) target(u string) string { return l.domain() + "/" + Label(u) }

// SystemDomain is launchd's system domain, where a job runs with nobody
// logged in.
const SystemDomain = "system"

func systemTarget(u string) string { return SystemDomain + "/" + Label(u) }

// find returns the domain target that holds unit's job ("" when none
// does) and what launchctl says of it.
func (l *Launchd) find(ctx context.Context, unit string) (string, job, error) {
	j, err := l.printTarget(ctx, l.target(unit))
	if err != nil || j.loaded || l.UserName == "" {
		if !j.loaded {
			return "", j, err
		}
		return l.target(unit), j, err
	}
	j, err = l.printTarget(ctx, systemTarget(unit))
	if err != nil || !j.loaded {
		return "", j, err
	}
	return systemTarget(unit), j, nil
}

// Domain names the domain a unit's job is in: "gui" (it can park),
// "system" (it runs with nobody logged in and cannot), or "" for none.
func (l *Launchd) Domain(ctx context.Context, unit string) string {
	t, _, err := l.find(ctx, unit)
	if err != nil || t == "" {
		return ""
	}
	d, _, _ := strings.Cut(t, "/")
	return d
}

// Label is a unit's launchd label.
func Label(unit string) string { return Prefix + strings.TrimSuffix(unit, ".service") }

// PlistPath is where a unit's job is written.
func (l *Launchd) PlistPath(unit string) string { return filepath.Join(l.Dir, Label(unit)+".plist") }

// LogPath is a unit's stdout and stderr.
func (l *Launchd) LogPath(unit string) string {
	if l.LogFor != nil {
		if p := l.LogFor(unit); p != "" {
			return p
		}
	}
	return filepath.Join(l.Dir, Label(unit)+".log")
}

// notFound is launchctl's "Could not find service" (exit 113), bootout's
// "No such process" (exit 3), and "Domain does not support specified
// action" (exit 125), which is what gui/<uid> answers when nobody is
// logged in and the domain does not exist (lab Mac, 16:24Z: it failed
// every start after a power loss).
func notFound(err error) bool {
	var ee *shell.ExitError
	return errors.As(err, &ee) && (ee.Result.ExitCode == 113 || ee.Result.ExitCode == 3 || ee.Result.ExitCode == 125)
}

// job is what `launchctl print` says of one job.
type job struct {
	loaded   bool
	running  bool
	pid      int
	lastExit int
	exited   bool
}

func (l *Launchd) print(ctx context.Context, unit string) (job, error) {
	_, j, err := l.find(ctx, unit)
	return j, err
}

func (l *Launchd) printTarget(ctx context.Context, target string) (job, error) {
	res, err := l.R.Run(ctx, "launchctl", "print", target)
	if notFound(err) {
		return job{}, nil
	}
	if err != nil {
		return job{}, err
	}
	return parsePrint(res.Stdout), nil
}

// parsePrint reads the top-level fields of `launchctl print` for a service.
// Nested blocks (environment, endpoints) are skipped by depth.
func parsePrint(out []byte) job {
	j := job{loaded: true}
	depth := 0
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasSuffix(t, "{") {
			depth++
			continue
		}
		if t == "}" {
			depth--
			continue
		}
		if depth != 1 {
			continue
		}
		k, v, ok := strings.Cut(t, " = ")
		if !ok {
			continue
		}
		switch k {
		case "state":
			j.running = v == "running" || v == "xpcproxy" || v == "spawn scheduled"
		case "pid":
			j.pid, _ = strconv.Atoi(v)
		case "last exit code":
			if n, err := strconv.Atoi(v); err == nil {
				j.lastExit, j.exited = n, true
			}
		}
	}
	if j.pid > 0 {
		j.running = true
	}
	return j
}

// Run implements systemd.Systemd: a job already running is left alone; a
// loaded job that has exited is removed and bootstrapped anew.
func (l *Launchd) Run(ctx context.Context, unit string, props []string, argv []string) error {
	t, j, err := l.find(ctx, unit)
	if err != nil {
		return err
	}
	if j.running {
		return nil
	}
	if j.loaded {
		if _, err := l.R.Run(ctx, "launchctl", "bootout", t); err != nil && !notFound(err) {
			return err
		}
	}
	domain, user := l.domain(), ""
	if l.UserName != "" && l.HasSession != nil && !l.HasSession(ctx) {
		domain, user = SystemDomain, l.UserName
	}
	if len(argv) == 0 {
		return errors.New("launchd: empty argv")
	}
	args := append([]string(nil), argv...)
	if args[0] == "cloud-hypervisor" {
		if l.Launcher == "" {
			return errors.New("launchd: no launcher for a cloud-hypervisor unit")
		}
		args[0] = l.Launcher
		if l.ExtraArgs != nil {
			args = append(args, l.ExtraArgs(unit, argv)...)
		}
	}
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return err
	}
	p := l.PlistPath(unit)
	if err := os.WriteFile(p, Plist(Label(unit), user, args, l.LogPath(unit), l.exitTimeOut()), 0o600); err != nil {
		return err
	}
	if l.ChownLog != nil {
		if f, err := os.OpenFile(l.LogPath(unit), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			_ = f.Close()
		}
		if err := l.ChownLog(l.LogPath(unit)); err != nil {
			return err
		}
	}
	_, err = l.R.Run(ctx, "launchctl", "bootstrap", domain, p)
	return err
}

func (l *Launchd) exitTimeOut() int {
	if l.ExitTimeOut <= 0 {
		return 60
	}
	return int(l.ExitTimeOut / time.Second)
}

// Plist renders a job: started once at bootstrap, never restarted by
// launchd (hostd decides), interactive so App Nap does not throttle a VM.
// userName, when set, runs a system-domain job as that user.
func Plist(label, userName string, argv []string, log string, exitTimeOut int) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
`)
	kv := func(k, v string) {
		b.WriteString("<key>")
		_ = xml.EscapeText(&b, []byte(k))
		b.WriteString("</key><string>")
		_ = xml.EscapeText(&b, []byte(v))
		b.WriteString("</string>\n")
	}
	kv("Label", label)
	if userName != "" {
		kv("UserName", userName)
	}
	b.WriteString("<key>ProgramArguments</key><array>\n")
	for _, a := range argv {
		b.WriteString("<string>")
		_ = xml.EscapeText(&b, []byte(a))
		b.WriteString("</string>\n")
	}
	b.WriteString("</array>\n")
	b.WriteString("<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><false/>\n")
	kv("ProcessType", "Interactive")
	fmt.Fprintf(&b, "<key>ExitTimeOut</key><integer>%d</integer>\n", exitTimeOut)
	kv("StandardOutPath", log)
	kv("StandardErrorPath", log)
	b.WriteString("</dict></plist>\n")
	return b.Bytes()
}

// IsActive implements systemd.Systemd.
func (l *Launchd) IsActive(ctx context.Context, unit string) (bool, error) {
	j, err := l.print(ctx, unit)
	return j.running, err
}

// Stop implements systemd.Systemd: bootout sends SIGTERM and, after the
// job's ExitTimeOut, SIGKILL. An absent job is stopped already.
func (l *Launchd) Stop(ctx context.Context, unit string) error {
	t, _, err := l.find(ctx, unit)
	if err != nil || t == "" {
		return err
	}
	_, err = l.R.Run(ctx, "launchctl", "bootout", t)
	if notFound(err) {
		return nil
	}
	return err
}

// Kill implements systemd.Systemd. The job stays loaded, so its exit
// status can still be read; the next Run removes it.
func (l *Launchd) Kill(ctx context.Context, unit string) error {
	t, _, err := l.find(ctx, unit)
	if err != nil || t == "" {
		return err
	}
	_, err = l.R.Run(ctx, "launchctl", "kill", "SIGKILL", t)
	if notFound(err) {
		return nil
	}
	return err
}

// Show implements systemd.Systemd for the properties hostd reads: MainPID,
// ExecMainStatus, Result, MemoryCurrent and CPUUsageNSec (from ps), and an
// empty ControlGroup (no cgroups on macOS). Others are empty.
func (l *Launchd) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	j, err := l.print(ctx, unit)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var rss, cpu string
	if j.pid > 0 {
		for _, p := range props {
			if p == "MemoryCurrent" || p == "CPUUsageNSec" {
				rss, cpu = l.ps(ctx, j.pid)
				break
			}
		}
	}
	for _, p := range props {
		switch p {
		case "MainPID":
			out[p] = strconv.Itoa(j.pid)
		case "ExecMainStatus":
			out[p] = strconv.Itoa(j.lastExit)
		case "Result":
			switch {
			case j.running || !j.exited || j.lastExit == 0:
				out[p] = "success"
			case j.lastExit < 0:
				out[p] = "signal"
			default:
				out[p] = "exit-code"
			}
		case "MemoryCurrent":
			out[p] = rss
		case "CPUUsageNSec":
			out[p] = cpu
		default:
			out[p] = ""
		}
	}
	return out, nil
}

// ps returns a process's resident bytes and CPU time in nanoseconds.
func (l *Launchd) ps(ctx context.Context, pid int) (rss, cpu string) {
	res, err := l.R.Run(ctx, "ps", "-o", "rss=,time=", "-p", strconv.Itoa(pid))
	if err != nil {
		return "", ""
	}
	f := strings.Fields(string(res.Stdout))
	if len(f) < 2 {
		return "", ""
	}
	if kb, err := strconv.ParseUint(f[0], 10, 64); err == nil {
		rss = strconv.FormatUint(kb*1024, 10)
	}
	if ns, ok := psTime(f[1]); ok {
		cpu = strconv.FormatUint(ns, 10)
	}
	return rss, cpu
}

// psTime parses ps's [[dd-]hh:]mm:ss.cc.
func psTime(s string) (uint64, bool) {
	var days uint64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseUint(d, 10, 64)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	var secs float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, false
		}
		secs = secs*60 + v
	}
	return uint64((secs + float64(days)*86400) * 1e9), true
}

// ListUnits implements systemd.Systemd: the running jobs of the domain
// whose unit name matches pattern (a glob, e.g. "guest@*").
func (l *Launchd) ListUnits(ctx context.Context, pattern string) ([]string, error) {
	res, err := l.R.Run(ctx, "launchctl", "print", l.domain())
	if err != nil && l.UserName == "" {
		return nil, err
	}
	// With nobody logged in there is no gui/<uid> domain to print: its
	// jobs are none, and the system domain's are listed below.
	units := parseServices(res.Stdout, pattern)
	if l.UserName != "" {
		sys, err := l.R.Run(ctx, "launchctl", "print", SystemDomain)
		if err != nil {
			return nil, err
		}
		units = append(units, parseServices(sys.Stdout, pattern)...)
	}
	return units, nil
}

// parseServices reads the "services = {" block of `launchctl print
// gui/<uid>`: one "PID LASTEXIT LABEL" row a job, PID 0 when not running.
func parseServices(out []byte, pattern string) []string {
	var units []string
	in := false
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		if !in {
			in = t == "services = {"
			continue
		}
		if t == "}" {
			break
		}
		f := strings.Fields(t)
		if len(f) != 3 || !strings.HasPrefix(f[2], Prefix) {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err != nil || pid <= 0 {
			continue
		}
		u := strings.TrimPrefix(f[2], Prefix)
		if ok, _ := path.Match(pattern, u); ok {
			units = append(units, u)
		}
	}
	return units
}

// WaitInactive implements systemd.Systemd.
func (l *Launchd) WaitInactive(ctx context.Context, unit string) error {
	poll := l.Poll
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		active, err := l.IsActive(ctx, unit)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
