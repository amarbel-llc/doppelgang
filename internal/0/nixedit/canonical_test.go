package nixedit

import (
	"strings"
	"testing"
)

// TestCanonicalFormDisabledByDefault confirms a flake with no opt-in
// directive reports Enabled=false and no Scattered findings, even though
// its inputs binding order is not contiguous — the opt-in gate must
// suppress detection entirely, not just --fix, per FDR 0007 (third-party
// flakes are never re-shaped or flagged).
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
		t.Errorf("Enabled = true, want false (no directive present)")
	}
	if len(got.Scattered) != 0 {
		t.Errorf("Scattered = %v, want none when not opted in", got.Scattered)
	}
}

// TestCanonicalFormDetectsScatteredBlock confirms that, once opted in via
// the structured directive, a follows binding for igloo separated from
// igloo's own bindings by another input's binding is flagged as scattered.
func TestCanonicalFormDetectsScatteredBlock(t *testing.T) {
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
	got, err := CanonicalForm([]byte(src))
	if err != nil {
		t.Fatalf("CanonicalForm: %v", err)
	}
	if !got.Enabled {
		t.Fatalf("Enabled = false, want true (directive present)")
	}
	if got.LegacySentinel {
		t.Errorf("LegacySentinel = true, want false (structured directive used)")
	}
	if !sameSet(got.Scattered, []string{"igloo"}) {
		t.Errorf("Scattered = %v, want [igloo]", got.Scattered)
	}
}

