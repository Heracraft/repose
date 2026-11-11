package cli

// `repose fork` was removed (DECISIONS I-1082). For one release the
// command stays, hidden, and says where to go; v0.1.46 deletes this file
// and its line in docs_test.go's hiddenCommands. The replacements are
// `repose run --worktree -p` (another agent on its own branch on the
// same machine), `repose run NAME` (a second machine for this checkout)
// and `repose snapshots restore ID --as NAME` (a copy of the disk).

// ForkCmd is what `repose fork` now does: it says where to go and exits
// 2. The command parses no flags, so an old `repose fork -n 3 --prompt
// ...` reaches it and reads the same line.
func ForkCmd() error {
	return exitf(ExitUsage, "repose fork was removed in v0.1.45. To try another approach on the same machine, run `repose run --worktree -p \"...\"`. For a second machine with this checkout, run `repose run NAME`.")
}
