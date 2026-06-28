package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/friedenberg/doppelgang/internal/0/flakelock"
	"github.com/friedenberg/doppelgang/internal/0/nixedit"
	"github.com/friedenberg/doppelgang/internal/alfa/lint"
)

// perFetchTimeout bounds each upstream flake.nix fetch so a slow or hanging
// remote cannot stall a `lint --fix` run. Transitive detection is best-effort,
// so a timeout simply skips that node.
const perFetchTimeout = 15 * time.Second

// maxFlakeNix caps how many bytes of a fetched flake.nix we read — a flake.nix
// is small; this guards against a pathological response.
const maxFlakeNix = 1 << 20 // 1 MiB

// transitiveDeadOverrides recovers dead follows overrides declared in *upstream*
// flakes' own flake.nix files — the ones doppelgang cannot see in the linted
// flake.nix or in the lock (Nix drops dead overrides from the lock). For each
// non-root node that could declare overrides it fetches that flake's flake.nix
// (a fast github raw HTTP fetch first, then a general `nix` fetch), extracts its
// overrides, and resolves them against that node's subtree in the lock.
//
// It is strictly best-effort: any fetch or parse failure is a silent no-op, so
// an offline or network-restricted run simply reports fewer (or no) transitive
// findings rather than erroring. Leaf nodes (no inputs) and nodes with no
// fetchable source are skipped, which keeps the common heavy inputs (nixpkgs,
// usually a leaf in the lock) from being fetched.
func transitiveDeadOverrides(ctx context.Context, lock *flakelock.Lock) []lint.DeadOverride {
	var out []lint.DeadOverride
	for _, key := range sortedNodeKeys(lock) {
		if key == lock.Root {
			continue
		}
		node := lock.Nodes[key]
		if node.Locked == nil || len(node.Inputs) == 0 {
			continue
		}
		src, ok := fetchFlakeNix(ctx, node.Locked)
		if !ok {
			continue
		}
		overrides, err := nixedit.Overrides(src)
		if err != nil {
			continue
		}
		out = append(out, lint.TransitiveDeadOverrides(lock, key, overrides, upstreamLabel(key, node.Locked))...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Via != out[j].Via {
			return out[i].Via < out[j].Via
		}
		return out[i].Override < out[j].Override
	})
	return out
}

func sortedNodeKeys(lock *flakelock.Lock) []string {
	keys := make([]string, 0, len(lock.Nodes))
	for k := range lock.Nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// upstreamLabel names the flake a transitive override must be fixed in:
// owner/repo when known, else the lock node key.
func upstreamLabel(key string, lk *flakelock.Locked) string {
	if lk.Owner != "" && lk.Repo != "" {
		return lk.Owner + "/" + lk.Repo
	}
	return key
}

// fetchFlakeNix returns the flake.nix bytes for a locked node, trying a fast
// github raw HTTP fetch first and falling back to a general `nix` fetch. It
// returns ok=false on any failure so the caller skips the node.
func fetchFlakeNix(ctx context.Context, lk *flakelock.Locked) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, perFetchTimeout)
	defer cancel()
	if lk.Type == "github" && lk.Owner != "" && lk.Repo != "" && lk.Rev != "" {
		if src, ok := githubRawFlakeNix(ctx, lk.Owner, lk.Repo, lk.Rev); ok {
			return src, true
		}
	}
	return nixFetchFlakeNix(ctx, lk)
}

// githubRawFlakeNix fetches flake.nix from raw.githubusercontent.com at the
// pinned rev. Best-effort: any non-200 or transport error yields ok=false.
func githubRawFlakeNix(ctx context.Context, owner, repo, rev string) ([]byte, bool) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/flake.nix", owner, repo, rev)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFlakeNix))
	if err != nil {
		return nil, false
	}
	return body, true
}

// nixFetchFlakeNix materializes a locked source via `nix eval` +
// builtins.fetchTree and reads its flake.nix. It is the general fallback for
// non-github source types (tarball, git, …) and for github when the raw fetch
// failed. Best-effort: a nix error yields ok=false.
func nixFetchFlakeNix(ctx context.Context, lk *flakelock.Locked) ([]byte, bool) {
	attrs := fetchTreeAttrs(lk)
	if attrs == "" {
		return nil, false
	}
	expr := fmt.Sprintf(`builtins.readFile ((builtins.fetchTree %s) + "/flake.nix")`, attrs)
	cmd := exec.CommandContext(ctx, "nix", "eval", "--raw",
		"--extra-experimental-features", "nix-command flakes", "--expr", expr)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	return out, true
}

// fetchTreeAttrs renders a builtins.fetchTree attribute set from a locked node,
// including only the fields that are set (type plus whichever of
// owner/repo/rev/url/narHash apply). Returns "" when the type is unknown.
func fetchTreeAttrs(lk *flakelock.Locked) string {
	if lk.Type == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("{ ")
	field := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s = %q; ", k, v)
		}
	}
	field("type", lk.Type)
	field("owner", lk.Owner)
	field("repo", lk.Repo)
	field("rev", lk.Rev)
	field("url", lk.URL)
	field("narHash", lk.NarHash)
	b.WriteString("}")
	return b.String()
}
