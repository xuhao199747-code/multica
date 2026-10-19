package service

import "testing"

func TestParseManualReviewReport(t *testing.T) {
	const id = "01900000-0000-7000-8000-000000000001"
	tests := []struct {
		name, input string
		valid       bool
	}{
		{"posted report", `Report <!-- multica-review {"reviews":[{"issue_id":"` + id + `","verdict":"needs_changes","findings":"Fix test"}]} -->`, true},
		{"empty", `{"reviews":[]}`, true},
		{"prose", `Please fix this`, false},
		{"unfinished marker", `<!-- multica-review {"reviews":[]}`, false},
		{"invalid id", `{"reviews":[{"issue_id":"bad","verdict":"needs_changes","findings":"fix"}]}`, false},
		{"no findings", `{"reviews":[{"issue_id":"` + id + `","verdict":"needs_changes","findings":""}]}`, false},
		{"pass", `{"reviews":[{"issue_id":"` + id + `","verdict":"pass","findings":""}]}`, false},
		{"trailing json", `{"reviews":[]} {}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseManualReviewReport(tc.input)
			if ok != tc.valid {
				t.Fatalf("valid = %v", ok)
			}
		})
	}
}
