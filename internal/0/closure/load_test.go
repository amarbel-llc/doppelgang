package closure

import "testing"

func TestParsePathInfoObjectForm(t *testing.T) {
	in := []byte(`{
  "/nix/store/aaaa-foo": {"narSize": 1024, "references": ["/nix/store/bbbb-bar"]},
  "/nix/store/bbbb-bar": {"narSize": 512, "references": []}
}`)
	g, err := ParsePathInfo(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(g), g)
	}
	foo := g["/nix/store/aaaa-foo"]
	if foo.NarSize != 1024 {
		t.Errorf("foo.NarSize = %d, want 1024", foo.NarSize)
	}
	if foo.Path != "/nix/store/aaaa-foo" {
		t.Errorf("foo.Path = %q, want /nix/store/aaaa-foo", foo.Path)
	}
	if len(foo.References) != 1 || foo.References[0] != "/nix/store/bbbb-bar" {
		t.Errorf("foo.References = %v", foo.References)
	}
}

func TestParsePathInfoArrayForm(t *testing.T) {
	in := []byte(`[
  {"path": "/nix/store/aaaa-foo", "narSize": 1024, "references": ["/nix/store/bbbb-bar"]},
  {"path": "/nix/store/bbbb-bar", "narSize": 512, "references": []}
]`)
	g, err := ParsePathInfo(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(g), g)
	}
	if g["/nix/store/aaaa-foo"].NarSize != 1024 {
		t.Errorf("foo.NarSize = %d, want 1024", g["/nix/store/aaaa-foo"].NarSize)
	}
}

func TestParsePathInfoEmpty(t *testing.T) {
	g, err := ParsePathInfo([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 0 {
		t.Errorf("expected empty graph, got %+v", g)
	}
}

func TestParsePathInfoBogus(t *testing.T) {
	_, err := ParsePathInfo([]byte("not json"))
	if err == nil {
		t.Error("expected error on bogus input")
	}
}
