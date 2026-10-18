package service

import (
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

// ReviewAgentConfig defines monitoring independently of an agent's system identity.
type ReviewAgentConfig struct {
	RecipientID    string   `json:"recipient_id"`
	Scope          string   `json:"scope"`
	ProjectIDs     []string `json:"project_ids,omitempty"`
	InstantEnabled bool     `json:"instant_enabled"`
	SummaryEnabled bool     `json:"summary_enabled"`
	SummaryTime    string   `json:"summary_time"`
	Timezone       string   `json:"timezone"`
}

func (c ReviewAgentConfig) Validate() error {
	if _, err := util.ParseUUID(c.RecipientID); err != nil {
		return fmt.Errorf("invalid recipient_id")
	}
	if c.Scope != "workspace" && c.Scope != "projects" {
		return fmt.Errorf("invalid scope")
	}
	if c.Scope == "workspace" && len(c.ProjectIDs) != 0 {
		return fmt.Errorf("workspace scope cannot include project_ids")
	}
	if c.Scope == "projects" && (len(c.ProjectIDs) == 0 || len(c.ProjectIDs) > 100) {
		return fmt.Errorf("project scope requires 1 to 100 projects")
	}
	seen := make(map[string]struct{}, len(c.ProjectIDs))
	for _, id := range c.ProjectIDs {
		parsed, err := util.ParseUUID(id)
		if err != nil {
			return fmt.Errorf("invalid project_id")
		}
		canonical := util.UUIDToString(parsed)
		if _, exists := seen[canonical]; exists {
			return fmt.Errorf("duplicate project_id")
		}
		seen[canonical] = struct{}{}
	}
	if c.SummaryEnabled {
		parsed, err := time.Parse("15:04", c.SummaryTime)
		if err != nil || parsed.Format("15:04") != c.SummaryTime {
			return fmt.Errorf("invalid summary_time")
		}
		if _, err := time.LoadLocation(c.Timezone); c.Timezone == "" || err != nil {
			return fmt.Errorf("invalid timezone")
		}
	}
	return nil
}
