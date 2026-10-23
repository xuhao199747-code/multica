package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCanAdvanceUpdate(t *testing.T) {
	path := []UpdatePhase{PhaseQueued, PhasePreparing, PhaseAwaitingReview, PhasePushing, PhaseBackingUp, PhaseDeploying, PhaseVerifying, PhaseSucceeded}
	for i := 1; i < len(path); i++ {
		if !CanAdvanceUpdate(path[i-1], path[i], 0, 0) {
			t.Errorf("rejected %s -> %s", path[i-1], path[i])
		}
	}
	for _, from := range []UpdatePhase{PhasePreparing, PhaseAwaitingReview} {
		if !CanAdvanceUpdate(from, PhaseRepairing, 0, 0) {
			t.Errorf("rejected first repair from %s", from)
		}
	}
	if !CanAdvanceUpdate(PhaseRepairing, PhasePreparing, 1, 0) {
		t.Error("rejected retry after first repair")
	}
	if CanAdvanceUpdate(PhasePreparing, PhaseRepairing, 1, 0) || CanAdvanceUpdate(PhaseAwaitingReview, PhaseRepairing, 1, 1) {
		t.Error("accepted second repair")
	}
	if CanAdvanceUpdate(PhaseRepairing, PhasePreparing, 1, 1) {
		t.Error("accepted second retry")
	}
	for _, from := range []UpdatePhase{PhaseQueued, PhasePreparing, PhaseAwaitingReview, PhaseRepairing, PhasePushing, PhaseBackingUp, PhaseDeploying, PhaseVerifying} {
		for _, to := range []UpdatePhase{PhaseFailed, PhaseNeedsDecision} {
			if !CanAdvanceUpdate(from, to, 0, 0) {
				t.Errorf("rejected stop %s -> %s", from, to)
			}
		}
	}
	for _, from := range []UpdatePhase{PhaseSucceeded, PhaseFailed, PhaseNeedsDecision} {
		for _, to := range append(path, PhaseRepairing, PhaseFailed, PhaseNeedsDecision) {
			if CanAdvanceUpdate(from, to, 0, 0) {
				t.Errorf("terminal transition %s -> %s", from, to)
			}
		}
	}
	for _, counts := range [][2]int{{2, 0}, {0, 2}, {-1, 0}, {0, -1}} {
		if CanAdvanceUpdate(PhasePreparing, PhaseRepairing, counts[0], counts[1]) || CanAdvanceUpdate(PhaseRepairing, PhasePreparing, counts[0], counts[1]) {
			t.Errorf("accepted invalid bounds %v", counts)
		}
	}
	for _, pair := range [][2]UpdatePhase{{PhaseQueued, PhaseSucceeded}, {PhaseDeploying, PhasePreparing}, {PhasePreparing, PhasePreparing}, {"unknown", PhaseFailed}, {PhasePreparing, "unknown"}} {
		if CanAdvanceUpdate(pair[0], pair[1], 0, 0) {
			t.Errorf("accepted invalid transition %v", pair)
		}
	}
}

