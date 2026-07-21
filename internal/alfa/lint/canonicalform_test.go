package lint

import "testing"

// TestClassifyCanonicalFormNotOptedIn confirms a flake with no opt-in
// directive yields no finding, even with scattered bindings — the check
// must never re-shape or flag a flake that hasn't opted in (FDR 0007).
func TestClassifyCanonicalFormNotOptedIn(t *testing.T) {
	const src = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
  };
  outputs = { self }: { };
}
`
	got, err := ClassifyCanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("ClassifyCanonicalForm: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil (not opted in)", got)
	}
}

// TestClassifyCanonicalFormScattered confirms an opted-in flake with
// scattered bindings yields a finding naming the scattered input.
func TestClassifyCanonicalFormScattered(t *testing.T) {
	const src = `{
  # doppelgang: canonical
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
  };
  outputs = { self }: { };
}
`
	got, err := ClassifyCanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("ClassifyCanonicalForm: %v", err)
	}
	if got == nil {
		t.Fatalf("got nil, want a finding")
	}
	if len(got.Scattered) != 1 || got.Scattered[0] != "igloo" {
		t.Errorf("Scattered = %v, want [igloo]", got.Scattered)
	}
	if got.LegacySentinel {
		t.Errorf("LegacySentinel = true, want false (structured directive used)")
	}
}

// TestClassifyCanonicalFormAlreadyCanonical confirms an opted-in flake whose
// bindings are already contiguous, using the structured directive, yields
// no finding.
func TestClassifyCanonicalFormAlreadyCanonical(t *testing.T) {
	const src = `{
  # doppelgang: canonical
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
    treefmt-nix.url = "github:numtide/treefmt-nix";
  };
  outputs = { self }: { };
}
`
	got, err := ClassifyCanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("ClassifyCanonicalForm: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil (already canonical)", got)
	}
}

// TestClassifyCanonicalFormLegacySentinelReported confirms a flake opted in
// via the deprecated `# canonical-form` sentinel yields a finding — even
// with contiguous bindings — since the deprecated spelling is itself
// actionable (--fix migrates it). This is the mid-session back-compat
// decision: the old spelling still works, but is surfaced so it gets
// upgraded rather than silently tolerated forever.
func TestClassifyCanonicalFormLegacySentinelReported(t *testing.T) {
	const src = `{
  # canonical-form
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
    treefmt-nix.url = "github:numtide/treefmt-nix";
  };
  outputs = { self }: { };
}
`
	got, err := ClassifyCanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("ClassifyCanonicalForm: %v", err)
	}
	if got == nil {
		t.Fatalf("got nil, want a finding (legacy sentinel used)")
	}
	if !got.LegacySentinel {
		t.Errorf("LegacySentinel = false, want true")
	}
	if len(got.Scattered) != 0 {
		t.Errorf("Scattered = %v, want none (bindings are contiguous)", got.Scattered)
	}
}
