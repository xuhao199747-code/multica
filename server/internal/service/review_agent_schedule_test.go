package service

import (
	"testing"
	"time"
)

func TestReviewSummaryDue(t *testing.T) {
	cases := []struct {
		name, now, lastDate, timezone, schedule string
		want                                    bool
	}{
		{"before schedule", "2026-10-18T00:59:00Z", "", "Asia/Shanghai", "09:00", false},
		{"at schedule", "2026-10-18T01:00:00Z", "", "Asia/Shanghai", "09:00", true},
		{"already sent today", "2026-10-18T03:00:00Z", "2026-10-18", "Asia/Shanghai", "09:00", false},
		{"yesterday sent", "2026-10-18T03:00:00Z", "2026-10-17", "Asia/Shanghai", "09:00", true},
		{"spring clock skips scheduled minute", "2026-03-08T07:00:00Z", "2026-03-07", "America/New_York", "02:30", true},
		{"repeated hour sent once", "2026-11-01T06:30:00Z", "2026-11-01", "America/New_York", "01:30", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			got, date, err := ReviewSummaryDue(now, tc.lastDate, tc.schedule, tc.timezone)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("due = %v, local date = %s; want %v", got, date, tc.want)
			}
		})
	}
}

func TestReviewSummaryDueRejectsInvalidSchedule(t *testing.T) {
	for _, tc := range []struct{ schedule, zone string }{{"25:00", "UTC"}, {"09:00", "not/a-zone"}} {
		if _, _, err := ReviewSummaryDue(time.Now(), "", tc.schedule, tc.zone); err == nil {
			t.Fatalf("expected error for %q in %q", tc.schedule, tc.zone)
		}
	}
}
