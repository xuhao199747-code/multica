package service

import "fmt"

// ProjectExecutionState is the durable state of one member-confirmed project
// delivery. It deliberately lives apart from issue status: one record may own
// several development and review issues over its lifetime.
type ProjectExecutionState string

const (
	ExecutionDevelopment         ProjectExecutionState = "development"
	ExecutionReview              ProjectExecutionState = "review"
	ExecutionRepair              ProjectExecutionState = "repair"
	ExecutionDelivering          ProjectExecutionState = "delivering"
	ExecutionNeedsMemberDecision ProjectExecutionState = "needs_member_decision"
)

// ProjectExecutionEvent is the only input that advances a project delivery.
// A state observation alone must never enqueue another agent task.
type ProjectExecutionEvent string

const (
	ExecutionEventDeliverySubmitted         ProjectExecutionEvent = "delivery_submitted"
	ExecutionEventReviewNeedsChanges        ProjectExecutionEvent = "review_needs_changes"
	ExecutionEventReviewPassed              ProjectExecutionEvent = "review_passed"
	ExecutionEventReviewNeedsMemberDecision ProjectExecutionEvent = "review_needs_member_decision"
)

// AdvanceProjectExecution applies the bounded repair/re-review rule. A
// delivery may enter review only from development or repair, and a third
// needs-changes result stops automation for a member decision.
func AdvanceProjectExecution(
	state ProjectExecutionState,
	reviewRounds int,
	event ProjectExecutionEvent,
) (ProjectExecutionState, int, error) {
	if reviewRounds < 0 {
		return "", reviewRounds, fmt.Errorf("review rounds must not be negative")
	}

	switch event {
	case ExecutionEventDeliverySubmitted:
		switch state {
		case ExecutionDevelopment, ExecutionRepair:
			return ExecutionReview, reviewRounds, nil
		default:
			return "", reviewRounds, fmt.Errorf("delivery cannot be submitted from %q", state)
		}
	case ExecutionEventReviewNeedsChanges:
		if state != ExecutionReview {
			return "", reviewRounds, fmt.Errorf("review result cannot be recorded from %q", state)
		}
		if reviewRounds >= 2 {
			return ExecutionNeedsMemberDecision, reviewRounds, nil
		}
		return ExecutionRepair, reviewRounds + 1, nil
	case ExecutionEventReviewPassed:
		if state != ExecutionReview {
			return "", reviewRounds, fmt.Errorf("review result cannot be recorded from %q", state)
		}
		return ExecutionDelivering, reviewRounds, nil
	case ExecutionEventReviewNeedsMemberDecision:
		if state != ExecutionReview {
			return "", reviewRounds, fmt.Errorf("review result cannot be recorded from %q", state)
		}
		return ExecutionNeedsMemberDecision, reviewRounds, nil
	default:
		return "", reviewRounds, fmt.Errorf("unknown project execution event %q", event)
	}
}
