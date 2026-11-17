// Package agentwindow builds the tmux window an agent starts in: the
// prompt as the agent's own command-line word (DECISIONS I-780) and the
// folder-trust shell that runs before it (I-486, I-544).
//
// It is a leaf package because two callers need the same words. The CLI
// opens a window over SSH for `repose run`; guestd opens one inside the
// machine for a trigger's run (I-1256), where there is no laptop and no
// SSH. I-752 moved the submit script to a leaf package for the same
// reason, and a second copy of either is how one caller's agent takes a
// prompt the other's does not.
//
// Every comment below is the evidence for one agent's form, read out of
// that agent's own help or bundle at the version the guest pins. It
// travels with the code.
package agentwindow

import (
	"fmt"
	"strings"
)

// Quote is one shell word, single-quoted, with any single quote in it
// closed and reopened. Both levels of StartCommand go through it, which
// is why a prompt holding quotes, a newline, `$`, a backtick or a
// leading `-` reaches the agent as one argv word and runs nothing.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// promptArgs is each agent's initial-prompt argument form: the words
// that go after the binary on the command line tmux starts it with, so
// the agent's own startup takes the prompt and stays interactive
// (DECISIONS I-780, amending I-752). The agent's own startup is what
// consumes it: nothing is typed, nothing is read back off the screen, and
// there is no readiness to judge. The live submission rate on a real
// guest is owed either way (I-780's OWED); I-752 is not closed by this. An agent with no entry here, or one whose entry
// refuses this particular prompt, keeps the typing path
// (submitPromptScript), as do the warm path (an agent window that is
// already open) and the Claude Code login waiter (I-607).
//
// Each form was verified against the release nix/overlay/agents/versions.json
// pins, in its own words, on the dev box; what was NOT verified is said so.
// A form must survive a prompt that begins with `-`, since every one of
// these parsers would otherwise read it as a flag.
var promptArgs = map[string]func(prompt string) ([]string, bool){
	// Claude Code: `claude [options] [command] [prompt]`, "Arguments:
	// prompt  Your prompt" (`claude --help`; read on 2.1.266, the dev
	// box's own copy, where the guest pins 2.1.283). `--` first because
	// its parser reads a leading `-` as a flag: `claude -- '--help'`
	// answered the prompt "--help" instead of printing the help.
	// Seen taking the argument in a tmux window on the dev box (a window
	// made by hand, NOT one repose created): the prompt is the first turn
	// in the transcript, the input is empty after it, and the pane still
	// runs claude. It also survives the folder-trust dialog, which showed
	// first and was answered by hand: Claude Code then submitted the
	// argument itself.
	"claude": func(p string) ([]string, bool) { return []string{"--", p}, true },
	// Codex: "If no subcommand is specified, options will be forwarded to
	// the interactive CLI", `codex [OPTIONS] [PROMPT]`, "[PROMPT]
	// Optional user prompt to start the session" (`codex --help` from the
	// pinned 0.157.1 binary, run on the dev box). `--` because clap
	// refuses a leading `-` and says so itself: `codex '-lead prompt'` is
	// "error: unexpected argument '-l' found / tip: to pass '-l' as a
	// value, use '-- -l'", and `codex -- '-lead prompt'` gets past the
	// argv to "stdin is not a terminal". The wrapper's daemon check
	// (nix/overlay/agents/wrap.nix) reads a first word that is no
	// subcommand as an interactive start, which `--` is. NOT verified
	// live: there is no Codex login on this box.
	"codex": func(p string) ([]string, bool) { return []string{"--", p}, true },
	// Gemini CLI: `-i/--prompt-interactive`, "Execute the provided prompt
	// and continue in interactive mode"; `-p/--prompt` is headless and is
	// never used here. Read in the pinned 0.61.0 bundle's own yargs
	// options (gemini-NPCD6A4N.js) and exercised with node on the dev
	// box. One word, joined with `=`, because the separate-word form
	// loses a leading `-`: `gemini -i '-dash first'` is "Not enough
	// arguments following: i", while `--prompt-interactive=-dash first`
	// parses. The positional `[query..]` would also work (it becomes
	// promptInteractive when not headless) but pushes a "Positional
	// arguments now default to interactive mode" line into the user's
	// pane, so the flag is used. NOT verified live: no Gemini auth here.
	"gemini": func(p string) ([]string, bool) { return []string{"--prompt-interactive=" + p}, true },
	// opencode: the TUI's `--prompt`, "prompt to use" (`opencode --help`).
	// Joined with `=`: `opencode --prompt '-lead'` falls out of yargs into
	// the help banner, `--prompt=-lead` starts the TUI. The positional is
	// a project path and `opencode run` is headless, so neither is used.
	// Seen taking the argument in a tmux window on the dev box (again a
	// hand-made window) on 1.18.30, where the guest pins 1.18.34: the
	// prompt appears as the submitted user message, the model answered
	// it, and the TUI stayed interactive.
	"opencode": func(p string) ([]string, bool) { return []string{"--prompt=" + p}, true },
	// pi: `pi [options] [--] [@files...] [messages...]`, "message
	// Provide an initial prompt", "`--` Stop option parsing so a prompt
	// can begin with `-`", and "With terminal stdin and stdout, Pi opens
	// the terminal UI unless --print, --mode json, or --mode rpc"
	// (`pi --help` and docs/cli.md in the pinned 1.0.4 tree, run and read
	// on the dev box). `pi '-lead prompt'` is "Error: Unknown option";
	// `pi -- '-lead prompt'` reaches the no-API-key message.
	//
	// A prompt that begins with "@" keeps the typing path: `--` stops
	// option parsing but not pi's `@file` include, so `pi -- '@nope.txt
	// hello'` exits with "Error: File not found" and the window would be
	// left with no agent and no prompt — worse than typing it. NOT
	// verified live otherwise: there is no pi login here.
	"pi": func(p string) ([]string, bool) {
		if strings.HasPrefix(p, "@") {
			return nil, false
		}
		return []string{"--", p}, true
	},
}

