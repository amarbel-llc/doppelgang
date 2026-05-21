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
		hashRoot  = "00000000000000000000000000000000"
		hashUserA = "11111111111111111111111111111111"
		hashUserB = "22222222222222222222222222222222"
		hashDupeA = "33333333333333333333333333333333"
		hashDupeB = "44444444444444444444444444444444"
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

func TestFindVersionDrift(t *testing.T) {
	const (
		h1 = "11111111111111111111111111111111"
		h2 = "22222222222222222222222222222222"
		h3 = "33333333333333333333333333333333"
		h4 = "44444444444444444444444444444444"
		h5 = "55555555555555555555555555555555"
		h6 = "66666666666666666666666666666666"
		h7 = "77777777777777777777777777777777"
		h8 = "88888888888888888888888888888888"
	)
	// clang has two versions; 21.1.8 has two distinct paths (exact dupe).
	clang7 := "/nix/store/" + h1 + "-clang-21.1.7"
	clang8a := "/nix/store/" + h2 + "-clang-21.1.8"
	clang8b := "/nix/store/" + h3 + "-clang-21.1.8"
	// glibc has three versions, all single-copy.
	glibc39 := "/nix/store/" + h4 + "-glibc-2.39"
	glibc40 := "/nix/store/" + h5 + "-glibc-2.40"
	glibc41 := "/nix/store/" + h6 + "-glibc-2.41"
	// uniq has only one version, must be excluded.
	uniq := "/nix/store/" + h7 + "-uniq-3.0"
	// setup-hook has no version, must be excluded.
	hook := "/nix/store/" + h8 + "-setup-hook"

	g := closure.Graph{
		clang7:  {Path: clang7, NarSize: 100 * 1024 * 1024},
		clang8a: {Path: clang8a, NarSize: 100 * 1024 * 1024},
		clang8b: {Path: clang8b, NarSize: 100 * 1024 * 1024},
		glibc39: {Path: glibc39, NarSize: 30 * 1024 * 1024},
		glibc40: {Path: glibc40, NarSize: 30 * 1024 * 1024},
		glibc41: {Path: glibc41, NarSize: 30 * 1024 * 1024},
		uniq:    {Path: uniq, NarSize: 1024},
		hook:    {Path: hook, NarSize: 1024},
	}

	drift := FindVersionDrift(g, nil, nil)
	if len(drift) != 2 {
		t.Fatalf("want 2 drift groups, got %d: %+v", len(drift), drift)
	}

	// clang has 300M total (3 copies × 100M), glibc has 90M, so clang first.
	if drift[0].Pname != "clang" {
		t.Errorf("drift[0].Pname = %q, want clang", drift[0].Pname)
	}
	if len(drift[0].Versions) != 2 {
		t.Fatalf("clang versions = %+v", drift[0].Versions)
	}
	if drift[0].Versions[0].Version != "21.1.7" || drift[0].Versions[0].Count != 1 || drift[0].Versions[0].IsExactDupe {
		t.Errorf("clang 21.1.7 = %+v", drift[0].Versions[0])
	}
	if drift[0].Versions[1].Version != "21.1.8" || drift[0].Versions[1].Count != 2 || !drift[0].Versions[1].IsExactDupe {
		t.Errorf("clang 21.1.8 = %+v (want Count=2 IsExactDupe=true)", drift[0].Versions[1])
	}

	if drift[1].Pname != "glibc" {
		t.Errorf("drift[1].Pname = %q, want glibc", drift[1].Pname)
	}
	if len(drift[1].Versions) != 3 {
		t.Errorf("glibc versions = %+v", drift[1].Versions)
	}
	for _, v := range drift[1].Versions {
		if v.IsExactDupe {
			t.Errorf("glibc %s flagged as exact dupe but only one copy", v.Version)
		}
	}
}

func TestFindVersionDriftStripsOutputSuffixes(t *testing.T) {
	const (
		h1 = "11111111111111111111111111111111"
		h2 = "22222222222222222222222222222222"
		h3 = "33333333333333333333333333333333"
		h4 = "44444444444444444444444444444444"
		h5 = "55555555555555555555555555555555"
	)
	// jq has one upstream version (1.8.1) split across out/bin outputs.
	// Without output-suffix stripping this would falsely report drift.
	jqOut := "/nix/store/" + h1 + "-jq-1.8.1"
	jqBin := "/nix/store/" + h2 + "-jq-1.8.1-bin"
	// nghttp2 has two upstream versions, each present as out and dev.
	n167 := "/nix/store/" + h3 + "-nghttp2-1.67.1"
	n167dev := "/nix/store/" + h4 + "-nghttp2-1.67.1-dev"
	n168 := "/nix/store/" + h5 + "-nghttp2-1.68.1"

	g := closure.Graph{
		jqOut:   {Path: jqOut, NarSize: 1024},
		jqBin:   {Path: jqBin, NarSize: 1024},
		n167:    {Path: n167, NarSize: 1024},
		n167dev: {Path: n167dev, NarSize: 1024},
		n168:    {Path: n168, NarSize: 1024},
	}

	drift := FindVersionDrift(g, nil, nil)
	if len(drift) != 1 {
		t.Fatalf("want only nghttp2 drift (jq collapses to one upstream version), got %d: %+v", len(drift), drift)
	}
	if drift[0].Pname != "nghttp2" {
		t.Errorf("drift[0].Pname = %q, want nghttp2", drift[0].Pname)
	}
	if len(drift[0].Versions) != 2 {
		t.Fatalf("want 2 nghttp2 versions, got %+v", drift[0].Versions)
	}
	if drift[0].Versions[0].Version != "1.67.1" || drift[0].Versions[1].Version != "1.68.1" {
		t.Errorf("nghttp2 versions = %+v", drift[0].Versions)
	}
	// 1.67.1 bucket holds two distinct paths (out + dev), so Count=2.
	if drift[0].Versions[0].Count != 2 {
		t.Errorf("nghttp2 1.67.1 Count = %d, want 2", drift[0].Versions[0].Count)
	}
	// But neither path's name is duplicated, so no exact-dupe overlap.
	if drift[0].Versions[0].IsExactDupe {
		t.Errorf("nghttp2 1.67.1 should not be flagged as exact dupe: %+v", drift[0].Versions[0])
	}
}

func TestFindVersionDriftSkipsVersionless(t *testing.T) {
	const (
		h1 = "11111111111111111111111111111111"
		h2 = "22222222222222222222222222222222"
	)
	g := closure.Graph{
		"/nix/store/" + h1 + "-setup-hook": {Path: "/nix/store/" + h1 + "-setup-hook", NarSize: 100},
		"/nix/store/" + h2 + "-foo-1.0":    {Path: "/nix/store/" + h2 + "-foo-1.0", NarSize: 100},
	}
	if drift := FindVersionDrift(g, nil, nil); len(drift) != 0 {
		t.Errorf("want no drift, got %+v", drift)
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
