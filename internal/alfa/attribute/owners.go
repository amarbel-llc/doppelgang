// Package attribute computes which top-level children of a closure root
// reach each path. The "owner set" of a path is the set of top-level
// installables that pull it into the closure — useful for tracing
// duplicates back to the flake inputs / packages responsible for them.
package attribute

import (
	"runtime"
	"sort"
	"sync"

	"github.com/friedenberg/doppelgang/internal/0/closure"
	"github.com/friedenberg/doppelgang/internal/0/storepath"
)

// Compute does a forward BFS from each direct reference of root, in
// parallel, and returns map[storePath][]string from store path to its
// sorted owner names (hash-stripped). Paths not reached by any
// top-level are absent from the returned map.
func Compute(g closure.Graph, root string) map[string][]string {
	rootInfo, ok := g[root]
	if !ok {
		return map[string][]string{}
	}
	tops := rootInfo.References
	if len(tops) == 0 {
		return map[string][]string{}
	}

	type result struct {
		owner   string
		visited []string
	}

	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > len(tops) {
		workers = len(tops)
	}

	jobs := make(chan string, len(tops))
	results := make(chan result, len(tops))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tl := range jobs {
				name := storepath.Name(tl)
				visited := walk(g, tl)
				results <- result{owner: name, visited: visited}
			}
		}()
	}
	for _, tl := range tops {
		jobs <- tl
	}
	close(jobs)
	go func() { wg.Wait(); close(results) }()

	ownerSet := make(map[string]map[string]struct{})
	for r := range results {
		for _, p := range r.visited {
			if _, ok := ownerSet[p]; !ok {
				ownerSet[p] = make(map[string]struct{})
			}
			ownerSet[p][r.owner] = struct{}{}
		}
	}

	out := make(map[string][]string, len(ownerSet))
	for p, set := range ownerSet {
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		out[p] = names
	}
	return out
}

// walk returns the set of paths reachable from start via .References,
// including start itself.
func walk(g closure.Graph, start string) []string {
	visited := make(map[string]struct{})
	stack := []string{start}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := visited[p]; seen {
			continue
		}
		visited[p] = struct{}{}
		info, ok := g[p]
		if !ok {
			continue
		}
		for _, r := range info.References {
			if _, seen := visited[r]; !seen {
				stack = append(stack, r)
			}
		}
	}
	out := make([]string, 0, len(visited))
	for p := range visited {
		out = append(out, p)
	}
	return out
}
