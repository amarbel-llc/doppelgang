package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/friedenberg/doppelgang/internal/alfa/lint"
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
	if len(recs) != 4 {
		t.Fatalf("want 4 records (plan + 2 checks + summary), got %d:\n%s", len(recs), buf.String())
	}
	plan, follows, multi, summary := recs[0], recs[1], recs[2], recs[3]

	if plan.Type != "plan" || plan.Count != 2 {
		t.Errorf("first record must be plan with count 2, got %+v", plan)
	}
	if follows.Type != "test" || follows.N != 1 || follows.Description != "follows opportunities" {
		t.Errorf("unexpected follows header: %+v", follows)
	}
	if multi.Type != "test" || multi.N != 2 || multi.Description != "multi-version inputs" {
		t.Errorf("unexpected multi-version header: %+v", multi)
	}
	if !follows.OK || !multi.OK {
		t.Errorf("clean checks must be ok: follows.OK=%v multi.OK=%v", follows.OK, multi.OK)
	}
	for _, c := range []ndjsonRec{follows, multi} {
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
	if summary.Passed != 2 || summary.Failed != 0 || summary.Total != 2 || summary.PlanCount != 2 {
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
	if len(recs) != 4 {
		t.Fatalf("want 4 top-level records, got %d:\n%s", len(recs), buf.String())
	}
	plan, follows, multi, summary := recs[0], recs[1], recs[2], recs[3]

	if plan.Type != "plan" || plan.Count != 2 {
		t.Errorf("first record must be plan with count 2, got %+v", plan)
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

	if summary.Passed != 0 || summary.Failed != 2 || summary.Total != 2 {
		t.Errorf("summary counts wrong with findings: %+v", summary)
	}
}
