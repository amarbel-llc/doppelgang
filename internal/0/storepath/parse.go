// Package storepath parses /nix/store/<hash>-<name> store paths.
package storepath

import (
	"path/filepath"
	"strings"
)

// Name strips the /nix/store/<hash>- prefix and any trailing .drv suffix,
// returning the bare <name>(-<version>)? portion.
//
//	/nix/store/abc...32-jq-1.8.1       -> "jq-1.8.1"
//	/nix/store/abc...32-jq-1.8.1-bin   -> "jq-1.8.1-bin"
//	/nix/store/abc...32-foo-bar.drv    -> "foo-bar"
//
// Inputs lacking a recognizable hash prefix (e.g. test fixtures) are
// returned unchanged except for the .drv suffix.
func Name(p string) string {
	base := filepath.Base(p)
	if i := strings.IndexByte(base, '-'); i > 0 && isHash(base[:i]) {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".drv")
}

// SplitName splits a hash-stripped store-path name into (pname, version)
// using the canonical nixpkgs lib.parseDrvName rule: the version begins
// at the first '-' immediately followed by an ASCII digit. Inputs with
// no such marker return pname=name, version="".
//
//	"jq-1.8.1"       -> ("jq", "1.8.1")
//	"jq-1.8.1-bin"   -> ("jq", "1.8.1-bin")
//	"clang-21.1.7"   -> ("clang", "21.1.7")
//	"foo-bar-1.0"    -> ("foo-bar", "1.0")
//	"setup-hook"     -> ("setup-hook", "")
func SplitName(name string) (pname, version string) {
	for i := 1; i < len(name)-1; i++ {
		if name[i] == '-' && name[i+1] >= '0' && name[i+1] <= '9' {
			return name[:i], name[i+1:]
		}
	}
	return name, ""
}

// nixOutputSuffixes is the closed set of output names whose appearance
// as the trailing `-<suffix>` of a parsed version string is almost
// always Nix output multiplicity (`-bin`, `-dev`, ...) rather than a
// genuine upstream version segment. Stripping these collapses
// false-positive drift between outputs of the same upstream version
// (e.g. `jq-1.8.1` vs `jq-1.8.1-bin`).
//
// The set includes both stdenv-level conventions and a handful of
// package-specific outputs caught during validation against ~/eng
// (nodejs `corepack`/`npm`, util-linux `mount`/`swap`/`login`/`lastlog`,
// ffmpeg `data`, ghostscript `fonts`). It will grow as new package
// outputs are observed — this is an intentional heuristic catalog, not
// a complete reflection of nixpkgs output names.
var nixOutputSuffixes = map[string]struct{}{
	"bin":      {},
	"corepack": {},
	"data":     {},
	"debug":    {},
	"dev":      {},
	"dist":     {},
	"doc":      {},
	"env":      {},
	"fonts":    {},
	"getent":   {},
	"info":     {},
	"lastlog":  {},
	"lib":      {},
	"libgcc":   {},
	"login":    {},
	"man":      {},
	"mount":    {},
	"npm":      {},
	"python":   {},
	"sandbox":  {},
	"static":   {},
	"swap":     {},
}

// TrimOutputSuffix removes a single trailing `-<nix-output>` segment if
// the suffix matches a known stdenv output name. Returns version
// unchanged otherwise. Non-output suffixes like `-bin`-shaped patches
// or `-unstable-<date>` survive intact because they are not in the set.
//
//	TrimOutputSuffix("1.8.1-bin")           -> "1.8.1"
//	TrimOutputSuffix("15.2.0-libgcc")       -> "15.2.0"
//	TrimOutputSuffix("1.0.20-unstable-...")  -> "1.0.20-unstable-..."
//	TrimOutputSuffix("5.3p9")               -> "5.3p9"
func TrimOutputSuffix(version string) string {
	i := strings.LastIndexByte(version, '-')
	if i < 0 {
		return version
	}
	if _, ok := nixOutputSuffixes[version[i+1:]]; ok {
		return version[:i]
	}
	return version
}

// isHash reports whether s looks like a Nix store-path hash. Modern Nix
// store hashes are exactly 32 base32-lowercase characters.
func isHash(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
