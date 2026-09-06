package nixedit

import (
	"strings"
	"testing"
)

// troupeShape reproduces the flake that motivated this code: a multi-line
// CLOSED outputs formals set followed by `let … in …`. Splicing an input into
// it without widening the signature produces
//
//	error: function 'outputs' called with unexpected argument 'nixpkgs-master'
//
// The `let`'s inner `;` truncates the shallow grammar's opaque value skip, so
// this fixture also guards that the formals — which sit before the `let` — are
// still found.
const troupeShape = `{
  description = "troupe";

  inputs = {
    nixpkgs.url = "github:amarbel-llc/igloo";
    bats.follows = "ringmaster/bats";
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
      treefmt-nix,
      ringmaster,
      bats,
    }:
    let
      version = "1";
    in
    { packages = version; };
}
`

func TestOutputsFormalsClosedMultiline(t *testing.T) {
	shape, names, err := OutputsFormals([]byte(troupeShape))
	if err != nil {
		t.Fatalf("OutputsFormals: %v", err)
	}
	if shape != OutputsClosed {
		t.Fatalf("shape = %v, want closed", shape)
	}
	want := []string{"self", "nixpkgs", "utils", "treefmt-nix", "ringmaster", "bats"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("names = %v, want %v", names, want)
	}
	if shape.AcceptsUnnamedInputs() {
		t.Error("closed formals must not report as accepting unnamed inputs")
	}
}

func TestWidenOutputsFormalsMultiline(t *testing.T) {
	out, changed, err := WidenOutputsFormals([]byte(troupeShape))
	if err != nil {
		t.Fatalf("WidenOutputsFormals: %v", err)
	}
	if !changed {
		t.Fatal("want changed=true for a closed formals set")
	}
	if !strings.Contains(string(out), "      bats,\n      ...\n    }:") {
		t.Errorf("ellipsis not appended at the formals indent:\n%s", out)
	}
	// Everything outside the formals must survive byte-for-byte.
	if !strings.Contains(string(out), `nixpkgs.url = "github:amarbel-llc/igloo";`) {
		t.Error("inputs block was disturbed")
	}
	shape, _, err := OutputsFormals(out)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if shape != OutputsEllipsis {
		t.Errorf("after widening shape = %v, want ellipsis", shape)
	}
}

func TestWidenOutputsFormalsIdempotent(t *testing.T) {
	once, _, err := WidenOutputsFormals([]byte(troupeShape))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	twice, changed, err := WidenOutputsFormals(once)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if changed {
		t.Error("second widening must be a no-op")
	}
	if string(twice) != string(once) {
		t.Errorf("second pass changed bytes:\n%s", twice)
	}
}

func TestOutputsFormalsShapes(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		shape OutputsShape
	}{
		{
			name:  "already has ellipsis",
			src:   "{\n  outputs = { self, nixpkgs, ... }: { };\n}\n",
			shape: OutputsEllipsis,
		},
		{
			name:  "simple identifier argument",
			src:   "{\n  outputs = inputs: { packages = inputs; };\n}\n",
			shape: OutputsSimpleArg,
		},
		{
			name:  "closed single line",
			src:   "{\n  outputs = { self, nixpkgs }: { };\n}\n",
			shape: OutputsClosed,
		},
		{
			name:  "ellipsis with @-binding",
			src:   "{\n  outputs = { self, ... }@inputs: { };\n}\n",
			shape: OutputsEllipsis,
		},
		{
			name:  "closed with @-binding",
			src:   "{\n  outputs = { self, nixpkgs }@inputs: { };\n}\n",
			shape: OutputsClosed,
		},
		{
			name:  "no outputs binding at all",
			src:   "{\n  inputs = { };\n}\n",
			shape: OutputsAbsent,
		},
		{
			name:  "ellipsis only in a default value does not count",
			src:   "{\n  outputs = { self, pkgs ? f { ... } }: { };\n}\n",
			shape: OutputsClosed,
		},
		{
			// Not a function at all — the leading attrset is an operand, not
			// a formals set. Splicing `...` into it would corrupt the file.
			name:  "value merely begins with an attrset",
			src:   "{\n  outputs = { a = 1; } // otherAttrs;\n}\n",
			shape: OutputsAbsent,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shape, _, err := OutputsFormals([]byte(tc.src))
			if err != nil {
				t.Fatalf("OutputsFormals: %v", err)
			}
			if shape != tc.shape {
				t.Fatalf("shape = %v, want %v", shape, tc.shape)
			}
		})
	}
}

func TestWidenOutputsFormalsNoOpWhenAlreadyOpen(t *testing.T) {
	for _, src := range []string{
		"{\n  outputs = { self, nixpkgs, ... }: { };\n}\n",
		"{\n  outputs = inputs: { packages = inputs; };\n}\n",
		"{\n  inputs = { };\n}\n",
	} {
		out, changed, err := WidenOutputsFormals([]byte(src))
		if err != nil {
			t.Fatalf("WidenOutputsFormals(%q): %v", src, err)
		}
		if changed {
			t.Errorf("changed=true for already-open source %q", src)
		}
		if string(out) != src {
			t.Errorf("bytes changed for %q:\n%s", src, out)
		}
	}
}

func TestWidenOutputsFormalsSingleLineForms(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "no trailing comma",
			src:  "{\n  outputs = { self, nixpkgs }: { };\n}\n",
			want: "{ self, nixpkgs, ... }:",
		},
		{
			name: "trailing comma",
			src:  "{\n  outputs = { self, nixpkgs, }: { };\n}\n",
			want: "{ self, nixpkgs, ... }:",
		},
		{
			name: "@-binding is preserved outside the braces",
			src:  "{\n  outputs = { self, nixpkgs }@inputs: { };\n}\n",
			want: "{ self, nixpkgs, ... }@inputs:",
		},
		{
			name: "empty formals",
			src:  "{\n  outputs = { }: { };\n}\n",
			want: "{ ... }:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, changed, err := WidenOutputsFormals([]byte(tc.src))
			if err != nil {
				t.Fatalf("WidenOutputsFormals: %v", err)
			}
			if !changed {
				t.Fatal("want changed=true")
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", out, tc.want)
			}
		})
	}
}

// A closed formals set whose last formal carries a comment must not have the
// ellipsis spliced into the comment text.
func TestWidenOutputsFormalsTrailingComment(t *testing.T) {
	src := `{
  outputs =
    {
      self,
      nixpkgs,
      # the fleet pin
    }:
    { };
}
`
	out, changed, err := WidenOutputsFormals([]byte(src))
	if err != nil {
		t.Fatalf("WidenOutputsFormals: %v", err)
	}
	if !changed {
		t.Fatal("want changed=true")
	}
	if strings.Contains(string(out), "# the fleet pin\n      ...") {
		t.Errorf("ellipsis landed after the comment, not after the last formal:\n%s", out)
	}
	shape, _, err := OutputsFormals(out)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if shape != OutputsEllipsis {
		t.Errorf("shape = %v, want ellipsis; got:\n%s", shape, out)
	}
}
