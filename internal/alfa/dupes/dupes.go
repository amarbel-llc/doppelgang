// Package dupes groups closure paths by their <name>-<version> and
// surfaces groups with multiple distinct copies, ranked by wasted bytes.
package dupes

import (
	"sort"

	"code.linenisgreat.com/doppelgang/internal/0/closure"
	"code.linenisgreat.com/doppelgang/internal/0/storepath"
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

// DriftGroup collects every path in the closure that shares a pname
// but spans more than one version. Reported only when len(Versions) > 1.
type DriftGroup struct {
	Pname string
	// Versions sorted lexically. Good enough for a v1 surface — semver
	// would require per-drv-style parsing and the user explicitly scoped
	// this as a first pass.
	Versions []DriftVersion
	// TotalBytes is the sum of every copy of every version under this
	// pname. Used as the sort key across drift groups.
	TotalBytes int64
}

// DriftVersion is one version within a DriftGroup, including the count
// of distinct store paths carrying that version. Count > 1 means the
// version is also an exact-duplicate group (overlap with Find output).
type DriftVersion struct {
	Version     string
	Count       int
	Size        int64
	IsExactDupe bool
	// Parents is the deduplicated, sorted union of immediate referrer
	// names (hash-stripped) across every path in this version's bucket.
	// nil when FindVersionDrift was called with parents==nil.
	Parents []string
	// Owners is the deduplicated, sorted union of top-level installable
	// names (hash-stripped) that reach this version. Populated only when
	// FindVersionDrift was called with a non-nil owners map (typically
	// from attribute.Compute via --by-owner).
	Owners []string
}

// FindVersionDrift groups every path in g by (pname, output-stripped
// version) and returns drift groups: pnames with more than one distinct
// upstream version. Paths whose name has no parseable version are
// skipped. The exact-dupe overlap flag fires when any full name within
// a version bucket appears more than once in the closure (i.e. the
// same name is its own exact-duplicate group in Find's output).
//
// parents (typically from InvertReferences) attaches the union of
// immediate referrer names to each version when non-nil. owners
// (typically from attribute.Compute when --by-owner) attaches the
// union of top-level installable names. Either map may be nil.
func FindVersionDrift(g closure.Graph, parents map[string][]string, owners map[string][]string) []DriftGroup {
	type bucketKey struct{ pname, version string }
	buckets := make(map[bucketKey][]string)
	for path := range g {
		name := storepath.Name(path)
		pname, version := storepath.SplitName(name)
		if version == "" {
			continue
		}
		version = storepath.TrimOutputSuffix(version)
		key := bucketKey{pname, version}
		buckets[key] = append(buckets[key], path)
	}

	versionsByPname := make(map[string][]DriftVersion)
	totalByPname := make(map[string]int64)
	for key, bucketPaths := range buckets {
		var size, total int64
		nameCounts := make(map[string]int)
		parentSet := make(map[string]struct{})
		ownerSet := make(map[string]struct{})
		for _, p := range bucketPaths {
			nameCounts[storepath.Name(p)]++
			if info, ok := g[p]; ok {
				if size == 0 {
					size = info.NarSize
				}
				total += info.NarSize
			}
			if parents != nil {
				for _, r := range parents[p] {
					parentSet[storepath.Name(r)] = struct{}{}
				}
			}
			if owners != nil {
				for _, o := range owners[p] {
					ownerSet[o] = struct{}{}
				}
			}
		}
		isExactDupe := false
		for _, c := range nameCounts {
			if c > 1 {
				isExactDupe = true
				break
			}
		}
		dv := DriftVersion{
			Version:     key.version,
			Count:       len(bucketPaths),
			Size:        size,
			IsExactDupe: isExactDupe,
		}
		if parents != nil {
			dv.Parents = sortedKeys(parentSet)
		}
		if owners != nil {
			dv.Owners = sortedKeys(ownerSet)
		}
		versionsByPname[key.pname] = append(versionsByPname[key.pname], dv)
		totalByPname[key.pname] += total
	}

	groups := make([]DriftGroup, 0)
	for pname, versions := range versionsByPname {
		if len(versions) < 2 {
			continue
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
		groups = append(groups, DriftGroup{
			Pname:      pname,
			Versions:   versions,
			TotalBytes: totalByPname[pname],
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].TotalBytes != groups[j].TotalBytes {
			return groups[i].TotalBytes > groups[j].TotalBytes
		}
		return groups[i].Pname < groups[j].Pname
	})
	return groups
}

// sortedKeys returns the keys of m in lexical order.
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
