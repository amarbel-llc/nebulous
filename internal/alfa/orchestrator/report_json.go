package orchestrator

import (
	"encoding/json"
	"io"

	"github.com/friedenberg/nebulous/internal/0/archive"
)

// WriteJSONReport emits a single JSON object describing the Report,
// pretty-printed with two-space indent. Intended for non-TTY
// stdout, where a tool like `jq` is the likely consumer. Unlike
// Args.StreamTAP, this is an atomic end-of-run emission — the whole
// report is buffered into a single JSON document and flushed in one
// Write call.
//
// Shape:
//
//	{
//	  "written":    [ {"policy_id": "...", "subject": "...", "path": "..."}, ... ],
//	  "skipped":    [ {"policy_id": "...", "subject": "...", "path": "...", "last_captured_at": "..."}, ... ],
//	  "failed":     [ {"policy_id": "...", "subject": "...", "kind": "...", "message": "..."}, ... ],
//	  "bailed_out": false
//	}
//
// A terminal `\n` is appended (json.Encoder convention).
func WriteJSONReport(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(reportJSON{
		Written:   toJSONJobs(r.Written),
		Skipped:   toJSONSkips(r.Skipped),
		Failed:    toJSONFailures(r.Failed),
		BailedOut: r.BailedOut,
	})
}

// reportJSON / jsonJob / jsonFailure / jsonSkip are the lowercased-
// field shapes emitted on the wire. Defining them explicitly avoids
// leaking Go field names and lets us evolve the internal types
// without breaking the external JSON surface.
type reportJSON struct {
	Written   []jsonJob     `json:"written"`
	Skipped   []jsonSkip    `json:"skipped"`
	Failed    []jsonFailure `json:"failed"`
	BailedOut bool          `json:"bailed_out"`
}

type jsonJob struct {
	PolicyID string `json:"policy_id"`
	Subject  string `json:"subject"`
	Path     string `json:"path"`
}

type jsonFailure struct {
	PolicyID string `json:"policy_id"`
	Subject  string `json:"subject"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
}

// jsonSkip serializes a Skip with last_captured_at in the same
// RFC3339-ms-UTC format as Record.CapturedAt, so consumers that
// already parse archive records can reuse their timestamp handling.
type jsonSkip struct {
	PolicyID       string `json:"policy_id"`
	Subject        string `json:"subject"`
	Path           string `json:"path"`
	LastCapturedAt string `json:"last_captured_at"`
}

func toJSONJobs(in []Job) []jsonJob {
	out := make([]jsonJob, 0, len(in))
	for _, j := range in {
		out = append(out, jsonJob{PolicyID: j.PolicyID, Subject: j.Subject, Path: j.Path})
	}
	return out
}

func toJSONFailures(in []JobFailure) []jsonFailure {
	out := make([]jsonFailure, 0, len(in))
	for _, f := range in {
		out = append(out, jsonFailure{
			PolicyID: f.PolicyID, Subject: f.Subject,
			Kind: f.Kind, Message: f.Message,
		})
	}
	return out
}

func toJSONSkips(in []Skip) []jsonSkip {
	out := make([]jsonSkip, 0, len(in))
	for _, s := range in {
		out = append(out, jsonSkip{
			PolicyID:       s.PolicyID,
			Subject:        s.Subject,
			Path:           s.Path,
			LastCapturedAt: archive.FormatTimestamp(s.LastCapturedAt),
		})
	}
	return out
}
