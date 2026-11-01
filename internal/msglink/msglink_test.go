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
		{"https://x.example/", "a\u202eb\u200bc\x07", Link{URL: "https://x.example/", Label: "abc"}, nil},
		{"javascript:alert(1)", "", Link{}, ErrURL},
		{"ftp://x.example/", "", Link{}, ErrURL},
		{"http:///path", "", Link{}, ErrURL},
		{"localhost:3000", "", Link{}, ErrURL},
		{"https://user:pass@evil.example/", "", Link{}, ErrUserInfo},
		{"https://bank.example@evil.example/", "", Link{}, ErrUserInfo},
		{"https://x.example/" + strings.Repeat("a", 2048), "", Link{}, ErrTooLong},
		{"http://a.example/?q=\xff", "", Link{}, ErrURL},
		{"http://a.example/?q=é", "", Link{}, ErrURL},
		{"HTTP://GitHub.COM/x", "", Link{URL: "http://github.com/x"}, nil},
		{"http://bücher.example/a", "", Link{URL: "http://xn--bcher-kva.example/a"}, nil},
		{"https://аpple.com/", "", Link{URL: "https://xn--pple-43d.com/"}, nil},
		{"http://ex<ample>.com/", "", Link{}, ErrURL},
		{"http://x.example:99999/", "", Link{}, ErrURL},
		{"https://x.example/", "Sign in · accounts.google.com", Link{URL: "https://x.example/", Label: "Sign in accounts.google.com"}, nil},
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
		{"http://[2001:db8::1]:8080/", "", "Open [2001:db8::1]:8080"},
		{"http://[::ffff:127.0.0.1]:3000/", "", "Open localhost:3000"},
	} {
		l, err := Check(c.url, c.label)
		if err != nil || l.Button() != c.want {
			t.Errorf("%q %q: %q %v", c.url, c.label, l.Button(), err)
		}
	}
}

func TestButtonFitKeepsTheHost(t *testing.T) {
	l, err := Check("https://aaaaaaaaaaaaaaaaaaaaaaaaaaaa.evil.example/", "Sign in at the accounts google page now")
	if err != nil {
		t.Fatal(err)
	}
	got := l.ButtonFit(64)
	if len(got) > 64 || !strings.HasSuffix(got, " · aaaaaaaaaaaaaaaaaaaaaaaaaaaa.evil.example") {
		t.Fatalf("%q (%d)", got, len(got))
	}
	long, _ := Check("https://"+strings.Repeat("b", 70)+".example/", "x")
	if got := long.ButtonFit(64); len(got) > 64 || !strings.HasPrefix(got, "bbbb") {
		t.Fatalf("%q", got)
	}
	short, _ := Check("http://localhost:8742/", "Compare resumes")
	if short.ButtonFit(64) != "Compare resumes · localhost:8742" {
		t.Fatal(short.ButtonFit(64))
	}
}
