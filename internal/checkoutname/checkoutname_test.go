package checkoutname

import (
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	for name, want := range map[string]bool{
		"":                            true,
		"todo-app":                    true,
		"job_search.v2":               true,
		"api-2":                       true,
		".":                           false,
		"..":                          false,
		"a/b":                         false,
		"../etc":                      false,
		"has space":                   false,
		"q?x":                         false,
		"a&b":                         false,
		"été":                         false,
		strings.Repeat("a", MaxLen):   true,
		strings.Repeat("a", MaxLen+1): false,
	} {
		if got := Valid(name); got != want {
			t.Errorf("Valid(%q) = %v, want %v", name, got, want)
		}
	}
}
