package dupes

import (
	"testing"

	"github.com/friedenberg/doppelgang/internal/0/closure"
)

func TestFindGroupsAndSortsByWasted(t *testing.T) {
	const (
		hashA = "11111111111111111111111111111111"
		hashB = "22222222222222222222222222222222"
		hashC = "33333333333333333333333333333333"
		hashD = "44444444444444444444444444444444"
		hashE = "55555555555555555555555555555555"
		hashF = "66666666666666666666666666666666"
	)
	pA := "/nix/store/" + hashA + "-foo-1.0"
	pB := "/nix/store/" + hashB + "-foo-1.0"
	pC := "/nix/store/" + hashC + "-bar-2.0"
	pD := "/nix/store/" + hashD + "-bar-2.0"
	pE := "/nix/store/" + hashE + "-bar-2.0"
	pF := "/nix/store/" + hashF + "-uniq-3.0"

	g := closure.Graph{
		pA: {Path: pA, NarSize: 1000},
		pB: {Path: pB, NarSize: 1000},
		pC: {Path: pC, NarSize: 5000},
		pD: {Path: pD, NarSize: 5000},
		pE: {Path: pE, NarSize: 5000},
		pF: {Path: pF, NarSize: 100},
	}
	groups := Find(g, nil)
	if len(groups) != 2 {
		t.Fatalf("want 2 groups, got %d: %+v", len(groups), groups)
	}
	if groups[0].Name != "bar-2.0" || groups[0].Wasted != 10000 || len(groups[0].Copies) != 3 {
		t.Errorf("first group = %+v", groups[0])
	}
	if groups[1].Name != "foo-1.0" || groups[1].Wasted != 1000 || len(groups[1].Copies) != 2 {
		t.Errorf("second group = %+v", groups[1])
	}
}

func TestFindAttachesParents(t *testing.T) {
	const (
		hashRoot   = "00000000000000000000000000000000"
		hashUserA  = "11111111111111111111111111111111"
		hashUserB  = "22222222222222222222222222222222"
		hashDupeA  = "33333333333333333333333333333333"
		hashDupeB  = "44444444444444444444444444444444"
	)
	root := "/nix/store/" + hashRoot + "-root"
	uA := "/nix/store/" + hashUserA + "-userA-1.0"
	uB := "/nix/store/" + hashUserB + "-userB-1.0"
	dA := "/nix/store/" + hashDupeA + "-shared-1.0"
	dB := "/nix/store/" + hashDupeB + "-shared-1.0"

	g := closure.Graph{
		root: {Path: root, References: []string{uA, uB}},
		uA:   {Path: uA, References: []string{dA}, NarSize: 100},
		uB:   {Path: uB, References: []string{dB}, NarSize: 100},
		dA:   {Path: dA, NarSize: 200},
		dB:   {Path: dB, NarSize: 200},
	}
	parents := InvertReferences(g)
	groups := Find(g, parents)
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %+v", groups)
	}
	gr := groups[0]
	if gr.Name != "shared-1.0" {
		t.Errorf("name = %q", gr.Name)
	}
	if len(gr.Copies) != 2 {
		t.Fatalf("copies = %+v", gr.Copies)
	}
	for _, c := range gr.Copies {
		if len(c.Parents) != 1 {
			t.Errorf("copy %q parents = %v, want exactly one", c.Path, c.Parents)
		}
	}
}

func TestInvertReferencesSkipsSelf(t *testing.T) {
	const h = "00000000000000000000000000000000"
	p := "/nix/store/" + h + "-self"
	g := closure.Graph{p: {Path: p, References: []string{p}}}
	parents := InvertReferences(g)
	if _, ok := parents[p]; ok {
		t.Errorf("self-reference should be skipped, got parents[p] = %v", parents[p])
	}
}