// TestCanonicalFormDetectsScatteredFlat is the flat-form analog: a follows
// binding for igloo separated by treefmt-nix's url is scattered.
func TestCanonicalFormDetectsScatteredFlat(t *testing.T) {
	const src = `{
  # doppelgang: canonical
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
		t.Fatalf("Enabled = false, want true (directive present)")
	}
	if !sameSet(got.Scattered, []string{"igloo"}) {
		t.Errorf("Scattered = %v, want [igloo]", got.Scattered)
	}
}

// TestCanonicalFormNoScatteredWhenContiguous confirms an already-canonical,
// opted-in flake reports zero Scattered findings.
func TestCanonicalFormNoScatteredWhenContiguous(t *testing.T) {
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

// TestCanonicalFormLegacySentinelStillEnables confirms the deprecated
// `# canonical-form` spelling still opts a flake in (back-compat) and is
// reported via LegacySentinel, per the mid-session decision to keep the old
// form working and have --fix migrate it rather than break existing
// opt-ins.
func TestCanonicalFormLegacySentinelStillEnables(t *testing.T) {
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
		t.Fatalf("Enabled = false, want true (legacy sentinel still opts in)")
	}
	if !got.LegacySentinel {
		t.Errorf("LegacySentinel = false, want true")
	}
	if len(got.Scattered) != 0 {
		t.Errorf("Scattered = %v, want none (bindings are contiguous)", got.Scattered)
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
// them back, then migrate any legacy sentinel) on a scattered, opted-in
// block-form flake, and confirms the result is both contiguous and a fixed
// point: fixing again is a no-op.
func TestCanonicalFormFixReachesFixedPoint(t *testing.T) {
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
	if _, migrated, err := MigrateLegacySentinel(fixed); err != nil || migrated {
		t.Errorf("second pass migrated=%v err=%v, want no-op (already structured)", migrated, err)
	}
}

// TestCanonicalFormFixNoopWhenAlreadyCanonical confirms --fix changes
// nothing, byte-for-byte, when run on an already-canonical opted-in flake —
// the defining fixed-point property from FDR 0007.
func TestCanonicalFormFixNoopWhenAlreadyCanonical(t *testing.T) {
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
	fixed := runCanonicalFormFix(t, []byte(src))
	if string(fixed) != src {
		t.Errorf("--fix changed an already-canonical flake:\n%s", fixed)
	}
}

// TestCanonicalFormFixMigratesLegacySentinelEvenWithoutScattering confirms
// --fix upgrades a legacy `# canonical-form` sentinel to the structured
// `# doppelgang: canonical` directive even when there is no scattering to
// relocate — migration is independent of the contiguity fix, per FDR 0007's
// "Legacy sentinel migration" subsection.
func TestCanonicalFormFixMigratesLegacySentinelEvenWithoutScattering(t *testing.T) {
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
	s := string(fixed)
	if !strings.Contains(s, "# doppelgang: canonical") {
		t.Errorf("legacy sentinel not migrated:\n%s", s)
	}
	if strings.Contains(s, "# canonical-form") {
		t.Errorf("legacy sentinel text still present after migration:\n%s", s)
	}
	report, err := CanonicalForm(fixed)
	if err != nil {
		t.Fatalf("CanonicalForm after fix: %v", err)
	}
	if report.LegacySentinel {
		t.Errorf("LegacySentinel = true after migration, want false")
	}
	if !report.Enabled {
		t.Errorf("Enabled = false after migration, want true")
	}
}

// TestMigrateLegacySentinelPreservesIndent confirms the rewritten directive
// line keeps the original comment's indentation, and that the rest of the
// file survives untouched.
func TestMigrateLegacySentinelPreservesIndent(t *testing.T) {
	const src = `{
  # canonical-form
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
  };
  outputs = { self }: { };
}
`
	out, changed, err := MigrateLegacySentinel([]byte(src))
	if err != nil {
		t.Fatalf("MigrateLegacySentinel: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	s := string(out)
	if !strings.Contains(s, "\n  # doppelgang: canonical\n") {
		t.Errorf("directive not written with matching indent:\n%s", s)
	}
	if !strings.Contains(s, `igloo.url = "github:amarbel-llc/igloo";`) {
		t.Errorf("clobbered existing binding:\n%s", s)
	}
}

// TestMigrateLegacySentinelPreservesCRLF confirms that on a CRLF-terminated
// flake.nix, the rewritten directive line stays CRLF-terminated like every
// other line in the file — regression test for #28, where the byte-range
// replace excised the sentinel line's trailing '\r' without reinserting it,
// leaving that one line LF-terminated in an otherwise-CRLF file.
func TestMigrateLegacySentinelPreservesCRLF(t *testing.T) {
	src := "{\r\n" +
		"  # canonical-form\r\n" +
		"  inputs = {\r\n" +
		"    igloo.url = \"github:amarbel-llc/igloo\";\r\n" +
		"  };\r\n" +
		"  outputs = { self }: { };\r\n" +
		"}\r\n"
	out, changed, err := MigrateLegacySentinel([]byte(src))
	if err != nil {
		t.Fatalf("MigrateLegacySentinel: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	s := string(out)
	if !strings.Contains(s, "\r\n  # doppelgang: canonical\r\n") {
		t.Errorf("directive line not CRLF-terminated:\n%q", s)
	}
	if strings.Contains(s, "canonical\n") && !strings.Contains(s, "canonical\r\n") {
		t.Errorf("directive line is bare-LF-terminated in a CRLF file:\n%q", s)
	}
	for i, line := range strings.Split(s, "\n") {
		if line == "" {
			continue // trailing split artifact after the final \n
		}
		if !strings.HasSuffix(line, "\r") {
			t.Errorf("line %d not CRLF-terminated: %q\nfull output:\n%q", i, line, s)
		}
	}
}

// TestMigrateLegacySentinelNoopCases confirms MigrateLegacySentinel is a
// byte-for-byte no-op both when the flake already uses the structured
// directive and when it has not opted in at all.
func TestMigrateLegacySentinelNoopCases(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "structured-directive-already",
			src: `{
  # doppelgang: canonical
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
  };
  outputs = { self }: { };
}
`,
		},
		{
			name: "not-opted-in",
			src: `{
  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
  };
  outputs = { self }: { };
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, changed, err := MigrateLegacySentinel([]byte(tc.src))
			if err != nil {
				t.Fatalf("MigrateLegacySentinel: %v", err)
			}
			if changed {
				t.Errorf("changed = true, want false")
			}
			if string(out) != tc.src {
				t.Errorf("output changed:\n%s", out)
			}
		})
	}
}

// runCanonicalFormFix applies the CanonicalFormFixTargets-derived
// delete-then-reapply pipeline, then MigrateLegacySentinel, mirroring
// lintFix's full wiring, and returns the result.
func runCanonicalFormFix(t *testing.T, src []byte) []byte {
	t.Helper()
	del, add, err := CanonicalFormFixTargets(src)
	if err != nil {
		t.Fatalf("CanonicalFormFixTargets: %v", err)
	}
	out := src
	if len(del) > 0 {
		out, _, err = DeleteBindings(out, del)
		if err != nil {
			t.Fatalf("DeleteBindings: %v", err)
		}
		out, _, err = Apply(out, add)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	out, _, err = MigrateLegacySentinel(out)
	if err != nil {
		t.Fatalf("MigrateLegacySentinel: %v", err)
	}
	return out
}
