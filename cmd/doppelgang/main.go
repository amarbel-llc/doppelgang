package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/friedenberg/doppelgang/internal/0/closure"
	"github.com/friedenberg/doppelgang/internal/0/flakelock"
	"github.com/friedenberg/doppelgang/internal/0/nixedit"
	"github.com/friedenberg/doppelgang/internal/0/storepath"
	"github.com/friedenberg/doppelgang/internal/alfa/attribute"
	"github.com/friedenberg/doppelgang/internal/alfa/dupes"
	"github.com/friedenberg/doppelgang/internal/alfa/lint"
	"github.com/friedenberg/doppelgang/internal/bravo/render"
)

// Populated at build time by the amarbel-llc/nixpkgs fork's buildGoApplication
// overlay via -X main.version=<flake.nix doppelgangVersion> and
// -X main.commit=<flake self shortRev>.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	flag.Usage = topUsage
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	switch flag.Arg(0) {
	case "dupes":
		os.Exit(dupesMain(ctx, flag.Args()[1:]))
	case "why":
		os.Exit(whyMain(ctx, flag.Args()[1:]))
	case "lint":
		os.Exit(lintMain(flag.Args()[1:]))
	case "version":
		fmt.Printf("doppelgang %s (%s)\n", version, commit)
		return
	default:
		fmt.Fprintf(os.Stderr, "doppelgang: unknown command %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}
}

func topUsage() {
	fmt.Fprintf(os.Stderr, "doppelgang — find duplicate packages in a Nix closure\n\n")
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  doppelgang dupes [--installable .#default] [--scope runtime|build]\n")
	fmt.Fprintf(os.Stderr, "                   [--top N] [--by-owner] [--no-version-drift] [--json]\n")
	fmt.Fprintf(os.Stderr, "  doppelgang why <regex|/nix/store/...> [--installable .#default] [--scope runtime|build]\n")
	fmt.Fprintf(os.Stderr, "  doppelgang lint [--flake .] [--format auto|text|json|ndjson] [--fix]\n")
	fmt.Fprintf(os.Stderr, "  doppelgang version\n\n")
	fmt.Fprintf(os.Stderr, "Defaults: --installable=./result, --scope=runtime (dupes) or build (why), --top=25.\n")
	fmt.Fprintf(os.Stderr, "`lint` reads <flake>/flake.lock and recommends `follows` for duplicate-source\n")
	fmt.Fprintf(os.Stderr, "inputs and flags inputs pinned at multiple revs. Exits 1 when any follows or\n")
	fmt.Fprintf(os.Stderr, "multi-version finding is reported, so it can serve as a CI gate. --format=auto\n")
	fmt.Fprintf(os.Stderr, "(the default) emits text on a TTY and tap NDJSON (amarbel-llc/tap) otherwise.\n")
	fmt.Fprintf(os.Stderr, "--fix applies the follows-opportunity edits to <flake>/flake.nix, re-locks via\n")
	fmt.Fprintf(os.Stderr, "`nix flake lock`, and stages the touched files (needs nix on PATH).\n")
	fmt.Fprintf(os.Stderr, "Multi-version inputs stay report-only — collapsing them changes behavior.\n")
	fmt.Fprintf(os.Stderr, "If `why` is given a /nix/store/... path, it traces that path directly without\n")
	fmt.Fprintf(os.Stderr, "scanning the closure. Otherwise the argument is treated as a name regex.\n")
	fmt.Fprintf(os.Stderr, "Requires nix-store, nix path-info, nix why-depends on PATH.\n")
}

func dupesMain(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("dupes", flag.ExitOnError)
	installable := fs.String("installable", "./result", "Nix installable, store path, or symlink to analyze")
	scopeStr := fs.String("scope", "runtime", "closure scope: runtime or build")
	top := fs.Int("top", 25, "max duplicate groups to report (0 = all)")
	byOwner := fs.Bool("by-owner", false, "attribute each copy to top-level installables that reach it")
	noVersionDrift := fs.Bool("no-version-drift", false, "suppress the pname-grouped version-drift section")
	asJSON := fs.Bool("json", false, "emit JSON instead of human-readable text")
	_ = fs.Parse(args)

	scope, err := parseScope(*scopeStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang: %v\n", err)
		return 2
	}

	root, g, err := closure.Load(ctx, *installable, scope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang: %v\n", err)
		return 1
	}

	parents := dupes.InvertReferences(g)
	groups := dupes.Find(g, parents)
	if *top > 0 && len(groups) > *top {
		groups = groups[:*top]
	}

	var owners map[string][]string
	if *byOwner {
		owners = attribute.Compute(g, root)
	}

	var totalBytes int64
	for _, info := range g {
		totalBytes += info.NarSize
	}

	sum := render.Summary{
		Scope:      scope.String(),
		TotalPaths: len(g),
		TotalBytes: totalBytes,
		Groups:     groups,
		Owners:     owners,
	}
	if !*noVersionDrift {
		sum.Drift = dupes.FindVersionDrift(g, parents, owners)
	}
	if *asJSON {
		return errExit(render.JSON(os.Stdout, sum))
	}
	return errExit(render.Text(os.Stdout, sum))
}

func lintMain(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	flakeDir := fs.String("flake", ".", "directory containing flake.lock")
	format := fs.String("format", "auto", "output format: auto, text, json, or ndjson (auto = text on a TTY, ndjson otherwise)")
	fix := fs.Bool("fix", false, "apply follows-opportunity edits to flake.nix and re-lock (needs nix on PATH)")
	_ = fs.Parse(args)

	resolved, err := resolveLintFormat(*format, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: %v\n", err)
		return 2
	}

	lock, err := flakelock.Load(*flakeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: %v\n", err)
		return 1
	}
	sum := render.LintSummary{Report: lint.Analyze(lock)}

	var renderErr error
	switch resolved {
	case "text":
		renderErr = render.LintText(os.Stdout, sum)
	case "json":
		renderErr = render.LintJSON(os.Stdout, sum)
	case "ndjson":
		renderErr = render.LintNDJSON(os.Stdout, sum)
	}
	if renderErr != nil {
		return errExit(renderErr)
	}

	if *fix {
		return lintFix(*flakeDir, sum.Report)
	}

	// Exit non-zero when actionable findings exist so `lint` can serve
	// as a CI gate.
	if len(sum.Report.Follows) > 0 || len(sum.Report.MultiVersion) > 0 {
		return 1
	}
	return 0
}

// lintFix applies the follows-opportunity edits from report to
// <flakeDir>/flake.nix, re-locks via `nix flake lock`, stages the
// touched files, and re-analyzes the regenerated lock to compute an
// honest exit code. It is invoked only under --fix, after the report has
// already been rendered.
//
// Multi-version findings are never auto-collapsed (choosing a revision
// changes behavior), so they are reported only and keep the exit code
// non-zero. The exit code is 0 only when, after the fix, neither follows
// nor multi-version findings remain.
//
// All progress goes to stderr so it does not pollute the machine-readable
// report already written to stdout.
func lintFix(flakeDir string, report lint.Report) int {
	var lines []string
	for _, r := range report.Follows {
		lines = append(lines, r.Lines...)
	}

	if len(lines) == 0 {
		if len(report.MultiVersion) > 0 {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: no follows opportunities to apply; %d multi-version input(s) remain (report-only, not auto-collapsed)\n", len(report.MultiVersion))
			return 1
		}
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: nothing to fix\n")
		return 0
	}

	nixPath := filepath.Join(flakeDir, "flake.nix")
	src, err := os.ReadFile(nixPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: read %s: %v\n", nixPath, err)
		return 1
	}

	out, applied, err := nixedit.Apply(src, lines)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: could not edit %s automatically (%v).\n"+
			"Apply the follows line(s) above by hand, then re-run `nix flake lock`.\n", nixPath, err)
		return 1
	}

	if len(applied) == 0 {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: follows already present in flake.nix; nothing to apply\n")
	} else {
		if err := os.WriteFile(nixPath, out, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: write %s: %v\n", nixPath, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: applied %d follows line(s) to %s:\n", len(applied), nixPath)
		for _, l := range applied {
			fmt.Fprintf(os.Stderr, "    %s\n", l)
		}
		if err := relock(flakeDir); err != nil {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: re-lock failed: %v\n", err)
			return 1
		}
		stageFixedFiles(flakeDir)
	}

	// Re-analyze the (possibly regenerated) lock for an honest exit code.
	lock, err := flakelock.Load(flakeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: reload flake.lock: %v\n", err)
		return 1
	}
	after := lint.Analyze(lock)
	if len(after.Follows) > 0 {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d follows opportunity group(s) remain after fix (re-run lint for detail)\n", len(after.Follows))
		return 1
	}
	if len(after.MultiVersion) > 0 {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d multi-version input(s) remain (report-only, not auto-collapsed)\n", len(after.MultiVersion))
		return 1
	}
	return 0
}