// agentPromptArgv is binary's form for this prompt: the words to put
// after it, and whether the prompt travels that way at all. An empty
// prompt never does.
func PromptArgv(binary, prompt string) ([]string, bool) {
	if prompt == "" {
		return nil, false
	}
	form, ok := promptArgs[binary]
	if !ok {
		return nil, false
	}
	return form(prompt)
}

// agentStartCommand is `tmux new-window`'s command argument for the
// agent's window: the binary's name alone, as every window had before
// I-780, or a whole shell command line — the binary and the prompt as
// its own word — quoted a second time, so the bash tmux runs it in (the
// session's default-shell, nix/guest/base/tmux.nix) splits it back into
// exactly those words. Both levels go through shQuote, which is why a
// prompt holding single quotes, double quotes, newlines, `$`, a backtick
// or a leading `-` reaches the agent as one argv word and runs nothing.
func StartCommand(binary, prompt string) string {
	args, ok := PromptArgv(binary, prompt)
	if !ok {
		return Quote(binary)
	}
	line := Quote(binary)
	for _, a := range args {
		line += " " + Quote(a)
	}
	return Quote(line)
}

// ClaudeTrustScript is shell, run before tmux starts claude in dir (a
// shell word: "~/<name>..." or "$repose_co"), that sets
// projects["<dir, symlinks resolved>"].hasTrustDialogAccepted to true in
// ~/.claude.json, the flag Claude Code (2.1.283) reads to skip its "Is
// this a project you trust?" dialog. That dialog's default is "No, exit",
// so the prompt and Enter `repose run` types would quit Claude Code
// (I-486). Only folders repose itself starts an agent in get here: the
// checkout, a --worktree directory, another checkout. A false Claude Code
// wrote after an earlier refusal is replaced, since this run is the user
// asking for an agent there; every other key in the file is kept. The
// file is written only when the flag is not already true, atomically, and
// never when it is not valid JSON. Best effort: the window starts whatever
// happens here, and waitPaneIdle catches a dialog that shows anyway.
// appr, the laptop's .mcp.json answers for the repository, adds to
// enabledMcpjsonServers and disabledMcpjsonServers each server the guest
// has no answer for in either list, so the .mcp.json dialog does not take
// the prompt either (I-556). Claude Code writes both lists empty into
// every project it opens, so an empty list is no answer; an answer given
// on the machine is kept. The write holds Claude Code's own lock, the
// directory ~/.claude.json.lock, as repose-mcp sync and agent-setup do
// (I-555): a lock older than 10 s is taken over, and after 5 s of waiting
// nothing is written.
func ClaudeTrustScript(dir, approvalsJSON string) string {
	a := approvalsJSON
	if a == "" {
		a = "{}"
	}
	return fmt.Sprintf(`{ repose_tp=$(cd %s 2>/dev/null && pwd -P) && command -v jq >/dev/null && repose_cj="$HOME/.claude.json" && repose_ta=%s && repose_lk="$repose_cj.lock" && repose_n=0 && until mkdir "$repose_lk" 2>/dev/null; do
    if [ "$(( $(date +%%s) - $(stat -c %%Y "$repose_lk" 2>/dev/null || date +%%s) ))" -gt 10 ] && rmdir "$repose_lk" 2>/dev/null; then continue; fi
    repose_n=$((repose_n + 1)); [ "$repose_n" -lt 100 ] || break; sleep 0.05
  done && [ "$repose_n" -lt 100 ] && {
  if [ ! -s "$repose_cj" ]; then
    repose_tt=$(mktemp "$repose_cj.XXXXXX") && jq -n --arg p "$repose_tp" --argjson a "$repose_ta" '{projects: {($p): ({hasTrustDialogAccepted: true} + $a)}}' > "$repose_tt" && chmod 600 "$repose_tt" && mv -f "$repose_tt" "$repose_cj"
  elif jq -e --arg p "$repose_tp" --argjson a "$repose_ta" '(.projects // {})[$p] as $e | ($e.hasTrustDialogAccepted != true) or (((($e // {}).enabledMcpjsonServers // []) + (($e // {}).disabledMcpjsonServers // [])) as $h | [$a[][] | select(. as $n | $h | index([$n]) | not)] | length > 0)' "$repose_cj" >/dev/null 2>&1; then
    repose_tt=$(mktemp "$repose_cj.XXXXXX") && jq --arg p "$repose_tp" --argjson a "$repose_ta" '.projects[$p] |= ((. // {}) | .hasTrustDialogAccepted = true | reduce ($a | to_entries[] | .key as $k | .value[] | {k: $k, n: .}) as $x (.; if (((.enabledMcpjsonServers // []) + (.disabledMcpjsonServers // [])) | index([$x.n])) then . else .[$x.k] = ((.[$x.k] // []) + [$x.n]) end))' "$repose_cj" > "$repose_tt" && chmod 600 "$repose_tt" && mv -f "$repose_tt" "$repose_cj"
  fi
  [ -z "${repose_tt:-}" ] || rm -f "$repose_tt"
  rmdir "$repose_lk"
}; } >/dev/null 2>&1 || true
`, dir, Quote(a))
}

