package nixedit

import (
	"errors"
	"strings"
	"testing"
)

// blockForm is an `inputs = { … }` flake.nix (the shape this repo uses).
const blockForm = `{
  description = "x";

  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "github:NixOS/nixpkgs/abc";
    utils.url = "https://example/f/0.1";
    treefmt-nix.url = "github:numtide/treefmt-nix";
  };

  outputs = { self, igloo }: { x = 1; };
}
`

// flatForm uses top-level flat `inputs.x.… = …` bindings instead of a
// block. Mixing the two is a Nix error, so this is a distinct shape.
const flatForm = `{
  description = "x";

  inputs.igloo.url = "github:amarbel-llc/igloo";
  inputs.utils.url = "https://example/f/0.1";

  outputs = { self, igloo }: { x = 1; };
}
`

func TestApplyBlockForm(t *testing.T) {
	lines := []string{
		`inputs.utils.inputs.systems.follows = "igloo/systems"`,
		`inputs.treefmt-nix.inputs.nixpkgs.follows = "igloo"`,
	}
	out, applied, err := Apply([]byte(blockForm), lines)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("applied = %v, want 2 lines", applied)
	}
	s := string(out)
	// Block-mode bindings drop the leading `inputs.` and live inside the
	// inputs attrset (before its closing brace), indented one level past
	// the brace (which sits at 2 spaces → bindings at 4).
	if !strings.Contains(s, "\n    utils.inputs.systems.follows = \"igloo/systems\";\n") {
		t.Errorf("missing block-form systems follows:\n%s", s)
	}
	if !strings.Contains(s, "\n    treefmt-nix.inputs.nixpkgs.follows = \"igloo\";\n") {
		t.Errorf("missing block-form nixpkgs follows:\n%s", s)
	}
	// The splice must be before the inputs block's closing brace, i.e.
	// before `outputs`.
	if strings.Index(s, "utils.inputs.systems.follows") > strings.Index(s, "outputs =") {
		t.Errorf("follows spliced after outputs (wrong block):\n%s", s)
	}
	// Untouched lines preserved.
	if !strings.Contains(s, `igloo.url = "github:amarbel-llc/igloo";`) {
		t.Errorf("clobbered existing binding:\n%s", s)
	}
}

func TestApplyFlatForm(t *testing.T) {
	lines := []string{`inputs.utils.inputs.systems.follows = "igloo/systems"`}
	out, applied, err := Apply([]byte(flatForm), lines)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied = %v, want 1", applied)
	}
	s := string(out)
	// Flat mode keeps the full `inputs.` form as a top-level sibling.
	if !strings.Contains(s, `inputs.utils.inputs.systems.follows = "igloo/systems";`) {
		t.Errorf("missing flat-form follows:\n%s", s)
	}
	// It should land among the inputs.* bindings, before outputs.
	if strings.Index(s, "inputs.utils.inputs.systems.follows") > strings.Index(s, "outputs =") {
		t.Errorf("flat follows spliced after outputs:\n%s", s)
	}
}

func TestApplyIdempotentBlock(t *testing.T) {
	lines := []string{`inputs.utils.inputs.systems.follows = "igloo/systems"`}
	out1, _, err := Apply([]byte(blockForm), lines)
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	out2, applied2, err := Apply(out1, lines)
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if len(applied2) != 0 {
		t.Errorf("second Apply applied %v, want no-op", applied2)
	}
	if string(out1) != string(out2) {
		t.Errorf("second Apply changed the file:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
	}
}

func TestApplyStripsMultiParentComment(t *testing.T) {
	lines := []string{
		`inputs.utils.inputs.systems.follows = "igloo/systems"   # node has multiple parents; repeat for each`,
	}
	out, applied, err := Apply([]byte(blockForm), lines)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied = %v, want 1", applied)
	}
	s := string(out)
	if strings.Contains(s, "multiple parents") {
		t.Errorf("multi-parent comment was not stripped:\n%s", s)
	}
	if !strings.Contains(s, `utils.inputs.systems.follows = "igloo/systems";`) {
		t.Errorf("binding missing after comment strip:\n%s", s)
	}
}

func TestApplyNoInputsBails(t *testing.T) {
	const noInputs = `{
  description = "x";
  outputs = { self }: { };
}
`
	_, _, err := Apply([]byte(noInputs), []string{`inputs.a.inputs.b.follows = "a"`})
	if !errors.Is(err, ErrUnparseable) {
		t.Errorf("err = %v, want ErrUnparseable", err)
	}
}

// TestApplySingleLineBlock covers an `inputs = { … };` block whose
// closing brace shares its line with content (no own-line `}`). The
// splice must land on its own line just before the brace, not be
// injected at the start of the line (which would corrupt the file).
func TestApplySingleLineBlock(t *testing.T) {
	const singleLine = `{
  inputs = { flake-utils.url = "github:numtide/flake-utils"; systems.url = "github:nix-systems/default"; };
  outputs = { self, flake-utils, systems }: { ok = true; };
}
`
	lines := []string{`inputs.flake-utils.inputs.systems.follows = "systems"`}
	out, applied, err := Apply([]byte(singleLine), lines)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied = %v, want 1", applied)
	}
	s := string(out)
	// The new binding must appear on its own line (preceded by a newline),
	// with the `inputs.` prefix stripped, and before the outputs binding.
	if !strings.Contains(s, "\n    flake-utils.inputs.systems.follows = \"systems\";") {
		t.Errorf("single-line block: binding not spliced on its own line:\n%s", s)
	}
	// The existing inner bindings must be intact (not split mid-line).
	if !strings.Contains(s, `flake-utils.url = "github:numtide/flake-utils";`) {
		t.Errorf("single-line block: clobbered existing binding:\n%s", s)
	}
	if strings.Index(s, "systems.follows") > strings.Index(s, "outputs =") {
		t.Errorf("single-line block: spliced after outputs:\n%s", s)
	}
}

