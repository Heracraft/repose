package msglink

import (
	"errors"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		url, label string
		want       Link
		err        error
	}{
		{"", "", Link{}, nil},
		{"", "x", Link{}, ErrLabel},
		{"http://localhost:8742/out/a.html", "Compare resumes", Link{URL: "http://localhost:8742/out/a.html", Label: "Compare resumes"}, nil},
		{" https://github.com/h/r/pull/12 ", "  PR\t12\n", Link{URL: "https://github.com/h/r/pull/12", Label: "PR 12"}, nil},
		{"https://x.example/", "a‮b​c\x07", Link{URL: "https://x.example/", Label: "abc"}, nil},
		{"javascript:alert(1)", "", Link{}, ErrURL},
		{"ftp://x.example/", "", Link{}, ErrURL},
		{"http:///path", "", Link{}, ErrURL},
		{"localhost:3000", "", Link{}, ErrURL},
		{"https://user:pass@evil.example/", "", Link{}, ErrUserInfo},
		{"https://bank.example@evil.example/", "", Link{}, ErrUserInfo},
		{"https://x.example/" + strings.Repeat("a", 2048), "", Link{}, ErrTooLong},
	} {
		got, err := Check(c.url, c.label)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("Check(%q, %q) = %+v, %v; want %+v, %v", c.url, c.label, got, err, c.want, c.err)
		}
	}
	if l, _ := Check("https://x.example/", strings.Repeat("é", 50)); len([]rune(l.Label)) != MaxLabel {
		t.Errorf("label not capped: %q", l.Label)
	}
}

func TestButton(t *testing.T) {
	for _, c := range []struct{ url, label, want string }{
		{"http://localhost:8742/out/a.html", "Compare resumes", "Compare resumes · localhost:8742"},
		{"http://127.0.0.1:5173/", "", "Open localhost:5173"},
		{"http://[::1]:3000/", "", "Open localhost:3000"},
		{"https://github.com/h/r/pull/12", "Verify your account", "Verify your account · github.com"},
	} {
		l, err := Check(c.url, c.label)
		if err != nil || l.Button() != c.want {
			t.Errorf("%q %q: %q %v", c.url, c.label, l.Button(), err)
		}
	}
}