// CodexTrustScript is shell, run before tmux starts codex in dir, that
// appends a [projects."<dir, symlinks resolved>"] table with trust_level =
// "trusted" to ${CODEX_HOME:-~/.codex}/config.toml, which Codex (0.157)
// reads to skip its "Trust this folder?" screen; that screen's default is
// "Trust and continue", so the Enter `repose run` types trusts the folder
// and the prompt is lost (I-544). Nothing is written when the file already
// has a table for that folder in either quoting, whatever its trust_level,
// so the user's "untrusted" stays. Only paths of the checkout's character
// set ([A-Za-z0-9._/-]) are written, since TOML would need escapes for
// others, nor into a config.toml that is a symlink (a file a dotfiles
// tool manages). Atomic and best effort, like ClaudeTrustScript.
func CodexTrustScript(dir string) string {
	return fmt.Sprintf(`{ repose_tp=$(cd %s 2>/dev/null && pwd -P) && case "$repose_tp" in *[!A-Za-z0-9._/-]*) false ;; esac && repose_xd="${CODEX_HOME:-$HOME/.codex}" && repose_xc="$repose_xd/config.toml" && [ ! -L "$repose_xc" ] && {
  if ! grep -qF -e "[projects.\"$repose_tp\"]" -e "[projects.'$repose_tp']" "$repose_xc" 2>/dev/null; then
    mkdir -p "$repose_xd" && repose_tt=$(mktemp "$repose_xc.XXXXXX") && { if [ -s "$repose_xc" ]; then cat "$repose_xc" && [ -z "$(tail -c1 "$repose_xc")" ] || echo; fi; printf '\n[projects."%%s"]\ntrust_level = "trusted"\n' "$repose_tp"; } > "$repose_tt" && chmod 600 "$repose_tt" && mv -f "$repose_tt" "$repose_xc"
  fi
  [ -z "${repose_tt:-}" ] || rm -f "$repose_tt"
}; } >/dev/null 2>&1 || true
`, dir)
}

// TrustScript is the shell that marks dir trusted for the agent
// binary starts, or "" for an agent without a folder trust dialog
// (opencode, pi) or with it off machine-wide (Gemini CLI, I-553).
func TrustScript(binary, dir, approvalsJSON string) string {
	switch binary {
	case "claude":
		return ClaudeTrustScript(dir, approvalsJSON)
	case "codex":
		return CodexTrustScript(dir)
	}
	return ""
}

// NewWindow is the whole shell line that opens one window: the folder
// trust first, then `tmux new-window` with the agent and its prompt.
// session is the tmux session (the project's slug), window the window's
// name, dir a shell word for the working directory (`~/<slug>` or
// `"$repose_co"`), binary the agent, prompt its first turn ("" for a
// window with no prompt), approvalsJSON the laptop's `.mcp.json`
// answers ("" for none, which is every trigger), and env the `KEY=VALUE`
// pairs the window's own process needs.
//
// env goes on `tmux new-window -e`, not in the caller's environment: a
// tmux window inherits the **server's** environment, not the client's,
// so a trigger's REPOSE_PAYLOAD set around the `tmux` command never
// reached the agent. The first live run said so itself — "REPOSE_PAYLOAD
// isn't set in this shell, so there was no file name to read"
// (2026-11-17) — while the test that was supposed to cover it asserted
// the environment of the client.
func NewWindow(session, window, dir, binary, prompt, approvalsJSON string, env ...string) string {
	line := TrustScript(binary, dir, approvalsJSON) +
		fmt.Sprintf("tmux new-window -t %s -n %s -c %s", session, window, dir)
	for _, kv := range env {
		if kv == "" {
			continue
		}
		line += " -e " + Quote(kv)
	}
	return line + " -d " + StartCommand(binary, prompt)
}
