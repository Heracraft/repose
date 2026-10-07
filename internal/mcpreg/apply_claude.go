package mcpreg

import "path/filepath"

// applyEntries applies changes to the servers object o.
func applyEntries(o *object, changes []change) {
	for _, c := range changes {
		switch c.act {
		case actAdd, actReplace:
			o.set(c.name, mustRaw(c.want))
		case actRemove:
			o.del(c.name)
		}
	}
}

// applyClaude merges want into ~/.claude.json: user-scope servers into
// mcpServers, local-scope ones into projects[path].mcpServers, under Claude
// Code's own lock, re-reading the file once the lock is held. It returns
// the record for rendered.json; on any problem it warns, leaves the file
// alone and returns prev.
func applyClaude(p Paths, want *Rendered, prev *agentRecord, warn func(string)) *agentRecord {
	path := filepath.Join(p.Home, ".claude.json")
	unlock, err := lockDir(path+".lock", claudeLockWait, claudeLockStale)
	if err != nil {
		if err == errLocked {
			warn("~/.claude.json stayed locked; MCP servers not updated for claude")
		} else {
			warn("cannot lock ~/.claude.json: " + err.Error())
		}
		return prev
	}
	defer unlock()

	b, exists, err := readFile(path)
	if err != nil {
		warn("cannot read ~/.claude.json: " + err.Error())
		return prev
	}
	doc, err := parseObject(b)
	if err != nil {
		warn("~/.claude.json is not valid JSON; leaving it alone")
		return prev
	}
	servers, err := doc.child("mcpServers")
	if err != nil {
		warn("~/.claude.json mcpServers is not an object; leaving it alone")
		return prev
	}
	changed := false
	rec := &agentRecord{}
	changes, owned := plan(rawMap(servers), want.User, prev.User, want.replaceable())
	if len(changes) > 0 {
		applyEntries(servers, changes)
		doc.setChild("mcpServers", servers)
		changed = true
	}
	if len(owned) > 0 {
		rec.User = owned
	}

	paths := map[string]bool{}
	for k := range want.Projects {
		paths[k] = true
	}
	for k := range prev.Projects {
		paths[k] = true
	}
	if len(paths) > 0 {
		projects, err := doc.child("projects")
		if err != nil {
			warn("~/.claude.json projects is not an object; project MCP servers not updated for claude")
		} else {
			pchanged := false
			for _, path := range sortedStrings(paths) {
				proj, err := projects.child(path)
				if err != nil {
					continue
				}
				ps, err := proj.child("mcpServers")
				if err != nil {
					continue
				}
				changes, owned := plan(rawMap(ps), want.Projects[path], prev.Projects[path], want.ProjectLegacy[path])
				if len(owned) > 0 {
					if rec.Projects == nil {
						rec.Projects = map[string]map[string]any{}
					}
					rec.Projects[path] = owned
				}
				if len(changes) == 0 {
					continue
				}
				applyEntries(ps, changes)
				proj.setChild("mcpServers", ps)
				projects.setChild(path, proj)
				pchanged = true
			}
			if pchanged {
				doc.setChild("projects", projects)
				changed = true
			}
		}
	}
	if !changed {
		return rec
	}
	if !exists && len(doc.keys) == 0 {
		return rec
	}
	if err := writeAtomic(path, doc.indented(), 0o600); err != nil {
		warn("cannot write ~/.claude.json: " + err.Error())
		return prev
	}
	return rec
}
