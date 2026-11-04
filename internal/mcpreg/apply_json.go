package mcpreg

import (
	"os"
	"path/filepath"
)

// GeminiExtension is the directory repose owns by name under
// ~/.gemini/extensions, beside repose-machine-guide.
const GeminiExtension = "repose-mcp"

// applyGemini writes the repose-mcp extension: the directory is repose's
// by name, and a server in the user's ~/.gemini/settings.json of the same
// name wins over it in Gemini CLI itself. With nothing to render the
// extension is removed.
func applyGemini(p Paths, want *Rendered, prev *agentRecord, warn func(string)) *agentRecord {
	dir := filepath.Join(p.Home, ".gemini", "extensions", GeminiExtension)
	file := filepath.Join(dir, "gemini-extension.json")
	if st, err := os.Lstat(dir); err == nil && !st.IsDir() {
		warn("~/.gemini/extensions/" + GeminiExtension + " is not a directory; leaving it alone")
		return prev
	}
	if len(want.User) == 0 {
		if _, err := os.Stat(file); err == nil {
			if err := os.Remove(file); err != nil {
				warn("cannot remove the " + GeminiExtension + " Gemini CLI extension: " + err.Error())
				return prev
			}
			_ = os.Remove(dir) // only when empty; anything the user put there stays
		}
		return &agentRecord{}
	}
	ext := map[string]any{"name": GeminiExtension, "version": "1.0.0", "mcpServers": want.User}
	if err := writeJSONFile(file, ext); err != nil {
		warn("cannot write the " + GeminiExtension + " Gemini CLI extension: " + err.Error())
		return prev
	}
	return &agentRecord{User: want.User}
}

// applyOpencode merges want into ~/.config/opencode/opencode.json
// `mcp.servers` (DECISIONS I-1050). opencode 2 loads opencode.json and
// opencode.jsonc and no longer reads the config.json an earlier base
// wrote beneath them, and it takes servers under `mcp.servers` only, not
// a name directly under `mcp` as version 1 did. repose merges into the
// .json and leaves the .jsonc, which a comment makes unsafe to rewrite,
// to the reader: a name defined there is theirs and repose renders none
// of it. An entry in opencode.json is repose's only while it equals what
// repose wrote.
func applyOpencode(p Paths, want *Rendered, prev *agentRecord, warn func(string)) *agentRecord {
	dir := filepath.Join(p.Home, ".config", "opencode")
	path := filepath.Join(dir, "opencode.json")
	theirs := map[string]bool{}
	for n := range opencodeServers(filepath.Join(dir, "opencode.jsonc")) {
		theirs[n] = true
	}
	// A name the reader left in version 1's place in the file repose
	// writes is still theirs, even though opencode 2 ignores it.
	for n := range jsonServers(path, "mcp") {
		if n != "servers" {
			theirs[n] = true
		}
	}
	user := map[string]any{}
	for n, v := range want.User {
		if !theirs[n] {
			user[n] = v
		}
	}
	b, exists, err := readFile(path)
	if err != nil {
		warn("cannot read ~/.config/opencode/opencode.json: " + err.Error())
		return prev
	}
	doc, err := parseObject(b)
	if err != nil {
		if jdoc, jerr := parseObject(stripJSONC(b)); jerr == nil {
			return opencodeCommented(jdoc, user, want, prev, warn)
		}
		warn("~/.config/opencode/opencode.json is not valid JSON; leaving it alone")
		return prev
	}
	mcp, err := doc.child("mcp")
	if err != nil {
		warn("~/.config/opencode/opencode.json mcp is not an object; leaving it alone")
		return prev
	}
	servers, err := mcp.child("servers")
	if err != nil {
		warn("~/.config/opencode/opencode.json mcp.servers is not an object; leaving it alone")
		return prev
	}
	changes, owned := plan(rawMap(servers), user, prev.User, want.replaceable())
	rec := &agentRecord{}
	if len(owned) > 0 {
		rec.User = owned
	}
	if len(changes) == 0 {
		return rec
	}
	applyEntries(servers, changes)
	mcp.setChild("servers", servers)
	doc.setChild("mcp", mcp)
	if !exists {
		if _, ok := doc.get("$schema"); !ok {
			doc = withSchema(doc)
		}
	}
	if err := writeAtomic(path, doc.indented(), 0o600); err != nil {
		warn("cannot write ~/.config/opencode/opencode.json: " + err.Error())
		return prev
	}
	return rec
}

// opencodeCommented is an opencode.json with comments or trailing commas,
// which opencode reads and a rewrite would lose: repose writes nothing
// and warns once per set of changes it holds back.
func opencodeCommented(doc *object, user map[string]any, want *Rendered, prev *agentRecord, warn func(string)) *agentRecord {
	mcp, err := doc.child("mcp")
	if err != nil {
		warn("~/.config/opencode/opencode.json mcp is not an object; leaving it alone")
		return prev
	}
	servers, err := mcp.child("servers")
	if err != nil {
		warn("~/.config/opencode/opencode.json mcp.servers is not an object; leaving it alone")
		return prev
	}
	changes, _ := plan(rawMap(servers), user, prev.User, want.replaceable())
	rec := &agentRecord{User: prev.User}
	if len(changes) == 0 {
		return rec
	}
	held := map[string]any{}
	for _, c := range changes {
		held[c.name] = c.want
	}
	if !equal(map[string]any(prev.Held), held) {
		warn("~/.config/opencode/opencode.json has comments, which a rewrite would lose; leaving it alone")
	}
	rec.Held = held
	return rec
}

func withSchema(doc *object) *object {
	o := newObject()
	o.set("$schema", mustRaw("https://opencode.ai/config.json"))
	for _, k := range doc.keys {
		o.set(k, doc.vals[k])
	}
	return o
}

// applyPi: pi has no file repose may share; the machine guide extension
// reads ~/.repose/mcp/agents/pi.json, which sync writes for every agent.
func applyPi(_ Paths, want *Rendered, _ *agentRecord, _ func(string)) *agentRecord {
	return &agentRecord{User: want.User}
}
