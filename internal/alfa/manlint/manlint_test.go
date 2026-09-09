package manlint

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// checksOf reduces findings to their check names, in order, for compact
// table assertions.
func checksOf(fs []Finding) []Check {
	out := make([]Check, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Check)
	}
	return out
}

func TestParseRoffNameSection(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    []Entry
		wantErr string
	}{
		{
			name: "man escaped dash",
			src:  ".TH FOO 1\n.SH NAME\nfoo \\- do the thing\n.SH SYNOPSIS\nfoo\n",
			want: []Entry{{Names: []string{"foo"}, Description: "do the thing", LineCount: 1}},
		},
		{
			name: "scdoc output with .PP and plain dash",
			src:  ".SH NAME\n.PP\nfoo - do the thing\n.PP\n.SH SYNOPSIS\n",
			want: []Entry{{Names: []string{"foo"}, Description: "do the thing", LineCount: 1}},
		},
		{
			name: "quoted heading and multiple names",
			src:  `.SH "NAME"` + "\nfoo, bar \\- two names\n.SH SYNOPSIS\n",
			want: []Entry{{Names: []string{"foo", "bar"}, Description: "two names", LineCount: 1}},
		},
		{
			name: "escapes are resolved before measuring",
			src:  ".SH NAME\ndodder\\-transform \\- run a \\fBLua\\fR list\\-in/list\\-out transform\n.SH SYNOPSIS\n",
			want: []Entry{{Names: []string{"dodder-transform"}, Description: "run a Lua list-in/list-out transform", LineCount: 1}},
		},
		{
			name: "wrapped description counts both lines",
			src:  ".SH NAME\nhyphence \\- format\\-only inspection and re\\-emission of on\\-disk\nhyphence documents\n.SH SYNOPSIS\n",
			want: []Entry{{Names: []string{"hyphence"}, Description: "format-only inspection and re-emission of on-disk hyphence documents", LineCount: 2}},
		},
		{
			name: "several entries separated by .br",
			src:  ".SH NAME\nfoo \\- first\n.br\nbar \\- second\n.SH SYNOPSIS\n",
			want: []Entry{
				{Names: []string{"foo"}, Description: "first", LineCount: 1},
				{Names: []string{"bar"}, Description: "second", LineCount: 1},
			},
		},
		{
			name: "font macro lines contribute their arguments",
			src:  ".SH NAME\n.B foo\n\\- bold name on its own line\n.SH SYNOPSIS\n",
			want: []Entry{{Names: []string{"foo"}, Description: "bold name on its own line", LineCount: 2}},
		},
		{
			name: "empty description",
			src:  ".SH NAME\nfoo \\-\n.SH SYNOPSIS\n",
			want: []Entry{{Names: []string{"foo"}, Description: "", LineCount: 1}},
		},
		{
			name: "comments do not end an entry",
			src:  ".SH NAME\n.\\\" generated\nfoo \\- thing\n.SH SYNOPSIS\n",
			want: []Entry{{Names: []string{"foo"}, Description: "thing", LineCount: 1}},
		},
		{
			name: "mdoc",
			src:  ".Dd 2026\n.Sh NAME\n.Nm ringmaster\n.Nd background-job platform\n.Sh SYNOPSIS\n",
			want: []Entry{{Names: []string{"ringmaster"}, Description: "background-job platform", LineCount: 1}},
		},
		{
			name: "mdoc description continued by an inline macro is wrapped",
			src:  ".Sh NAME\n.Nm moxy-batch\n.Nd the\n.Li batch\nmeta tool: one approval\n.Sh DESCRIPTION\n",
			want: []Entry{{Names: []string{"moxy-batch"}, Description: "the batch meta tool: one approval", LineCount: 3}},
		},
		{
			name:    "no separator",
			src:     ".SH NAME\ncutting-garden, cg\n.SH SYNOPSIS\n",
			wantErr: "no ` - ` separator",
		},
		{
			name:    "no NAME section",
			src:     ".TH FOO 1\n.SH DESCRIPTION\nfoo - bar\n",
			wantErr: "no NAME section",
		},
		{
			name:    "empty NAME section",
			src:     ".SH NAME\n.PP\n.SH SYNOPSIS\n",
			wantErr: "no content",
		},
		{
			name:    "mdoc without .Nd",
			src:     ".Sh NAME\n.Nm foo\n.Sh SYNOPSIS\n",
			wantErr: "no .Nd",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRoffNameSection(tc.src)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseScdocNameSection(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    []Entry
		wantErr string
	}{
		{
			name: "single line",
			src:  "foo(1)\n\n# NAME\n\nfoo - do the thing\n\n# SYNOPSIS\n\n*foo*\n",
			want: []Entry{{Names: []string{"foo"}, Description: "do the thing", LineCount: 1}},
		},
		{
			name: "formatting stripped, escapes and inner underscores kept",
			src:  "# NAME\n\n*foo_bar* - _emphasised_ 100\\% snake_case\n\n# SYNOPSIS\n",
			want: []Entry{{Names: []string{"foo_bar"}, Description: "emphasised 100% snake_case", LineCount: 1}},
		},
		{
			name: "wrapped paragraph",
			src:  "# NAME\n\nfoo - a description that the author\nwrapped by hand\n\n# SYNOPSIS\n",
			want: []Entry{{Names: []string{"foo"}, Description: "a description that the author wrapped by hand", LineCount: 2}},
		},
		{
			name: "two paragraphs are two entries",
			src:  "# NAME\n\nfoo - first\n\nbar, baz - second\n\n# SYNOPSIS\n",
			want: []Entry{
				{Names: []string{"foo"}, Description: "first", LineCount: 1},
				{Names: []string{"bar", "baz"}, Description: "second", LineCount: 1},
			},
		},
		{
			name:    "no separator",
			src:     "# NAME\n\nfoo the thing\n\n# SYNOPSIS\n",
			wantErr: "no ` - ` separator",
		},
		{
			name:    "no NAME heading",
			src:     "foo(1)\n\n# SYNOPSIS\n\nfoo\n",
			wantErr: "no NAME section",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseScdocNameSection(tc.src)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLintEntries(t *testing.T) {
	long := strings.Repeat("x", 73)
	tests := []struct {
		name    string
		entries []Entry
		want    []Check
	}{
		{"clean", []Entry{{Names: []string{"foo"}, Description: "fine", LineCount: 1}}, nil},
		{"at the limit passes", []Entry{{Names: []string{"foo"}, Description: strings.Repeat("y", 72), LineCount: 1}}, nil},
		{"long", []Entry{{Names: []string{"foo"}, Description: long, LineCount: 1}}, []Check{CheckLong}},
		{"wrapped", []Entry{{Names: []string{"foo"}, Description: "fine", LineCount: 2}}, []Check{CheckWrapped}},
		{"empty", []Entry{{Names: []string{"foo"}, Description: "", LineCount: 1}}, []Check{CheckEmpty}},
		{"wrapped and empty", []Entry{{Names: []string{"foo"}, Description: "", LineCount: 2}}, []Check{CheckWrapped, CheckEmpty}},
		{"trailing period", []Entry{{Names: []string{"foo"}, Description: "fine.", LineCount: 1}}, []Check{CheckTrailingPeriod}},
		{"runes not bytes", []Entry{{Names: []string{"just"}, Description: "🤖 " + strings.Repeat("z", 70), LineCount: 1}}, nil},
		{
			"second entry named in detail",
			[]Entry{{Names: []string{"foo"}, Description: "ok", LineCount: 1}, {Names: []string{"bar"}, Description: long, LineCount: 1}},
			[]Check{CheckLong},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LintEntries("p", tc.entries, DefaultMaxDescription)
			if !reflect.DeepEqual(checksOf(got), tc.want) && !(len(got) == 0 && tc.want == nil) {
				t.Errorf("got %v, want %v", checksOf(got), tc.want)
			}
			for _, f := range got {
				wantSev := SeverityError
				if f.Check == CheckTrailingPeriod {
					wantSev = SeverityWarning
				}
				if f.Severity != wantSev {
					t.Errorf("%s severity = %s, want %s", f.Check, f.Severity, wantSev)
				}
			}
		})
	}
	multi := LintEntries("p", []Entry{
		{Names: []string{"foo"}, Description: "ok", LineCount: 1},
		{Names: []string{"bar"}, Description: long, LineCount: 1},
	}, DefaultMaxDescription)
	if len(multi) != 1 || !strings.HasPrefix(multi[0].Detail, "entry bar: ") {
		t.Errorf("multi-entry detail = %+v, want an `entry bar: ` prefix", multi)
	}
	if !HasErrors(multi) {
		t.Error("HasErrors = false for a long finding")
	}
	if HasErrors(LintEntries("p", []Entry{{Names: []string{"foo"}, Description: "x.", LineCount: 1}}, 72)) {
		t.Error("HasErrors = true for a lone trailing-period warning")
	}
}

// writeGz writes src gzip-compressed to path.
func writeGz(t *testing.T, path, src string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(src)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCollectAndLintManpathRoot builds a manpath root the way a nix profile
// lays one out — section dirs, gzipped and plain pages, a symlinked page, a
// stray non-page file — and checks discovery, gzip decoding, and the
// per-page verdicts end to end.
func TestCollectAndLintManpathRoot(t *testing.T) {
	root := t.TempDir()
	store := t.TempDir()
	writeGz(t, filepath.Join(store, "long.1.gz"), ".SH NAME\nlong \\- "+strings.Repeat("w", 80)+"\n.SH SYNOPSIS\n")
	writeFile(t, filepath.Join(root, "man1", "ok.1"), ".SH NAME\nok \\- fine\n.SH SYNOPSIS\n")
	writeGz(t, filepath.Join(root, "man1", "wrapped.1.gz"), ".SH NAME\nwrapped \\- first half\nsecond half\n.SH SYNOPSIS\n")
	writeFile(t, filepath.Join(root, "man7", "bad.7"), ".SH NAME\nbad, worse\n.SH DESCRIPTION\n")
	writeFile(t, filepath.Join(root, "man7", "README"), "not a page\n")
	writeFile(t, filepath.Join(root, "man7", "bz.7.bz2"), "\x00")
	if err := os.Symlink(filepath.Join(store, "long.1.gz"), filepath.Join(root, "man1", "long.1.gz")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "index.db"), "")

	pages, err := CollectPages([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range pages {
		names = append(names, filepath.Base(p.Path))
		if p.Kind != KindRoff {
			t.Errorf("%s kind = %v, want KindRoff", p.Path, p.Kind)
		}
	}
	wantNames := []string{"long.1.gz", "ok.1", "wrapped.1.gz", "bad.7", "bz.7.bz2"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("pages = %v, want %v", names, wantNames)
	}

	findings := LintPages(pages, DefaultMaxDescription)
	got := map[string]Check{}
	for _, f := range findings {
		got[filepath.Base(f.Path)] = f.Check
	}
	want := map[string]Check{
		"long.1.gz":    CheckLong,
		"wrapped.1.gz": CheckWrapped,
		"bad.7":        CheckUnparsable,
		"bz.7.bz2":     CheckUnreadable,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	if !HasErrors(findings) {
		t.Error("HasErrors = false")
	}
}

func TestCollectPagesSectionDirAndScdoc(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "man1", "a.1"), ".SH NAME\na \\- x\n")
	pages, err := CollectPages([]string{filepath.Join(root, "man1")})
	if err != nil || len(pages) != 1 {
		t.Fatalf("section dir: pages=%v err=%v", pages, err)
	}

	doc := t.TempDir()
	writeFile(t, filepath.Join(doc, "b.1.scd"), "b(1)\n\n# NAME\n\nb - fine\n")
	writeFile(t, filepath.Join(doc, "c.1.scd"), "c(1)\n\n# NAME\n\nc - ends with a period.\n")
	pages, err = CollectPages([]string{doc, filepath.Join(doc, "b.1.scd")})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].Kind != KindScdoc || pages[1].Kind != KindScdoc {
		t.Fatalf("scdoc dir: pages=%+v", pages)
	}
	findings := LintPages(pages, DefaultMaxDescription)
	if len(findings) != 1 || findings[0].Check != CheckTrailingPeriod || HasErrors(findings) {
		t.Errorf("findings = %+v, want one trailing-period warning", findings)
	}

	if _, err := CollectPages([]string{filepath.Join(root, "missing")}); err == nil {
		t.Error("missing path: want error")
	}
	if _, err := CollectPages([]string{t.TempDir()}); err == nil {
		t.Error("empty dir: want error")
	}
}
