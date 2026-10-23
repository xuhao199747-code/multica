package service

type UpdatePhase string

const (
	PhaseQueued         UpdatePhase = "queued"
	PhasePreparing      UpdatePhase = "preparing"
	PhaseAwaitingReview UpdatePhase = "awaiting_review"
	PhaseRepairing      UpdatePhase = "repairing"
	PhasePushing        UpdatePhase = "pushing"
	PhaseBackingUp      UpdatePhase = "backing_up"
	PhaseDeploying      UpdatePhase = "deploying"
	PhaseVerifying      UpdatePhase = "verifying"
	PhaseSucceeded      UpdatePhase = "succeeded"
	PhaseFailed         UpdatePhase = "failed"
	PhaseNeedsDecision  UpdatePhase = "needs_decision"
)

// CanAdvanceUpdate validates a transition using the job's current counters.
// Entering repairing consumes one repair; repairing -> preparing consumes one retry.
func CanAdvanceUpdate(from, to UpdatePhase, repairCount, retryCount int) bool {
	if repairCount < 0 || repairCount > 1 || retryCount < 0 || retryCount > 1 {
		return false
	}
	switch from {
	case PhaseQueued, PhasePreparing, PhaseAwaitingReview, PhaseRepairing, PhasePushing, PhaseBackingUp, PhaseDeploying, PhaseVerifying:
	default:
		return false
	}
	if to == PhaseFailed || to == PhaseNeedsDecision {
		return true
	}
	switch from {
	case PhaseQueued:
		return to == PhasePreparing
	case PhasePreparing:
		return to == PhaseAwaitingReview || (to == PhaseRepairing && repairCount == 0)
	case PhaseAwaitingReview:
		return to == PhasePushing || (to == PhaseRepairing && repairCount == 0)
	case PhaseRepairing:
		return to == PhasePreparing && repairCount == 1 && retryCount == 0
	case PhasePushing:
		return to == PhaseBackingUp
	case PhaseBackingUp:
		return to == PhaseDeploying
	case PhaseDeploying:
		return to == PhaseVerifying
	case PhaseVerifying:
		return to == PhaseSucceeded
	}
	return false
}
