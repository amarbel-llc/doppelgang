package nixedit

import (
	"strings"
	"testing"
)

// TestCanonicalFormDisabledByDefault confirms a flake with no
// `# canonical-form` sentinel reports Enabled=false and no Scattered
// findings, even though its inputs binding order is not contiguous — the
// opt-in gate must suppress detection entirely, not just --fix, per
// FDR 0007 (third-party flakes are never re-shaped or flagged).
func TestCanonicalFormDisabledByDefault(t *testing.T) {
	const src = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
  };
  outputs = { self }: { };
}
`
	got, err := CanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("CanonicalForm: %v", err)
	}
	if got.Enabled {
		t.Errorf("Enabled = true, want false (no sentinel present)")
	}
	if len(got.Scattered) != 0 {
		t.Errorf("Scattered = %v, want none when not opted in", got.Scattered)
	}
}

// TestCanonicalFormDetectsScatteredBlock confirms that, once opted in via
// the sentinel, a follows binding for igloo separated from igloo's own
// bindings by another input's binding is flagged as scattered.
func TestCanonicalFormDetectsScatteredBlock(t *testing.T) {
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
	got, err := CanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("CanonicalForm: %v", err)
	}
	if !got.Enabled {
		t.Fatalf("Enabled = false, want true (sentinel present)")
	}
	if !sameSet(got.Scattered, []string{"igloo"}) {
		t.Errorf("Scattered = %v, want [igloo]", got.Scattered)
	}
}

// TestCanonicalFormDetectsScatteredFlat is the flat-form analog: a follows
// binding for igloo separated by treefmt-nix's url is scattered.
func TestCanonicalFormDetectsScatteredFlat(t *testing.T) {
	const src = `{
  # canonical-form
  inputs.igloo.url = "github:amarbel-llc/igloo";
  inputs.treefmt-nix.url = "github:numtide/treefmt-nix";
  inputs.igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
  outputs = { self }: { };
}
`
	got, err := CanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("CanonicalForm: %v", err)
	}
	if !got.Enabled {
		t.Fatalf("Enabled = false, want true (sentinel present)")
	}
	if !sameSet(got.Scattered, []string{"igloo"}) {
		t.Errorf("Scattered = %v, want [igloo]", got.Scattered)
	}
}

// TestCanonicalFormNoScatteredWhenContiguous confirms an already-canonical,
// opted-in flake reports zero Scattered findings.
func TestCanonicalFormNoScatteredWhenContiguous(t *testing.T) {
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
	got, err := CanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("CanonicalForm: %v", err)
	}
	if !got.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if len(got.Scattered) != 0 {
		t.Errorf("Scattered = %v, want none", got.Scattered)
	}
}

// TestCanonicalFormFixTargetsDisabledIsNoop confirms --fix targets are empty
// when the flake has not opted in, regardless of scattering.
func TestCanonicalFormFixTargetsDisabledIsNoop(t *testing.T) {
	const src = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
  };
  outputs = { self }: { };
}
`
	del, add, err := CanonicalFormFixTargets([]byte(src))
	if err != nil {
		t.Fatalf("CanonicalFormFixTargets: %v", err)
	}
	if len(del) != 0 || len(add) != 0 {
		t.Errorf("del=%v add=%v, want none (not opted in)", del, add)
	}
}

// TestCanonicalFormFixReachesFixedPoint drives the full --fix pipeline (as
// main.go's lintFix does: DeleteBindings the scattered follows, then Apply
// them back) on a scattered, opted-in block-form flake, and confirms the
// result is both contiguous and a fixed point: fixing again is a no-op.
func TestCanonicalFormFixReachesFixedPoint(t *testing.T) {
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
	fixed := runCanonicalFormFix(t, []byte(src))
	report, err := CanonicalForm(fixed)
	if err != nil {
		t.Fatalf("CanonicalForm after fix: %v", err)
	}
	if len(report.Scattered) != 0 {
		t.Fatalf("still scattered after fix: %v\n%s", report.Scattered, fixed)
	}
	if !strings.Contains(string(fixed), `igloo.inputs.nixpkgs-master.follows`) {
		t.Fatalf("follows binding lost during fix:\n%s", fixed)
	}

	// Fixed point: fixing the already-fixed output changes nothing.
	del2, add2, err := CanonicalFormFixTargets(fixed)
	if err != nil {
		t.Fatalf("CanonicalFormFixTargets (second pass): %v", err)
	}
	if len(del2) != 0 || len(add2) != 0 {
		t.Errorf("second pass del=%v add=%v, want none (already canonical)", del2, add2)
	}
}

// TestCanonicalFormFixNoopWhenAlreadyCanonical confirms --fix changes
// nothing, byte-for-byte, when run on an already-canonical opted-in flake —
// the defining fixed-point property from FDR 0007.
func TestCanonicalFormFixNoopWhenAlreadyCanonical(t *testing.T) {
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
	fixed := runCanonicalFormFix(t, []byte(src))
	if string(fixed) != src {
		t.Errorf("--fix changed an already-canonical flake:\n%s", fixed)
	}
}

// runCanonicalFormFix applies the CanonicalFormFixTargets-derived
// delete-then-reapply pipeline once, mirroring lintFix's wiring, and returns
// the result.
func runCanonicalFormFix(t *testing.T, src []byte) []byte {
	t.Helper()
	del, add, err := CanonicalFormFixTargets(src)
	if err != nil {
		t.Fatalf("CanonicalFormFixTargets: %v", err)
	}
	if len(del) == 0 {
		return src
	}
	out, _, err := DeleteBindings(src, del)
	if err != nil {
		t.Fatalf("DeleteBindings: %v", err)
	}
	out, _, err = Apply(out, add)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return out
}
