package scheduler

import (
	"context"
	"time"
)

type ReviewAgentDispatcher interface {
	Tick(context.Context) error
	TickSummaries(context.Context) error
}

// ReviewAgentDispatchJob claims pending review events and dispatches review
// runs through the same durable lease as the other dispatch jobs. Events
// persist until the dispatch commits, so a missed bucket cannot drop work.
func ReviewAgentDispatchJob(dispatcher ReviewAgentDispatcher) JobSpec {
	return JobSpec{
		Name: "review_agent_dispatch", Cadence: 30 * time.Second, CatchUpMode: CatchUpLatestOnly, CatchUpWindow: time.Hour,
		RunTimeout: 45 * time.Second, StaleTimeout: time.Minute, HeartbeatInterval: 10 * time.Second,
		AllowStaleReentry: true, MaxAttempts: 1, Scopes: StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			return HandlerResult{}, dispatcher.Tick(ctx)
		},
	}
}

// ReviewAgentSummaryJob ticks the daily-summary watermark pass. CatchUpLatestOnly
// is safe because the watermark itself decides due-ness: a restart after the
// scheduled local time still sees date != lastDate and fires once.
func ReviewAgentSummaryJob(dispatcher ReviewAgentDispatcher) JobSpec {
	return JobSpec{
		Name: "review_agent_summary", Cadence: time.Minute, CatchUpMode: CatchUpLatestOnly, CatchUpWindow: 2 * time.Hour,
		RunTimeout: 45 * time.Second, StaleTimeout: time.Minute, HeartbeatInterval: 10 * time.Second,
		AllowStaleReentry: true, MaxAttempts: 1, Scopes: StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			return HandlerResult{}, dispatcher.TickSummaries(ctx)
		},
	}
}
