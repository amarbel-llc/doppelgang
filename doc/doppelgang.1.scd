doppelgang(1)

# NAME

doppelgang - find duplicate packages in a Nix closure and lint flake inputs

# SYNOPSIS

*doppelgang dupes* [*--installable* _installable_] [*--scope* runtime|build] [*--top* _n_] [*--by-owner*] [*--no-version-drift*] [*--json*]

*doppelgang why* _regex_ | _store-path_ [*--installable* _installable_] [*--scope* runtime|build]

*doppelgang lint* [*--flake* _dir_] [*--format* auto|text|json|ndjson] [*--checks* _list_] [*--online*] [*--fix*] [*--nixpkgs-master-sha* _sha_] [*--papi-domain* _domain_]

*doppelgang version*

# DESCRIPTION

*doppelgang* inspects Nix closures and flake inputs. It answers three
questions: what is duplicated in a closure and what that waste costs
(*dupes*), why a given path is in the closure at all (*why*), and what
reducible duplication or convention drift a flake's input graph carries
(*lint*).

*dupes* and *why* shell out to *nix-store*, *nix path-info*, and *nix
why-depends*, so those must be on PATH. *lint* is offline by default and
reads only _flake.lock_ and _flake.nix_; *--online* and *--fix* make it
impure.

# COMMANDS

## dupes

Lists true duplicates: store paths sharing the same _name-version_ but with
different content hashes, typically because two flake inputs each carry
their own pinned nixpkgs. Multi-output derivations (_jq_, _jq-bin_,
_jq-dev_, _jq-man_) are not duplicates and are never reported.

Groups are sorted by wasted bytes. Each copy is printed with its immediate
parents in the closure, so you can see which library pulled it in.

*--installable* _installable_
	Nix installable, store path, or symlink to analyze. Default _./result_.

*--scope* runtime|build
	Closure scope. Default _runtime_.

*--top* _n_
	Report at most _n_ duplicate groups. _0_ means all. Default _25_.

*--by-owner*
	Attribute each copy to the top-level installables (direct references of
	the root) that reach it, instead of to its immediate parents. Useful for
	charging waste back to specific flake inputs.

*--no-version-drift*
	Suppress the pname-grouped version-drift section.

*--json*
	Emit JSON instead of human-readable text.

## why

A thin wrapper over *nix why-depends*, polymorphic in its argument. A
_/nix/store/..._ path is traced directly, without enumerating the closure -
handy when you already have an exact path from a *dupes* run. Any other
argument is treated as a name regex, and every closure path whose name
matches has its dependency chain printed.

*--installable* _installable_
	Nix installable, store path, or symlink to query. Default _./result_.

*--scope* runtime|build
	Default _build_, which passes *--derivation* to *why-depends* so
	build-time-only paths such as setup hooks remain reachable. _runtime_
	traces output paths only.

## lint

Reads _<flake>/flake.lock_ and _<flake>/flake.nix_ and reports reducible
input duplication, rot, and convention drift. Exits non-zero when any
selected check reports a finding, so it can serve as a CI gate. See
*CHECKS*.

*--flake* _dir_
	Directory containing _flake.nix_ and _flake.lock_. Default _._.

*--format* auto|text|json|ndjson
	Output format. _auto_ (the default) emits text on a TTY and tap NDJSON
	otherwise.

*--checks* _list_
	Comma-separated subset of checks to run. Gates the exit code, the
	output, and *--fix* alike. _all_ selects every check. Default: the
	three lock analyses (_follows_, _multi-version_, _dead-overrides_).

*--online*
	Additionally detect transitive dead overrides by fetching upstream
	_flake.nix_ files. Read-only and best-effort; any fetch failure is a
	silent no-op. Implied by *--fix*.

*--fix*
	Apply the repairs described under *CHECKS* to _flake.nix_, then re-lock
	via *nix flake lock* and stage the touched files. Needs *nix* on PATH,
	so unlike a plain *lint* it is not offline. Idempotent.

*--nixpkgs-master-sha* _sha_
	The 40-character lowercase-hex nixpkgs revision the _nixpkgs-master_
	input should be pinned to. Required with *--fix* when the
	_nixpkgs-master_ check is selected. Supplying it in check mode also
	enables staleness detection; see _nixpkgs-master_ under *CHECKS*. A
	value that is not 40-hex is rejected before any analysis.

*--papi-domain* _domain_
	PAPI identity domain for the _canonical-inputs_ check, for example
	_linenisgreat.com_. Also read from *PAPI\_DOMAIN*. When absent the
	check degrades to a no-op.

## version

Print the version and commit burnt into the binary, then exit.

# CHECKS

The first three checks run by default. The rest are opt-in: they encode
fleet policy rather than universal findings, so a plain *lint* on an
arbitrary flake must not fail for lacking them. Select them by name or via
_--checks all_.

*follows*
	Nodes pinning a byte-identical source (same _narHash_ and _rev_) more
	than once. For each, *lint* prints the concrete
	_inputs.X.follows = "Y"_ line to add. *--fix* splices those lines in.

*multi-version*
	A single _owner/repo_ pinned at more than one revision. Report-only:
	choosing a revision changes behavior, so this is never auto-collapsed.

*dead-overrides*
	An _inputs.X.follows_ override targeting an input the dependency no
	longer declares - what Nix warns about as an override for a
	non-existent input. A _direct_ dead override lives in the linted
	_flake.nix_ and *--fix* prunes it. A _transitive_ one lives in an
	upstream flake and is report-only; the fix lands upstream.