// relock regenerates <flakeDir>/flake.lock via `nix flake lock` so the
// lock reflects the newly added follows. Output is inherited so nix's own
// diagnostics reach the user.
func relock(flakeDir string) error {
	cmd := exec.Command("nix", "flake", "lock", flakeDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// stageFixedFiles runs `git add flake.nix flake.lock` in flakeDir so the
// repair self-stages, composing with a `nix fmt` / pre-commit --staged
// repair flow (per the conformist/dewey repair convention). A failure
// here (e.g. not a git repo) is non-fatal: the edits are already written.
func stageFixedFiles(flakeDir string) {
	cmd := exec.Command("git", "-C", flakeDir, "add", "flake.nix", "flake.lock")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: could not stage flake.nix/flake.lock (%v); stage them yourself\n", err)
	}
}

// resolveLintFormat maps the --format value to a concrete renderer. "auto"
// (the default) picks text when out is a character device (an interactive
// TTY) and tap NDJSON otherwise, so piped or redirected output is
// machine-readable while a human at a terminal still gets the bordered
// text view.
func resolveLintFormat(format string, out *os.File) (string, error) {
	switch format {
	case "text", "json", "ndjson":
		return format, nil
	case "auto", "":
		if isTerminal(out) {
			return "text", nil
		}
		return "ndjson", nil
	default:
		return "", fmt.Errorf("--format must be auto, text, json, or ndjson, got %q", format)
	}
}

// isTerminal reports whether f is an interactive character device.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func whyMain(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("why", flag.ExitOnError)
	installable := fs.String("installable", "./result", "Nix installable, store path, or symlink to query")
	scopeStr := fs.String("scope", "build", "scope: runtime or build")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "doppelgang why: missing argument (regex or /nix/store/... path)\n\n")
		topUsage()
		return 2
	}
	scope, err := parseScope(*scopeStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang: %v\n", err)
		return 2
	}

	rootOut, err := closure.ResolveInstallable(ctx, *installable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang why: %v\n", err)
		return 1
	}

	// If the caller passes a /nix/store/... path, run why-depends directly
	// on it without enumerating the closure. This is the equivalent of the
	// old `debug-nix-why-depends path` recipe — useful when you already have
	// the exact path from a previous `dupes` run.
	if strings.HasPrefix(fs.Arg(0), "/nix/store/") {
		return whyOnce(ctx, rootOut, fs.Arg(0), scope)
	}

	pat, err := regexp.Compile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang why: invalid regex %q: %v\n", fs.Arg(0), err)
		return 2
	}

	rootRef, paths, useDeriv, err := whyClosurePaths(ctx, rootOut, scope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang why: %v\n", err)
		return 1
	}

	var matches []string
	for _, p := range paths {
		if pat.MatchString(storepath.Name(p)) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(os.Stderr, "doppelgang why: no closure path matched %q\n", pat)
		return 1
	}

	for _, p := range matches {
		fmt.Printf("===== %s =====\n", storepath.Name(p))
		runWhyDepends(ctx, rootRef, p, useDeriv)
		fmt.Println()
	}
	return 0
}

