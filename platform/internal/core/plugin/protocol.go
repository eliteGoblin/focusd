package plugin

import "encoding/json"

// JobInput is the resolved job configuration the platform writes to a
// temp JSON file and passes to the plugin via `--config <path>`.
// Plugins read this; keeping it typed documents the contract.
type JobInput struct {
	JobID    string         `json:"job_id"`
	PluginID string         `json:"plugin_id"`
	Config   map[string]any `json:"config"`
}

// Result is the structured JSON a job plugin writes to stdout. Exit code
// remains the source of truth for success/failure (see spec §Exit code
// convention); Result carries human/diagnostic detail.
type Result struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// ParseResult decodes a plugin's stdout. Lenient: trailing whitespace or
// an empty body is tolerated (empty => zero Result, caller decides based
// on exit code).
func ParseResult(stdout []byte) (Result, error) {
	var r Result
	trimmed := trimSpace(stdout)
	if len(trimmed) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(trimmed, &r); err != nil {
		return r, err
	}
	return r, nil
}

func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && isSpace(b[i]) {
		i++
	}
	for j > i && isSpace(b[j-1]) {
		j--
	}
	return b[i:j]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