func TestSelfUpdateJobsActiveAndIdempotency(t *testing.T) {
	pool := sharedTestPool(t)
	ctx := context.Background()
	fx := testutil.New(pool, "", "")
	requester := fx.User(t, "update requester", fmt.Sprintf("update-%d@example.test", time.Now().UnixNano()))
	q := db.New(pool)
	key := fmt.Sprintf("update-%d", time.Now().UnixNano())
	args := db.CreateSelfUpdateJobParams{RequesterID: util.MustParseUUID(requester), TargetTag: "v1.0.0", TargetSha: "target", SourceSha: "source", IdempotencyKey: key}
	first, err := q.CreateSelfUpdateJob(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	fx.Cleanup(t, "DELETE FROM self_update_job WHERE id=$1", first.ID)
	replay, err := q.CreateSelfUpdateJob(ctx, args)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("idempotent replay: %v, id %v want %v", err, replay.ID, first.ID)
	}
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			other := args
			other.IdempotencyKey = fmt.Sprintf("%s-%d", key, i)
			_, err := q.CreateSelfUpdateJob(ctx, other)
			errs <- err
		}(i)
	}
	for i := 0; i < 2; i++ {
		var conflict *pgconn.PgError
		if err := <-errs; !errors.As(err, &conflict) || conflict.Code != "23505" {
			t.Fatalf("concurrent job must conflict: %v", err)
		}
	}
	if got := fx.Count(t, "SELECT count(*) FROM self_update_job WHERE phase NOT IN ('succeeded','failed','needs_decision')"); got != 1 {
		t.Fatalf("active jobs = %d", got)
	}
	found, err := q.FindSelfUpdateJobByIdempotencyKey(ctx, key)
	if err != nil || found.ID != first.ID {
		t.Fatalf("find replay: %v", err)
	}
	claimed, err := q.ClaimSelfUpdateJob(ctx)
	if err != nil || claimed.ID != first.ID || claimed.Phase != "preparing" {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	if _, err := q.ClaimSelfUpdateJob(ctx); err == nil {
		t.Fatal("claimed job twice")
	}
	advance := db.AdvanceSelfUpdateJobParams{ID: first.ID, ExpectedPhase: "preparing", Phase: "repairing", RepairCount: 1, Step: []byte(`{"name":"repair"}`), Result: []byte(`{}`)}
	repaired, err := q.AdvanceSelfUpdateJob(ctx, advance)
	if err != nil || repaired.RepairCount != 1 {
		t.Fatalf("first repair: %v %+v", err, repaired)
	}
	if _, err := q.AdvanceSelfUpdateJob(ctx, advance); err == nil {
		t.Fatal("stale advance succeeded")
	}
	advance.ExpectedPhase = "repairing"
	advance.ExpectedRepairCount = 1
	advance.Phase = "preparing"
	advance.RetryCount = 1
	if _, err := q.AdvanceSelfUpdateJob(ctx, advance); err != nil {
		t.Fatal(err)
	}
	advance.ExpectedPhase = "preparing"
	advance.ExpectedRetryCount = 1
	advance.Phase = "repairing"
	if _, err := q.AdvanceSelfUpdateJob(ctx, advance); err == nil {
		t.Fatal("second repair with unchanged counter succeeded")
	}
	got, err := q.GetSelfUpdateJob(ctx, first.ID)
	if err != nil || got.Phase != "preparing" {
		t.Fatalf("read job: %v %+v", err, got)
	}
	advance.Phase = "needs_decision"
	stopped, err := q.AdvanceSelfUpdateJob(ctx, advance)
	if err != nil || !stopped.FinishedAt.Valid {
		t.Fatalf("terminal timestamp: %v %+v", err, stopped)
	}
	advance.ExpectedPhase = "needs_decision"
	advance.Phase = "preparing"
	if _, err := q.AdvanceSelfUpdateJob(ctx, advance); err == nil {
		t.Fatal("terminal job advanced")
	}
	for _, phase := range []string{"preparing", "awaiting_review", "repairing", "pushing", "backing_up", "deploying", "verifying"} {
		fx.Exec(t, "UPDATE self_update_job SET phase=$1 WHERE id=$2", phase, first.ID)
		other := args
		other.IdempotencyKey = key + "-" + phase
		_, err := q.CreateSelfUpdateJob(ctx, other)
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || conflict.ConstraintName != "idx_self_update_job_active" {
			t.Fatalf("phase %s did not reserve active slot: %v", phase, err)
		}
	}
	for _, phase := range []string{"succeeded", "failed", "needs_decision"} {
		fx.Exec(t, "UPDATE self_update_job SET phase=$1 WHERE id=$2", phase, first.ID)
		other := args
		other.IdempotencyKey = key + "-" + phase
		next, err := q.CreateSelfUpdateJob(ctx, other)
		if err != nil {
			t.Fatalf("terminal %s did not release active slot: %v", phase, err)
		}
		fx.Exec(t, "DELETE FROM self_update_job WHERE id=$1", next.ID)
	}
	replay, err = q.CreateSelfUpdateJob(ctx, args)
	if err != nil || replay.ID != first.ID || replay.Phase != "needs_decision" {
		t.Fatalf("terminal replay mutated job: %v %+v", err, replay)
	}
}

func TestSelfUpdateInstallationProvenance(t *testing.T) {
	pool := sharedTestPool(t)
	ctx := context.Background()
	fx := testutil.New(pool, "", "")
	fx.Cleanup(t, "DELETE FROM self_update_installation WHERE singleton=true")
	q := db.New(pool)
	args := db.UpsertSelfUpdateInstallationParams{DeployedReleaseTag: "v1.0.0", DeployedCustomSha: "custom", BackendDigest: "sha256:backend", WebDigest: "sha256:web"}
	stamp, err := q.UpsertSelfUpdateInstallation(ctx, args)
	if err != nil || stamp.DeployedCustomSha != "custom" {
		t.Fatalf("stamp: %v %+v", err, stamp)
	}
	heartbeat, err := q.HeartbeatSelfUpdateWorker(ctx)
	if err != nil || !heartbeat.WorkerHeartbeatAt.Valid || heartbeat.BackendDigest != "sha256:backend" {
		t.Fatalf("heartbeat clobbered provenance: %v %+v", err, heartbeat)
	}
	args.DeployedReleaseTag = "v1.0.1"
	if _, err := q.UpsertSelfUpdateInstallation(ctx, args); err != nil {
		t.Fatal(err)
	}
	got, err := q.GetSelfUpdateInstallation(ctx)
	if err != nil || got.DeployedReleaseTag != "v1.0.1" || !got.WorkerHeartbeatAt.Valid {
		t.Fatalf("upsert clobbered heartbeat: %v %+v", err, got)
	}
	if count := fx.Count(t, "SELECT count(*) FROM self_update_installation"); count != 1 {
		t.Fatalf("installation rows = %d", count)
	}
}
