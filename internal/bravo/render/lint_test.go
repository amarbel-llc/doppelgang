package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"code.linenisgreat.com/doppelgang/internal/alfa/lint"
)

// ndjsonRec decodes any record of the tap test-result NDJSON schema. Test
// and summary fields share one struct because their field names do not
// collide (only `type` overlaps); the discriminator is `type`.
type ndjsonRec struct {
	Type        string          `json:"type"`
	N           int             `json:"n"`
	Description string          `json:"description"`
	OK          bool            `json:"ok"`
	Directive   json.RawMessage `json:"directive"`
	Diagnostic  json.RawMessage `json:"diagnostic"`
	Output      json.RawMessage `json:"output"`
	Subtest     []ndjsonRec     `json:"subtest"`
	Line        int             `json:"line"`

	// plan record
	Count int `json:"count"`

	// summary record
	Passed      int             `json:"passed"`
	Failed      int             `json:"failed"`
	Total       int             `json:"total"`
	PlanCount   int             `json:"plan_count"`
	Bailed      bool            `json:"bailed"`
	Valid       bool            `json:"valid"`
	Diagnostics json.RawMessage `json:"diagnostics"`
}

func decodeNDJSON(t *testing.T, b []byte) []ndjsonRec {
	t.Helper()
	var recs []ndjsonRec
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r ndjsonRec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("invalid NDJSON line %q: %v", line, err)
		}
		recs = append(recs, r)
	}
	return recs
}

func TestLintNDJSONClean(t *testing.T) {
	var buf bytes.Buffer
	if err := LintNDJSON(&buf, LintSummary{}); err != nil {
		t.Fatalf("LintNDJSON: %v", err)
	}

	if !strings.Contains(buf.String(), `"subtest":null`) {
		t.Errorf("a clean check must emit subtest:null, not []; got:\n%s", buf.String())
	}

	recs := decodeNDJSON(t, buf.Bytes())
	if len(recs) != 5 {
		t.Fatalf("want 5 records (plan + 3 checks + summary), got %d:\n%s", len(recs), buf.String())
	}
	plan, follows, multi, dead, summary := recs[0], recs[1], recs[2], recs[3], recs[4]

	if plan.Type != "plan" || plan.Count != 3 {
		t.Errorf("first record must be plan with count 3, got %+v", plan)
	}
	if follows.Type != "test" || follows.N != 1 || follows.Description != "follows opportunities" {
		t.Errorf("unexpected follows header: %+v", follows)
	}
	if multi.Type != "test" || multi.N != 2 || multi.Description != "multi-version inputs" {
		t.Errorf("unexpected multi-version header: %+v", multi)
	}
	if dead.Type != "test" || dead.N != 3 || dead.Description != "dead follows overrides" {
		t.Errorf("unexpected dead-overrides header: %+v", dead)
	}
	if !follows.OK || !multi.OK || !dead.OK {
		t.Errorf("clean checks must be ok: follows.OK=%v multi.OK=%v dead.OK=%v", follows.OK, multi.OK, dead.OK)
	}
	for _, c := range []ndjsonRec{follows, multi, dead} {
		if len(c.Subtest) != 0 {
			t.Errorf("clean check %q must have no subtests, got %d", c.Description, len(c.Subtest))
		}
		if string(c.Directive) != "null" || string(c.Diagnostic) != "null" || string(c.Output) != "null" {
			t.Errorf("check %q: directive/diagnostic/output must be null, got %s/%s/%s",
				c.Description, c.Directive, c.Diagnostic, c.Output)
		}
	}

	if summary.Type != "summary" {
		t.Fatalf("final record must be summary, got %q", summary.Type)
	}
	if summary.Passed != 3 || summary.Failed != 0 || summary.Total != 3 || summary.PlanCount != 3 {
		t.Errorf("clean summary counts wrong: %+v", summary)
	}
	if summary.Bailed || !summary.Valid {
		t.Errorf("summary must be valid and not bailed: %+v", summary)
	}
	if string(summary.Diagnostics) != "[]" {
		t.Errorf("diagnostics must be an empty array, got %s", summary.Diagnostics)
	}
}

