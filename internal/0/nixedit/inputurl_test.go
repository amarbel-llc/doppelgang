package nixedit

import (
	"errors"
	"testing"
)

// pinnedSHA is a real 40-hex nixpkgs revision, used as the repair target.
const (
	pinnedSHA = "567a49d1913ce81ac6e9582e3553dd90a955875f"
	pinnedURL = "github:NixOS/nixpkgs/" + pinnedSHA
)

// TestInputURLBlockForm reads a nixpkgs-master url declared flat inside an
// `inputs = { … }` block — the shape most of the fleet uses.
func TestInputURLBlockForm(t *testing.T) {
	const src = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "` + pinnedURL + `";
  };
  outputs = { self, igloo }: { };
}
`
	url, present, err := InputURL([]byte(src), "nixpkgs-master")
	if err != nil {
		t.Fatalf("InputURL: %v", err)
	}
	if !present {
		t.Fatalf("present = false, want true")
	}
	if url != pinnedURL {
		t.Errorf("url = %q, want %q", url, pinnedURL)
	}
}

// TestInputURLFlatForm reads a nixpkgs-master url declared as a top-level
// flat `inputs.nixpkgs-master.url` binding (e.g. gomod2nix's flake.nix).
func TestInputURLFlatForm(t *testing.T) {
	const src = `{
  inputs.nixpkgs-master.url = "` + pinnedURL + `";
  outputs = { self }: { };
}
`
	url, present, err := InputURL([]byte(src), "nixpkgs-master")
	if err != nil {
		t.Fatalf("InputURL: %v", err)
	}
	if !present || url != pinnedURL {
		t.Errorf("InputURL = (%q, %v), want (%q, true)", url, present, pinnedURL)
	}
}

// TestInputURLNestedForm reads a nixpkgs-master url declared inside a nested
// sub-attrset input value `nixpkgs-master = { url = …; }`.
func TestInputURLNestedForm(t *testing.T) {
	const src = `{
  inputs = {
    nixpkgs-master = {
      url = "` + pinnedURL + `";
    };
  };
  outputs = { self }: { };
}
`
	url, present, err := InputURL([]byte(src), "nixpkgs-master")
	if err != nil {
		t.Fatalf("InputURL: %v", err)
	}
	if !present || url != pinnedURL {
		t.Errorf("InputURL = (%q, %v), want (%q, true)", url, present, pinnedURL)
	}
}

// TestInputURLAbsent reports not-present when the input is not declared —
// including the case where nixpkgs-master appears only as a follows override
// on a dependency (which is not a top-level `.url` declaration).
func TestInputURLAbsent(t *testing.T) {
	const src = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
  };
  outputs = { self, igloo }: { };
}
`
	url, present, err := InputURL([]byte(src), "nixpkgs-master")
	if err != nil {
		t.Fatalf("InputURL: %v", err)
	}
	if present {
		t.Errorf("present = true, want false (only a follows override, no top-level url); url=%q", url)
	}
}

