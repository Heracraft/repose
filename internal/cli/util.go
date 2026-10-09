package cli

import (
	"encoding/base64"
	"encoding/json"
	"io"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// writeJSONOut is what every read command's --json flag uses: one JSON
// value, nothing else on stdout (07-cli.md checklist: "repose status
// --json | jq . in CI").
func writeJSONOut(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeJSONLine writes v as one compact line: what a stream's --json
// prints for each item (`logs`, `events`, `status -f`), so `while read`
// and `jq -c` take one item per line (NDJSON, DECISIONS I-609).
func writeJSONLine(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// printJSON is writeJSONOut, or writeJSONLine when the command prints a
// value per refresh.
func (e *Env) printJSON(v any) error {
	if e.jsonLines {
		return writeJSONLine(e.Out, v)
	}
	return writeJSONOut(e.Out, v)
}
