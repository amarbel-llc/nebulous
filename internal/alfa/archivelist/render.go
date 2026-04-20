package archivelist

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// WriteJSONL writes one Summary per line as compact JSON, newline
// terminated. Intended for non-TTY consumers (jq, shell loops).
func WriteJSONL(w io.Writer, summaries []Summary) error {
	enc := json.NewEncoder(w)
	for _, s := range summaries {
		if err := enc.Encode(s); err != nil {
			return fmt.Errorf("archivelist: encode summary: %w", err)
		}
	}
	return nil
}

// WriteTable renders summaries as a tab-aligned columnar table for
// interactive terminals. Columns: SUBJECT, POLICY_ID, CAPTURED_AT,
// CAPTURES (N/M). Path is deliberately omitted — scripts that want
// the record path should use WriteJSONL.
func WriteTable(w io.Writer, summaries []Summary) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "SUBJECT\tPOLICY_ID\tCAPTURED_AT\tCAPTURES"); err != nil {
		return err
	}
	for _, s := range summaries {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%d/%d\n",
			s.Subject, s.PolicyID, s.CapturedAt, s.CapturesOK, s.CapturesTotal); err != nil {
			return err
		}
	}
	return tw.Flush()
}
