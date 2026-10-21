package service

import "testing"

func TestParseProjectDeliveryModeDefaultsToNotifyOnly(t *testing.T) {
	mode, err := ParseProjectDeliveryMode("")
	if err != nil || mode != ProjectDeliveryNotifyOnly {
		t.Fatalf("empty delivery mode = (%q, %v), want (%q, nil)", mode, err, ProjectDeliveryNotifyOnly)
	}
}

func TestParseProjectDeliveryModeRejectsUnknownMode(t *testing.T) {
	if _, err := ParseProjectDeliveryMode("docker_everywhere"); err == nil {
		t.Fatal("unknown delivery mode must be rejected")
	}
}
