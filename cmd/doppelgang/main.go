package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"

	"github.com/friedenberg/doppelgang/internal/0/closure"
	"github.com/friedenberg/doppelgang/internal/0/storepath"
	"github.com/friedenberg/doppelgang/internal/alfa/attribute"
	"github.com/friedenberg/doppelgang/internal/alfa/dupes"
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
	fmt.Fprintf(os.Stderr, "  doppelgang version\n\n")
	fmt.Fprintf(os.Stderr, "Defaults: --installable=./result, --scope=runtime (dupes) or build (why), --top=25.\n")
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
