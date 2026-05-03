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
