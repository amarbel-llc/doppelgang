package lint

import "testing"

// TestClassifyCanonicalFormNotOptedIn confirms a flake with no
// `# canonical-form` sentinel yields no finding, even with scattered
// bindings — the check must never re-shape or flag a flake that hasn't
// opted in (FDR 0007).
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
  # canonical-form
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
}

// TestClassifyCanonicalFormAlreadyCanonical confirms an opted-in flake whose
// bindings are already contiguous yields no finding.
func TestClassifyCanonicalFormAlreadyCanonical(t *testing.T) {
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
	if got != nil {
		t.Errorf("got %+v, want nil (already canonical)", got)
	}
}
