package render

import (
	"encoding/json"
	"io"

	"code.linenisgreat.com/doppelgang/internal/alfa/dupes"
)

type jsonGroup struct {
	Name    string     `json:"name"`
	Count   int        `json:"count"`
	PerCopy int64      `json:"perCopySize"`
	Wasted  int64      `json:"wastedBytes"`
	Copies  []jsonCopy `json:"copies"`
}

type jsonCopy struct {
	Path    string   `json:"path"`
	Size    int64    `json:"size"`
	Parents []string `json:"parents,omitempty"`
	Owners  []string `json:"owners,omitempty"`
}

type jsonDriftGroup struct {
	Pname      string             `json:"pname"`
	TotalBytes int64              `json:"totalBytes"`
	Versions   []jsonDriftVersion `json:"versions"`
}

type jsonDriftVersion struct {
	Version     string   `json:"version"`
	Count       int      `json:"count"`
	Size        int64    `json:"size"`
	IsExactDupe bool     `json:"isExactDupe"`
	Parents     []string `json:"parents,omitempty"`
	Owners      []string `json:"owners,omitempty"`
}

// JSON writes the summary as a single indented JSON document. When
// Summary.Owners is non-nil, each copy carries owners; otherwise it
// carries parents. When Summary.Drift is non-nil, a top-level
// "versionDrift" array is emitted alongside the duplicates list.
func JSON(w io.Writer, s Summary) error {
	out := struct {
		Scope        string           `json:"scope"`
		TotalPaths   int              `json:"totalPaths"`
		TotalBytes   int64            `json:"totalBytes"`
		Duplicates   []jsonGroup      `json:"duplicates"`
		VersionDrift []jsonDriftGroup `json:"versionDrift,omitempty"`
	}{
		Scope:      s.Scope,
		TotalPaths: s.TotalPaths,
		TotalBytes: s.TotalBytes,
		Duplicates: make([]jsonGroup, 0, len(s.Groups)),
	}
	for _, gr := range s.Groups {
		jg := jsonGroup{
			Name:    gr.Name,
			Count:   len(gr.Copies),
			PerCopy: gr.Copies[0].Size,
			Wasted:  gr.Wasted,
			Copies:  make([]jsonCopy, 0, len(gr.Copies)),
		}
		for _, c := range gr.Copies {
			jc := jsonCopy{Path: c.Path, Size: c.Size}
			if s.Owners != nil {
				jc.Owners = s.Owners[c.Path]
			} else {
				jc.Parents = c.Parents
			}
			jg.Copies = append(jg.Copies, jc)
		}
		out.Duplicates = append(out.Duplicates, jg)
	}
	out.VersionDrift = driftToJSON(s.Drift)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// driftToJSON converts a slice of drift groups into their JSON shape.
// Returns nil when drift is nil so the enclosing field stays omitted.
func driftToJSON(drift []dupes.DriftGroup) []jsonDriftGroup {
	if drift == nil {
		return nil
	}
	out := make([]jsonDriftGroup, 0, len(drift))
	for _, dg := range drift {
		jdg := jsonDriftGroup{
			Pname:      dg.Pname,
			TotalBytes: dg.TotalBytes,
			Versions:   make([]jsonDriftVersion, 0, len(dg.Versions)),
		}
		for _, v := range dg.Versions {
			jdg.Versions = append(jdg.Versions, jsonDriftVersion{
				Version:     v.Version,
				Count:       v.Count,
				Size:        v.Size,
				IsExactDupe: v.IsExactDupe,
				Parents:     v.Parents,
				Owners:      v.Owners,
			})
		}
		out = append(out, jdg)
	}
	return out
}
