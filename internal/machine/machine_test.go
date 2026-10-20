package machine

import (
	"errors"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"homebox", "homebox"},
		{"Home Box", "home-box"},
		{"  spare  ", "spare"},
		{"a__b", "a-b"},
		{"-lead-and-trail-", "lead-and-trail"},
		{"ümlaut", "mlaut"},
		{"box.1", "box-1"},
		{"", ""},
		{"----", ""},
		{"0123456789012345678901234567890123456789extra", "0123456789012345678901234567890123456789"},
		{"012345678901234567890123456789012345678-x", "012345678901234567890123456789012345678"},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCheckName(t *testing.T) {
	taken := []string{"homebox", "todo-app"}
	cases := []struct {
		name string
		ok   bool
		is   error
	}{
		{"workshop", true, nil},
		{"box2", true, nil},
		{"a", true, nil},
		{"a-b-c", true, nil},
		{"", false, nil},
		{"Homebox", false, nil},  // not lowercase
		{"home box", false, nil}, // a space ends the Host line
		{"home.box", false, nil}, // a dot makes a third label in the alias
		{"-box", false, nil},     // ssh reads a leading dash as an option
		{"box-", false, nil},
		{"a--b", false, nil},
		{"box*", false, nil}, // an ssh pattern matches other hosts
		{"box?", false, nil},
		{"!box", false, nil},
		{"homebox", false, ErrExists},  // a machine already added
		{"todo-app", false, ErrExists}, // a project this laptop reaches
	}
	for _, c := range cases {
		err := CheckName(c.name, taken)
		if c.ok != (err == nil) {
			t.Errorf("CheckName(%q) = %v, want ok=%v", c.name, err, c.ok)
			continue
		}
		if c.is != nil && !errors.Is(err, c.is) {
			t.Errorf("CheckName(%q) = %v, want %v", c.name, err, c.is)
		}
	}
}

func TestAlias(t *testing.T) {
	if got := Alias("homebox"); got != "homebox.repose" {
		t.Errorf("Alias = %q", got)
	}
}
