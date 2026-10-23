package selfupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWorkerPrepareAndReconcile(t *testing.T) {
	for _, tc := range []struct {
		name, fail   string
		offline, dry bool
		wantPhase    string
	}{{name: "success", wantPhase: "awaiting_review"}, {name: "repair", fail: "merge --no-ff", wantPhase: "repairing"}, {name: "unsafe env", fail: "worktree-env", wantPhase: "needs_decision"}, {name: "dry", dry: true, wantPhase: "needs_decision"}, {name: "lost response", offline: true, wantPhase: "awaiting_review"}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, job := candidateFixture(t)
			cfg.DryRun = tc.dry
			remote := job
			claims, advances, gets := 0, 0, 0
			offline := tc.offline
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("missing dedicated bearer")
					w.WriteHeader(401)
					return
				}
				switch r.URL.Path {
				case "/api/self-update/worker/active-job":
					w.WriteHeader(204)
				case "/api/self-update/worker/heartbeat":
					w.WriteHeader(200)
				case "/api/self-update/worker/claim":
					claims++
					if claims > 1 {
						w.WriteHeader(204)
						return
					}
					_ = json.NewEncoder(w).Encode(job)
				case "/api/self-update/worker/jobs/" + job.ID:
					if r.Method == http.MethodGet {
						gets++
						_ = json.NewEncoder(w).Encode(remote)
						return
					}
					var a Advance
					if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
						t.Error(err)
					}
					advances++
					if a.ExpectedPhase != remote.Phase {
						w.WriteHeader(409)
						return
					}
					remote.Phase = a.Phase
					remote.CandidateSHA = a.CandidateSHA
					remote.StagingBranch = a.StagingBranch
					if offline {
						offline = false
						w.WriteHeader(503)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": remote.ID, "phase": remote.Phase})
				default:
					t.Errorf("unexpected route: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			cfg.ServerURL = server.URL
			fake := &fakeRunner{fail: tc.fail}
			worker := Worker{Config: cfg, Runner: fake, Client: server.Client()}
			err := worker.PollOnce(context.Background())
			if (err != nil) != tc.offline {
				t.Fatalf("poll error %v", err)
			}
			if remote.Phase != tc.wantPhase {
				t.Fatalf("remote phase %q", remote.Phase)
			}
			count := len(fake.calls)
			if err := worker.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			wantClaims, wantGets := 1, 2
			if terminalPhase(tc.wantPhase) {
				wantClaims, wantGets = 2, 1
			}
			if tc.offline {
				wantGets = 1
			}
			if len(fake.calls) != count || claims != wantClaims || advances != 1 || gets != wantGets {
				t.Fatalf("restart duplicated candidate or transition: commands=%d/%d claims=%d advances=%d reads=%d", len(fake.calls), count, claims, advances, gets)
			}
			data, _ := os.ReadFile(cfg.StateDir + "/" + job.ID + "/journal.json")
			for _, secret := range []string{cfg.WorkerToken, "secret must not escape"} {
				if strings.Contains(string(data), secret) {
					t.Fatal("secret in journal")
				}
			}
		})
	}
}

func TestWorkerClaimResponseLostWithoutJournal(t *testing.T) {
	cfg, job := candidateFixture(t)
	commands, claims, advances := &fakeRunner{}, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/self-update/worker/heartbeat":
			return
		case "/api/self-update/worker/active-job":
			_ = json.NewEncoder(w).Encode(job)
		case "/api/self-update/worker/claim":
			claims++
			w.WriteHeader(204)
		default:
			if r.Method == http.MethodPost {
				var a Advance
				_ = json.NewDecoder(r.Body).Decode(&a)
				job.Phase = a.Phase
				job.CandidateSHA = a.CandidateSHA
				job.StagingBranch = a.StagingBranch
				advances++
			}
			_ = json.NewEncoder(w).Encode(job)
		}
	}))
	defer server.Close()
	cfg.ServerURL = server.URL
	if err := (&Worker{Config: cfg, Runner: commands, Client: server.Client()}).PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if claims != 0 || len(commands.calls) != 0 || advances != 1 || job.Phase != "needs_decision" {
		t.Fatalf("claim gap repeated work: commands=%d claims=%d advances=%d phase=%s", len(commands.calls), claims, advances, job.Phase)
	}
}

func TestWorkerInterruptedPreparationStopsWithoutCommands(t *testing.T) {
	cfg, job := candidateFixture(t)
	if err := SaveJournal(cfg.StateDir, Journal{Job: job, Phase: "preparing"}); err != nil {
		t.Fatal(err)
	}
	claims := 0
	remote := job
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "heartbeat") {
			return
		}
		if strings.HasSuffix(r.URL.Path, "claim") {
			claims++
			return
		}
		if r.Method == http.MethodPost {
			var a Advance
			_ = json.NewDecoder(r.Body).Decode(&a)
			remote.Phase = a.Phase
		}
		_ = json.NewEncoder(w).Encode(remote)
	}))
	defer server.Close()
	cfg.ServerURL = server.URL
	fake := &fakeRunner{}
	if err := (&Worker{Config: cfg, Runner: fake, Client: server.Client()}).PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if claims != 0 || len(fake.calls) != 0 || remote.Phase != "needs_decision" {
		t.Fatalf("unsafe restart: claims=%d commands=%d phase=%s", claims, len(fake.calls), remote.Phase)
	}
}

func TestWorkerRejectsDivergedReconciliation(t *testing.T) {
	cfg, job := candidateFixture(t)
	candidate := Candidate{JobID: job.ID, CandidateSHA: mergedSHA, StagingBranch: "self-update/" + job.ID}
	pending := Advance{ExpectedPhase: "preparing", Phase: "awaiting_review", CandidateSHA: mergedSHA, StagingBranch: candidate.StagingBranch}
	if err := SaveJournal(cfg.StateDir, Journal{Job: job, Candidate: candidate, Phase: "prepared", Pending: &pending}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "heartbeat") {
			return
		}
		if r.Method != http.MethodGet {
			t.Error("diverged journal must not advance or claim")
		}
		job.Phase = "awaiting_review"
		job.CandidateSHA = releaseSHA
		_ = json.NewEncoder(w).Encode(job)
	}))
	defer server.Close()
	cfg.ServerURL = server.URL
	if err := (&Worker{Config: cfg, Runner: &fakeRunner{}, Client: server.Client()}).PollOnce(context.Background()); err == nil {
		t.Fatal("accepted diverged server candidate")
	}
}
