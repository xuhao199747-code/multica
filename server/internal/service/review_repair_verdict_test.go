package service

import "testing"

func TestParseReviewRepairVerdict(t *testing.T) {
	cases := []struct {
		name, output, verdict string
		ok                    bool
	}{
		{"actionable", `{"verdict":"needs_changes","findings":"Fix the missing test"}`, "needs_changes", true},
		{"passed", `{"verdict":"pass"}`, "pass", true},
		{"decision", `{"verdict":"needs_decision","findings":"Need product direction"}`, "needs_decision", true},
		{"no findings", `{"verdict":"needs_changes"}`, "", false},
		{"unknown", `{"verdict":"maybe"}`, "", false},
		{"free text", `looks good`, "", false},
		{"duplicate field", `{"verdict":"pass","verdict":"needs_changes","findings":"x"}`, "", false},
		{"multiple documents", `{"verdict":"pass"} {"verdict":"needs_changes","findings":"x"}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseReviewRepairVerdict(tc.output)
			if ok != tc.ok || (ok && got.Verdict != tc.verdict) {
				t.Fatalf("got %#v, %v", got, ok)
			}
		})
	}
}
