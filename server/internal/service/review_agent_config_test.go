package service

import "testing"

func TestReviewAgentConfigValidation(t *testing.T) {
	valid := ReviewAgentConfig{RecipientID: "00000000-0000-0000-0000-000000000001", Scope: "workspace", InstantEnabled: true, SummaryEnabled: true, SummaryTime: "09:00", Timezone: "Asia/Shanghai"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for name, mutate := range map[string]func(*ReviewAgentConfig){
		"recipient": func(c *ReviewAgentConfig) { c.RecipientID = "invalid" },
		"scope":     func(c *ReviewAgentConfig) { c.Scope = "other" },
		"time":      func(c *ReviewAgentConfig) { c.SummaryTime = "25:00" },
		"timezone":  func(c *ReviewAgentConfig) { c.Timezone = "not/a-zone" },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
