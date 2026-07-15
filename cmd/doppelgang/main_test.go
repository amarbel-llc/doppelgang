package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/friedenberg/doppelgang/internal/alfa/lint"
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

	rep, err := analyzeFlake(context.Background(), dir, lint.AllSelection(), false, "")
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
	if !reportHasFindings(rep, lint.AllSelection()) {
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

	rep, err := analyzeFlake(context.Background(), dir, lint.AllSelection(), false, "")
	if err != nil {
		t.Fatalf("analyzeFlake: %v", err)
	}
	if len(rep.DeadOverrides) != 0 {
		t.Errorf("want no dead overrides for a flake.nix with no overrides, got %+v", rep.DeadOverrides)
	}
}

// TestAnalyzeFlakeSkipsDeadOverridesWhenDeselected confirms that excluding
// dead-overrides via --checks skips the flake.nix dead-override pass
// entirely (so a real dead override is not computed), not merely hidden at
// render time.
func TestAnalyzeFlakeSkipsDeadOverridesWhenDeselected(t *testing.T) {
	dir := writeFlake(t, `{
  inputs = {
    dep.url = "github:o/dep";
    dep.inputs.gone.follows = "dep";
  };
  outputs = { self, dep }: { };
}
`, depLock)

	sel, err := lint.ParseSelection("follows,multi-version")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	rep, err := analyzeFlake(context.Background(), dir, sel, false, "")
	if err != nil {
		t.Fatalf("analyzeFlake: %v", err)
	}
	if len(rep.DeadOverrides) != 0 {
		t.Errorf("dead-overrides deselected: want none computed, got %+v", rep.DeadOverrides)
	}
	if reportHasFindings(rep, sel) {
		t.Errorf("reportHasFindings = true, want false (only the deselected dead override exists)")
	}
}

// TestReportHasFindingsRespectsSelection is the exit-code-over-a-subset
// regression: the eng#205 shape where a flake intentionally pins inputs at
// multiple revs (a multi-version finding) but has clean follows and dead
// overrides. Gating on follows+dead-overrides must NOT exit non-zero on the
// excluded multi-version finding.
func TestReportHasFindingsRespectsSelection(t *testing.T) {
	multiOnly := lint.Report{MultiVersion: []lint.MultiVersionInput{{Source: "o/r"}}}
	if !reportHasFindings(multiOnly, lint.AllSelection()) {
		t.Errorf("all checks: want findings (multi-version present)")
	}
	sel, err := lint.ParseSelection("follows,dead-overrides")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	if reportHasFindings(multiOnly, sel) {
		t.Errorf("follows,dead-overrides: a multi-version finding must not gate the exit")
	}

	// And the converse: a follows finding must not gate a multi-version-only
	// selection.
	followsOnly := lint.Report{Follows: []lint.FollowsRec{{Identity: "o/r", Canonical: "a"}}}
	mv, err := lint.ParseSelection("multi-version")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	if reportHasFindings(followsOnly, mv) {
		t.Errorf("multi-version: a follows finding must not gate the exit")
	}
}

// TestReportOnlyCountRespectsSelection confirms the --fix report-only
// accounting drops a deselected multi-version finding (so `--fix
// --checks follows,dead-overrides` does not report it as a remaining
// report-only finding).
func TestReportOnlyCountRespectsSelection(t *testing.T) {
	rep := lint.Report{
		MultiVersion:  []lint.MultiVersionInput{{Source: "o/r"}, {Source: "o/s"}},
		DeadOverrides: []lint.DeadOverride{{Override: "x", Direct: false}}, // transitive
	}
	if got := reportOnlyCount(rep, lint.AllSelection()); got != 3 {
		t.Errorf("all: reportOnlyCount = %d, want 3 (2 multi-version + 1 transitive)", got)
	}
	sel, err := lint.ParseSelection("follows,dead-overrides")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	if got := reportOnlyCount(rep, sel); got != 1 {
		t.Errorf("multi-version deselected: reportOnlyCount = %d, want 1 (transitive only)", got)
	}
}

// TestAnalyzeFlakeDetectsMissingNixpkgsMaster: a flake.nix declaring no
// nixpkgs-master input yields a Missing finding under the nixpkgs-master
// selection, and gates the exit.
func TestAnalyzeFlakeDetectsMissingNixpkgsMaster(t *testing.T) {
	dir := writeFlake(t, `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
  };
  outputs = { self, igloo }: { };
}
`, depLock)

	sel, err := lint.ParseSelection("nixpkgs-master")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	rep, err := analyzeFlake(context.Background(), dir, sel, false, "")
	if err != nil {
		t.Fatalf("analyzeFlake: %v", err)
	}
	if rep.NixpkgsMaster == nil || rep.NixpkgsMaster.Status != lint.NixpkgsMasterMissing {
		t.Fatalf("want a Missing nixpkgs-master finding, got %+v", rep.NixpkgsMaster)
	}
	if !reportHasFindings(rep, sel) {
		t.Errorf("reportHasFindings = false, want true (missing nixpkgs-master)")
	}
}

// TestAnalyzeFlakeNixpkgsMasterConformant: a flake pinned to the convention
// yields no finding and does not gate the exit.
func TestAnalyzeFlakeNixpkgsMasterConformant(t *testing.T) {
	dir := writeFlake(t, `{
  inputs = {
    nixpkgs-master.url = "github:NixOS/nixpkgs/567a49d1913ce81ac6e9582e3553dd90a955875f";
  };
  outputs = { self }: { };
}
`, depLock)

	sel, err := lint.ParseSelection("nixpkgs-master")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	rep, err := analyzeFlake(context.Background(), dir, sel, false, "")
	if err != nil {
		t.Fatalf("analyzeFlake: %v", err)
	}
	if rep.NixpkgsMaster != nil {
		t.Errorf("conformant flake flagged: %+v", rep.NixpkgsMaster)
	}
	if reportHasFindings(rep, sel) {
		t.Errorf("reportHasFindings = true, want false (conformant)")
	}
}

