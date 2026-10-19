package service

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type reviewRepairVerdict struct {
	Verdict  string `json:"verdict"`
	Findings string `json:"findings"`
}

// parseReviewRepairVerdict accepts only one explicit machine-readable result.
// An ambiguous or incomplete response must never trigger a repair.
func parseReviewRepairVerdict(output string) (reviewRepairVerdict, bool) {
	var result reviewRepairVerdict
	if len(output) > 64*1024 {
		return result, false
	}
	keys := json.NewDecoder(bytes.NewBufferString(output))
	first, err := keys.Token()
	if err != nil || first != json.Delim('{') {
		return result, false
	}
	seen := map[string]bool{}
	for keys.More() {
		token, err := keys.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return result, false
		}
		seen[key] = true
		var value json.RawMessage
		if keys.Decode(&value) != nil {
			return result, false
		}
	}
	if _, err := keys.Token(); err != nil {
		return result, false
	}
	decoder := json.NewDecoder(bytes.NewBufferString(output))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil {
		return reviewRepairVerdict{}, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return reviewRepairVerdict{}, false
	}
	result.Findings = strings.TrimSpace(result.Findings)
	switch result.Verdict {
	case "pass":
	case "needs_changes", "needs_decision":
		if result.Findings == "" {
			return reviewRepairVerdict{}, false
		}
	default:
		return reviewRepairVerdict{}, false
	}
	return result, true
}
