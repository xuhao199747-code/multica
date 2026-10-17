package service

import (
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

// ReviewAgentConfig describes delivery monitoring independently of system-agent identity.
type ReviewAgentConfig struct {
	RecipientID    string `json:"recipient_id"`
	Scope          string `json:"scope"`
	InstantEnabled bool   `json:"instant_enabled"`
	SummaryEnabled bool   `json:"summary_enabled"`
	SummaryTime    string `json:"summary_time"`
	Timezone       string `json:"timezone"`
}

func (c ReviewAgentConfig) Validate() error {
	if _, err := util.ParseUUID(c.RecipientID); err != nil {
		return fmt.Errorf("invalid recipient_id")
	}
	if c.Scope != "workspace" {
		return fmt.Errorf("invalid scope")
	}
	if c.SummaryEnabled {
		parsed, err := time.Parse("15:04", c.SummaryTime)
		if err != nil || parsed.Format("15:04") != c.SummaryTime {
			return fmt.Errorf("invalid summary_time")
		}
		if c.Timezone == "" {
			return fmt.Errorf("invalid timezone")
		}
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return fmt.Errorf("invalid timezone")
		}
	}
	return nil
}
