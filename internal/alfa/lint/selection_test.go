package lint

import "testing"

// TestParseSelectionDefaultIsDefaultChecks: an absent --checks selects the
// DefaultChecks subset (the three lock/flake analyses), NOT the opt-in
// nixpkgs-master convention check.
func TestParseSelectionDefaultIsDefaultChecks(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		sel, err := ParseSelection(raw)
		if err != nil {
			t.Fatalf("ParseSelection(%q): %v", raw, err)
		}
		if got := sel.Count(); got != len(DefaultChecks) {
			t.Errorf("ParseSelection(%q).Count() = %d, want %d", raw, got, len(DefaultChecks))
		}
		for _, c := range DefaultChecks {
			if !sel.Has(c) {
				t.Errorf("ParseSelection(%q) missing default check %q", raw, c)
			}
		}
		if sel.Has(CheckNixpkgsMaster) {
			t.Errorf("ParseSelection(%q) must not select the opt-in nixpkgs-master check", raw)
		}
	}
}

// TestParseSelectionAllAliasIsEveryCheck: the `all` alias selects every
// check, including the opt-in nixpkgs-master convention check.
func TestParseSelectionAllAliasIsEveryCheck(t *testing.T) {
	for _, raw := range []string{"all", " all "} {
		sel, err := ParseSelection(raw)
		if err != nil {
			t.Fatalf("ParseSelection(%q): %v", raw, err)
		}
		if got := sel.Count(); got != len(AllChecks) {
			t.Errorf("ParseSelection(%q).Count() = %d, want %d", raw, got, len(AllChecks))
		}
		for _, c := range AllChecks {
			if !sel.Has(c) {
				t.Errorf("ParseSelection(%q) missing %q", raw, c)
			}
		}
		if !sel.Has(CheckNixpkgsMaster) {
			t.Errorf("ParseSelection(%q) 'all' must include nixpkgs-master", raw)
		}
	}
}

func TestParseSelectionSubset(t *testing.T) {
	// The eng#205 selection: follows + dead-overrides, multi-version excluded.
	sel, err := ParseSelection("follows,dead-overrides")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	if !sel.Has(CheckFollows) || !sel.Has(CheckDeadOverrides) {
		t.Errorf("selection missing a requested check: %v", sel)
	}
	if sel.Has(CheckMultiVersion) {
		t.Errorf("multi-version should be excluded: %v", sel)
	}
	if got := sel.Count(); got != 2 {
		t.Errorf("Count() = %d, want 2", got)
	}
}

func TestParseSelectionToleratesWhitespaceAndEmptyFields(t *testing.T) {
	sel, err := ParseSelection(" follows , , dead-overrides,")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	if got := sel.Count(); got != 2 {
		t.Errorf("Count() = %d, want 2 (whitespace/empty fields ignored)", got)
	}
}

func TestParseSelectionAllAliasWithNames(t *testing.T) {
	sel, err := ParseSelection("follows,all")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	if got := sel.Count(); got != len(AllChecks) {
		t.Errorf("'all' alias should select every check, Count() = %d", got)
	}
}

func TestParseSelectionUnknownName(t *testing.T) {
	for _, raw := range []string{"bogus", "follows,bogus", "multiversion"} {
		if _, err := ParseSelection(raw); err == nil {
			t.Errorf("ParseSelection(%q): want error for unknown name, got nil", raw)
		}
	}
}

func TestAllSelectionHasEvery(t *testing.T) {
	sel := AllSelection()
	if sel.Count() != len(AllChecks) {
		t.Errorf("AllSelection().Count() = %d, want %d", sel.Count(), len(AllChecks))
	}
}
