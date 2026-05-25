package flakelock

import "testing"

const sampleLock = `{
  "nodes": {
    "root": { "inputs": { "a": "a", "b": "b_2" } },
    "a": {
      "inputs": { "shared": "shared", "viaFollows": ["a", "shared"] },
      "locked": { "type": "github", "owner": "o", "repo": "a", "rev": "aaa", "narHash": "sha-a" }
    },
    "b_2": {
      "inputs": { "shared": "shared_2" },
      "locked": { "type": "github", "owner": "o", "repo": "b", "rev": "bbb", "narHash": "sha-b" }
    },
    "shared": {
      "locked": { "type": "github", "owner": "x", "repo": "s", "rev": "sss", "narHash": "sha-s" }
    },
    "shared_2": {
      "locked": { "type": "github", "owner": "x", "repo": "s", "rev": "sss", "narHash": "sha-s" }
    }
  },
  "root": "root",
  "version": 7
}`

func TestParseRootAndNodes(t *testing.T) {
	l, err := Parse([]byte(sampleLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if l.Root != "root" {
		t.Errorf("Root = %q, want root", l.Root)
	}
	if l.Version != 7 {
		t.Errorf("Version = %d, want 7", l.Version)
	}
	if len(l.Nodes) != 5 {
		t.Errorf("len(Nodes) = %d, want 5", len(l.Nodes))
	}
}

func TestInputRefDualShape(t *testing.T) {
	l, err := Parse([]byte(sampleLock))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a := l.Nodes["a"]

	node := a.Inputs["shared"]
	if node.Node != "shared" {
		t.Errorf("string-form Node = %q, want shared", node.Node)
	}
	if node.Follows != nil {
		t.Errorf("string-form Follows = %v, want nil", node.Follows)
	}

	fol := a.Inputs["viaFollows"]
	if fol.Node != "" {
		t.Errorf("array-form Node = %q, want empty", fol.Node)
	}
	if len(fol.Follows) != 2 || fol.Follows[0] != "a" || fol.Follows[1] != "shared" {
		t.Errorf("array-form Follows = %v, want [a shared]", fol.Follows)
	}
}

func TestLockedFields(t *testing.T) {
	l, _ := Parse([]byte(sampleLock))
	lk := l.Nodes["shared"].Locked
	if lk == nil {
		t.Fatal("shared has no Locked")
	}
	if lk.NarHash != "sha-s" || lk.Owner != "x" || lk.Repo != "s" || lk.Rev != "sss" {
		t.Errorf("unexpected Locked: %+v", lk)
	}
}

func TestParseRejectsMissingRoot(t *testing.T) {
	if _, err := Parse([]byte(`{"nodes":{},"version":7}`)); err == nil {
		t.Fatal("want error for missing root, got nil")
	}
	if _, err := Parse([]byte(`{"nodes":{},"root":"nope","version":7}`)); err == nil {
		t.Fatal("want error for dangling root, got nil")
	}
}
