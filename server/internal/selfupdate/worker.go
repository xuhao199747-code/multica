package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Advance struct {
	ExpectedPhase string `json:"expected_phase"`
	Phase         string `json:"phase"`
	StagingBranch string `json:"staging_branch,omitempty"`
	CandidateSHA  string `json:"candidate_sha,omitempty"`
	Step          struct {
		Name string `json:"name"`
	} `json:"step"`
	Result struct {
		ChecksPassed bool   `json:"checks_passed"`
		Code         string `json:"code,omitempty"`
	} `json:"result"`
}
type Worker struct {
	Config WorkerConfig
	Runner CommandRunner
	Client *http.Client
	// OnRetry reports transient control-plane failures before the next poll.
	OnRetry func(error)
}

// Serve serializes candidate claims while independently maintaining the heartbeat
// during long checks/builds. A host lock prevents two processes sharing a journal.
func (w *Worker) Serve(ctx context.Context) error {
	if err := w.Config.Validate(); err != nil {
		return err
	}
	if err := privateDir(w.Config.StateDir); err != nil {
		return err
	}
	lockPath := filepath.Join(w.Config.StateDir, "worker.lock")
	if err := noSymlinks(lockPath); err != nil {
		return err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another host worker owns this state directory")
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	heartbeatCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_ = w.request(heartbeatCtx, http.MethodPost, "/api/self-update/worker/heartbeat", nil, nil)
			}
		}
	}()
	interval := w.Config.PollInterval
	if interval == 0 {
		interval = 10 * time.Second
	}
	for {
		if err = w.PollOnce(ctx); err != nil {
			if !retryableControlPlaneError(err) {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if w.OnRetry != nil {
				w.OnRetry(err)
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func retryableControlPlaneError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return message == "worker control plane unavailable" ||
		strings.HasPrefix(message, "worker control plane HTTP 5")
}

// PollOnce reconciles every durable job before asking the server for new work.
// Interrupted preparation is never rerun: its staging ref/worktree is preserved
// and the server is asked for an operator decision.
func (w *Worker) PollOnce(ctx context.Context) error {
	if err := w.Config.Validate(); err != nil {
		return err
	}
	if w.Runner == nil {
		return fmt.Errorf("command runner is required")
	}
	if err := privateDir(w.Config.StateDir); err != nil {
		return err
	}
	if err := w.request(ctx, http.MethodPost, "/api/self-update/worker/heartbeat", nil, nil); err != nil {
		return err
	}
	entries, err := os.ReadDir(w.Config.StateDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !jobID.MatchString(entry.Name()) {
			continue
		}
		j, err := LoadJournal(w.Config.StateDir, entry.Name())
		if err != nil {
			return err
		}
		if terminalPhase(j.Phase) && j.Pending == nil {
			continue
		}
		var remote Job
		if err = w.request(ctx, http.MethodGet, "/api/self-update/worker/jobs/"+j.Job.ID, nil, &remote); err != nil {
			return err
		}
		if err = w.reconcile(ctx, &j, remote); err != nil {
			return err
		}
		// Keep review/repair/deployment work reserved for the coordinator. This
		// worker does not invent an approval or execute the future deployment phase.
		return nil
	}
	var active Job
	if err = w.request(ctx, http.MethodGet, "/api/self-update/worker/active-job", nil, &active); err != nil {
		return err
	}
	if active.ID != "" && active.Phase != "queued" {
		if !jobID.MatchString(active.ID) || !commitSHA.MatchString(active.SourceSHA) || !commitSHA.MatchString(active.TargetSHA) {
			return fmt.Errorf("invalid active job identity")
		}
		j := Journal{Job: active, Phase: "interrupted", Candidate: Candidate{JobID: active.ID, BaseSHA: active.SourceSHA, ReleaseSHA: active.TargetSHA, CandidateSHA: active.CandidateSHA, StagingBranch: active.StagingBranch}}
		if err = SaveJournal(w.Config.StateDir, j); err != nil {
			return err
		}
		return w.reconcile(ctx, &j, active)
	}
	var job Job
	if err = w.request(ctx, http.MethodPost, "/api/self-update/worker/claim", nil, &job); err != nil {
		return err
	}
	if job.ID == "" {
		return nil
	}
	candidate, prepareErr := PrepareCandidate(ctx, w.Runner, w.Config, job)
	j, err := LoadJournal(w.Config.StateDir, job.ID)
	if err != nil {
		return err
	}
	j.Candidate = candidate
	pending := candidateAdvance(j, w.Config.DryRun, prepareErr)
	j.Pending = &pending
	if err = SaveJournal(w.Config.StateDir, j); err != nil {
		return err
	}
	return w.sendPending(ctx, &j)
}

func candidateAdvance(j Journal, dry bool, prepareErr error) Advance {
	a := Advance{ExpectedPhase: "preparing", Phase: "awaiting_review", CandidateSHA: j.Candidate.CandidateSHA, StagingBranch: j.Candidate.StagingBranch}
	a.Step.Name = "candidate_checks"
	a.Result.ChecksPassed = prepareErr == nil
	a.Result.Code = "candidate_ready"
	if dry {
		a.Phase = "needs_decision"
		a.Result.Code = "dry_run_complete"
		if prepareErr != nil {
			a.Result.Code = "dry_run_failed"
		}
		return a
	}
	if prepareErr != nil {
		var candidateErr *CandidateError
		a.Phase = "needs_decision"
		a.Result.Code = "candidate_failed"
		if errors.As(prepareErr, &candidateErr) {
			a.Result.Code = candidateErr.Step
			if candidateErr.Repairable && j.Job.RepairCount == 0 {
				a.Phase = "repairing"
			}
		}
	}
	return a
}
func (w *Worker) reconcile(ctx context.Context, j *Journal, remote Job) error {
	if remote.ID != j.Job.ID || remote.SourceSHA != j.Job.SourceSHA || remote.TargetSHA != j.Job.TargetSHA || remote.TargetTag != j.Job.TargetTag {
		return fmt.Errorf("server and journal job identity differ")
	}
	if j.Pending != nil {
		if remote.Phase == j.Pending.Phase {
			if remote.CandidateSHA != j.Pending.CandidateSHA || remote.StagingBranch != j.Pending.StagingBranch {
				return fmt.Errorf("server and journal candidate differ")
			}
			j.Phase = remote.Phase
			j.Pending = nil
			return SaveJournal(w.Config.StateDir, *j)
		}
		if remote.Phase != j.Pending.ExpectedPhase {
			return fmt.Errorf("server and pending journal phase differ")
		}
		return w.sendPending(ctx, j)
	}
	if remote.Phase != "preparing" {
		if j.Candidate.CandidateSHA != "" && remote.CandidateSHA != "" && remote.CandidateSHA != j.Candidate.CandidateSHA {
			return fmt.Errorf("server candidate changed")
		}
		j.Phase = remote.Phase
		return SaveJournal(w.Config.StateDir, *j)
	}
	a := candidateAdvance(*j, w.Config.DryRun, nil)
	if j.Phase != "prepared" {
		a.Phase = "needs_decision"
		a.Result.ChecksPassed = false
		a.Result.Code = "interrupted_preparation"
	}
	j.Pending = &a
	if err := SaveJournal(w.Config.StateDir, *j); err != nil {
		return err
	}
	return w.sendPending(ctx, j)
}
func (w *Worker) sendPending(ctx context.Context, j *Journal) error {
	var remote Job
	// Advance deliberately returns a redacted browser DTO. Read the authenticated
	// worker resource to verify full persisted identity before clearing the journal.
	if err := w.request(ctx, http.MethodPost, "/api/self-update/worker/jobs/"+j.Job.ID, j.Pending, nil); err != nil {
		return err
	}
	if err := w.request(ctx, http.MethodGet, "/api/self-update/worker/jobs/"+j.Job.ID, nil, &remote); err != nil {
		return err
	}
	if remote.ID != j.Job.ID || remote.Phase != j.Pending.Phase || remote.CandidateSHA != j.Pending.CandidateSHA || remote.StagingBranch != j.Pending.StagingBranch {
		return fmt.Errorf("unexpected server transition response")
	}
	j.Phase = remote.Phase
	j.Pending = nil
	return SaveJournal(w.Config.StateDir, *j)
}
func terminalPhase(phase string) bool {
	return phase == "succeeded" || phase == "failed" || phase == "needs_decision"
}
func (w *Worker) request(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(w.Config.ServerURL, "/")+path, reader)
	if err != nil {
		return fmt.Errorf("invalid worker request")
	}
	req.Header.Set("Authorization", "Bearer "+w.Config.WorkerToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := copyClient.Do(req)
	if err != nil {
		return fmt.Errorf("worker control plane unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent && (strings.HasSuffix(path, "/claim") || strings.HasSuffix(path, "/active-job")) {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("worker control plane HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(out); err != nil {
			return fmt.Errorf("invalid worker control plane response")
		}
	}
	return nil
}
