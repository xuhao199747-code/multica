package service

import "testing"

func TestProjectExecutionTransitionRequiresFreshDeliveryBeforeReview(t *testing.T) {
	next, rounds, err := AdvanceProjectExecution(ExecutionDevelopment, 0, ExecutionEventDeliverySubmitted)
	if err != nil {
		t.Fatalf("submit delivery: %v", err)
	}
	if next != ExecutionReview || rounds != 0 {
		t.Fatalf("submit delivery = (%q, %d), want (%q, 0)", next, rounds, ExecutionReview)
	}

	if _, _, err := AdvanceProjectExecution(ExecutionReview, 0, ExecutionEventDeliverySubmitted); err == nil {
		t.Fatal("review must not enqueue another review from the same delivery")
	}
}

func TestProjectExecutionTransitionStopsAfterTwoRepairRounds(t *testing.T) {
	next, rounds, err := AdvanceProjectExecution(ExecutionReview, 0, ExecutionEventReviewNeedsChanges)
	if err != nil {
		t.Fatalf("first review result: %v", err)
	}
	if next != ExecutionRepair || rounds != 1 {
		t.Fatalf("first review result = (%q, %d), want (%q, 1)", next, rounds, ExecutionRepair)
	}

	next, rounds, err = AdvanceProjectExecution(ExecutionRepair, rounds, ExecutionEventDeliverySubmitted)
	if err != nil || next != ExecutionReview || rounds != 1 {
		t.Fatalf("first repair delivery = (%q, %d, %v), want (%q, 1, nil)", next, rounds, err, ExecutionReview)
	}
	next, rounds, err = AdvanceProjectExecution(next, rounds, ExecutionEventReviewNeedsChanges)
	if err != nil || next != ExecutionRepair || rounds != 2 {
		t.Fatalf("second review result = (%q, %d, %v), want (%q, 2, nil)", next, rounds, err, ExecutionRepair)
	}

	next, rounds, err = AdvanceProjectExecution(next, rounds, ExecutionEventDeliverySubmitted)
	if err != nil || next != ExecutionReview || rounds != 2 {
		t.Fatalf("second repair delivery = (%q, %d, %v), want (%q, 2, nil)", next, rounds, err, ExecutionReview)
	}
	next, rounds, err = AdvanceProjectExecution(next, rounds, ExecutionEventReviewNeedsChanges)
	if err != nil {
		t.Fatalf("third review result: %v", err)
	}
	if next != ExecutionNeedsMemberDecision || rounds != 2 {
		t.Fatalf("third review result = (%q, %d), want (%q, 2)", next, rounds, ExecutionNeedsMemberDecision)
	}
}
