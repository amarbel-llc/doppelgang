package attribute

import (
	"reflect"
	"testing"

	"github.com/friedenberg/doppelgang/internal/0/closure"
)

func TestComputeOwnersAcrossTopLevels(t *testing.T) {
	const (
		hashRoot   = "00000000000000000000000000000000"
		hashTLA    = "11111111111111111111111111111111"
		hashTLB    = "22222222222222222222222222222222"
		hashShared = "33333333333333333333333333333333"
		hashAOnly  = "44444444444444444444444444444444"
		hashBOnly  = "55555555555555555555555555555555"
	)
	root := "/nix/store/" + hashRoot + "-root"
	tlA := "/nix/store/" + hashTLA + "-tlA"
	tlB := "/nix/store/" + hashTLB + "-tlB"
	shared := "/nix/store/" + hashShared + "-shared"
	aOnly := "/nix/store/" + hashAOnly + "-aOnly"
	bOnly := "/nix/store/" + hashBOnly + "-bOnly"

	g := closure.Graph{
		root:   {Path: root, References: []string{tlA, tlB}},
		tlA:    {Path: tlA, References: []string{shared, aOnly}},
		tlB:    {Path: tlB, References: []string{shared, bOnly}},
		shared: {Path: shared},
		aOnly:  {Path: aOnly},
		bOnly:  {Path: bOnly},
	}

	got := Compute(g, root)

	if !reflect.DeepEqual(got[shared], []string{"tlA", "tlB"}) {
		t.Errorf("shared owners = %v, want [tlA tlB]", got[shared])
	}
	if !reflect.DeepEqual(got[aOnly], []string{"tlA"}) {
		t.Errorf("aOnly owners = %v, want [tlA]", got[aOnly])
	}
	if !reflect.DeepEqual(got[bOnly], []string{"tlB"}) {
		t.Errorf("bOnly owners = %v, want [tlB]", got[bOnly])
	}
	if !reflect.DeepEqual(got[tlA], []string{"tlA"}) {
		t.Errorf("tlA owners = %v, want [tlA] (top-level owns itself)", got[tlA])
	}
}

func TestComputeNoTopLevels(t *testing.T) {
	const hashRoot = "00000000000000000000000000000000"
	root := "/nix/store/" + hashRoot + "-root"
	g := closure.Graph{root: {Path: root}}
	got := Compute(g, root)
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestComputeMissingRoot(t *testing.T) {
	g := closure.Graph{}
	got := Compute(g, "/nix/store/missing")
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}
