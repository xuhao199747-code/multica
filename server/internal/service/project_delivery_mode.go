package service

import "fmt"

// ProjectDeliveryMode is selected per project. No deployment behavior is
// implied by enabling Mica collaboration.
type ProjectDeliveryMode string

const (
	ProjectDeliveryNotifyOnly       ProjectDeliveryMode = "notify_only"
	ProjectDeliveryPullRequest      ProjectDeliveryMode = "pull_request"
	ProjectDeliveryManualRelease    ProjectDeliveryMode = "manual_release"
	ProjectDeliveryAutomatedRelease ProjectDeliveryMode = "automated_release"
)

func ParseProjectDeliveryMode(value string) (ProjectDeliveryMode, error) {
	if value == "" {
		return ProjectDeliveryNotifyOnly, nil
	}
	mode := ProjectDeliveryMode(value)
	switch mode {
	case ProjectDeliveryNotifyOnly, ProjectDeliveryPullRequest, ProjectDeliveryManualRelease, ProjectDeliveryAutomatedRelease:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown project delivery mode %q", value)
	}
}