*nixpkgs-master*
	Verifies _flake.nix_ declares a top-level _nixpkgs-master_ input pinned
	to _github:NixOS/nixpkgs/_ followed by a 40-hex revision, the shape
	eng's update-nix cascade requires. Four failure modes are reported:

	- _missing_ - no such input is declared. A bare
	  _X.inputs.nixpkgs-master.follows_ override does not count.
	- _floating_ - a _github:NixOS/nixpkgs_ ref not pinned to a full 40-hex
	  revision: no rev, a branch or tag name, a short rev, or uppercase hex.
	- _non-github_ - not a _github:NixOS/nixpkgs_ ref at all.
	- _stale_ - a well-formed pin naming a revision other than
	  *--nixpkgs-master-sha*. Only reachable when that target is supplied;
	  without it the check is shape-only and any well-formed pin passes.
	  This is what lets the cascade advance an already-pinned repo.

	*--fix* splices the binding in when the input is missing, and rewrites
	the url in place when it floats, is non-github, or is stale. Detection
	reads _flake.nix_ alone - no lock - so it works on a freshly cloned
	repo. The repair deliberately does NOT re-lock; materializing the input
	into _flake.lock_ is left to the caller.

*canonical-inputs*
	Verifies each top-level input whose name matches a repo published by
	the PAPI domain uses that repo's canonical forge URL. The check
	governs which forge an input is fetched from, not which revision, so
	an input already in the canonical form, differing from it only by
	pinning a 40-hex revision, is conformant: it is reported with status
	_pinned_, and neither fails the check nor is touched by *--fix*. The
	pin is written the way that form takes one - the tarball form as
	_.../<repo>/archive/<rev>.tar.gz_, the git+https form as
	_?rev=<rev>_, the github: form as a third path segment.

	An input in some other form is still a finding, even on the canonical
	host, because the fetcher type is part of what makes two repos'
	inputs collapse onto one lock node. When such a URL pins a revision
	the repair carries that revision into the canonical form rather than
	floating the input to the canonical ref. A branch or tag in the ref
	position is a float, not a pin, so it is rewritten as usual. *--fix*
	rewrites non-canonical URLs byte-preservingly and does not re-lock.
	Requires *--papi-domain*.

*canonical-form*
	Flags inputs whose bindings are not contiguous under the top-level
	_inputs_ attrset, i.e. another input's binding is interleaved between
	two of theirs. Per-flake opt-in: only runs on a flake carrying a
	_# doppelgang: canonical_ directive comment on the line immediately
	above its _inputs_ binding. *--fix* relocates scattered bindings
	adjacent to their siblings.

*outputs-participation*
	Flags inputs the flake declares that its _outputs_ function cannot
	accept. Nix passes every declared input to _outputs_, so a formals set
	enumerating names with no trailing ellipsis fails to evaluate on an
	input it does not name. *--fix* appends the ellipsis. Silent when the
	signature already accepts unnamed inputs. Detection needs no lock, no
	network, and no fleet parameter, which is what lets this check run
	inside a sandboxed gate where _nixpkgs-master_ and _canonical-inputs_
	cannot.

Independently of the *outputs-participation* selection, *--fix* widens a
closed _outputs_ signature whenever a repair may have ADDED an input - the
_nixpkgs-master_ and _canonical-inputs_ repairs both splice. A repair must
never leave behind a flake that cannot be evaluated.

# EXIT STATUS

_0_
	Success. For *lint*, no selected check reported a finding.

_1_
	A selected check reported a finding, or the operation failed at
	runtime - the closure could not be loaded, _flake.lock_ could not be
	read, or a *--fix* repair could not be applied.

_2_
	Usage error: an unknown subcommand, a missing required argument, an
	invalid *--scope* or *--format* value, or a missing or malformed
	*--nixpkgs-master-sha* where one is required.

# ENVIRONMENT

*PAPI\_DOMAIN*
	Fallback for *--papi-domain*. The flag takes precedence.

# FILES

_<flake>/flake.nix_
	Read by the *nixpkgs-master*, *canonical-form*,
	*outputs-participation*, *dead-overrides*, and *canonical-inputs*
	checks, and rewritten in place by *--fix*.

_<flake>/flake.lock_
	Read by the *follows*, *multi-version*, *dead-overrides*, and
	*canonical-inputs* checks. The checks that read only _flake.nix_ do
	not need it, so they work on a repo that is not yet locked.

# EXAMPLES

Report the 10 largest duplicate groups in a build, attributed to the
top-level inputs that pull them in:

```
doppelgang dupes --installable .#default --top 10 --by-owner
```

Trace why a specific store path is in the closure:

```
doppelgang why /nix/store/...-openssl-3.0.13
```

Gate CI on the default checks, emitting tap NDJSON:

```
doppelgang lint --format ndjson
```

Pin the _nixpkgs-master_ input to a revision, adding it if missing and
rewriting it if it floats or lags - the shape eng's update-nix cascade
invokes:

```
doppelgang lint --fix --checks nixpkgs-master --nixpkgs-master-sha 567a49d1913ce81ac6e9582e3553dd90a955875f
```

Detect a stale pin without repairing it:

```
doppelgang lint --checks nixpkgs-master --nixpkgs-master-sha 567a49d1913ce81ac6e9582e3553dd90a955875f
```

# SEE ALSO

*eng*(7), *nix*(1)