func TestLintNDJSONFindings(t *testing.T) {
	rep := lint.Report{
		Follows: []lint.FollowsRec{{
			Identity:  "NixOS/nixpkgs @ d233902",
			Canonical: "nixpkgs",
			NodeCount: 3,
			Lines:     []string{`inputs.nixpkgs.inputs.systems.follows = "nixpkgs"`},
		}},
		MultiVersion: []lint.MultiVersionInput{{
			Source: "NixOS/nixpkgs",
			Versions: []lint.InputVersion{
				{Rev: "abc1234def5678", Path: "nixpkgs"},
				{Rev: "fed4321cba8765", Path: "nixpkgs/nixpkgs-master"},
			},
		}},
	}

	var buf bytes.Buffer
	if err := LintNDJSON(&buf, LintSummary{Report: rep}); err != nil {
		t.Fatalf("LintNDJSON: %v", err)
	}
	recs := decodeNDJSON(t, buf.Bytes())
	if len(recs) != 5 {
		t.Fatalf("want 5 top-level records, got %d:\n%s", len(recs), buf.String())
	}
	plan, follows, multi, summary := recs[0], recs[1], recs[2], recs[4]

	if plan.Type != "plan" || plan.Count != 3 {
		t.Errorf("first record must be plan with count 3, got %+v", plan)
	}
	if follows.OK {
		t.Errorf("follows check with a finding must not be ok")
	}
	if len(follows.Subtest) != 1 {
		t.Fatalf("want 1 follows subtest, got %d", len(follows.Subtest))
	}
	fsub := follows.Subtest[0]
	if fsub.OK || fsub.N != 1 || fsub.Type != "test" {
		t.Errorf("unexpected follows subtest: %+v", fsub)
	}
	var fd struct {
		Identity  string   `json:"identity"`
		Canonical string   `json:"canonical"`
		NodeCount int      `json:"nodeCount"`
		Lines     []string `json:"lines"`
	}
	if err := json.Unmarshal(fsub.Diagnostic, &fd); err != nil {
		t.Fatalf("follows subtest diagnostic is not an object: %v", err)
	}
	if fd.Canonical != "nixpkgs" || fd.NodeCount != 3 || len(fd.Lines) != 1 {
		t.Errorf("follows diagnostic wrong: %+v", fd)
	}

	if multi.OK {
		t.Errorf("multi-version check with a finding must not be ok")
	}
	if len(multi.Subtest) != 1 {
		t.Fatalf("want 1 multi-version subtest, got %d", len(multi.Subtest))
	}
	var md struct {
		Source   string `json:"source"`
		Versions []struct {
			Rev  string `json:"rev"`
			Path string `json:"path"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(multi.Subtest[0].Diagnostic, &md); err != nil {
		t.Fatalf("multi-version subtest diagnostic is not an object: %v", err)
	}
	if md.Source != "NixOS/nixpkgs" || len(md.Versions) != 2 {
		t.Fatalf("multi-version diagnostic wrong: %+v", md)
	}
	// NDJSON preserves the full rev (text output shortens it).
	if md.Versions[0].Rev != "abc1234def5678" {
		t.Errorf("rev must not be shortened, got %q", md.Versions[0].Rev)
	}

	// follows + multi fail; the dead-overrides check is clean here, so it
	// passes — 1 passed, 2 failed, 3 total.
	if summary.Passed != 1 || summary.Failed != 2 || summary.Total != 3 || summary.PlanCount != 3 {
		t.Errorf("summary counts wrong with findings: %+v", summary)
	}
}

// TestLintDeadOverridesRendering covers the third finding category in text
// and NDJSON: a direct dead override is named with its target and missing
// input, and the NDJSON dead-overrides check carries a structured diagnostic.
func TestLintDeadOverridesRendering(t *testing.T) {
	rep := lint.Report{
		DeadOverrides: []lint.DeadOverride{{
			Override: `inputs.nebulous.inputs.chrest.follows`,
			Target:   "nebulous",
			Input:    "chrest",
			Direct:   true,
		}},
	}

	var text bytes.Buffer
	if err := LintText(&text, LintSummary{Report: rep}); err != nil {
		t.Fatalf("LintText: %v", err)
	}
	ts := text.String()
	if !strings.Contains(ts, "── dead follows overrides ──") {
		t.Errorf("missing dead-overrides section header:\n%s", ts)
	}
	if !strings.Contains(ts, `inputs.nebulous.inputs.chrest.follows: "nebulous" has no input "chrest" (direct)`) {
		t.Errorf("dead-override line missing/wrong:\n%s", ts)
	}

	var nd bytes.Buffer
	if err := LintNDJSON(&nd, LintSummary{Report: rep}); err != nil {
		t.Fatalf("LintNDJSON: %v", err)
	}
	recs := decodeNDJSON(t, nd.Bytes())
	if len(recs) != 5 {
		t.Fatalf("want 5 records, got %d:\n%s", len(recs), nd.String())
	}
	dead := recs[3]
	if dead.Description != "dead follows overrides" || dead.OK {
		t.Errorf("dead check with a finding must not be ok: %+v", dead)
	}
	if len(dead.Subtest) != 1 {
		t.Fatalf("want 1 dead subtest, got %d", len(dead.Subtest))
	}
	var dd struct {
		Override string `json:"override"`
		Target   string `json:"target"`
		Input    string `json:"input"`
		Direct   bool   `json:"direct"`
	}
	if err := json.Unmarshal(dead.Subtest[0].Diagnostic, &dd); err != nil {
		t.Fatalf("dead subtest diagnostic is not an object: %v", err)
	}
	if dd.Override != `inputs.nebulous.inputs.chrest.follows` || dd.Target != "nebulous" || dd.Input != "chrest" || !dd.Direct {
		t.Errorf("dead diagnostic wrong: %+v", dd)
	}

	// The summary must now count three checks, one of them failed.
	summary := recs[4]
	if summary.Total != 3 || summary.Failed != 1 || summary.Passed != 2 {
		t.Errorf("summary counts wrong: %+v", summary)
	}
}

// reportAllThree is a report with a finding in every check category, used
// to prove the --checks selection excludes a deselected check from both the
// count and the emitted records.
func reportAllThree() lint.Report {
	return lint.Report{
		Follows: []lint.FollowsRec{{
			Identity: "o/r", Canonical: "nixpkgs", NodeCount: 2,
			Lines: []string{`inputs.r.inputs.systems.follows = "nixpkgs"`},
		}},
		MultiVersion: []lint.MultiVersionInput{{
			Source: "o/r",
			Versions: []lint.InputVersion{
				{Rev: "aaa", Path: "r"}, {Rev: "bbb", Path: "r/master"},
			},
		}},
		DeadOverrides: []lint.DeadOverride{{
			Override: `inputs.r.inputs.gone.follows`, Target: "r", Input: "gone", Direct: true,
		}},
	}
}

// TestLintNDJSONSelectionPlanCount is the ndjson plan-count regression for
// #14: a follows+dead-overrides selection emits exactly those two test
// points (renumbered 1..2), a plan/summary count of 2, and no multi-version
// record — even though the report carries a multi-version finding.
func TestLintNDJSONSelectionPlanCount(t *testing.T) {
	sel, err := lint.ParseSelection("follows,dead-overrides")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	var buf bytes.Buffer
	if err := LintNDJSON(&buf, LintSummary{Report: reportAllThree(), Selection: sel}); err != nil {
		t.Fatalf("LintNDJSON: %v", err)
	}
	recs := decodeNDJSON(t, buf.Bytes())
	// plan + follows + dead + summary = 4 records (no multi-version).
	if len(recs) != 4 {
		t.Fatalf("want 4 records (plan + 2 checks + summary), got %d:\n%s", len(recs), buf.String())
	}
	plan, follows, dead, summary := recs[0], recs[1], recs[2], recs[3]
	if plan.Type != "plan" || plan.Count != 2 {
		t.Errorf("plan count must equal the 2 selected checks, got %+v", plan)
	}
	if follows.N != 1 || follows.Description != "follows opportunities" {
		t.Errorf("first selected check must be follows at N=1, got %+v", follows)
	}
	if dead.N != 2 || dead.Description != "dead follows overrides" {
		t.Errorf("second selected check must be dead-overrides renumbered to N=2, got %+v", dead)
	}
	for _, r := range recs {
		if r.Description == "multi-version inputs" {
			t.Errorf("deselected multi-version check must not be emitted:\n%s", buf.String())
		}
	}
	if summary.Type != "summary" || summary.Total != 2 || summary.PlanCount != 2 || summary.Failed != 2 {
		t.Errorf("summary must reflect 2 selected checks (both failing), got %+v", summary)
	}
}

// TestLintNixpkgsMasterRendering covers the opt-in nixpkgs-master check in
// all three formats: a non-conformant (floating) input is named with its
// url in text, carries a structured diagnostic in NDJSON, and reports
// conformant:false in JSON. The check must be explicitly selected (it is not
// a default check).
func TestLintNixpkgsMasterRendering(t *testing.T) {
	sel, err := lint.ParseSelection("nixpkgs-master")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	rep := lint.Report{
		NixpkgsMaster: &lint.NixpkgsMasterFinding{
			Status: lint.NixpkgsMasterFloating,
			URL:    "github:NixOS/nixpkgs",
		},
	}
	sum := LintSummary{Report: rep, Selection: sel}

	var text bytes.Buffer
	if err := LintText(&text, sum); err != nil {
		t.Fatalf("LintText: %v", err)
	}
	ts := text.String()
	if !strings.Contains(ts, "── nixpkgs-master convention ──") {
		t.Errorf("missing nixpkgs-master section header:\n%s", ts)
	}
	if !strings.Contains(ts, "nixpkgs-master floating") || !strings.Contains(ts, "github:NixOS/nixpkgs") {
		t.Errorf("floating diagnostic missing/wrong:\n%s", ts)
	}
	// The three default sections must NOT render (nixpkgs-master-only sel).
	if strings.Contains(ts, "follows opportunities") || strings.Contains(ts, "dead follows overrides") {
		t.Errorf("deselected default sections rendered:\n%s", ts)
	}

	var nd bytes.Buffer
	if err := LintNDJSON(&nd, sum); err != nil {
		t.Fatalf("LintNDJSON: %v", err)
	}
	recs := decodeNDJSON(t, nd.Bytes())
	// plan + 1 check + summary = 3 records.
	if len(recs) != 3 {
		t.Fatalf("want 3 records (plan + nixpkgs-master + summary), got %d:\n%s", len(recs), nd.String())
	}
	plan, check, summary := recs[0], recs[1], recs[2]
	if plan.Type != "plan" || plan.Count != 1 {
		t.Errorf("plan count must be 1 for a single selected check, got %+v", plan)
	}
	if check.Description != "nixpkgs-master convention" || check.OK {
		t.Errorf("nixpkgs-master check with a finding must not be ok: %+v", check)
	}
	if len(check.Subtest) != 1 {
		t.Fatalf("want 1 nixpkgs-master subtest, got %d", len(check.Subtest))
	}
	var nmd struct {
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(check.Subtest[0].Diagnostic, &nmd); err != nil {
		t.Fatalf("nixpkgs-master subtest diagnostic is not an object: %v", err)
	}
	if nmd.Status != "floating" || nmd.URL != "github:NixOS/nixpkgs" {
		t.Errorf("nixpkgs-master diagnostic wrong: %+v", nmd)
	}
	if summary.Total != 1 || summary.Failed != 1 || summary.PlanCount != 1 {
		t.Errorf("summary must reflect 1 selected failing check, got %+v", summary)
	}

	var js bytes.Buffer
	if err := LintJSON(&js, sum); err != nil {
		t.Fatalf("LintJSON: %v", err)
	}
	jsStr := js.String()
	if !strings.Contains(jsStr, `"nixpkgsMaster"`) || !strings.Contains(jsStr, `"conformant": false`) {
		t.Errorf("JSON must carry a non-conformant nixpkgsMaster object:\n%s", jsStr)
	}
	if strings.Contains(jsStr, "followsOpportunities") {
		t.Errorf("deselected default checks must be absent from JSON:\n%s", jsStr)
	}
}

// TestLintNixpkgsMasterConformantRendering confirms a conformant flake (nil
// finding) still renders a positive text line and an ok NDJSON check when the
// check is selected — so "checked, clean" is distinguishable from "not
// checked".
func TestLintNixpkgsMasterConformantRendering(t *testing.T) {
	sel, err := lint.ParseSelection("nixpkgs-master")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	sum := LintSummary{Report: lint.Report{}, Selection: sel}

	var text bytes.Buffer
	if err := LintText(&text, sum); err != nil {
		t.Fatalf("LintText: %v", err)
	}
	if !strings.Contains(text.String(), "(nixpkgs-master is pinned to github:NixOS/nixpkgs/") {
		t.Errorf("conformant nixpkgs-master must render a positive line:\n%s", text.String())
	}

	var nd bytes.Buffer
	if err := LintNDJSON(&nd, sum); err != nil {
		t.Fatalf("LintNDJSON: %v", err)
	}
	recs := decodeNDJSON(t, nd.Bytes())
	if len(recs) != 3 || !recs[1].OK || recs[2].Passed != 1 {
		t.Errorf("conformant check must be ok and pass:\n%s", nd.String())
	}
}

// TestLintJSONSelectionOmitsDeselected confirms the json format omits a
// deselected check's key entirely (so a consumer can tell "not checked"
// from "checked, clean") while keeping selected keys.
func TestLintJSONSelectionOmitsDeselected(t *testing.T) {
	sel, err := lint.ParseSelection("follows,dead-overrides")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	var buf bytes.Buffer
	if err := LintJSON(&buf, LintSummary{Report: reportAllThree(), Selection: sel}); err != nil {
		t.Fatalf("LintJSON: %v", err)
	}
	s := buf.String()
	if strings.Contains(s, "multiVersionInputs") {
		t.Errorf("deselected multi-version key must be absent:\n%s", s)
	}
	if !strings.Contains(s, "followsOpportunities") || !strings.Contains(s, "deadOverrides") {
		t.Errorf("selected keys must be present:\n%s", s)
	}
}

// TestLintTextSelectionOmitsDeselected confirms the text format renders only
// the selected check's section.
func TestLintTextSelectionOmitsDeselected(t *testing.T) {
	sel, err := lint.ParseSelection("follows")
	if err != nil {
		t.Fatalf("ParseSelection: %v", err)
	}
	var buf bytes.Buffer
	if err := LintText(&buf, LintSummary{Report: reportAllThree(), Selection: sel}); err != nil {
		t.Fatalf("LintText: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, "── follows opportunities ──") {
		t.Errorf("selected follows section missing:\n%s", s)
	}
	if strings.Contains(s, "multi-version inputs") || strings.Contains(s, "dead follows overrides") {
		t.Errorf("deselected sections must not render:\n%s", s)
	}
}
