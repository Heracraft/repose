package mcpreg

type action int

const (
	actAdd action = iota + 1
	actReplace
	actRemove
)

type change struct {
	name string
	act  action
	want any
}

// plan decides, per name, what sync does to an agent's entries (DECISIONS
// I-246, I-555). cur is what the agent's file holds; want what repose
// renders now; prev what it wrote last time (rendered.json); retired the
// values earlier bases wrote. An entry is repose's while its value equals
// want, prev or a retired value; repose then replaces or removes it.
// Anything else under a name is the user's and stays. owned is what the
// file holds of repose's after the changes, for rendered.json.
func plan(cur, want, prev map[string]any, retired map[string][]any) (changes []change, owned map[string]any) {
	owned = map[string]any{}
	ours := func(name string, v any) bool {
		if p, ok := prev[name]; ok && equal(v, p) {
			return true
		}
		for _, r := range retired[name] {
			if equal(v, r) {
				return true
			}
		}
		return false
	}
	for _, name := range sortedKeys(want) {
		w := want[name]
		c, ok := cur[name]
		switch {
		case !ok:
			changes = append(changes, change{name, actAdd, w})
			owned[name] = w
		case equal(c, w):
			owned[name] = w
		case ours(name, c):
			changes = append(changes, change{name, actReplace, w})
			owned[name] = w
		}
	}
	for _, name := range sortedKeys(cur) {
		if _, ok := want[name]; ok {
			continue
		}
		if ours(name, cur[name]) {
			changes = append(changes, change{name: name, act: actRemove})
		}
	}
	return changes, owned
}