// TestSetInputURLBlockSplicesMissing splices a missing nixpkgs-master input
// into an `inputs = { … }` block, preserving every other byte. The expected
// output is asserted exactly so any stray whitespace or clobbered byte fails.
func TestSetInputURLBlockSplicesMissing(t *testing.T) {
	const src = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
  };
  outputs = { self, igloo }: { };
}
`
	const want = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "` + pinnedURL + `";
  };
  outputs = { self, igloo }: { };
}
`
	out, changed, err := SetInputURL([]byte(src), "nixpkgs-master", pinnedURL)
	if err != nil {
		t.Fatalf("SetInputURL: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (input was missing)")
	}
	if string(out) != want {
		t.Errorf("splice mismatch\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// TestSetInputURLFlatSplicesMissing splices a missing nixpkgs-master input as
// a top-level flat `inputs.nixpkgs-master.url` sibling.
func TestSetInputURLFlatSplicesMissing(t *testing.T) {
	const src = `{
  inputs.igloo.url = "github:amarbel-llc/igloo";
  outputs = { self, igloo }: { };
}
`
	const want = `{
  inputs.igloo.url = "github:amarbel-llc/igloo";
  inputs.nixpkgs-master.url = "` + pinnedURL + `";
  outputs = { self, igloo }: { };
}
`
	out, changed, err := SetInputURL([]byte(src), "nixpkgs-master", pinnedURL)
	if err != nil {
		t.Fatalf("SetInputURL: %v", err)
	}
	if !changed || string(out) != want {
		t.Errorf("flat splice mismatch (changed=%v)\n--- got ---\n%s\n--- want ---\n%s", changed, out, want)
	}
}

// TestSetInputURLRewritesFloating rewrites a floating (unpinned) nixpkgs-master
// url in place, leaving every other byte — including the surrounding bindings
// and layout — untouched.
func TestSetInputURLRewritesFloating(t *testing.T) {
	const src = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "github:NixOS/nixpkgs";
    utils.url = "https://example/f/0.1";
  };
  outputs = { self, igloo }: { };
}
`
	const want = `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "` + pinnedURL + `";
    utils.url = "https://example/f/0.1";
  };
  outputs = { self, igloo }: { };
}
`
	out, changed, err := SetInputURL([]byte(src), "nixpkgs-master", pinnedURL)
	if err != nil {
		t.Fatalf("SetInputURL: %v", err)
	}
	if !changed || string(out) != want {
		t.Errorf("floating rewrite mismatch (changed=%v)\n--- got ---\n%s\n--- want ---\n%s", changed, out, want)
	}
}

// TestSetInputURLRewritesBranchRef rewrites a branch-name ref
// (`github:NixOS/nixpkgs/nixpkgs-unstable`) to the pinned sha.
func TestSetInputURLRewritesBranchRef(t *testing.T) {
	const src = `{
  inputs = {
    nixpkgs-master.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };
  outputs = { self }: { };
}
`
	const want = `{
  inputs = {
    nixpkgs-master.url = "` + pinnedURL + `";
  };
  outputs = { self }: { };
}
`
	out, changed, err := SetInputURL([]byte(src), "nixpkgs-master", pinnedURL)
	if err != nil {
		t.Fatalf("SetInputURL: %v", err)
	}
	if !changed || string(out) != want {
		t.Errorf("branch-ref rewrite mismatch (changed=%v)\n--- got ---\n%s\n--- want ---\n%s", changed, out, want)
	}
}

// TestSetInputURLRewritesNested rewrites the url inside a nested sub-attrset
// input value in place.
func TestSetInputURLRewritesNested(t *testing.T) {
	const src = `{
  inputs = {
    nixpkgs-master = {
      url = "github:NixOS/nixpkgs";
    };
  };
  outputs = { self }: { };
}
`
	const want = `{
  inputs = {
    nixpkgs-master = {
      url = "` + pinnedURL + `";
    };
  };
  outputs = { self }: { };
}
`
	out, changed, err := SetInputURL([]byte(src), "nixpkgs-master", pinnedURL)
	if err != nil {
		t.Fatalf("SetInputURL: %v", err)
	}
	if !changed || string(out) != want {
		t.Errorf("nested rewrite mismatch (changed=%v)\n--- got ---\n%s\n--- want ---\n%s", changed, out, want)
	}
}

// TestSetInputURLIdempotentNoop is a no-op (changed=false, bytes identical)
// when the input is already pinned to the target url — the already-conformant
// case run under --fix.
func TestSetInputURLIdempotentNoop(t *testing.T) {
	const src = `{
  inputs = {
    nixpkgs-master.url = "` + pinnedURL + `";
  };
  outputs = { self }: { };
}
`
	out, changed, err := SetInputURL([]byte(src), "nixpkgs-master", pinnedURL)
	if err != nil {
		t.Fatalf("SetInputURL: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (already conformant)")
	}
	if string(out) != src {
		t.Errorf("idempotent SetInputURL changed the file:\n%s", out)
	}
}

// TestSetInputURLNoInputsBails returns ErrUnparseable when the flake has no
// editable `inputs` region to splice a missing input into, so the caller
// falls back to print-only.
func TestSetInputURLNoInputsBails(t *testing.T) {
	const src = `{
  description = "x";
  outputs = { self }: { };
}
`
	_, _, err := SetInputURL([]byte(src), "nixpkgs-master", pinnedURL)
	if !errors.Is(err, ErrUnparseable) {
		t.Errorf("err = %v, want ErrUnparseable", err)
	}
}