// TestAnalyzeFlakeNixpkgsMasterWithoutLock is the self-onboarding regression:
// `--checks nixpkgs-master` must work on a freshly-cloned repo that has a
// flake.nix but no flake.lock yet — the lock is not loaded when only the
// nixpkgs-master check is selected.
func TestAnalyzeFlakeNixpkgsMasterWithoutLock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(`{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
  };
  outputs = { self, igloo }: { };
}
`), 0o644); err != nil {
		t.Fatalf("write flake.nix: %v", err)
	}
	// Deliberately no flake.lock in dir.

	sel, err := lint.ParseSelection("nixpkgs-master")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	rep, err := analyzeFlake(context.Background(), dir, sel, false, "")
	if err != nil {
		t.Fatalf("analyzeFlake must not require a lock for the nixpkgs-master check: %v", err)
	}
	if rep.NixpkgsMaster == nil || rep.NixpkgsMaster.Status != lint.NixpkgsMasterMissing {
		t.Errorf("want a Missing finding for the lockless flake, got %+v", rep.NixpkgsMaster)
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

// TestPapiRepoURLsFromJSONSingleEntry: a name that appears exactly once is
// accepted unconditionally regardless of the canonical field — this is the
// unchanged single-entry path.
func TestPapiRepoURLsFromJSONSingleEntry(t *testing.T) {
	data := []byte(`[{"name":"myrepo","url":"https://code.example.com/myrepo"}]`)
	var w bytes.Buffer
	m := papiRepoURLsFromJSON("example.com", data, &w)
	if w.Len() != 0 {
		t.Errorf("single entry: unexpected stderr: %q", w.String())
	}
	want := "git+https://code.example.com/myrepo.git"
	if got := m["myrepo"]; got != want {
		t.Errorf("single entry: m[\"myrepo\"] = %q, want %q", got, want)
	}
}

// TestPapiRepoURLsFromJSONDuplicateWithMarker: when a name appears twice and
// exactly one entry carries canonical:true, that entry's URL wins.
func TestPapiRepoURLsFromJSONDuplicateWithMarker(t *testing.T) {
	data := []byte(`[
		{"name":"myrepo","url":"https://code.example.com/myrepo","canonical":true},
		{"name":"myrepo","url":"https://github.com/owner/myrepo","canonical":false}
	]`)
	var w bytes.Buffer
	m := papiRepoURLsFromJSON("example.com", data, &w)
	if w.Len() != 0 {
		t.Errorf("duplicate with marker: unexpected stderr: %q", w.String())
	}
	want := "git+https://code.example.com/myrepo.git"
	if got := m["myrepo"]; got != want {
		t.Errorf("duplicate with marker: m[\"myrepo\"] = %q, want %q", got, want)
	}
}

// TestPapiRepoURLsFromJSONDuplicateNoMarker: when a name appears multiple
// times with no canonical marker (pre-amendment server), the repo is skipped
// and a warning is written — never let order pick silently.
func TestPapiRepoURLsFromJSONDuplicateNoMarker(t *testing.T) {
	data := []byte(`[
		{"name":"myrepo","url":"https://code.example.com/myrepo"},
		{"name":"myrepo","url":"https://github.com/owner/myrepo"}
	]`)
	var w bytes.Buffer
	m := papiRepoURLsFromJSON("example.com", data, &w)
	if _, ok := m["myrepo"]; ok {
		t.Errorf("duplicate no marker: myrepo should be absent from map, got %q", m["myrepo"])
	}
	if !strings.Contains(w.String(), "ambiguous") {
		t.Errorf("duplicate no marker: expected ambiguous warning in stderr, got %q", w.String())
	}
}

// TestPapiRepoURLsFromJSONDuplicateMultipleMarkers: when a name appears with
// multiple canonical:true entries (server nonconformance), the repo is skipped
// and a warning is written.
func TestPapiRepoURLsFromJSONDuplicateMultipleMarkers(t *testing.T) {
	data := []byte(`[
		{"name":"myrepo","url":"https://code.example.com/myrepo","canonical":true},
		{"name":"myrepo","url":"https://github.com/owner/myrepo","canonical":true}
	]`)
	var w bytes.Buffer
	m := papiRepoURLsFromJSON("example.com", data, &w)
	if _, ok := m["myrepo"]; ok {
		t.Errorf("multiple markers: myrepo should be absent from map, got %q", m["myrepo"])
	}
	if !strings.Contains(w.String(), "nonconformance") {
		t.Errorf("multiple markers: expected nonconformance warning in stderr, got %q", w.String())
	}
}

// TestPapiRepoURLsFromJSONUnparseable: an invalid JSON body returns nil and
// writes an unparseable notice (offline-degrade contract).
func TestPapiRepoURLsFromJSONUnparseable(t *testing.T) {
	var w bytes.Buffer
	m := papiRepoURLsFromJSON("example.com", []byte(`not json`), &w)
	if m != nil {
		t.Errorf("unparseable: expected nil map, got %v", m)
	}
	if !strings.Contains(w.String(), "unparseable") {
		t.Errorf("unparseable: expected unparseable notice in stderr, got %q", w.String())
	}
}
