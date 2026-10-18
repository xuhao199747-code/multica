package service

import (
	"fmt"
	"time"
)

// ReviewSummaryDue returns the local calendar date used to persist the daily
// delivery watermark. Persisting it atomically with dispatch prevents a second
// summary during a repeated daylight-saving hour or after a scheduler restart.
func ReviewSummaryDue(now time.Time, lastDate, schedule, timezone string) (bool, string, error) {
	parsed, err := time.Parse("15:04", schedule)
	if err != nil || parsed.Format("15:04") != schedule {
		return false, "", fmt.Errorf("invalid summary_time")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" {
		return false, "", fmt.Errorf("invalid timezone")
	}
	local := now.In(location)
	date := local.Format("2006-01-02")
	return date != lastDate && local.Format("15:04") >= schedule, date, nil
}
