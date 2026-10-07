set -e
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT
tar -x -C "$t"
sh -e "$t/part-1.sh" "$t" || echo '#failed MCP servers'
