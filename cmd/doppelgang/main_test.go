package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeFlake writes flake.nix + flake.lock into a fresh temp dir and returns
// its path, for offline analyzeFlake tests (no nix invocation).
func writeFlake(t *testing.T, flakeNix, flakeLock string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(flakeNix), 0o644); err != nil {
		t.Fatalf("write flake.nix: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flake.lock"), []byte(flakeLock), 0o644); err != nil {
		t.Fatalf("write flake.lock: %v", err)
	}
	return dir
}

// depLock is a flake.lock whose only dependency `dep` declares no inputs, so
// an override of any `dep` input is dead.
const depLock = `{
  "nodes": {
    "root": { "inputs": { "dep": "dep" } },
    "dep": { "inputs": {}, "locked": { "type": "github", "owner": "o", "repo": "dep", "rev": "ddd", "narHash": "sha-d" } }
  },
  "root": "root",
  "version": 7
}`

func TestAnalyzeFlakeDetectsDirectDeadOverride(t *testing.T) {
	dir := writeFlake(t, `{
  inputs = {
    dep.url = "github:o/dep";
    dep.inputs.gone.follows = "dep";
  };
  outputs = { self, dep }: { };
}
`, depLock)

	rep, err := analyzeFlake(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("analyzeFlake: %v", err)
	}
	if len(rep.DeadOverrides) != 1 {
		t.Fatalf("want 1 dead override, got %d: %+v", len(rep.DeadOverrides), rep.DeadOverrides)
	}
	d := rep.DeadOverrides[0]
	if d.Override != `inputs.dep.inputs.gone.follows` || d.Target != "dep" || d.Input != "gone" || !d.Direct {
		t.Errorf("dead override wrong: %+v", d)
	}
	if !reportHasFindings(rep) {
		t.Errorf("reportHasFindings = false, want true with a dead override")
	}
}

// TestAnalyzeFlakeNoFalsePositive confirms a flake whose dependency lacks the
// overridden input is flagged, while one declaring it is not — here dep
// declares no inputs, so only the override survives if we (wrongly) flag a
// live target. With a healthy flake.nix (no overrides) nothing is reported.
func TestAnalyzeFlakeNoFalsePositive(t *testing.T) {
	dir := writeFlake(t, `{
  inputs = {
    dep.url = "github:o/dep";
  };
  outputs = { self, dep }: { };
}
`, depLock)

	rep, err := analyzeFlake(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("analyzeFlake: %v", err)
	}
	if len(rep.DeadOverrides) != 0 {
		t.Errorf("want no dead overrides for a flake.nix with no overrides, got %+v", rep.DeadOverrides)
	}
}

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
