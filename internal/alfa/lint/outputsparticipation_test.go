package lint

import (
	"strings"
	"testing"
)

func TestOutputsParticipationFlagsClosedSignature(t *testing.T) {
	// The troupe shape: nixpkgs-master has been spliced into `inputs` but the
	// closed outputs formals do not name it, so nix fails to evaluate.
	src := []byte(`{
  inputs = {
    nixpkgs.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "github:NixOS/nixpkgs/1111111111111111111111111111111111111111";
  };

  outputs =
    {
      self,
      nixpkgs,
    }:
    { };
}
`)
	f, err := ClassifyOutputsParticipation(src)
	if err != nil {
		t.Fatalf("ClassifyOutputsParticipation: %v", err)
	}
	if f == nil {
		t.Fatal("want a finding for a closed signature missing a declared input")
	}
	if strings.Join(f.Missing, ",") != "nixpkgs-master" {
		t.Errorf("Missing = %v, want [nixpkgs-master]", f.Missing)
	}
	if !strings.Contains(f.String(), string(CheckOutputsParticipation)) {
		t.Errorf("finding text must name its own check so a conformist finding is unambiguous: %q", f.String())
	}
}

func TestOutputsParticipationSilentWhenSignatureAccepts(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "ellipsis absorbs the unnamed input",
			src: `{
  inputs = {
    nixpkgs.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "github:NixOS/nixpkgs/1111111111111111111111111111111111111111";
  };
  outputs = { self, nixpkgs, ... }: { };
}
`,
		},
		{
			name: "closed but every declared input is named",
			src: `{
  inputs = {
    nixpkgs.url = "github:amarbel-llc/igloo";
  };
  outputs = { self, nixpkgs }: { };
}
`,
		},
		{
			name: "simple identifier argument",
			src: `{
  inputs.nixpkgs.url = "github:amarbel-llc/igloo";
  outputs = inputs: { };
}
`,
		},
		{
			name: "no outputs binding to judge",
			src: `{
  inputs.nixpkgs.url = "github:amarbel-llc/igloo";
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := ClassifyOutputsParticipation([]byte(tc.src))
			if err != nil {
				t.Fatalf("ClassifyOutputsParticipation: %v", err)
			}
			if f != nil {
				t.Errorf("want no finding, got missing=%v", f.Missing)
			}
		})
	}
}

// Flat-form inputs (`inputs.x.url = …`) must be enumerated too, not just the
// `inputs = { … }` block form.
func TestOutputsParticipationFlatInputsForm(t *testing.T) {
	src := []byte(`{
  inputs.nixpkgs.url = "github:amarbel-llc/igloo";
  inputs.nixpkgs-master.url = "github:NixOS/nixpkgs/1111111111111111111111111111111111111111";

  outputs = { self, nixpkgs }: { };
}
`)
	f, err := ClassifyOutputsParticipation(src)
	if err != nil {
		t.Fatalf("ClassifyOutputsParticipation: %v", err)
	}
	if f == nil {
		t.Fatal("want a finding for flat-form inputs the signature does not name")
	}
	if strings.Join(f.Missing, ",") != "nixpkgs-master" {
		t.Errorf("Missing = %v, want [nixpkgs-master]", f.Missing)
	}
}

func TestOutputsParticipationIsSelectable(t *testing.T) {
	sel, err := ParseSelection("outputs-participation")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	if !sel.Has(CheckOutputsParticipation) {
		t.Error("outputs-participation not enabled by its own name")
	}
	// Held out of the default set to preserve existing consumers' exit codes.
	if DefaultSelection().Has(CheckOutputsParticipation) {
		t.Error("outputs-participation must stay opt-in, not in DefaultChecks")
	}
	if !AllSelection().Has(CheckOutputsParticipation) {
		t.Error("outputs-participation must be part of the `all` alias")
	}
}
