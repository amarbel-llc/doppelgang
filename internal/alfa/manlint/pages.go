package manlint

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind is the source format of a page to lint.
type Kind int

const (
	// KindRoff is a rendered page — man(7) or mdoc(7) source, possibly
	// gzipped — the shape found under a manpath root.
	KindRoff Kind = iota
	// KindScdoc is scdoc(5) source, the `*.scd` files a repo authors.
	KindScdoc
)

// Page is one file selected for linting.
type Page struct {
	Path string
	Kind Kind
}

// CollectPages resolves the positional arguments into pages, sorted by
// path. A directory is a manpath root (its `man*/` section directories are
// scanned; a directory that is itself a section directory, or one holding
// `*.scd` sources, is accepted too). A file is scdoc source when it ends in
// `.scd` and a rendered page otherwise. Symlinks are followed throughout,
// since a nix-profile manpath is a symlink farm. A path that does not exist
// or yields no pages is an error rather than a silent pass, so a misspelt
// argument cannot read as a clean gate.
func CollectPages(args []string) ([]Page, error) {
	var (
		pages []Page
		seen  = map[string]bool{}
	)
	add := func(p Page) {
		if !seen[p.Path] {
			seen[p.Path] = true
			pages = append(pages, p)
		}
	}
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			add(Page{Path: arg, Kind: kindFromPath(arg)})
			continue
		}
		found, err := collectFromDir(arg)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, fmt.Errorf("%s: no man*/ section directories, pages, or *.scd sources found", arg)
		}
		for _, p := range found {
			add(p)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Path < pages[j].Path })
	return pages, nil
}

// collectFromDir lists the pages a directory argument stands for: the
// contents of its man* section directories, else its own contents when it is
// itself a section directory, else its scdoc sources.
func collectFromDir(dir string) ([]Page, error) {
	sectionDirs, err := filepath.Glob(filepath.Join(dir, "man*"))
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, sd := range sectionDirs {
		if info, err := os.Stat(sd); err == nil && info.IsDir() {
			dirs = append(dirs, sd)
		}
	}
	if len(dirs) == 0 && strings.HasPrefix(filepath.Base(dir), "man") {
		dirs = []string{dir}
	}
	var pages []Page
	for _, sd := range dirs {
		entries, err := os.ReadDir(sd)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			p := filepath.Join(sd, e.Name())
			info, err := os.Stat(p)
			if err != nil || info.IsDir() || !hasSectionSuffix(e.Name()) {
				continue
			}
			pages = append(pages, Page{Path: p, Kind: KindRoff})
		}
	}
	if len(pages) > 0 {
		return pages, nil
	}
	scds, err := filepath.Glob(filepath.Join(dir, "*.scd"))
	if err != nil {
		return nil, err
	}
	for _, s := range scds {
		pages = append(pages, Page{Path: s, Kind: KindScdoc})
	}
	return pages, nil
}

// compressedExts are the page compressions a manpath may carry. Only gzip
// is decoded; the rest are reported as unreadable rather than skipped, so a
// gap in the gate is visible.
var compressedExts = []string{".gz", ".bz2", ".xz", ".zst", ".Z", ".lzma"}

// hasSectionSuffix reports whether a filename looks like a page —
// `name.N[.gz]` — so a stray README in a section directory is not linted.
func hasSectionSuffix(name string) bool {
	for _, ext := range compressedExts {
		name = strings.TrimSuffix(name, ext)
	}
	dot := strings.LastIndex(name, ".")
	return dot > 0 && dot < len(name)-1
}

func kindFromPath(path string) Kind {
	if strings.EqualFold(filepath.Ext(path), ".scd") {
		return KindScdoc
	}
	return KindRoff
}

// ReadPage returns a page's source, transparently decompressing gzip.
func ReadPage(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only
	var r io.Reader = f
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".gz":
		zr, err := gzip.NewReader(f)
		if err != nil {
			return "", err
		}
		defer zr.Close() //nolint:errcheck // read-only
		r = zr
	case ".bz2", ".xz", ".zst", ".lzma", ".z":
		return "", fmt.Errorf("unsupported compression %s", ext)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// LintPage reads and checks one page, returning its findings. A page that
// cannot be read or parsed yields a single unreadable/unparsable finding.
func LintPage(p Page, maxDescription int) []Finding {
	src, err := ReadPage(p.Path)
	if err != nil {
		return []Finding{{Path: p.Path, Check: CheckUnreadable, Severity: SeverityError, Detail: err.Error()}}
	}
	var entries []Entry
	switch p.Kind {
	case KindScdoc:
		entries, err = ParseScdocNameSection(src)
	default:
		entries, err = ParseRoffNameSection(src)
	}
	if err != nil {
		return []Finding{{Path: p.Path, Check: CheckUnparsable, Severity: SeverityError, Detail: err.Error()}}
	}
	return LintEntries(p.Path, entries, maxDescription)
}

// LintPages lints every page in order and concatenates the findings.
func LintPages(pages []Page, maxDescription int) []Finding {
	var findings []Finding
	for _, p := range pages {
		findings = append(findings, LintPage(p, maxDescription)...)
	}
	return findings
}
