// Package flakelock parses a Nix flake.lock (lockfile schema v7) into an
// in-memory node graph. Each node is a pinned input; an input's children
// are recorded either as a direct node reference (string form) or as an
// already-resolved follows path (array form).
package flakelock

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Lock is a parsed flake.lock.
type Lock struct {
	Nodes   map[string]Node `json:"nodes"`
	Root    string          `json:"root"`
	Version int             `json:"version"`
}

// Node is one entry in the lockfile's node table. The root node has no
// Locked/Original; every pinned input carries both.
type Node struct {
	Inputs   map[string]InputRef `json:"inputs,omitempty"`
	Locked   *Locked             `json:"locked,omitempty"`
	Original *Original           `json:"original,omitempty"`
}

// Locked is the resolved, content-addressed pin for a node.
type Locked struct {
	Type         string `json:"type,omitempty"`
	Owner        string `json:"owner,omitempty"`
	Repo         string `json:"repo,omitempty"`
	Rev          string `json:"rev,omitempty"`
	NarHash      string `json:"narHash,omitempty"`
	URL          string `json:"url,omitempty"`
	LastModified int64  `json:"lastModified,omitempty"`
}

// Original is the unresolved flake reference the user wrote.
type Original struct {
	Type  string `json:"type,omitempty"`
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`
	Rev   string `json:"rev,omitempty"`
	URL   string `json:"url,omitempty"`
}

// InputRef is one edge out of a node. In flake.lock an input value is
// either a string (the key of the node this input resolves to) or an
// array of strings (a follows path that was already applied, which does
// not introduce a node). Exactly one of Node / Follows is set.
type InputRef struct {
	// Node is the node-table key this input points at (string form).
	Node string
	// Follows is the resolved follows path, e.g. ["nixpkgs", "systems"]
	// (array form). Such inputs do not create their own node.
	Follows []string
}

// UnmarshalJSON dispatches on the JSON shape: a string is a node key, an
// array is a follows path.
func (r *InputRef) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("flakelock: empty input ref")
	}
	switch b[0] {
	case '"':
		return json.Unmarshal(b, &r.Node)
	case '[':
		return json.Unmarshal(b, &r.Follows)
	default:
		return fmt.Errorf("flakelock: unexpected input ref shape: %.20s", b)
	}
}

// Parse unmarshals raw flake.lock bytes.
func Parse(b []byte) (*Lock, error) {
	var l Lock
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("parse flake.lock: %w", err)
	}
	if l.Root == "" {
		return nil, fmt.Errorf("flake.lock has no root node")
	}
	if _, ok := l.Nodes[l.Root]; !ok {
		return nil, fmt.Errorf("flake.lock root %q missing from nodes", l.Root)
	}
	return &l, nil
}

// Load reads and parses <flakeDir>/flake.lock.
func Load(flakeDir string) (*Lock, error) {
	path := filepath.Join(flakeDir, "flake.lock")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(b)
}
