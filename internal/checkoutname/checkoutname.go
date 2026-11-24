// Package checkoutname is the one rule for the name of a project's
// checkout under /home/dev that the api stores (DECISIONS I-1610): the
// api, the fake api and the CLI apply it, and the dashboard puts a name
// that passes it into a claude:// link.
package checkoutname

// MaxLen is the longest name the api stores.
const MaxLen = 100

// Valid is whether name can be stored as the checkout: "" (the machine
// has no checkout; work happens in the home), or one path element of
// letters, digits, '.', '_' and '-' (what the CLI's dirProjectName
// keeps), at most MaxLen long, and never "." or "..".
func Valid(name string) bool {
	if name == "" {
		return true
	}
	if len(name) > MaxLen || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !allowed(r) {
			return false
		}
	}
	return true
}

func allowed(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
}
