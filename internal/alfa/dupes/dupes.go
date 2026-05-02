// Package dupes groups closure paths by their <name>-<version> and
// surfaces groups with multiple distinct copies, ranked by wasted bytes.
package dupes

import (
	"sort"

	"github.com/friedenberg/doppelgang/internal/0/closure"
	"github.com/friedenberg/doppelgang/internal/0/storepath"
)

// Group is the set of closure paths sharing the same name. Only groups
// with len(Copies) > 1 are reported as duplicates.
type Group struct {
	Name string
	// Wasted = (len(Copies) - 1) * Copies[0].Size, i.e. the bytes that
	// would be reclaimed if all copies collapsed to one.
	Wasted int64
	Copies []Copy
}

// Copy is one store path within a duplicate group.
type Copy struct {
	Path string
	Size int64
	// Parents lists the names (hash-stripped) of paths in the closure
	// that directly reference this copy. Empty for closure roots.
	Parents []string
}

// Find groups every path in g by storepath.Name and returns the groups
// whose copy count is greater than 1, sorted by Wasted descending.
//
// parents may be nil; if non-nil, each copy is annotated with the names
// of its immediate referrers.
func Find(g closure.Graph, parents map[string][]string) []Group {
	byName := make(map[string][]string)
	for path := range g {
		name := storepath.Name(path)
		byName[name] = append(byName[name], path)
	}

	groups := make([]Group, 0)
	for name, paths := range byName {
		if len(paths) < 2 {
			continue
		}
		sort.Strings(paths)
		copies := make([]Copy, 0, len(paths))
		for _, p := range paths {
			c := Copy{Path: p, Size: g[p].NarSize}
			if parents != nil {
				if refs, ok := parents[p]; ok {
					names := make([]string, 0, len(refs))
					for _, r := range refs {
						names = append(names, storepath.Name(r))
					}
					sort.Strings(names)
					c.Parents = names
				}
			}
			copies = append(copies, c)
		}
		size := copies[0].Size
		groups = append(groups, Group{
			Name:   name,
			Copies: copies,
			Wasted: int64(len(copies)-1) * size,
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Wasted != groups[j].Wasted {
			return groups[i].Wasted > groups[j].Wasted
		}
		return groups[i].Name < groups[j].Name
	})
	return groups
}

// InvertReferences builds the immediate-referrer map: for each path p,
// the list of paths in g that directly reference p (excluding self).
func InvertReferences(g closure.Graph) map[string][]string {
	parents := make(map[string][]string)
	for path, info := range g {
		for _, ref := range info.References {
			if ref != path {
				parents[ref] = append(parents[ref], path)
			}
		}
	}
	return parents
}