// whyOnce traces a single store path back to the installable. The caller's
// argument is taken literally — no closure scan, no name matching. Mirrors
// the eng/justfile `debug-nix-why-depends path` one-liner. Build scope adds
// --derivation and rewrites both source and target to .drv form so setup
// hooks and other build-time-only paths resolve.
func whyOnce(ctx context.Context, rootOut, target string, scope closure.Scope) int {
	rootRef := rootOut
	useDeriv := false
	if scope == closure.Build {
		// Rewrite root to its .drv. The target is left as-is — callers that
		// pass an output path expect runtime tracing even within --derivation
		// mode; nix why-depends rejects mixed pairings rather than guessing.
		drvOut, err := exec.CommandContext(ctx, "nix-store", "-q", "--deriver", rootOut).Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "doppelgang why: nix-store -q --deriver: %v\n", err)
			return 1
		}
		drv := strings.TrimSpace(string(drvOut))
		if drv == "" || drv == "unknown-deriver" {
			fmt.Fprintf(os.Stderr, "doppelgang why: no deriver for %s\n", rootOut)
			return 1
		}
		rootRef = drv
		useDeriv = true
	}
	fmt.Printf("===== %s =====\n", storepath.Name(target))
	runWhyDepends(ctx, rootRef, target, useDeriv)
	return 0
}

