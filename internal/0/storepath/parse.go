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
