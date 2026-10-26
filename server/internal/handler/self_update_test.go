package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/selfupdate"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const updateTestSHA = "1234567890123456789012345678901234567890"
const updateTestToken = "private-worker-token-never-public"

func TestSelfUpdateBootstrapInput(t *testing.T) {
	valid := selfUpdateBootstrapRequest{SourceSHA: updateTestSHA, ReleaseTag: "v1.2.3", BackendDigest: "sha256:" + strings.Repeat("a", 64), WebDigest: "sha256:" + strings.Repeat("b", 64), BackupVerified: true, ReadyzOK: true, LocalOK: true, ExternalOK: true}
	if !valid.valid() {
		t.Fatal("valid baseline evidence rejected")
	}
	for _, change := range []func(*selfUpdateBootstrapRequest){func(b *selfUpdateBootstrapRequest) { b.SourceSHA = "main" }, func(b *selfUpdateBootstrapRequest) { b.ReleaseTag = "v1.2.3-beta" }, func(b *selfUpdateBootstrapRequest) { b.BackendDigest = "dev" }, func(b *selfUpdateBootstrapRequest) { b.WebDigest = "sha256:bad" }, func(b *selfUpdateBootstrapRequest) { b.BackupVerified = false }, func(b *selfUpdateBootstrapRequest) { b.ReadyzOK = false }, func(b *selfUpdateBootstrapRequest) { b.LocalOK = false }, func(b *selfUpdateBootstrapRequest) { b.ExternalOK = false }} {
		bad := valid
		change(&bad)
		if bad.valid() {
			t.Fatal("unverified baseline accepted")
		}
	}
}
func TestSelfUpdateBootstrapRejectsBeforeDatabase(t *testing.T) {
	h := &Handler{selfUpdate: parseSelfUpdateConfig(Config{SelfUpdateEnabled: true, SelfUpdateAdminIDs: "11111111-1111-4111-8111-111111111111", SelfUpdateProjectID: "22222222-2222-4222-8222-222222222222", SelfUpdateWorkerToken: updateTestToken})}
	for _, token := range []string{"", "wrong", updateTestToken} {
		req := httptest.NewRequest(http.MethodPost, "/api/self-update/worker/bootstrap", strings.NewReader(`{}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.SelfUpdateWorkerBootstrap(w, req)
		want := 401
		if token == updateTestToken {
			want = 400
		}
		if w.Code != want {
			t.Fatalf("status=%d want=%d", w.Code, want)
		}
	}
	w := httptest.NewRecorder()
	h.SelfUpdateWorkerBootstrapStatus(w, httptest.NewRequest(http.MethodGet, "/api/self-update/worker/bootstrap", nil))
	if w.Code != 401 {
		t.Fatalf("unauthenticated preflight status=%d", w.Code)
	}
}

type updateReleaseSource struct {
	release selfupdate.Release
	err     error
}

type blockingUpdateReleaseSource struct {
	entered chan struct{}
	release chan struct{}
}

func updateTestReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent update request")
	}
	var zero T
	return zero
}

func (s blockingUpdateReleaseSource) LatestStable(ctx context.Context) (selfupdate.Release, error) {
	select {
	case s.entered <- struct{}{}:
	case <-ctx.Done():
		return selfupdate.Release{}, ctx.Err()
	}
	select {
	case <-s.release:
		return selfupdate.Release{Tag: "v1.0.1", CommitSHA: updateTestSHA}, nil
	case <-ctx.Done():
		return selfupdate.Release{}, ctx.Err()
	}
}

func (s updateReleaseSource) LatestStable(context.Context) (selfupdate.Release, error) {
	return s.release, s.err
}

func updateTestHandler(t *testing.T) (*Handler, *pgxpool.Pool) {
	t.Helper()
	// A disposable namespace copies real constraints and allows genuinely concurrent
	// requests without touching any preexisting installation, jobs or provenance.
	ctx := context.Background()
	schema := pgx.Identifier{"self_update_test_" + randomID()}.Sanitize()
	if _, err := testPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	for _, table := range []string{"self_update_installation", "self_update_job"} {
		if _, err := testPool.Exec(ctx, "CREATE TABLE "+schema+"."+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	poolCfg := testPool.Config()
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg := Config{SelfUpdateEnabled: true, SelfUpdateAdminIDs: testUserID, SelfUpdateWorkerToken: updateTestToken, SelfUpdateProjectID: testWorkspaceID}
	h := &Handler{Queries: db.New(pool), DB: pool, cfg: cfg, selfUpdate: parseSelfUpdateConfig(cfg), SelfUpdateReleases: updateReleaseSource{release: selfupdate.Release{Tag: "v1.0.1", CommitSHA: updateTestSHA}}}
	return h, pool
}

func updateBaseline(t *testing.T, h *Handler) {
	t.Helper()
	_, err := h.Queries.UpsertSelfUpdateInstallation(context.Background(), db.UpsertSelfUpdateInstallationParams{DeployedReleaseTag: "v1.0.0", DeployedCustomSha: updateTestSHA, BackendDigest: "sha256:" + strings.Repeat("a", 64), WebDigest: "sha256:" + strings.Repeat("b", 64), ProjectID: util.MustParseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.Queries.HeartbeatSelfUpdateWorker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func updateRequest(key string) *http.Request {
	r := newRequest("POST", "/api/self-update/jobs", map[string]string{"target_tag": "v1.0.1"})
	r.Header.Set("Idempotency-Key", key)
	return r
}
func workerRequest(path string, body any) *http.Request {
	r := newRequest("POST", path, body)
	r.Header.Set("Authorization", "Bearer "+updateTestToken)
	return r
}

func TestSelfUpdateWorkerGet(t *testing.T) {
	h, _ := updateTestHandler(t)
	updateBaseline(t, h)
	var job db.SelfUpdateJob
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("worker-get")).Want(201).JSON(&job)
	id := util.UUIDToString(job.ID)
	request := func(token string, id string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/self-update/worker/jobs/"+id, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		return withURLParam(r, "id", id)
	}
	for _, token := range []string{"", "wrong"} {
		testutil.Call(t, h.SelfUpdateWorkerGet, request(token, id)).Want(401)
	}
	testutil.Call(t, h.SelfUpdateWorkerGet, request(updateTestToken, "bad")).Want(400)
	testutil.Call(t, h.SelfUpdateWorkerGet, request(updateTestToken, "11111111-1111-4111-8111-111111111111")).Want(404)
	var got db.SelfUpdateJob
	testutil.Call(t, h.SelfUpdateWorkerGet, request(updateTestToken, id)).Want(200).JSON(&got)
	if got.ID != job.ID || got.SourceSha != job.SourceSha || got.TargetSha != job.TargetSha || got.Phase != job.Phase {
		t.Fatalf("worker reconciliation identity mismatch: %+v", got)
	}
}

func TestSelfUpdateWorkerActiveJob(t *testing.T) {
	h, _ := updateTestHandler(t)
	for _, token := range []string{"", "wrong"} {
		r := httptest.NewRequest(http.MethodGet, "/api/self-update/worker/active-job", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		testutil.Call(t, h.SelfUpdateWorkerActiveJob, r).Want(401)
	}
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/self-update/worker/active-job", nil)
		r.Header.Set("Authorization", "Bearer "+updateTestToken)
		return r
	}
	testutil.Call(t, h.SelfUpdateWorkerActiveJob, request()).Want(204)
	updateBaseline(t, h)
	var job, got db.SelfUpdateJob
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("worker-active")).Want(201).JSON(&job)
	testutil.Call(t, h.SelfUpdateWorkerActiveJob, request()).Want(200).JSON(&got)
	if got.ID != job.ID || got.Phase != "queued" {
		t.Fatalf("wrong active job: %+v", got)
	}
	testutil.Call(t, h.SelfUpdateWorkerClaim, workerRequest("/", nil)).Want(200)
	testutil.Call(t, h.SelfUpdateWorkerActiveJob, request()).Want(200).JSON(&got)
	if got.ID != job.ID || got.Phase != "preparing" {
		t.Fatalf("claim gap cannot be reconciled: %+v", got)
	}
}

func TestSelfUpdateConfiguration(t *testing.T) {
	for _, cfg := range []Config{{}, {SelfUpdateEnabled: true}, {SelfUpdateEnabled: true, SelfUpdateAdminIDs: "invalid", SelfUpdateWorkerToken: updateTestToken, SelfUpdateProjectID: testWorkspaceID}, {SelfUpdateEnabled: true, SelfUpdateAdminIDs: testUserID, SelfUpdateWorkerToken: updateTestToken, SelfUpdateProjectID: "invalid"}} {
		h, _ := updateTestHandler(t)
		h.cfg = cfg
		h.selfUpdate = parseSelfUpdateConfig(cfg)
		testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("disabled")).Want(http.StatusForbidden)
		var status map[string]any
		testutil.Call(t, h.GetSelfUpdateStatus, newRequest("GET", "/", nil)).Want(200).JSON(&status)
		want := "unsupported"
		if cfg.SelfUpdateEnabled {
			want = "needs_setup"
		}
		if status["capability"] != want || status["can_update"] != false {
			t.Fatalf("invalid config status: %v", status)
		}
		testutil.Call(t, h.SelfUpdateWorkerHeartbeat, workerRequest("/", nil)).Want(401)
	}
}

func TestSelfUpdatePermissionsAndBaseline(t *testing.T) {
	h, tx := updateTestHandler(t)
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("no-baseline")).Want(http.StatusConflict)
	updateBaseline(t, h)
	// The fixture user owns the workspace, but deployment authority is a UUID allowlist.
	h.cfg.SelfUpdateAdminIDs = "11111111-1111-4111-8111-111111111111"
	h.selfUpdate = parseSelfUpdateConfig(h.cfg)
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("owner")).Want(http.StatusForbidden)
	h.cfg.SelfUpdateAdminIDs = testUserID
	h.selfUpdate = parseSelfUpdateConfig(h.cfg)
	r := updateRequest("wrong-user")
	r.Header.Set("X-User-ID", "22222222-2222-4222-8222-222222222222")
	testutil.Call(t, h.CreateSelfUpdateJob, r).Want(http.StatusForbidden)
	if _, err := tx.Exec(context.Background(), "UPDATE self_update_installation SET worker_heartbeat_at=now()-interval '10 minutes'"); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("offline")).Want(http.StatusConflict)
}

func TestSelfUpdateMalformedBaselineDigests(t *testing.T) {
	for _, field := range []string{"backend_digest", "web_digest"} {
		for _, digest := range []string{"", " ", "sha256:/host/path", "sha256:" + strings.Repeat("a", 63), "sha256:" + strings.Repeat("z", 64)} {
			t.Run(field+"/"+digest, func(t *testing.T) {
				h, pool := updateTestHandler(t)
				updateBaseline(t, h)
				if _, err := pool.Exec(context.Background(), "UPDATE self_update_installation SET "+field+"=$1", digest); err != nil {
					t.Fatal(err)
				}
				testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("malformed-baseline")).Want(http.StatusConflict)
				var status map[string]any
				testutil.Call(t, h.GetSelfUpdateStatus, newRequest("GET", "/", nil)).Want(http.StatusOK).JSON(&status)
				if status["capability"] != "needs_setup" || status["can_update"] != false {
					t.Fatalf("malformed baseline grants capability: %v", status)
				}
			})
		}
	}
}

func TestSelfUpdateCreateAndReplay(t *testing.T) {
	h, _ := updateTestHandler(t)
	updateBaseline(t, h)
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("")).Want(http.StatusBadRequest)
	r := updateRequest("unknown")
	r.Body = http.NoBody
	testutil.Call(t, h.CreateSelfUpdateJob, r).Want(http.StatusBadRequest)
	r = newRequest("POST", "/", map[string]string{"target_tag": "v0.0.0"})
	r.Header.Set("Idempotency-Key", "unknown")
	testutil.Call(t, h.CreateSelfUpdateJob, r).Want(http.StatusBadRequest)
	h.SelfUpdateReleases = updateReleaseSource{err: fmt.Errorf("secret /host/path")}
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("unavailable")).Want(http.StatusServiceUnavailable)
	h.SelfUpdateReleases = updateReleaseSource{release: selfupdate.Release{Tag: "v1.0.1", CommitSHA: updateTestSHA}}
	first := testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("once")).Want(http.StatusCreated)
	replay := testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("once")).Want(http.StatusOK)
	var a, b map[string]any
	first.JSON(&a)
	replay.JSON(&b)
	if a["id"] == nil || a["id"] != b["id"] {
		t.Fatalf("replay IDs: %v %v", a, b)
	}
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("second")).Want(http.StatusConflict)
	r = withURLParam(newRequest("GET", "/", nil), "id", a["id"].(string))
	testutil.Call(t, h.GetSelfUpdateJob, r).Want(http.StatusOK)
}

func TestSelfUpdateSafeRead(t *testing.T) {
	h, _ := updateTestHandler(t)
	updateBaseline(t, h)
	var status map[string]any
	w := testutil.Call(t, h.GetSelfUpdateStatus, newRequest("GET", "/", nil)).Want(http.StatusOK)
	w.JSON(&status)
	if status["update_available"] != true || status["deployed_release"] != "v1.0.0" || status["can_update"] != true || status["capability"] != "ready" {
		t.Fatalf("status: %v", status)
	}
	for _, handler := range []http.HandlerFunc{h.GetSelfUpdateStatus, h.GetConfig} {
		w := testutil.Call(t, handler, newRequest("GET", "/", nil)).Want(http.StatusOK)
		for _, secret := range []string{updateTestToken, "admin_ids", "worker_token", "/host/path"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("read leaked %q", secret)
			}
		}
	}
	var public map[string]any
	testutil.Call(t, h.GetConfig, newRequest("GET", "/", nil)).Want(200).JSON(&public)
	if _, exists := public["can_update"]; exists {
		t.Fatal("public config grants deployment permission")
	}
	h.SelfUpdateReleases = updateReleaseSource{err: fmt.Errorf("%s /host/path", updateTestToken)}
	w = testutil.Call(t, h.GetSelfUpdateStatus, newRequest("GET", "/", nil)).Want(http.StatusOK)
	if strings.Contains(w.Body.String(), updateTestToken) || strings.Contains(w.Body.String(), "/host/path") {
		t.Fatal("release error leaked")
	}
}

func TestSelfUpdateStatusCustomBaselineNotUpdateable(t *testing.T) {
	h, _ := updateTestHandler(t)
	// A custom self-host fork records a v0.0.0 placeholder release. It is not a
	// descendant of any official release, so it must never present an update.
	_, err := h.Queries.UpsertSelfUpdateInstallation(context.Background(), db.UpsertSelfUpdateInstallationParams{DeployedReleaseTag: "v0.0.0", DeployedCustomSha: updateTestSHA, BackendDigest: "sha256:" + strings.Repeat("a", 64), WebDigest: "sha256:" + strings.Repeat("b", 64), ProjectID: util.MustParseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.Queries.HeartbeatSelfUpdateWorker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h.SelfUpdateReleases = updateReleaseSource{release: selfupdate.Release{Tag: "v0.7.0", CommitSHA: updateTestSHA}}
	var status map[string]any
	testutil.Call(t, h.GetSelfUpdateStatus, newRequest("GET", "/", nil)).Want(http.StatusOK).JSON(&status)
	if status["update_available"] != false || status["can_update"] != false || status["capability"] != "ready" {
		t.Fatalf("custom baseline should not auto-update: %v", status)
	}
}

func TestSelfUpdateTerminalReplayNeedsNoWorkerOrReleaseCheck(t *testing.T) {
	h, pool := updateTestHandler(t)
	updateBaseline(t, h)
	var job map[string]any
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("terminal-replay")).Want(201).JSON(&job)
	if _, err := pool.Exec(context.Background(), `UPDATE self_update_job SET phase='failed' WHERE id=$1`, job["id"]); err != nil {
		t.Fatal(err)
	}
	h.SelfUpdateReleases = updateReleaseSource{err: fmt.Errorf("offline")}
	if _, err := pool.Exec(context.Background(), `UPDATE self_update_installation SET worker_heartbeat_at=NULL`); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("terminal-replay")).Want(200)
	r := newRequest("POST", "/", map[string]string{"target_tag": "v1.0.2"})
	r.Header.Set("Idempotency-Key", "terminal-replay")
	testutil.Call(t, h.CreateSelfUpdateJob, r).Want(409)
	r = updateRequest("terminal-replay")
	r.Header.Set("X-User-ID", "11111111-1111-4111-8111-111111111111")
	testutil.Call(t, h.CreateSelfUpdateJob, r).Want(403)
}

func TestSelfUpdateWorker(t *testing.T) {
	h, _ := updateTestHandler(t)
	updateBaseline(t, h)
	for _, handler := range []http.HandlerFunc{h.SelfUpdateWorkerHeartbeat, h.SelfUpdateWorkerClaim, h.SelfUpdateWorkerAdvance} {
		for _, token := range []string{"", "Bearer wrong"} {
			r := newRequest("POST", "/", nil)
			r.Header.Set("Authorization", token)
			testutil.Call(t, handler, r).Want(http.StatusUnauthorized)
		}
	}
	testutil.Call(t, h.SelfUpdateWorkerHeartbeat, workerRequest("/", nil)).Want(http.StatusOK)
	testutil.Call(t, h.SelfUpdateWorkerClaim, workerRequest("/", nil)).Want(http.StatusNoContent)
	var job map[string]any
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("worker")).Want(http.StatusCreated).JSON(&job)
	testutil.Call(t, h.SelfUpdateWorkerClaim, workerRequest("/", nil)).Want(http.StatusOK)
	request := func(body any) *http.Request { return withURLParam(workerRequest("/", body), "id", job["id"].(string)) }
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "preparing", "phase": "succeeded"})).Want(http.StatusConflict)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "preparing", "phase": "awaiting_review", "candidate_sha": "bad"})).Want(http.StatusBadRequest)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "preparing", "phase": "awaiting_review", "candidate_sha": updateTestSHA, "staging_branch": "self-update/test"})).Want(http.StatusOK)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "awaiting_review", "phase": "pushing"})).Want(http.StatusBadRequest)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "awaiting_review", "phase": "repairing"})).Want(http.StatusOK)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "repairing", "phase": "preparing"})).Want(http.StatusOK)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "preparing", "phase": "repairing"})).Want(http.StatusConflict)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "preparing", "phase": "failed", "result": map[string]any{"log": "/host/path " + updateTestToken}})).Want(http.StatusBadRequest)
}

func TestSelfUpdateRejectsChangedCandidateAfterReview(t *testing.T) {
	h, _ := updateTestHandler(t)
	updateBaseline(t, h)
	var job map[string]any
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("immutable")).Want(201).JSON(&job)
	testutil.Call(t, h.SelfUpdateWorkerClaim, workerRequest("/", nil)).Want(200)
	request := func(body any) *http.Request { return withURLParam(workerRequest("/", body), "id", job["id"].(string)) }
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "preparing", "phase": "awaiting_review", "candidate_sha": updateTestSHA, "staging_branch": "self-update/test"})).Want(200)
	otherSHA := strings.Repeat("a", 40)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "awaiting_review", "phase": "pushing", "candidate_sha": otherSHA, "approved_sha": otherSHA, "result": map[string]bool{"checks_passed": true, "review_approved": true}})).Want(400)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "awaiting_review", "phase": "pushing", "approved_sha": updateTestSHA, "result": map[string]bool{"checks_passed": true, "review_approved": true}})).Want(200)
	testutil.Call(t, h.SelfUpdateWorkerAdvance, request(map[string]any{"expected_phase": "pushing", "phase": "backing_up", "pushed_custom_sha": updateTestSHA, "approved_sha": otherSHA})).Want(400)
}

func TestSelfUpdateWorkerDeploymentEvidence(t *testing.T) {
	h, _ := updateTestHandler(t)
	updateBaseline(t, h)
	var job map[string]any
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("deploy-evidence")).Want(201).JSON(&job)
	testutil.Call(t, h.SelfUpdateWorkerClaim, workerRequest("/", nil)).Want(200)
	advance := func(body map[string]any) *testutil.Response {
		return testutil.Call(t, h.SelfUpdateWorkerAdvance, withURLParam(workerRequest("/", body), "id", job["id"].(string)))
	}
	advance(map[string]any{"expected_phase": "preparing", "phase": "awaiting_review", "candidate_sha": updateTestSHA, "staging_branch": "self-update/test"}).Want(200)
	advance(map[string]any{"expected_phase": "awaiting_review", "phase": "pushing", "approved_sha": updateTestSHA, "result": map[string]bool{"checks_passed": true, "review_approved": true}}).Want(200)
	advance(map[string]any{"expected_phase": "pushing", "phase": "backing_up"}).Want(400)
	advance(map[string]any{"expected_phase": "pushing", "phase": "backing_up", "pushed_custom_sha": updateTestSHA}).Want(200)
	advance(map[string]any{"expected_phase": "backing_up", "phase": "deploying"}).Want(400)
	advance(map[string]any{"expected_phase": "backing_up", "phase": "deploying", "result": map[string]bool{"backup_verified": true}}).Want(200)
	advance(map[string]any{"expected_phase": "deploying", "phase": "verifying"}).Want(200)
	advance(map[string]any{"expected_phase": "verifying", "phase": "succeeded"}).Want(400)
	evidence := map[string]any{"readyz_ok": true, "local_ok": true, "external_ok": true, "backend_digest": "sha256:/host/path", "web_digest": "sha256:/host/path"}
	advance(map[string]any{"expected_phase": "verifying", "phase": "succeeded", "result": evidence}).Want(400)
	evidence["backend_digest"] = "sha256:" + strings.Repeat("a", 64)
	evidence["web_digest"] = "sha256:" + strings.Repeat("b", 64)
	advance(map[string]any{"expected_phase": "verifying", "phase": "succeeded", "result": evidence}).Want(200)
	advance(map[string]any{"expected_phase": "succeeded", "phase": "failed"}).Want(409)
}

func TestSelfUpdatePublicJobRedaction(t *testing.T) {
	h, pool := updateTestHandler(t)
	updateBaseline(t, h)
	var job map[string]any
	testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("redaction")).Want(201).JSON(&job)
	if _, err := pool.Exec(context.Background(), `UPDATE self_update_job SET step=$1, result=$2 WHERE id=$3`, []byte(`{"name":"/host/path","token":"`+updateTestToken+`"}`), []byte(`{"log":"/host/path `+updateTestToken+`"}`), job["id"]); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{h.GetSelfUpdateStatus, h.GetSelfUpdateJob} {
		w := testutil.Call(t, handler, withURLParam(newRequest("GET", "/", nil), "id", job["id"].(string))).Want(200)
		if strings.Contains(w.Body.String(), updateTestToken) || strings.Contains(w.Body.String(), "/host/path") {
			t.Fatal("raw evidence escaped into browser response")
		}
	}
}

func TestSelfUpdateMalformedRequests(t *testing.T) {
	h, _ := updateTestHandler(t)
	updateBaseline(t, h)
	for _, raw := range []string{`{`, `{"target_tag":"v1.0.1","command":"rm"}`, `{"target_tag":"v1.0.1"} {}`, strings.Repeat(" ", 33<<10) + `{}`} {
		r := updateRequest("malformed")
		r.Body = io.NopCloser(strings.NewReader(raw))
		testutil.Call(t, h.CreateSelfUpdateJob, r).Want(400)
	}
	for _, release := range []selfupdate.Release{{Tag: "v1.0.1-rc.1", CommitSHA: updateTestSHA}, {Tag: "v1.0.1", CommitSHA: "main"}, {Tag: "/host/path", CommitSHA: updateTestSHA}} {
		h.SelfUpdateReleases = updateReleaseSource{release: release}
		testutil.Call(t, h.CreateSelfUpdateJob, updateRequest("invalid-release")).Want(400)
	}
	r := newRequest("GET", "/", nil)
	r.Header.Del("X-User-ID")
	testutil.Call(t, h.GetSelfUpdateStatus, r).Want(401)
	r = withURLParam(newRequest("GET", "/", nil), "id", "invalid")
	testutil.Call(t, h.GetSelfUpdateJob, r).Want(400)
	r = withURLParam(newRequest("GET", "/", nil), "id", "11111111-1111-4111-8111-111111111111")
	testutil.Call(t, h.GetSelfUpdateJob, r).Want(404)
}

func TestSelfUpdateConcurrentPosts(t *testing.T) {
	h, tx := updateTestHandler(t)
	updateBaseline(t, h)
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			w := httptest.NewRecorder()
			h.CreateSelfUpdateJob(w, updateRequest(fmt.Sprintf("concurrent-%d", i)))
			codes <- w.Code
		}(i)
	}
	a, b := <-codes, <-codes
	if !((a == 201 && b == 409) || (a == 409 && b == 201)) {
		t.Fatalf("concurrent results: %d %d", a, b)
	}
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM self_update_job").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestSelfUpdateConcurrentIdempotencyOwnership(t *testing.T) {
	h, _ := updateTestHandler(t)
	updateBaseline(t, h)
	otherID := "11111111-1111-4111-8111-111111111111"
	h.cfg.SelfUpdateAdminIDs = testUserID + "," + otherID
	h.selfUpdate = parseSelfUpdateConfig(h.cfg)
	source := blockingUpdateReleaseSource{make(chan struct{}, 2), make(chan struct{})}
	h.SelfUpdateReleases = source
	codes := make(chan int, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, id := range []string{testUserID, otherID} {
		go func(id string) {
			r := updateRequest("shared-key")
			r = r.WithContext(ctx)
			r.Header.Set("X-User-ID", id)
			w := httptest.NewRecorder()
			h.CreateSelfUpdateJob(w, r)
			codes <- w.Code
		}(id)
	}
	updateTestReceive(t, source.entered)
	updateTestReceive(t, source.entered)
	close(source.release)
	a, b := updateTestReceive(t, codes), updateTestReceive(t, codes)
	if !((a == 201 && b == 409) || (a == 409 && b == 201)) {
		t.Fatalf("concurrent key ownership: %d %d", a, b)
	}
}

func TestSelfUpdateConcurrentTerminalReplayVerifiesOwner(t *testing.T) {
	h, pool := updateTestHandler(t)
	updateBaseline(t, h)
	source := blockingUpdateReleaseSource{make(chan struct{}, 1), make(chan struct{})}
	h.SelfUpdateReleases = source
	codes := make(chan int, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		w := httptest.NewRecorder()
		h.CreateSelfUpdateJob(w, updateRequest("terminal-race").WithContext(ctx))
		codes <- w.Code
	}()
	updateTestReceive(t, source.entered)
	// Another authorized request completes while this request resolves the release.
	job, err := h.Queries.CreateSelfUpdateJob(context.Background(), db.CreateSelfUpdateJobParams{RequesterID: util.MustParseUUID("11111111-1111-4111-8111-111111111111"), TargetTag: "v1.0.1", TargetSha: updateTestSHA, SourceSha: updateTestSHA, IdempotencyKey: "terminal-race"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE self_update_job SET phase='failed' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	close(source.release)
	if code := updateTestReceive(t, codes); code != 409 {
		t.Fatalf("terminal replay with different owner: got %d want 409", code)
	}
}
