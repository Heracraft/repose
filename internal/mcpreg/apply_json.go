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

// applyOpencode merges want into ~/.config/opencode/config.json `mcp`,
// which opencode loads beneath the user's opencode.json: a server of the
// same name there wins field by field in opencode itself, and an entry in
// config.json is repose's only while it equals what repose wrote.
func applyOpencode(p Paths, want *Rendered, prev *agentRecord, warn func(string)) *agentRecord {
	path := filepath.Join(p.Home, ".config", "opencode", "config.json")
	b, exists, err := readFile(path)
	if err != nil {
		warn("cannot read ~/.config/opencode/config.json: " + err.Error())
		return prev
	}
	doc, err := parseObject(b)
	if err != nil {
		warn("~/.config/opencode/config.json is not valid JSON; leaving it alone")
		return prev
	}
	servers, err := doc.child("mcp")
	if err != nil {
		warn("~/.config/opencode/config.json mcp is not an object; leaving it alone")
		return prev
	}
	changes, owned := plan(rawMap(servers), want.User, prev.User, want.Retired)
	rec := &agentRecord{}
	if len(owned) > 0 {
		rec.User = owned
	}
	if len(changes) == 0 {
		return rec
	}
	applyEntries(servers, changes)
	doc.setChild("mcp", servers)
	if !exists {
		if _, ok := doc.get("$schema"); !ok {
			doc = withSchema(doc)
		}
	}
	if err := writeAtomic(path, doc.indented(), 0o600); err != nil {
		warn("cannot write ~/.config/opencode/config.json: " + err.Error())
		return prev
	}
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
