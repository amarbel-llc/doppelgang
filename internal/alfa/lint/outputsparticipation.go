package lint

import (
	"fmt"
	"strings"

	"code.linenisgreat.com/doppelgang/internal/0/nixedit"
)

// OutputsParticipationFinding reports a flake whose `outputs` function cannot
// accept every input the flake declares.
//
// Nix calls `outputs` with one attrset holding `self` plus every declared
// input. When the formals enumerate names without a trailing `...`, an input
// the signature does not name is a hard eval failure:
//
//	error: function 'outputs' called with unexpected argument 'nixpkgs-master'
//
// This is what makes a fleet flake unable to PARTICIPATE in the
// nixpkgs-master convention: splicing the input in is not enough if the
// signature then rejects it. The finding names the offending inputs so the
// operator can see which ones the signature is missing.
type OutputsParticipationFinding struct {
	// Missing are the declared inputs the closed formals do not name, in
	// flake.nix source order.
	Missing []string
	// Formals are the names the outputs signature does bind, `self` included.
	Formals []string
}

// String renders the finding in the shape conformist's own linters use — the
// check name leading, so an operator can map it back to a `--checks` id. A
// conformist finding is attributed to the linter stanza name
// (`doppelgang-flake`), which carries several checks, so the check must name
// itself here or the finding is ambiguous.
func (f *OutputsParticipationFinding) String() string {
	return fmt.Sprintf(
		"%s: outputs signature rejects declared input(s) %s — the formals enumerate names with no `...`, so nix fails with \"function 'outputs' called with unexpected argument\". Add `...` to the outputs argument set (or name the input in it).",
		CheckOutputsParticipation, strings.Join(f.Missing, ", "),
	)
}

// ClassifyOutputsParticipation reports whether flake.nix declares inputs its
// `outputs` signature cannot accept, returning nil when the flake is fine.
//
// It is deliberately conservative and returns nil — never a finding — when:
//
//   - the outputs signature already accepts unnamed inputs (`...` or a simple
//     identifier argument), which is the overwhelmingly common fleet shape;
//   - there is no recognisable top-level `outputs` binding to judge;
//   - the shallow grammar cannot parse flake.nix (ErrUnparseable), matching
//     how every other flake.nix-reading check degrades.
//
// Detection is fully offline: it reads flake.nix and nothing else — no lock,
// no network, and no fleet parameter such as a nixpkgs revision. That is what
// lets this check run inside a sandboxed conformist gate, where the
// nixpkgs-master check cannot (its repair needs --nixpkgs-master-sha).
func ClassifyOutputsParticipation(flakeNix []byte) (*OutputsParticipationFinding, error) {
	shape, formals, err := nixedit.OutputsFormals(flakeNix)
	if err != nil {
		return nil, err
	}
	if shape != nixedit.OutputsClosed {
		return nil, nil
	}
	declared, err := nixedit.InputNames(flakeNix)
	if err != nil {
		return nil, err
	}
	bound := make(map[string]bool, len(formals))
	for _, n := range formals {
		bound[n] = true
	}
	var missing []string
	for _, in := range declared {
		if !bound[in] {
			missing = append(missing, in)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	return &OutputsParticipationFinding{Missing: missing, Formals: formals}, nil
}
