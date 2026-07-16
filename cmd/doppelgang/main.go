package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
		os.Exit(lintMain(ctx, flag.Args()[1:]))
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
	fmt.Fprintf(os.Stderr, "  doppelgang lint [--flake .] [--format auto|text|json|ndjson]\n")
	fmt.Fprintf(os.Stderr, "                  [--checks follows,multi-version,dead-overrides,nixpkgs-master,canonical-inputs]\n")
	fmt.Fprintf(os.Stderr, "                  [--online] [--fix] [--nixpkgs-master-sha <40-hex>]\n")
	fmt.Fprintf(os.Stderr, "                  [--papi-domain <domain>]\n")
	fmt.Fprintf(os.Stderr, "  doppelgang version\n\n")
	fmt.Fprintf(os.Stderr, "Defaults: --installable=./result, --scope=runtime (dupes) or build (why), --top=25.\n")
	fmt.Fprintf(os.Stderr, "`lint` reads <flake>/flake.lock and recommends `follows` for duplicate-source\n")
	fmt.Fprintf(os.Stderr, "inputs, flags inputs pinned at multiple revs, and (reading <flake>/flake.nix)\n")
	fmt.Fprintf(os.Stderr, "flags dead `follows` overrides that target an input the dependency no longer\n")
	fmt.Fprintf(os.Stderr, "declares. Exits 1 when any selected check reports a finding, so it can serve as\n")
	fmt.Fprintf(os.Stderr, "a CI gate. --checks restricts to a comma-separated subset (follows,\n")
	fmt.Fprintf(os.Stderr, "multi-version, dead-overrides, nixpkgs-master; default is the first three,\n")
	fmt.Fprintf(os.Stderr, "'all' selects every check including nixpkgs-master) — it gates the exit code, the\n")
	fmt.Fprintf(os.Stderr, "output, and --fix alike.\n")
	fmt.Fprintf(os.Stderr, "The nixpkgs-master check (opt-in) verifies flake.nix declares a nixpkgs-master\n")
	fmt.Fprintf(os.Stderr, "input pinned to github:NixOS/nixpkgs/<40-hex>, failing on a missing input, a\n")
	fmt.Fprintf(os.Stderr, "floating ref, or a non-github shape. --fix pins it to --nixpkgs-master-sha (which\n")
	fmt.Fprintf(os.Stderr, "is required with --fix when the check is selected): the url is spliced in when the\n")
	fmt.Fprintf(os.Stderr, "input is missing, or rewritten when it floats. This repair edits flake.nix only\n")
	fmt.Fprintf(os.Stderr, "and does NOT re-lock — materializing the input into flake.lock is left to the\n")
	fmt.Fprintf(os.Stderr, "caller (e.g. a following `nix flake update`).\n")
	fmt.Fprintf(os.Stderr, "--format=auto (the default) emits text on a TTY and tap NDJSON otherwise.\n")
	fmt.Fprintf(os.Stderr, "--online additionally detects transitive dead overrides (declared in an upstream\n")
	fmt.Fprintf(os.Stderr, "flake.nix) by fetching those files — read-only and best-effort. --fix splices the\n")
	fmt.Fprintf(os.Stderr, "follows-opportunity edits into <flake>/flake.nix and prunes direct dead overrides,\n")
	fmt.Fprintf(os.Stderr, "then re-locks via `nix flake lock` and stages the touched files (needs nix on PATH;\n")
	fmt.Fprintf(os.Stderr, "implies --online). Multi-version inputs and transitive dead overrides stay\n")
	fmt.Fprintf(os.Stderr, "report-only — collapsing/relocating them is not a local mechanical edit.\n")
	fmt.Fprintf(os.Stderr, "The canonical-inputs check (opt-in) verifies each top-level input whose name\n")
	fmt.Fprintf(os.Stderr, "matches a repo published by the PAPI domain uses that repo's canonical forge URL\n")
	fmt.Fprintf(os.Stderr, "(git+https://...). --fix rewrites non-canonical URLs in flake.nix (byte-preserving)\n")
	fmt.Fprintf(os.Stderr, "and does NOT re-lock — locking is left to the caller (the cascade's nix flake\n")
	fmt.Fprintf(os.Stderr, "update). --papi-domain sets the identity domain for the papi call (also read from\n")
	fmt.Fprintf(os.Stderr, "PAPI_DOMAIN env var); when absent the check degrades gracefully to no-op.\n")
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

