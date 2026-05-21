package storepath

import "testing"

func TestName(t *testing.T) {
	const hash = "33cw374lsp6s03nnzj3dp63sv23yab84"
	cases := []struct {
		in, want string
	}{
		{"/nix/store/" + hash + "-jq-1.8.1", "jq-1.8.1"},
		{"/nix/store/" + hash + "-jq-1.8.1-bin", "jq-1.8.1-bin"},
		{"/nix/store/" + hash + "-claude-code-2.1.111", "claude-code-2.1.111"},
		{"/nix/store/" + hash + "-install-shell-files.drv", "install-shell-files"},
		{"/nix/store/" + hash + "-foo.drv", "foo"},
		// missing prefix: returned as-is, .drv stripped
		{"foo-1.0.drv", "foo-1.0"},
		{"foo-1.0", "foo-1.0"},
		// short prefix that's clearly not a hash
		{"/nix/store/go-1.26.2", "go-1.26.2"},
	}
	for _, c := range cases {
		got := Name(c.in)
		if got != c.want {
			t.Errorf("Name(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSplitName(t *testing.T) {
	cases := []struct {
		in             string
		pname, version string
	}{
		{"jq-1.8.1", "jq", "1.8.1"},
		{"jq-1.8.1-bin", "jq", "1.8.1-bin"},
		{"clang-21.1.7", "clang", "21.1.7"},
		{"clang-21.1.8", "clang", "21.1.8"},
		{"foo-bar-1.0", "foo-bar", "1.0"},
		// no digit after dash => no version
		{"setup-hook", "setup-hook", ""},
		{"install-shell-files", "install-shell-files", ""},
		// leading dash-digit is not a marker (would imply empty pname)
		{"-1.0", "-1.0", ""},
		// dash-digit (not digit alone) is the marker, so trailing-digit
		// pnames like python3 / ruby3.1 keep their digit suffix.
		{"python3-3.13.13", "python3", "3.13.13"},
		// canonical rule splits at the FIRST dash-digit, so a numeric
		// segment inside a multi-dash pname becomes part of the version.
		{"libfoo-2-1.0", "libfoo", "2-1.0"},
	}
	for _, c := range cases {
		gp, gv := SplitName(c.in)
		if gp != c.pname || gv != c.version {
			t.Errorf("SplitName(%q) = (%q, %q); want (%q, %q)",
				c.in, gp, gv, c.pname, c.version)
		}
	}
}

func TestTrimOutputSuffix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// known stdenv outputs: strip.
		{"1.8.1-bin", "1.8.1"},
		{"1.8.1-dev", "1.8.1"},
		{"1.8.1-lib", "1.8.1"},
		{"15.2.0-libgcc", "15.2.0"},
		{"148.0.7778.167-sandbox", "148.0.7778.167"},
		{"0.18-doc", "0.18"},
		{"2.46-debug", "2.46"},
		// unknown suffix: keep.
		{"5.3p9", "5.3p9"},                        // patch-letter, no dash
		{"2026a", "2026a"},                        // tzdata-style letter
		{"1.0.20-unstable-2025-12-31", "1.0.20-unstable-2025-12-31"}, // trailing date segment
		// no dash at all: keep.
		{"1.8.1", "1.8.1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := TrimOutputSuffix(c.in); got != c.want {
			t.Errorf("TrimOutputSuffix(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestIsHash(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"33cw374lsp6s03nnzj3dp63sv23yab84", true},
		{"00000000000000000000000000000000", true},
		{"33cw374lsp6s03nnzj3dp63sv23yab8", false},   // 31 chars
		{"33cw374lsp6s03nnzj3dp63sv23yab84z", false}, // 33 chars
		{"33CW374lsp6s03nnzj3dp63sv23yab84", false},  // uppercase
		{"go", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isHash(c.in); got != c.want {
			t.Errorf("isHash(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}
