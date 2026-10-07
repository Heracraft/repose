# The guest half of the MCP carry (carry_mcp.go, DECISIONS I-556), after
# checkoutVar has set $repose_co. Writes ~/.repose/mcp/laptop.json and
# never an agent's config: repose-mcp sync renders it at the next agent
# start. Reports, once per change: #mcpleft for each server left on the
# laptop, #mcpsecret for each secret the machine lacks, #mcpcmd for each
# command it lacks, #mcpold on a base without repose-mcp when there are
# servers to carry.
t=$1
d="$HOME/.repose/mcp"
mkdir -p "$d"
chmod 700 "$d"
co=$(cd "$repose_co" 2>/dev/null && pwd -P) || co=$repose_co
o="$d/laptop.json"
if [ -s "$o" ] && jq -e 'type == "object"' "$o" >/dev/null 2>&1; then
  cp "$o" "$t/mcp/old.json"
else
  echo '{}' > "$t/mcp/old.json"
fi
jq -S -n --slurpfile n "$t/mcp/in.json" --slurpfile o "$t/mcp/old.json" --arg co "$co" -f "$t/mcp/laptop.jq" > "$t/mcp/new.json"
jq empty "$t/mcp/new.json"
if ! cmp -s "$t/mcp/new.json" "$o"; then
  cp "$t/mcp/new.json" "$o.tmp"
  chmod 600 "$o.tmp"
  mv -f "$o.tmp" "$o"
fi
# The directories an agent's PATH has beyond this shell's; tests set
# REPOSE_TOOL_DIRS (space-separated, may be empty) instead.
tool_dirs=${REPOSE_TOOL_DIRS-"$HOME/.nix-profile/bin /etc/profiles/per-user/$(id -un)/bin /run/current-system/sw/bin $HOME/.npm-global/bin $HOME/.local/bin $HOME/go/bin $HOME/.cargo/bin"}
have() {
  command -v "$1" >/dev/null 2>&1 && return 0
  for p in $tool_dirs; do
    [ -x "$p/$1" ] && return 0
  done
  return 1
}
while IFS= read -r l; do
  [ -n "$l" ] && printf '#mcpleft %s\n' "$l"
done < "$t/mcp/left"
sd=${REPOSE_SECRETS_DIR:-/run/repose/secrets}
jq -r '.secrets | to_entries[] | "\(.key) \(.value | join(","))"' "$o" | while read -r name servers; do
  [ -e "$sd/$name" ] && continue
  if grep -qxF "$name" "$t/mcp/templated"; then
    echo "#mcpsecret $name $servers laptop"
  else
    echo "#mcpsecret $name $servers"
  fi
done
while read -r s b; do
  [ -n "$b" ] || continue
  have "$b" || echo "#mcpcmd $s $b"
done < "$t/mcp/cmds"
old=
if ! have repose-mcp; then
  old=:old
  # Only a laptop with servers to carry has something waiting.
  if jq -e '(.user // {} | length) + (.project // {} | length) > 0' "$t/mcp/in.json" >/dev/null 2>&1; then
    echo '#mcpold'
  fi
fi
mkdir -p ~/.repose/carry
printf '%s%s\n' "$(cat "$t/mcp/hash-user")" "$old" > ~/.repose/carry/claude-mcp
printf '%s%s\n' "$(cat "$t/mcp/hash-proj")" "$old" > ~/.repose/carry/claude-mcp-project
