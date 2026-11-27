package cli

import "strings"

// keychainAddArgs and keychainAddStdin are how the login is written to
// the Mac's Keychain without the token on a command line, where any
// local user reads it from ps while `security` runs (ENG-275):
// `security -i` reads its commands from stdin, and the command carries
// the value there.
func keychainAddArgs() []string { return []string{"-i"} }

// keychainAddStdin is the one command `security -i` reads. Its parser
// splits on spaces and honours double quotes with backslash escapes, so
// each value is quoted and its `"` and `\` escaped.
func keychainAddStdin(service, account, value string) string {
	return "add-generic-password -U -s " + keychainQuote(service) + " -a " + keychainQuote(account) +
		" -w " + keychainQuote(value) + "\n"
}

func keychainQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