func lintMain(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	flakeDir := fs.String("flake", ".", "directory containing flake.lock")
	format := fs.String("format", "auto", "output format: auto, text, json, or ndjson (auto = text on a TTY, ndjson otherwise)")
	checks := fs.String("checks", "", "comma-separated subset of checks to gate on: follows, multi-version, dead-overrides (default all; 'all' is an alias)")
	fix := fs.Bool("fix", false, "apply follows-opportunity edits and prune dead overrides in flake.nix, then re-lock (needs nix on PATH)")
	online := fs.Bool("online", false, "additionally detect transitive dead overrides by fetching upstream flake.nix files (read-only, best-effort; implied by --fix)")
	nixpkgsMasterSHA := fs.String("nixpkgs-master-sha", "", "40-hex nixpkgs revision to pin the nixpkgs-master input to; required with --fix when the nixpkgs-master check is selected")
	papiDomain := fs.String("papi-domain", "", "PAPI identity domain for the canonical-inputs check (e.g. linenisgreat.com); also read from PAPI_DOMAIN env var")
	_ = fs.Parse(args)

	sel, err := lint.ParseSelection(*checks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: %v\n", err)
		return 2
	}

	// Repairing the nixpkgs-master convention needs a target revision. Fail
	// loudly and early when --fix selects the check without a valid sha,
	// rather than analyzing first and discovering mid-repair we cannot fix
	// it. Check mode (no --fix) never needs the sha.
	if *fix && sel.Has(lint.CheckNixpkgsMaster) {
		if *nixpkgsMasterSHA == "" {
			fmt.Fprintf(os.Stderr, "doppelgang lint: --fix with the nixpkgs-master check requires --nixpkgs-master-sha <40-hex sha>\n")
			return 2
		}
		if !lint.ValidNixpkgsSHA(*nixpkgsMasterSHA) {
			fmt.Fprintf(os.Stderr, "doppelgang lint: --nixpkgs-master-sha must be a 40-char lowercase-hex nixpkgs revision, got %q\n", *nixpkgsMasterSHA)
			return 2
		}
	}

	// Resolve the PAPI domain for the canonical-inputs check: --papi-domain
	// flag takes precedence; PAPI_DOMAIN env var is the fallback.
	resolvedPAPIDomain := *papiDomain
	if resolvedPAPIDomain == "" {
		resolvedPAPIDomain = os.Getenv("PAPI_DOMAIN")
	}
	if sel.Has(lint.CheckCanonicalInputs) && resolvedPAPIDomain == "" {
		fmt.Fprintf(os.Stderr, "doppelgang lint: --papi-domain (or PAPI_DOMAIN) not set; skipping canonical-inputs check\n")
	}

	resolved, err := resolveLintFormat(*format, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: %v\n", err)
		return 2
	}

	// Transitive dead-override detection fetches upstream flake.nix files, so
	// it runs only when explicitly opted in: --online (read-only) or --fix
	// (which is already impure). Plain `lint` stays offline. The selection
	// also gates which checks are analyzed, rendered, and counted toward the
	// exit code.
	report, err := analyzeFlake(ctx, *flakeDir, sel, *fix || *online, resolvedPAPIDomain)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: %v\n", err)
		return 1
	}
	sum := render.LintSummary{Report: report, Selection: sel}

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
		return lintFix(ctx, *flakeDir, report, sel, *nixpkgsMasterSHA)
	}

	// Exit non-zero when a *selected* check reports a finding so `lint` can
	// serve as a CI gate over the chosen subset.
	if reportHasFindings(report, sel) {
		return 1
	}
	return 0
}

