# The guest half of the MCP carry (carry_mcp.go, DECISIONS I-556): the
# laptop's templated list ($n) onto the guest's laptop.json ($o), for the
# checkout whose real path is $co. User scope is replaced whole; local
# scope replaces this checkout's entry and keeps other checkouts'; a null
# project (a run from outside a repository) leaves every one alone.
# "secrets" is every ${NAME} the result names without a default, the
# ambient variables left out, with the servers that name it.
def co_path: walk(if type == "string" then gsub("@@REPOSE_CHECKOUT@@"; $co) else . end);
def ambient: . == "HOME" or . == "USER" or . == "PWD" or . == "TMPDIR" or . == "PATH"
  or . == "SHELL" or . == "LANG" or . == "LOGNAME" or . == "TERM" or startswith("XDG_");
($n[0]) as $n
| ($o[0] // {}) as $o
| (($o.projects // {}) | if type == "object" then . else {} end) as $p
| (($n.user // {}) | co_path) as $user
| (if $n.project == null then $p
   else ($p | del(.[$co])) + (if ($n.project | length) > 0 then {($co): ($n.project | co_path)} else {} end)
   end) as $projects
| [ ($user | to_entries[]), ($projects | to_entries[] | .value | to_entries[]) ] as $servers
| ([ $servers[] | .key as $s
     | [ .value | .. | strings | scan("\\$\\{([A-Za-z_][A-Za-z0-9_]*)\\}") | .[0] | select(ambient | not) ]
     | unique[] | {k: ., s: $s} ]
   | group_by(.k) | map({key: .[0].k, value: (map(.s) | unique)}) | from_entries) as $secrets
| {version: 1, user: $user, projects: $projects, skipped: ($n.skipped // []), secrets: $secrets}
