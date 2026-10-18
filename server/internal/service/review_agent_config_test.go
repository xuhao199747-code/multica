package service

import "testing"

func TestReviewAgentConfigValidation(t *testing.T) {
	valid := ReviewAgentConfig{
		RecipientID:    "2b8d7763-a611-43f6-b354-d978a8193319",
		Scope:          "workspace",
		InstantEnabled: true,
		SummaryEnabled: true,
		SummaryTime:    "09:30",
		Timezone:       "Asia/Shanghai",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	tests := map[string]func(*ReviewAgentConfig){
		"invalid recipient": func(c *ReviewAgentConfig) { c.RecipientID = "not-a-uuid" },
		"unknown scope":     func(c *ReviewAgentConfig) { c.Scope = "all" },
		"invalid time":      func(c *ReviewAgentConfig) { c.SummaryTime = "25:00" },
		"invalid timezone":  func(c *ReviewAgentConfig) { c.Timezone = "not/a-zone" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			change(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	project := valid
	project.Scope = "projects"
	project.ProjectIDs = []string{"2b8d7763-a611-43f6-b354-d978a8193319"}
	if err := project.Validate(); err != nil {
		t.Fatalf("valid project scope: %v", err)
	}
	project.ProjectIDs = nil
	if err := project.Validate(); err == nil {
		t.Fatal("project scope without projects must fail")
	}
}