// analyzeFlake runs the lock-only checks on <flakeDir>/flake.lock and, when
// the dead-overrides check is selected and <flakeDir>/flake.nix is present
// and parseable, the direct dead-override check on top. A missing or
// unparseable flake.nix degrades dead-override detection to skipped (the
// lock-only report is returned) rather than failing the run, so `lint`
// stays useful in an unbuilt checkout. Direct detection is fully offline.
//
// The follows and multi-version analyses are lock-only and computed
// unconditionally (filtering them is cheap and happens at render/exit
// time); only the dead-overrides check — which parses flake.nix and may
// fetch upstream files — is skipped when deselected, so a caller that
// excludes it pays neither the parse-error note nor any network cost.
//
// When online is true (the impure --fix run) and dead-overrides is
// selected, it also attempts best-effort detection of transitive dead
// overrides — those declared in an upstream flake's flake.nix — by fetching
// those files. Any fetch failure is a silent no-op, so transitive findings
// are present only when reachable.
//
// When canonical-inputs is selected and papiDomain is non-empty, the check
// calls `papi repos <domain>` to discover canonical URLs and compares each
// root-level input. A papi failure degrades to zero findings (offline
// degrade), not a hard error.
func analyzeFlake(ctx context.Context, flakeDir string, sel lint.Selection, online bool, papiDomain string) (lint.Report, error) {
	var report lint.Report
	// Load the lock when any lock-dependent check is selected. The
	// nixpkgs-master check reads flake.nix alone and does not need the lock,
	// so `--checks nixpkgs-master` works on a freshly-cloned repo with no
	// flake.lock yet (the self-onboarding case). canonical-inputs joins the
	// three original lock-dependent checks here.
	needsLock := sel.Has(lint.CheckFollows) || sel.Has(lint.CheckMultiVersion) ||
		sel.Has(lint.CheckDeadOverrides) || sel.Has(lint.CheckCanonicalInputs)
	var lock *flakelock.Lock
	if needsLock {
		var err error
		lock, err = flakelock.Load(flakeDir)
		if err != nil {
			return lint.Report{}, err
		}
	}
	// Preserve pre-existing behaviour: the Follows+MultiVersion analyses are
	// always computed together when any of the original three lock checks are
	// selected (filtering is cheap and happens at render/exit time).
	if sel.Has(lint.CheckFollows) || sel.Has(lint.CheckMultiVersion) || sel.Has(lint.CheckDeadOverrides) {
		report = lint.Analyze(lock)
		if sel.Has(lint.CheckDeadOverrides) {
			report.DeadOverrides = directDeadOverrides(flakeDir, lock)
			if online {
				report.DeadOverrides = append(report.DeadOverrides, transitiveDeadOverrides(ctx, lock)...)
			}
		}
	}
	if sel.Has(lint.CheckNixpkgsMaster) {
		report.NixpkgsMaster = nixpkgsMasterFinding(flakeDir)
	}
	if sel.Has(lint.CheckCanonicalInputs) {
		report.CanonicalInputs = canonicalInputFindings(ctx, flakeDir, lock, papiDomain)
	}
	return report, nil
}

// canonicalInputFindings queries the PAPI domain for the canonical repo-URL
// map and checks each root-level lock input against it. Returns nil when
// papiDomain is empty, flake.nix is absent/empty, or the papi call fails
// (offline degrade). flake.nix is read before the network call so a missing
// file skips the papi round-trip entirely (fixes #20).
func canonicalInputFindings(ctx context.Context, flakeDir string, lock *flakelock.Lock, papiDomain string) []lint.CanonicalInputFinding {
	if papiDomain == "" {
		return nil
	}
	src, _ := os.ReadFile(filepath.Join(flakeDir, "flake.nix"))
	if len(src) == 0 {
		return nil
	}
	repoURLs := papiRepoURLs(ctx, papiDomain)
	return lint.CanonicalInputs(lock, src, repoURLs)
}

// papiRepoURLs calls `papi repos <domain>` and returns a map of repo name to
// canonical nix flake URL (git+<https-url>.git). Returns nil when papi is
// unavailable or the response is unparseable (offline degrade). All errors
// are reported to stderr and treated as non-fatal.
func papiRepoURLs(ctx context.Context, domain string) map[string]string {
	out, err := exec.CommandContext(ctx, "papi", "repos", domain).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: papi repos %s unavailable (%v); skipping canonical-inputs check\n", domain, err)
		return nil
	}
	return papiRepoURLsFromJSON(domain, out, os.Stderr)
}

