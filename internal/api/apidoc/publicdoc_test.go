package apidoc

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestPublicAPIDocNamesRealRoutes: every route the public /docs/api page
// names (DECISIONS I-1324) is a documented route of
// docs/interfaces/api.md, so the page cannot promise a route the api
// lacks, and every route a token's scopes open is on the page.
func TestPublicAPIDocNamesRealRoutes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "apps", "web", "src", "content", "docs", "api.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, r := range documented {
		have[r.Method+" "+r.Path] = true
	}
	re := regexp.MustCompile("`(GET|POST|PUT|PATCH|DELETE) (/[^`]*)`")
	named := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		key := m[1] + " " + m[2]
		named[key] = true
		if !have[key] {
			t.Errorf("/docs/api names %s, which docs/interfaces/api.md does not document", key)
		}
	}
	// The routes a token opens, by the api's policy, are all on the
	// page: the never-list and the token routes themselves are the only
	// documented user routes allowed to be absent. `POST /hosts` and
	// `DELETE /hosts/{id}` are on the never-list (I-1366): adding a host
	// hands out a join token and removing one retires a machine, so
	// both take a login.
	absentOK := regexp.MustCompile(`^(PATCH|DELETE) /me$|^POST /me/notify-test$|^POST /certs|^POST /projects/:id/browser/view$|/billing|^GET /usage$|/me/tokens|^POST /projects/:id/fork$|^(POST|DELETE) /hosts|^GET /browser/stream$|^GET /public/seats$|/notify/unsubscribe$|^(GET|POST) /questions/reply$|^POST /billing/webhook$|^/internal`)
	for _, r := range documented {
		key := r.Method + " " + r.Path
		if strings.HasPrefix(r.Path, "/internal") || absentOK.MatchString(key) {
			continue
		}
		if !named[key] {
			t.Errorf("docs/interfaces/api.md documents %s, which /docs/api does not list under a scope", key)
		}
	}
}
