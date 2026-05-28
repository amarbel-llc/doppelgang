package main

import (
	"os"
	"testing"
)

func TestResolveLintFormatExplicit(t *testing.T) {
	for _, f := range []string{"text", "json", "ndjson"} {
		got, err := resolveLintFormat(f, os.Stdout)
		if err != nil {
			t.Errorf("resolveLintFormat(%q): unexpected error %v", f, err)
		}
		if got != f {
			t.Errorf("resolveLintFormat(%q) = %q, want %q", f, got, f)
		}
	}
}

func TestResolveLintFormatInvalid(t *testing.T) {
	if _, err := resolveLintFormat("yaml", os.Stdout); err == nil {
		t.Error("resolveLintFormat(\"yaml\"): want error, got nil")
	}
}

func TestResolveLintFormatAutoNonTTY(t *testing.T) {
	// A pipe write end is not a character device, so auto must resolve to
	// ndjson — this is the redirected/piped case lint runs in under CI.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	got, err := resolveLintFormat("auto", w)
	if err != nil {
		t.Fatalf("resolveLintFormat(\"auto\", pipe): %v", err)
	}
	if got != "ndjson" {
		t.Errorf("auto on a non-TTY = %q, want ndjson", got)
	}
}