// papiRepoURLsFromJSON decodes the JSON output of `papi repos <domain>` and
// returns a name→canonicalNixURL map. It handles dual-homed repos (papi#50):
// when a name appears multiple times, exactly one entry must carry
// canonical:true; zero or multiple markers are both treated as ambiguous and
// the repo is skipped with a note written to w.
//
// When an entry carries flake_url (papi#56), that value is used verbatim as
// the canonical nix URL. When absent, the git+https form is derived from the
// web URL via CanonicalNixURL — the two forms coexist during rollout and the
// choice is purely per-entry.
func papiRepoURLsFromJSON(domain string, data []byte, w io.Writer) map[string]string {
	type repoEntry struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		Canonical bool   `json:"canonical"`
		FlakeURL  string `json:"flake_url"`
	}
	// nixURL returns the canonical nix flake URL for an entry: flake_url
	// verbatim when present (papi#56 tarball form), git+https derivation
	// otherwise (original form, rollout-order-independent fallback).
	nixURL := func(e repoEntry) string {
		if e.FlakeURL != "" {
			return e.FlakeURL
		}
		return lint.CanonicalNixURL(e.URL)
	}
	var repos []repoEntry
	if err := json.Unmarshal(data, &repos); err != nil {
		fmt.Fprintf(w, "doppelgang lint: papi repos %s response unparseable; skipping canonical-inputs check\n", domain)
		return nil
	}
	byName := make(map[string][]repoEntry, len(repos))
	for _, r := range repos {
		if r.Name == "" || r.URL == "" {
			continue
		}
		byName[r.Name] = append(byName[r.Name], r)
	}
	m := make(map[string]string, len(byName))
	for name, entries := range byName {
		if len(entries) == 1 {
			m[name] = nixURL(entries[0])
			continue
		}
		var canonical repoEntry
		canonicalCount := 0
		for _, e := range entries {
			if e.Canonical {
				canonicalCount++
				canonical = e
			}
		}
		switch canonicalCount {
		case 1:
			m[name] = nixURL(canonical)
		case 0:
			fmt.Fprintf(w, "doppelgang lint: papi repos %s: %q listed %d times with no canonical marker; skipping (ambiguous)\n", domain, name, len(entries))
		default:
			fmt.Fprintf(w, "doppelgang lint: papi repos %s: %q has %d canonical markers; skipping (server nonconformance)\n", domain, name, canonicalCount)
		}
	}
	return m
}

// nixpkgsMasterFinding reads <flakeDir>/flake.nix and classifies its
// top-level `nixpkgs-master` input against the SHA-pinned convention,
// returning a finding (or nil when conformant). A read failure yields nil
// (an unbuilt/exotic checkout with no flake.nix to inspect is not the
// convention's concern); an unparseable flake.nix yields nil with a stderr
// note. A parseable flake.nix that simply lacks the input yields a Missing
// finding — the issue's "input missing entirely" fail case. Detection is
// fully offline.
func nixpkgsMasterFinding(flakeDir string) *lint.NixpkgsMasterFinding {
	src, err := os.ReadFile(filepath.Join(flakeDir, "flake.nix"))
	if err != nil {
		return nil
	}
	url, present, err := nixedit.InputURL(src, "nixpkgs-master")
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint: flake.nix not parseable; skipping nixpkgs-master convention check (%v)\n", err)
		return nil
	}
	return lint.ClassifyNixpkgsMaster(url, present)
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

// reportHasFindings reports whether any *selected* check found something
// actionable, used to gate the non-zero CI exit. A deselected check's
// findings never contribute, so a caller can gate on a chosen subset.
func reportHasFindings(r lint.Report, sel lint.Selection) bool {
	if sel.Has(lint.CheckFollows) && len(r.Follows) > 0 {
		return true
	}
	if sel.Has(lint.CheckMultiVersion) && len(r.MultiVersion) > 0 {
		return true
	}
	if sel.Has(lint.CheckDeadOverrides) && len(r.DeadOverrides) > 0 {
		return true
	}
	if sel.Has(lint.CheckNixpkgsMaster) && r.NixpkgsMaster != nil {
		return true
	}
	if sel.Has(lint.CheckCanonicalInputs) && len(r.CanonicalInputs) > 0 {
		return true
	}
	return false
}

// reportOnlyCount counts selected findings --fix cannot auto-resolve:
// multi-version inputs (collapsing them changes behavior) and transitive
// dead overrides (the fix lands in an upstream flake.nix, not this one). A
// deselected check contributes nothing.
func reportOnlyCount(r lint.Report, sel lint.Selection) int {
	n := 0
	if sel.Has(lint.CheckMultiVersion) {
		n += len(r.MultiVersion)
	}
	if sel.Has(lint.CheckDeadOverrides) {
		n += transitiveCount(r)
	}
	return n
}

