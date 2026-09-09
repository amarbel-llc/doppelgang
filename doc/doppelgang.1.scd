doppelgang(1)

# NAME

doppelgang - find duplicate packages in a Nix closure and lint flake inputs

# SYNOPSIS

*doppelgang dupes* [*--installable* _installable_] [*--scope* runtime|build] [*--top* _n_] [*--by-owner*] [*--no-version-drift*] [*--json*]

*doppelgang why* _regex_ | _store-path_ [*--installable* _installable_] [*--scope* runtime|build]

*doppelgang lint* [*--flake* _dir_] [*--format* auto|text|json|ndjson] [*--checks* _list_] [*--online*] [*--fix*] [*--nixpkgs-master-sha* _sha_] [*--papi-domain* _domain_]

*doppelgang lint-man* [*--max* _n_] [*--format* auto|text|json] _path_...

*doppelgang version*

# DESCRIPTION

*doppelgang* inspects Nix closures, flake inputs, and man pages. It answers
four questions: what is duplicated in a closure and what that waste costs
(*dupes*), why a given path is in the closure at all (*why*), what
reducible duplication or convention drift a flake's input graph carries
(*lint*), and whether a set of man pages' NAME lines keep the contract
index builders rely on (*lint-man*).

*dupes* and *why* shell out to *nix-store*, *nix path-info*, and *nix
why-depends*, so those must be on PATH. *lint* is offline by default and
reads only _flake.lock_ and _flake.nix_; *--online* and *--fix* make it
impure. *lint-man* is offline and read-only.

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

## lint-man

Checks the NAME section of every page named by its positional arguments
against the *NAME LINE CONTRACT* below and prints one line per finding:

```
<path>: <check>: <detail>
```

for example _.../dodder-transform.1.gz: long: 79 > 72 chars_. Exits
non-zero when any error-level finding remains, so it can gate a manpath.

Each _path_ is one of:

- a directory, taken as a manpath root: its _man\*/_ section directories
  are scanned and every page beneath them linted, following symlinks
  (a nix profile's manpath is a symlink farm). A directory that is itself
  a section directory (_.../man7_) is accepted, as is one holding scdoc
  sources (_doc/_).
- a single rendered page, in man(7) or mdoc(7) source, optionally
  gzipped.
- an scdoc(5) source file, _\*.scd_, whose NAME is the _# NAME_ section.

The format is chosen by extension: _.scd_ is scdoc source, anything else a
rendered page, decompressed when it ends in _.gz_. A path that does not
exist, or a directory yielding no pages, is an error rather than a clean
pass.

*--max* _n_
	Maximum description length in characters (Unicode code points, not
	bytes). Default _72_.

*--format* auto|text|json
	Output format. _text_ is the one-line-per-finding form above, with a
	tally on stderr. _json_ is a single document carrying the budget, the
	page count, and every finding with its severity. _auto_ (the default)
	emits text on a TTY and JSON otherwise.

## version

Print the version and commit burnt into the binary, then exit.

# NAME LINE CONTRACT

whatis(1), apropos(1), and lexgrog(1) read a page's one-line description
from its NAME section, and so does spinclass's manpage index (spinclass
FDR 0030), which renders _name(section)_ plus that description for every
first-party page into each session's system prompt. Generated pages break
the contract in ways a human author would not: a Go codegen pastes an MCP
tool's paragraph-long help text as the description, a NAME section lists
names with no separator at all, or a description is wrapped over two roff
lines, which lexgrog joins and the index does not, truncating mid-word.
*lint-man* enforces, per page:

*unparsable*
	The NAME section must exist and read as _name - description_. Several
	names may precede the separator, comma-separated (_a, b - desc_), and
	several entries may follow one another separated by a break or
	paragraph macro. The separator is a space-delimited dash: _\\-_, _-_,
	_--_, or an em or en dash, resolved from roff escapes first. mdoc(7)
	pages use _.Nm_ and _.Nd_. This is the verdict lexgrog(1) reports as
	"parse failed"; a page that cannot be read or decompressed is reported
	as *unreadable* instead.

*empty*
	The description after the separator must be non-empty.

*wrapped*
	The entry must occupy one physical source line. A description
	continued on a second line - or, in mdoc, past the _.Nd_ line by text
	or an inline macro - is a failure regardless of lexgrog's verdict,
	because the index reads only the first line.

*long*
	The description, with escapes resolved and font changes stripped,
	must be at most *--max* characters.

*trailing-period*
	The description should not end with a period: it is a phrase, not a
	sentence. This is a warning. It is printed (suffixed _(warning)_ in
	text, _"severity": "warning"_ in JSON) but never fails the gate on its
	own.

Every other check is an error and fails the gate. Font escapes such as
_\\fB_ and character escapes such as _\\-_ and _\\(em_ are resolved before
the description is measured, so the count is what a reader sees. In scdoc
source, _\*bold\*_ and _\_underline\__ markers are stripped the same way.

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
	Success. For *lint*, no selected check reported a finding. For
	*lint-man*, no error-level finding; warnings alone still exit _0_.

_1_
	A selected check reported a finding, or the operation failed at
	runtime - the closure could not be loaded, _flake.lock_ could not be
	read, a *--fix* repair could not be applied, or a *lint-man* path did
	not exist or yielded no pages.

_2_
	Usage error: an unknown subcommand, a missing required argument, an
	invalid *--scope*, *--format*, or *--max* value, or a missing or
	malformed *--nixpkgs-master-sha* where one is required.

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

Gate a repo's own scdoc sources, then a whole rendered manpath:

```
doppelgang lint-man doc/*.scd
doppelgang lint-man ~/.local/share/first-party-manpages/linenisgreat/man
```

# SEE ALSO

*eng*(7), *eng-manpages*(7), *lexgrog*(1), *nix*(1), *scdoc*(5)
