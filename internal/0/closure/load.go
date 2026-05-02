// Package closure resolves a Nix installable to a store path and loads
// the runtime or build-time closure as an in-memory graph.
package closure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Path is one node in the closure: a store path with its size and the
// store paths it directly references.
type Path struct {
	Path       string   `json:"path,omitempty"`
	NarSize    int64    `json:"narSize"`
	References []string `json:"references"`
}

// Graph maps each store path in the closure to its metadata.
type Graph map[string]Path

// Scope selects which closure to load.
type Scope int

const (
	// Runtime: paths the installed system references at run-time.
	// What `nix-store -q --requisites <output>` returns.
	Runtime Scope = iota
	// Build: realized output paths of every build dep, including
	// toolchain and intermediate sources. .drv files are dropped because
	// `nix path-info` only operates on outputs.
	Build
)

func (s Scope) String() string {
	switch s {
	case Runtime:
		return "runtime"
	case Build:
		return "build"
	default:
		return "unknown"
	}
}

// Load resolves installable to a store path, then loads the closure at
// the requested scope and returns both the resolved root path and the
// graph rooted at it.
func Load(ctx context.Context, installable string, scope Scope) (root string, g Graph, err error) {
	root, err = ResolveInstallable(ctx, installable)
	if err != nil {
		return "", nil, fmt.Errorf("resolve %q: %w", installable, err)
	}
	switch scope {
	case Runtime:
		g, err = loadRuntime(ctx, root)
	case Build:
		g, err = loadBuild(ctx, root)
	default:
		return "", nil, fmt.Errorf("unknown scope: %v", scope)
	}
	if err != nil {
		return "", nil, err
	}
	return root, g, nil
}

// ResolveInstallable returns the store path for an installable. The
// resolution order:
//
//  1. /nix/store/... store paths are returned as-is.
//  2. Symlinks are followed (typical: ./result).
//  3. Anything else is passed to `nix path-info` to resolve.
//
// Multi-output installables resolve to the first reported path.
func ResolveInstallable(ctx context.Context, installable string) (string, error) {
	if strings.HasPrefix(installable, "/nix/store/") {
		return installable, nil
	}
	if abs, err := filepath.Abs(installable); err == nil {
		if target, err := filepath.EvalSymlinks(abs); err == nil &&
			strings.HasPrefix(target, "/nix/store/") {
			return target, nil
		}
	}
	cmd := exec.CommandContext(ctx, "nix", "path-info", installable)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("nix path-info %s: %w", installable, augment(err))
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i != -1 {
		line = line[:i]
	}
	if !strings.HasPrefix(line, "/nix/store/") {
		return "", fmt.Errorf("unexpected nix path-info output: %q", line)
	}
	return line, nil
}

func loadRuntime(ctx context.Context, root string) (Graph, error) {
	cmd := exec.CommandContext(ctx, "nix", "path-info", "-r", "--json", root)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nix path-info -r --json: %w", augment(err))
	}
	return ParsePathInfo(out)
}

func loadBuild(ctx context.Context, root string) (Graph, error) {
	deriv, err := deriverOf(ctx, root)
	if err != nil {
		return nil, err
	}
	reqCmd := exec.CommandContext(ctx, "nix-store", "-q", "--requisites", "--include-outputs", deriv)
	reqOut, err := reqCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nix-store -q --requisites --include-outputs: %w", augment(err))
	}
	var paths []string
	for _, p := range strings.Split(strings.TrimSpace(string(reqOut)), "\n") {
		if p == "" || strings.HasSuffix(p, ".drv") {
			continue
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		return Graph{}, nil
	}
	args := append([]string{"path-info", "--json"}, paths...)
	cmd := exec.CommandContext(ctx, "nix", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nix path-info --json (build): %w", augment(err))
	}
	return ParsePathInfo(out)
}

func deriverOf(ctx context.Context, output string) (string, error) {
	cmd := exec.CommandContext(ctx, "nix-store", "-q", "--deriver", output)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("nix-store -q --deriver: %w", augment(err))
	}
	deriv := strings.TrimSpace(string(out))
	if deriv == "" || deriv == "unknown-deriver" {
		return "", fmt.Errorf("no deriver for %s (was the path built locally?)", output)
	}
	return deriv, nil
}

// ParsePathInfo handles both `nix path-info --json` shapes:
//
//   - Object form (older Nix):
//     { "/nix/store/...": { "narSize": ..., "references": [...] } }
//   - Array form (newer Nix):
//     [ { "path": "/nix/store/...", "narSize": ..., "references": [...] } ]
func ParsePathInfo(b []byte) (Graph, error) {
	t := bytes.TrimSpace(b)
	if len(t) == 0 {
		return Graph{}, nil
	}
	g := make(Graph)
	switch t[0] {
	case '{':
		var m map[string]Path
		if err := json.Unmarshal(t, &m); err != nil {
			return nil, fmt.Errorf("parse path-info object form: %w", err)
		}
		for p, info := range m {
			info.Path = p
			g[p] = info
		}
	case '[':
		var arr []Path
		if err := json.Unmarshal(t, &arr); err != nil {
			return nil, fmt.Errorf("parse path-info array form: %w", err)
		}
		for _, info := range arr {
			if info.Path == "" {
				return nil, fmt.Errorf("array-form entry missing path field")
			}
			g[info.Path] = info
		}
	default:
		return nil, fmt.Errorf("unrecognized path-info JSON: %.40q", t)
	}
	return g, nil
}

// augment appends captured stderr to an exec error message when available.
func augment(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}