// lintFix applies the auto-fixable edits from report to <flakeDir>/flake.nix
// — splicing in follows-opportunity lines, pruning direct dead follows
// overrides, pinning nixpkgs-master, and rewriting non-canonical input URLs —
// then re-locks via `nix flake lock` (for lock-affecting edits), stages the
// touched files, and re-analyzes to compute an honest exit code. It is
// invoked only under --fix, after the report has already been rendered.
//
// Two finding classes are report-only and never auto-resolved: multi-version
// inputs (choosing a revision changes behavior) and transitive dead overrides
// (the offending binding lives in an upstream flake.nix). Either keeps the
// exit code non-zero. The exit is 0 only when, after the fix, no finding of
// any category remains.
//
// All progress goes to stderr so it does not pollute the machine-readable
// report already written to stdout.
//
// The selection gates which classes are auto-fixed: follows lines only when
// the follows check is selected, direct dead overrides only when the
// dead-overrides check is selected, the nixpkgs-master pin only when the
// nixpkgs-master check is selected, and canonical-input URL rewrites only
// when the canonical-inputs check is selected. A deselected check is neither
// applied nor counted toward the report-only accounting or the exit code.
//
// Locking policy: the follows-collapse and dead-override prunes are
// re-locked (`nix flake lock`) here because they rewrite the lock graph the
// finding was derived from. The nixpkgs-master pin and canonical-input URL
// rewrites are NOT re-locked by this tool — they edit flake.nix only and
// leave materializing the new/updated inputs into flake.lock to the caller.
// eng's update-nix cascade runs `nix flake update` immediately after this
// repair. When a canonical-inputs edit rides alongside a follows/dead edit,
// the shared re-lock those require will pick it up as a side effect.
// flake.nix is always staged regardless.
func lintFix(ctx context.Context, flakeDir string, report lint.Report, sel lint.Selection, nixpkgsMasterSHA string) int {
	var followsLines []string
	if sel.Has(lint.CheckFollows) {
		for _, r := range report.Follows {
			followsLines = append(followsLines, r.Lines...)
		}
	}
	var deadTargets []string
	if sel.Has(lint.CheckDeadOverrides) {
		for _, d := range report.DeadOverrides {
			if d.Direct {
				deadTargets = append(deadTargets, d.Override)
			}
		}
	}
	// The upfront validation in lintMain guarantees a valid sha here whenever
	// the nixpkgs-master check is selected under --fix.
	fixNixpkgsMaster := sel.Has(lint.CheckNixpkgsMaster) && report.NixpkgsMaster != nil
	fixCanonicalInputs := sel.Has(lint.CheckCanonicalInputs) && len(report.CanonicalInputs) > 0

	if len(followsLines) == 0 && len(deadTargets) == 0 && !fixNixpkgsMaster && !fixCanonicalInputs {
		if remaining := reportOnlyCount(report, sel); remaining > 0 {
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
	var nixpkgsMasterURL string
	nixpkgsMasterChanged := false
	if fixNixpkgsMaster {
		nixpkgsMasterURL = lint.NixpkgsMasterURL(nixpkgsMasterSHA)
		out, nixpkgsMasterChanged, err = nixedit.SetInputURL(out, "nixpkgs-master", nixpkgsMasterURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: could not pin nixpkgs-master in %s automatically (%v).\n"+
				"Set `nixpkgs-master.url = %q` by hand.\n", nixPath, err, nixpkgsMasterURL)
			return 1
		}
	}
	var canonicalURLsRewritten int
	if fixCanonicalInputs {
		for _, f := range report.CanonicalInputs {
			var changed bool
			out, changed, err = nixedit.SetInputURL(out, f.Input, f.CanonicalURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "doppelgang lint --fix: could not rewrite %s.url in %s automatically (%v).\n"+
					"Set `%s.url = %q` by hand.\n", f.Input, nixPath, err, f.Input, f.CanonicalURL)
				return 1
			}
			if changed {
				canonicalURLsRewritten++
				fmt.Fprintf(os.Stderr, "doppelgang lint --fix: rewrote %s.url in %s: %s → %s\n", f.Input, nixPath, f.CurrentURL, f.CanonicalURL)
			}
		}
	}

	// Follows and dead-override edits rewrite the lock graph, so they trigger
	// a re-lock below; nixpkgs-master and canonical-input URL edits do not
	// (their locks are left to the caller — see the function doc).
	lockAffecting := len(appliedFollows) > 0 || len(removed) > 0
	flakeNixChanged := lockAffecting || nixpkgsMasterChanged || canonicalURLsRewritten > 0

	if !flakeNixChanged {
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
		if nixpkgsMasterChanged {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: pinned nixpkgs-master in %s to %s\n", nixPath, nixpkgsMasterURL)
		}
		// Re-lock only for lock-affecting edits. nixpkgs-master and
		// canonical-inputs edits leave lock materialization to the caller
		// (eng's cascade runs `nix flake update` next).
		if lockAffecting {
			if err := relock(flakeDir); err != nil {
				fmt.Fprintf(os.Stderr, "doppelgang lint --fix: re-lock failed: %v\n", err)
				return 1
			}
		}
		stageFixedFiles(flakeDir)
	}

	// Re-analyze the (possibly regenerated) lock + edited flake.nix for an
	// honest exit code, under the same selection. This pass is offline
	// (online=false, papiDomain=""): pruning a direct override cannot change
	// the transitive findings, so those are carried forward from the pre-fix
	// report rather than re-fetched. canonical-inputs findings after the URL
	// rewrite would require re-fetching papi (the URLs are already in
	// report.CanonicalInputs); we check the length directly instead.
	// Each remaining-finding check is gated by the selection so a deselected
	// category never holds the exit non-zero.
	after, err := analyzeFlake(ctx, flakeDir, sel, false, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: re-analyze: %v\n", err)
		return 1
	}
	if sel.Has(lint.CheckFollows) && len(after.Follows) > 0 {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d follows opportunity group(s) remain after fix (re-run lint for detail)\n", len(after.Follows))
		return 1
	}
	if sel.Has(lint.CheckDeadOverrides) {
		if n := len(after.DeadOverrides); n > 0 {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d direct dead override(s) remain after fix (re-run lint for detail)\n", n)
			return 1
		}
		if n := transitiveCount(report); n > 0 {
			fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d transitive dead override(s) remain (fix in the upstream flake.nix, not here)\n", n)
			return 1
		}
	}
	if sel.Has(lint.CheckMultiVersion) && len(after.MultiVersion) > 0 {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d multi-version input(s) remain (report-only, not auto-collapsed)\n", len(after.MultiVersion))
		return 1
	}
	if sel.Has(lint.CheckNixpkgsMaster) && after.NixpkgsMaster != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: nixpkgs-master still non-conformant after fix (%s); re-run lint for detail\n", after.NixpkgsMaster.Status)
		return 1
	}
	// canonical-inputs: the re-analyze pass runs with empty papiDomain (no
	// network call), so after.CanonicalInputs is always nil. Instead verify
	// that every finding in the pre-fix report was actually rewritten.
	if sel.Has(lint.CheckCanonicalInputs) && canonicalURLsRewritten < len(report.CanonicalInputs) {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: %d canonical-input URL(s) not rewritten (already correct or unparseable); re-run lint for detail\n",
			len(report.CanonicalInputs)-canonicalURLsRewritten)
		return 1
	}
	return 0
}

// transitiveCount counts the transitive (report-only) dead overrides in a
// report — those whose fix lands in an upstream flake.nix.
func transitiveCount(r lint.Report) int {
	n := 0
	for _, d := range r.DeadOverrides {
		if !d.Direct {
			n++
		}
	}
	return n
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

// stageFixedFiles runs `git add` on the touched files in flakeDir so the
// repair self-stages, composing with a `nix fmt` / pre-commit --staged
// repair flow (per the conformist/dewey repair convention). flake.lock is
// staged only when it exists, because the nixpkgs-master pin edits flake.nix
// alone without re-locking — a bad pathspec would make `git add` fail
// atomically and leave flake.nix unstaged too. A failure here (e.g. not a
// git repo) is non-fatal: the edits are already written.
func stageFixedFiles(flakeDir string) {
	paths := []string{"flake.nix"}
	if _, err := os.Stat(filepath.Join(flakeDir, "flake.lock")); err == nil {
		paths = append(paths, "flake.lock")
	}
	args := append([]string{"-C", flakeDir, "add"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "doppelgang lint --fix: could not stage %v (%v); stage them yourself\n", paths, err)
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