// runWhyDepends invokes `nix why-depends [--derivation] <root> <target>`
// inheriting stdout/stderr. Errors are not surfaced because nix already
// prints diagnostics to stderr; callers continue on the next path.
func runWhyDepends(ctx context.Context, rootRef, target string, useDeriv bool) {
	whyArgs := []string{"why-depends"}
	if useDeriv {
		whyArgs = append(whyArgs, "--derivation")
	}
	whyArgs = append(whyArgs, rootRef, target)
	cmd := exec.CommandContext(ctx, "nix", whyArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// whyClosurePaths returns the why-depends source ref (output path or .drv),
// the list of closure paths to match against, and whether to pass
// `--derivation` to nix why-depends.
//
// Build scope: source ref is the root .drv, paths are every requisite
// .drv. Use --derivation so chains are shown at drv level (so build-time
// helpers like setup hooks, which never appear in runtime closures, are
// reachable).
//
// Runtime scope: source ref is the output path itself, paths are every
// runtime-reachable output. No --derivation.
func whyClosurePaths(ctx context.Context, rootOut string, scope closure.Scope) (string, []string, bool, error) {
	switch scope {
	case closure.Build:
		drvOut, err := exec.CommandContext(ctx, "nix-store", "-q", "--deriver", rootOut).Output()
		if err != nil {
			return "", nil, false, fmt.Errorf("nix-store -q --deriver: %w", err)
		}
		drv := strings.TrimSpace(string(drvOut))
		if drv == "" || drv == "unknown-deriver" {
			return "", nil, false, fmt.Errorf("no deriver for %s", rootOut)
		}
		reqOut, err := exec.CommandContext(ctx, "nix-store", "-q", "--requisites", drv).Output()
		if err != nil {
			return "", nil, false, fmt.Errorf("nix-store -q --requisites: %w", err)
		}
		paths := splitLines(string(reqOut))
		return drv, paths, true, nil
	case closure.Runtime:
		reqOut, err := exec.CommandContext(ctx, "nix-store", "-q", "--requisites", rootOut).Output()
		if err != nil {
			return "", nil, false, fmt.Errorf("nix-store -q --requisites: %w", err)
		}
		paths := splitLines(string(reqOut))
		return rootOut, paths, false, nil
	default:
		return "", nil, false, fmt.Errorf("unknown scope: %v", scope)
	}
}

func parseScope(s string) (closure.Scope, error) {
	switch s {
	case "runtime":
		return closure.Runtime, nil
	case "build":
		return closure.Build, nil
	default:
		return 0, fmt.Errorf("--scope must be runtime or build, got %q", s)
	}
}

func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func errExit(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang: %v\n", err)
		return 1
	}
	return 0
}
