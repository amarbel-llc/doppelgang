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
	fmt.Fprintf(os.Stderr, "inputs, flags inputs pinned at multiple revs, and (reading <flake>/flake.nix)\n")
	fmt.Fprintf(os.Stderr, "flags dead `follows` overrides that target an input the dependency no longer\n")
	fmt.Fprintf(os.Stderr, "declares. Exits 1 when any finding is reported, so it can serve as a CI gate.\n")
	fmt.Fprintf(os.Stderr, "--format=auto (the default) emits text on a TTY and tap NDJSON otherwise.\n")
	fmt.Fprintf(os.Stderr, "--fix splices the follows-opportunity edits into <flake>/flake.nix and prunes\n")
	fmt.Fprintf(os.Stderr, "direct dead overrides, then re-locks via `nix flake lock` and stages the touched\n")
	fmt.Fprintf(os.Stderr, "files (needs nix on PATH). Multi-version inputs and transitive dead overrides\n")
	fmt.Fprintf(os.Stderr, "stay report-only — collapsing/relocating them is not a local mechanical edit.\n")
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

	report, err := analyzeFlake(*flakeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: %v\n", err)
		return 1
	}
	sum := render.LintSummary{Report: report}

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
		return lintFix(*flakeDir, report)
	}

	// Exit non-zero when actionable findings exist so `lint` can serve
	// as a CI gate.
	if reportHasFindings(report) {
		return 1
	}
	return 0
}

// analyzeFlake runs the lock-only checks on <flakeDir>/flake.lock and, when
// <flakeDir>/flake.nix is present and parseable, the direct dead-override
// check on top. A missing or unparseable flake.nix degrades dead-override
// detection to skipped (the lock-only report is returned) rather than failing
// the run, so `lint` stays useful in an unbuilt checkout. Detection is fully
// offline; only --fix's re-lock needs nix.
func analyzeFlake(flakeDir string) (lint.Report, error) {
	lock, err := flakelock.Load(flakeDir)
	if err != nil {
		return lint.Report{}, err
	}
	report := lint.Analyze(lock)
	report.DeadOverrides = directDeadOverrides(flakeDir, lock)
	return report, nil
}

// directDeadOverrides reads <flakeDir>/flake.nix and returns the dead follows
// overrides declared there. A read failure yields nil (an unbuilt checkout
// may have no flake.nix to inspect); an unparseable flake.nix yields nil with
// a stderr note. Either way detection is skipped, never fatal.
func directDeadOverrides(flakeDir string, lock *flakelock.Lock) []lint.DeadOverride {
	src, err := os.ReadFile(filepath.Join(flakeDir, "flake.nix"))
	if err != nil {
		return nil
	}
	overrides, err := nixedit.Overrides(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: flake.nix not parseable; skipping dead-override detection (%v)\n", err)
		return nil
	}
	return lint.DeadOverrides(lock, overrides)
}

// reportHasFindings reports whether any of the three checks found something
// actionable, used to gate the non-zero CI exit.
func reportHasFindings(r lint.Report) bool {
	return len(r.Follows) > 0 || len(r.MultiVersion) > 0 || len(r.DeadOverrides) > 0
}

// reportOnlyCount counts findings --fix cannot auto-resolve: multi-version
// inputs (collapsing them changes behavior) and transitive dead overrides
// (the fix lands in an upstream flake.nix, not this one).
func reportOnlyCount(r lint.Report) int {
	n := len(r.MultiVersion)
	for _, d := range r.DeadOverrides {
		if !d.Direct {
			n++
		}
	}
	return n
}

// lintFix applies the auto-fixable edits from report to <flakeDir>/flake.nix
// — splicing in follows-opportunity lines and pruning direct dead follows
// overrides — then re-locks via `nix flake lock`, stages the touched files,
// and re-analyzes to compute an honest exit code. It is invoked only under
// --fix, after the report has already been rendered.
//
// Two finding classes are report-only and never auto-resolved: multi-version
// inputs (choosing a revision changes behavior) and transitive dead overrides
// (the offending binding lives in an upstream flake.nix). Either keeps the
// exit code non-zero. The exit is 0 only when, after the fix, no finding of
// any category remains.
//
// All progress goes to stderr so it does not pollute the machine-readable
// report already written to stdout.
func lintFix(flakeDir string, report lint.Report) int {
	var followsLines []string
	for _, r := range report.Follows {
		followsLines = append(followsLines, r.Lines...)
	}
	var deadTargets []string
	for _, d := range report.DeadOverrides {
		if d.Direct {
			deadTargets = append(deadTargets, d.Override)
		}
	}

	if len(followsLines) == 0 && len(deadTargets) == 0 {
		if remaining := reportOnlyCount(report); remaining > 0 {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: nothing auto-fixable; %d report-only finding(s) remain (multi-version inputs and/or transitive dead overrides)\n", remaining)
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

	out := src
	var appliedFollows []string
	if len(followsLines) > 0 {
		out, appliedFollows, err = nixedit.Apply(out, followsLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: could not edit %s automatically (%v).\n"+
				"Apply the follows line(s) above by hand, then re-run `nix flake lock`.\n", nixPath, err)
			return 1
		}
	}
	var removed []string
	if len(deadTargets) > 0 {
		out, removed, err = nixedit.DeleteBindings(out, deadTargets)
		if err != nil {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: could not prune dead override(s) from %s automatically (%v).\n"+
				"Remove the dead follows line(s) above by hand, then re-run `nix flake lock`.\n", nixPath, err)
			return 1
		}
	}

	if len(appliedFollows) == 0 && len(removed) == 0 {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: edits already present in flake.nix; nothing to apply\n")
	} else {
		if err := os.WriteFile(nixPath, out, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: write %s: %v\n", nixPath, err)
			return 1
		}
		if len(appliedFollows) > 0 {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: applied %d follows line(s) to %s:\n", len(appliedFollows), nixPath)
			for _, l := range appliedFollows {
				fmt.Fprintf(os.Stderr, "    %s\n", l)
			}
		}
		if len(removed) > 0 {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: pruned %d dead follows override(s) from %s:\n", len(removed), nixPath)
			for _, l := range removed {
				fmt.Fprintf(os.Stderr, "    %s\n", l)
			}
		}
		if err := relock(flakeDir); err != nil {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: re-lock failed: %v\n", err)
			return 1
		}
		stageFixedFiles(flakeDir)
	}

	// Re-analyze the (possibly regenerated) lock + edited flake.nix for an
	// honest exit code.
	after, err := analyzeFlake(flakeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: re-analyze: %v\n", err)
		return 1
	}
	if len(after.Follows) > 0 {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d follows opportunity group(s) remain after fix (re-run lint for detail)\n", len(after.Follows))
		return 1
	}
	if n := len(after.DeadOverrides); n > 0 {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d dead override(s) remain after fix (transitive overrides are fixed in the upstream flake.nix, not here)\n", n)
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
