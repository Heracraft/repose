package cli

// The help topics' tables (DECISIONS I-635): `repose help environment`,
// `help exit-codes` and `help config-file` print these, and
// TestHelpTopicsMatchTheDocs fails when a row here and its row in
// apps/web/src/content/docs/cli.md differ, so the two cannot drift.
// Each cell is the docs' Markdown; the help prints it as plain text.

// topicRow is one row: the first cells (the variable, the code, the key
// and its default) and what it does.
type topicRow struct {
	keys []string
	text string
}

// envTopic is cli.md's "Environment variables" table.
var envTopic = []topicRow{
	{[]string{"`REPOSE_PROJECT`"}, "The project to act on, like `--project`."},
	{[]string{"`REPOSE_NO_FORWARD=1`"}, "Don't forward ports automatically while attached."},
	{[]string{"`REPOSE_TIMING=1`"}, "Print how long each step of `run` and `attach` took."},
	{[]string{"`REPOSE_NO_SPINNER=1`"}, "One line per step instead of a progress line. `TERM=dumb` does the same. Questions are still asked."},
	{[]string{"`REPOSE_NO_FASTPATH=1`"}, "Check with the server before every connection instead of reusing the last one. Slower; for when a connection keeps failing."},
	{[]string{"`REPOSE_NO_BROWSER=1`"}, "Never open a browser, even with `repose login --browser` or for a link the machine opens while you're attached."},
	{[]string{"`REPOSE_NO_INPUT_PROXY=1`"}, "Don't copy dropped files or pasted images to the machine; `run` and `attach` hand the terminal straight to `ssh`. `REPOSE_INPUT_PROXY=0` is its old name."},
	{[]string{"`REPOSE_NO_CLIPBOARD_PATH=1`"}, "On macOS, leave the clipboard alone while you are attached, so `Cmd+V` with only an image on it pastes nothing; `Ctrl+V` still pastes the image. `REPOSE_CLIPBOARD_PATH=0` is its old name."},
	{[]string{"`REPOSE_API_URL`"}, "Like `--api-url`."},
	{[]string{"`REPOSE=1`"}, "Set on every repose machine, so scripts can tell where they run."},
	{[]string{"`XDG_CONFIG_HOME`"}, "If set, the CLI's files are in `$XDG_CONFIG_HOME/repose/`."},
	{[]string{"`CLAUDE_CONFIG_DIR`"}, "Where your laptop's Claude Code setup is copied from, instead of `~/.claude`."},
	{[]string{"`CODEX_HOME`"}, "Where `repose mcp forward` reads your Codex config, instead of `~/.codex`."},
	{[]string{"`VISUAL`, `EDITOR`"}, "The editor for `repose config edit`. Default `vi`."},
	{[]string{"`WAYLAND_DISPLAY`"}, "On Linux, `repose paste` reads the Wayland clipboard with `wl-paste` when this is set."},
	{[]string{"`DISPLAY`"}, "Otherwise it reads the X11 clipboard with `xclip`."},
	{[]string{"`REPOSE_EDITOR`"}, "The editor `repose code` opens: `code`, `cursor` or `zed`, over `editor` in config.toml."},
}

// exitTopic is cli.md's "Exit codes" table.
var exitTopic = []topicRow{
	{[]string{"0"}, "Worked."},
	{[]string{"1"}, "Failed, or you answered no to a question; the message says why. A network failure, the login server's included, exits 1 too."},
	{[]string{"2"}, "Wrong usage."},
	{[]string{"3"}, "Not logged in, or the login expired."},
	{[]string{"4"}, "No such project."},
	{[]string{"5"}, "The machine isn't running."},
	{[]string{"6"}, "The machine changed files the sync would write, or is in the middle of a merge or rebase; the sync stopped."},
	{[]string{"7"}, "A plan or account limit refused it (memory, disk, egress, the project count), or a payment problem; the message names it."},
	{[]string{"8"}, "No capacity right now, before or during a start; try again in a few minutes. Choosing a plan while every seat is taken answers with your place on the [waitlist](/docs/limits#when-repose-is-full) and this code too."},
	{[]string{"10"}, "The configuration build failed."},
	{[]string{"130"}, "Interrupted with `Ctrl-C`, at a question too."},
}

// configTopic is cli.md's "config.toml" table.
var configTopic = []topicRow{
	{[]string{"`default_size`", "`large`"}, "Size of new projects. `default_class` is its old name and still works; `default_size` wins when a file has both."},
	{[]string{"`default_agent`", "`claude`"}, "Agent for new projects."},
	{[]string{"`editor`", "none"}, "What `repose code` opens: `code`, `cursor` or `zed`. `--editor` and `REPOSE_EDITOR` win over it."},
	{[]string{"`default_multiplexer`", "none"}, "`tmux` or `herdr` for new projects. Without it, a project you create from a herdr pane gets herdr and any other gets tmux. Any other value stops every command with an error naming the key."},
	{[]string{"`sync.exclude`", "none"}, "More gitignore-style patterns the sync leaves out."},
	{[]string{"`logins.skip`", "none"}, "Logins `repose run` leaves on your laptop: `gh`, `codex`, `opencode`, `env`, `mcp`. `repose secrets choose` sets it."},
	{[]string{"`mcp.forward`", "none"}, "MCP servers [`repose mcp forward`](#repose-mcp-forward-name) runs whenever you're attached to any project, until the last attach to that project ends."},
	{[]string{"`projects`", "none"}, "Per-project tables. `[projects.NAME.logins]` with `skip` replaces `logins.skip` for that project; `skip = []` copies everything for it. `[projects.NAME.mcp]` with `forward` adds servers for that project."},
	{[]string{"`api_url`", "hosted"}, "See [Other servers](#other-servers)."},
	{[]string{"`logto_issuer`", "hosted"}, "The login server. See [Other servers](#other-servers)."},
	{[]string{"`logto_client_id`", "hosted"}, "The CLI's application id there. See [Other servers](#other-servers)."},
}
