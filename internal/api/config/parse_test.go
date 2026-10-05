package config

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseCheck(t *testing.T) {
	p, ok := NewParser()
	if !ok {
		t.Skip("nix-instantiate not on PATH")
	}
	ctx := context.Background()
	if err := p.Check(ctx, "{ pkgs, ... }: { home.packages = [ pkgs.ripgrep ]; }"); err != nil {
		t.Fatalf("valid fragment rejected: %v", err)
	}
	err := p.Check(ctx, "{ home.packages = [ pkgs.ripgrep ; }")
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ParseError, got %v", err)
	}
	if pe.Line != 1 || !strings.Contains(pe.Message, "syntax error") || !strings.Contains(pe.Message, "fragment.nix:1:") {
		t.Fatalf("got %+v", pe)
	}
	err = p.Check(ctx, "{\n  a = 1;\n  b = ;\n}")
	if !errors.As(err, &pe) || pe.Line != 3 {
		t.Fatalf("line 3 expected, got %v", err)
	}
	if err := p.Check(ctx, strings.Repeat("x", MaxFragmentBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size cap: %v", err)
	}
}

// A personal layer's syntax error names machine.nix (DECISIONS I-490).
func TestParseCheckPersonal(t *testing.T) {
	p, ok := NewParser()
	if !ok {
		t.Skip("nix-instantiate not on PATH")
	}
	err := p.CheckNamed(context.Background(), "{\n  home.packages = [ ;\n}", PersonalName)
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Line != 2 || !strings.HasPrefix(pe.Message, "syntax error at machine.nix:2:") || strings.Contains(pe.Message, "fragment.nix") {
		t.Fatalf("got %v", err)
	}
}