func TestApplyEmptyLinesNoop(t *testing.T) {
	out, applied, err := Apply([]byte(blockForm), nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 0 || string(out) != blockForm {
		t.Errorf("empty lines should be a no-op; applied=%v changed=%v", applied, string(out) != blockForm)
	}
}

// TestApplyNaturalVariations exercises the block-form splice against the
// natural spelling/formatting variations a hand-written flake.nix takes:
// tab indentation, comments and blank lines inside the inputs block, a
// nested sub-attrset input value, a comment elsewhere that mentions the
// word "inputs", and an already-present follows (idempotent skip). Each
// case applies one follows line and asserts it lands inside the inputs
// block (before `outputs`) with the `inputs.` prefix stripped, and that
// a representative existing binding survives.
func TestApplyNaturalVariations(t *testing.T) {
	const line = `inputs.flake-utils.inputs.systems.follows = "systems"`
	const wantBinding = `flake-utils.inputs.systems.follows = "systems";`

	cases := []struct {
		name       string
		src        string
		keepSubstr string // an existing fragment that must survive
		wantApply  int    // expected count of applied lines
	}{
		{
			name: "tab-indented",
			src: "{\n" +
				"\tinputs = {\n" +
				"\t\tflake-utils.url = \"github:numtide/flake-utils\";\n" +
				"\t\tsystems.url = \"github:nix-systems/default\";\n" +
				"\t};\n" +
				"\toutputs = { self }: { };\n" +
				"}\n",
			keepSubstr: `flake-utils.url = "github:numtide/flake-utils";`,
			wantApply:  1,
		},
		{
			name: "comment-and-blank-lines-inside",
			src: `{
  inputs = {
    # pin flake-utils; it brings its own systems
    flake-utils.url = "github:numtide/flake-utils";

    systems.url = "github:nix-systems/default";
  };
  outputs = { self }: { };
}
`,
			keepSubstr: "# pin flake-utils",
			wantApply:  1,
		},
		{
			name: "comment-mentions-inputs-word",
			src: `{
  # these inputs each pull their own systems; collapse below
  inputs = {
    flake-utils.url = "github:numtide/flake-utils";
    systems.url = "github:nix-systems/default";
  };
  outputs = { self }: { };
}
`,
			keepSubstr: "# these inputs each pull their own systems",
			wantApply:  1,
		},
		{
			name: "nested-subattrset-input-value",
			src: `{
  inputs = {
    flake-utils = {
      url = "github:numtide/flake-utils";
    };
    systems.url = "github:nix-systems/default";
  };
  outputs = { self }: { };
}
`,
			keepSubstr: `url = "github:numtide/flake-utils";`,
			wantApply:  1,
		},
		{
			name: "crlf-line-endings",
			src: "{\r\n" +
				"  inputs = {\r\n" +
				"    flake-utils.url = \"github:numtide/flake-utils\";\r\n" +
				"    systems.url = \"github:nix-systems/default\";\r\n" +
				"  };\r\n" +
				"  outputs = { self }: { };\r\n" +
				"}\r\n",
			keepSubstr: "flake-utils.url",
			wantApply:  1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, applied, err := Apply([]byte(tc.src), []string{line})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(applied) != tc.wantApply {
				t.Fatalf("applied = %v, want %d", applied, tc.wantApply)
			}
			s := string(out)
			if !strings.Contains(s, wantBinding) {
				t.Errorf("missing spliced binding %q in:\n%s", wantBinding, s)
			}
			if !strings.Contains(s, tc.keepSubstr) {
				t.Errorf("clobbered existing content %q in:\n%s", tc.keepSubstr, s)
			}
			if i, j := strings.Index(s, "follows ="), strings.Index(s, "outputs ="); i > j {
				t.Errorf("follows spliced after outputs:\n%s", s)
			}
		})
	}
}

// TestApplyIdempotentNaturalBlock confirms a follows already written into
// a natural block (with surrounding comments) is recognized and skipped.
func TestApplyIdempotentNaturalBlock(t *testing.T) {
	const src = `{
  inputs = {
    flake-utils.url = "github:numtide/flake-utils";
    systems.url = "github:nix-systems/default";
    # already collapsed:
    flake-utils.inputs.systems.follows = "systems";
  };
  outputs = { self }: { };
}
`
	out, applied, err := Apply([]byte(src), []string{`inputs.flake-utils.inputs.systems.follows = "systems"`})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("applied = %v, want no-op (follows already present)", applied)
	}
	if string(out) != src {
		t.Errorf("idempotent apply changed the file:\n%s", out)
	}
}

// TestApplyCompoundInputsValueBails confirms that an `inputs` value that
// is a compound expression rather than a bare attrset — here `let … in {
// … }` — is not mis-spliced. soleGroup rejects it, so Apply bails with
// ErrUnparseable and the caller falls back to print-only.
func TestApplyCompoundInputsValueBails(t *testing.T) {
	const src = `{
  inputs = let base = "github:numtide/flake-utils"; in {
    flake-utils.url = base;
    systems.url = "github:nix-systems/default";
  };
  outputs = { self }: { };
}
`
	_, _, err := Apply([]byte(src), []string{`inputs.flake-utils.inputs.systems.follows = "systems"`})
	if !errors.Is(err, ErrUnparseable) {
		t.Errorf("err = %v, want ErrUnparseable for compound inputs value", err)
	}
}
