package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var updateSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var updateDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var updateTag = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
var updateStepName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var updateBranch = regexp.MustCompile(`^self-update/[a-zA-Z0-9-]+$`)

type selfUpdateConfig struct {
	enabled   bool
	admins    map[pgtype.UUID]bool
	token     [32]byte
	projectID pgtype.UUID
}

// Invalid or incomplete operator configuration disables every mutation.
func parseSelfUpdateConfig(cfg Config) selfUpdateConfig {
	out := selfUpdateConfig{}
	if !cfg.SelfUpdateEnabled || strings.TrimSpace(cfg.SelfUpdateWorkerToken) == "" {
		return out
	}
	project, err := util.ParseUUID(cfg.SelfUpdateProjectID)
	if err != nil || project == (pgtype.UUID{}) || project.Bytes == [16]byte{} {
		return out
	}
	admins := map[pgtype.UUID]bool{}
	for _, raw := range strings.Split(cfg.SelfUpdateAdminIDs, ",") {
		id, err := util.ParseUUID(strings.TrimSpace(raw))
		if err != nil || id.Bytes == [16]byte{} {
			return out
		}
		admins[id] = true
	}
	if len(admins) == 0 {
		return out
	}
	return selfUpdateConfig{enabled: true, admins: admins, token: sha256.Sum256([]byte(cfg.SelfUpdateWorkerToken)), projectID: project}
}

func (h *Handler) selfUpdateAdmin(r *http.Request) bool {
	id, err := util.ParseUUID(r.Header.Get("X-User-ID"))
	return h.selfUpdate.enabled && err == nil && h.selfUpdate.admins[id]
}

func (h *Handler) requireSelfUpdateAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !h.selfUpdateAdmin(r) {
		writeError(w, http.StatusForbidden, "deployment administrator required")
		return false
	}
	return true
}

func (h *Handler) requireSelfUpdateWorker(w http.ResponseWriter, r *http.Request) bool {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	digest := sha256.Sum256([]byte(token))
	valid := subtle.ConstantTimeCompare(digest[:], h.selfUpdate.token[:])
	if !h.selfUpdate.enabled || !ok || token == "" || valid != 1 {
		writeError(w, http.StatusUnauthorized, "invalid worker credentials")
		return false
	}
	return true
}

func updateBaselineValid(i db.SelfUpdateInstallation, project pgtype.UUID) bool {
	return updateTag.MatchString(i.DeployedReleaseTag) && updateSHA.MatchString(i.DeployedCustomSha) && updateDigest.MatchString(i.BackendDigest) && updateDigest.MatchString(i.WebDigest) && i.ProjectID == project
}

func updateWorkerOnline(i db.SelfUpdateInstallation) bool {
	age := time.Since(i.WorkerHeartbeatAt.Time)
	return i.WorkerHeartbeatAt.Valid && age >= -time.Minute && age <= 90*time.Second
}

type selfUpdateReleaseResponse struct {
	Tag string `json:"tag"`
	SHA string `json:"sha"`
	URL string `json:"url"`
}
type selfUpdateJobResponse struct {
	ID          pgtype.UUID        `json:"id"`
	Phase       string             `json:"phase"`
	Step        string             `json:"step"`
	TargetTag   string             `json:"target_tag"`
	TargetSHA   string             `json:"target_sha"`
	SourceSHA   string             `json:"source_sha"`
	RepairCount int32              `json:"repair_count"`
	RetryCount  int32              `json:"retry_count"`
	CreatedAt   pgtype.Timestamptz `json:"created_at"`
	UpdatedAt   pgtype.Timestamptz `json:"updated_at"`
	FinishedAt  pgtype.Timestamptz `json:"finished_at"`
}

func safeUpdateJob(j db.SelfUpdateJob) selfUpdateJobResponse {
	var step struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(j.Step, &step)
	if !updateStepName.MatchString(step.Name) {
		step.Name = ""
	}
	return selfUpdateJobResponse{j.ID, j.Phase, step.Name, j.TargetTag, j.TargetSha, j.SourceSha, j.RepairCount, j.RetryCount, j.CreatedAt, j.UpdatedAt, j.FinishedAt}
}

func (h *Handler) GetSelfUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := util.ParseUUID(r.Header.Get("X-User-ID")); err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	status := struct {
		Capability        string                     `json:"capability"`
		CanUpdate         bool                       `json:"can_update"`
		LatestRelease     *selfUpdateReleaseResponse `json:"latest_release"`
		DeployedRelease   string                     `json:"deployed_release"`
		DeployedCustomSHA string                     `json:"deployed_custom_sha"`
		ActiveJob         *selfUpdateJobResponse     `json:"active_job"`
		UpdateAvailable   bool                       `json:"update_available"`
		CheckFailed       bool                       `json:"check_failed"`
	}{Capability: "unsupported"}
	installation, err := h.Queries.GetSelfUpdateInstallation(r.Context())
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "unable to read update status")
		return
	}
	if updateTag.MatchString(installation.DeployedReleaseTag) {
		status.DeployedRelease = installation.DeployedReleaseTag
	}
	if updateSHA.MatchString(installation.DeployedCustomSha) {
		status.DeployedCustomSHA = installation.DeployedCustomSha
	}
	if h.SelfUpdateReleases != nil {
		release, err := h.SelfUpdateReleases.LatestStable(r.Context())
		if err == nil && updateTag.MatchString(release.Tag) && updateSHA.MatchString(release.CommitSHA) {
			// The source URL is informational. Only the official release URL is exposed.
			status.LatestRelease = &selfUpdateReleaseResponse{release.Tag, release.CommitSHA, "https://github.com/multica-ai/multica/releases/tag/" + release.Tag}
			status.UpdateAvailable = status.DeployedRelease != "" && release.Tag != status.DeployedRelease
		} else {
			status.CheckFailed = true
		}
	} else {
		status.CheckFailed = true
	}
	if h.cfg.SelfUpdateEnabled {
		status.Capability = "needs_setup"
		if h.selfUpdate.enabled && updateBaselineValid(installation, h.selfUpdate.projectID) {
			status.Capability = "worker_offline"
			if updateWorkerOnline(installation) {
				status.Capability = "ready"
			}
		}
	}
	if h.DB != nil {
		var id pgtype.UUID
		err := h.DB.QueryRow(r.Context(), `SELECT id FROM self_update_job WHERE phase NOT IN ('succeeded','failed','needs_decision') LIMIT 1`).Scan(&id)
		if err == nil {
			j, err := h.Queries.GetSelfUpdateJob(r.Context(), id)
			if err != nil {
				writeError(w, 500, "unable to read update job")
				return
			}
			safe := safeUpdateJob(j)
			status.ActiveJob = &safe
		} else if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 500, "unable to read update job")
			return
		}
	}
	status.CanUpdate = h.selfUpdateAdmin(r) && status.Capability == "ready" && status.LatestRelease != nil && status.UpdateAvailable && status.ActiveJob == nil
	writeJSON(w, http.StatusOK, status)
}

func decodeUpdateBody(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, 400, "invalid update request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid update request")
		return false
	}
	return true
}

func (h *Handler) CreateSelfUpdateJob(w http.ResponseWriter, r *http.Request) {
	if !h.requireSelfUpdateAdmin(w, r) {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeError(w, 400, "Idempotency-Key required (maximum 200 characters)")
		return
	}
	var body struct {
		TargetTag string `json:"target_tag"`
	}
	if !decodeUpdateBody(w, r, &body) {
		return
	}
	existing, err := h.Queries.FindSelfUpdateJobByIdempotencyKey(r.Context(), key)
	if err == nil {
		id, _ := util.ParseUUID(r.Header.Get("X-User-ID"))
		if existing.RequesterID != id || existing.TargetTag != body.TargetTag {
			writeError(w, 409, "idempotency key already used")
			return
		}
		writeJSON(w, 200, safeUpdateJob(existing))
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "unable to read update request")
		return
	}
	installation, err := h.Queries.GetSelfUpdateInstallation(r.Context())
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "unable to read installation")
		return
	}
	if !updateBaselineValid(installation, h.selfUpdate.projectID) {
		writeError(w, 409, "verified deployment baseline required")
		return
	}
	if !updateWorkerOnline(installation) {
		writeError(w, 409, "update worker is offline")
		return
	}
	if h.SelfUpdateReleases == nil {
		writeError(w, 503, "release check unavailable")
		return
	}
	release, err := h.SelfUpdateReleases.LatestStable(r.Context())
	if err != nil {
		writeError(w, 503, "release check unavailable")
		return
	}
	if body.TargetTag != release.Tag || !updateTag.MatchString(release.Tag) || !updateSHA.MatchString(release.CommitSHA) {
		writeError(w, 400, "unknown stable release")
		return
	}
	if release.Tag == installation.DeployedReleaseTag {
		writeError(w, 409, "release already deployed")
		return
	}
	id, _ := util.ParseUUID(r.Header.Get("X-User-ID"))
	job, err := h.Queries.CreateSelfUpdateJob(r.Context(), db.CreateSelfUpdateJobParams{RequesterID: id, TargetTag: release.Tag, TargetSha: release.CommitSHA, SourceSha: installation.DeployedCustomSha, IdempotencyKey: key})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, 409, "an update is already active")
			return
		}
		writeError(w, 500, "unable to create update job")
		return
	}
	// The idempotent insert may return a row created after our initial lookup.
	if job.RequesterID != id || job.TargetTag != body.TargetTag || job.TargetSha != release.CommitSHA {
		writeError(w, http.StatusConflict, "idempotency key already used")
		return
	}
	writeJSON(w, http.StatusCreated, safeUpdateJob(job))
}

func (h *Handler) GetSelfUpdateJob(w http.ResponseWriter, r *http.Request) {
	if !h.requireSelfUpdateAdmin(w, r) {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	job, err := h.Queries.GetSelfUpdateJob(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "update job not found")
		return
	}
	if err != nil {
		writeError(w, 500, "unable to read update job")
		return
	}
	writeJSON(w, 200, safeUpdateJob(job))
}

func (h *Handler) SelfUpdateWorkerHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !h.requireSelfUpdateWorker(w, r) {
		return
	}
	if _, err := h.Queries.HeartbeatSelfUpdateWorker(r.Context()); err != nil {
		writeError(w, 500, "unable to record heartbeat")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) SelfUpdateWorkerClaim(w http.ResponseWriter, r *http.Request) {
	if !h.requireSelfUpdateWorker(w, r) {
		return
	}
	job, err := h.Queries.ClaimSelfUpdateJob(r.Context())
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(204)
		return
	}
	if err != nil {
		writeError(w, 500, "unable to claim update job")
		return
	}
	writeJSON(w, 200, job)
}

// Evidence uses typed fields; raw commands, credentials and host paths stay on the worker.
type selfUpdateEvidence struct {
	ChecksPassed   bool   `json:"checks_passed"`
	ReviewApproved bool   `json:"review_approved"`
	BackupVerified bool   `json:"backup_verified"`
	ReadyzOK       bool   `json:"readyz_ok"`
	LocalOK        bool   `json:"local_ok"`
	ExternalOK     bool   `json:"external_ok"`
	BackendDigest  string `json:"backend_digest"`
	WebDigest      string `json:"web_digest"`
	Code           string `json:"code"`
}

func (h *Handler) SelfUpdateWorkerAdvance(w http.ResponseWriter, r *http.Request) {
	if !h.requireSelfUpdateWorker(w, r) {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var body struct {
		ExpectedPhase   string `json:"expected_phase"`
		Phase           string `json:"phase"`
		StagingBranch   string `json:"staging_branch"`
		CandidateSHA    string `json:"candidate_sha"`
		ApprovedSHA     string `json:"approved_sha"`
		PushedCustomSHA string `json:"pushed_custom_sha"`
		Step            struct {
			Name string `json:"name"`
		} `json:"step"`
		Result selfUpdateEvidence `json:"result"`
	}
	if !decodeUpdateBody(w, r, &body) {
		return
	}
	job, err := h.Queries.GetSelfUpdateJob(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "update job not found")
		return
	}
	if err != nil {
		writeError(w, 500, "unable to read update job")
		return
	}
	if body.ExpectedPhase != job.Phase || !service.CanAdvanceUpdate(service.UpdatePhase(job.Phase), service.UpdatePhase(body.Phase), int(job.RepairCount), int(job.RetryCount)) {
		writeError(w, 409, "invalid or stale update transition")
		return
	}
	if (body.CandidateSHA != "" && body.CandidateSHA != job.CandidateSha && job.Phase != "preparing") ||
		(body.StagingBranch != "" && job.StagingBranch != "" && body.StagingBranch != job.StagingBranch) ||
		(body.ApprovedSHA != "" && body.Phase != "pushing" && body.ApprovedSHA != job.ApprovedSha) ||
		(body.PushedCustomSHA != "" && body.Phase != "backing_up" && body.PushedCustomSHA != job.PushedCustomSha) {
		writeError(w, http.StatusBadRequest, "commit evidence cannot change in this phase")
		return
	}
	for _, sha := range []string{body.CandidateSHA, body.ApprovedSHA, body.PushedCustomSHA} {
		if sha != "" && !updateSHA.MatchString(sha) {
			writeError(w, 400, "invalid commit evidence")
			return
		}
	}
	if body.Step.Name != "" && !updateStepName.MatchString(body.Step.Name) || body.Result.Code != "" && !updateStepName.MatchString(body.Result.Code) || body.StagingBranch != "" && !updateBranch.MatchString(body.StagingBranch) {
		writeError(w, 400, "invalid update evidence")
		return
	}
	if body.CandidateSHA == "" {
		body.CandidateSHA = job.CandidateSha
	}
	if body.ApprovedSHA == "" {
		body.ApprovedSHA = job.ApprovedSha
	}
	if body.PushedCustomSHA == "" {
		body.PushedCustomSHA = job.PushedCustomSha
	}
	if body.StagingBranch == "" {
		body.StagingBranch = job.StagingBranch
	}
	valid := true
	switch body.Phase {
	case "awaiting_review":
		valid = updateSHA.MatchString(body.CandidateSHA) && updateBranch.MatchString(body.StagingBranch)
	case "pushing":
		valid = body.Result.ChecksPassed && body.Result.ReviewApproved && updateSHA.MatchString(body.CandidateSHA) && body.ApprovedSHA == body.CandidateSHA
	case "backing_up":
		valid = body.PushedCustomSHA == job.ApprovedSha && updateSHA.MatchString(body.PushedCustomSHA)
	case "deploying":
		valid = body.Result.BackupVerified
	case "succeeded":
		valid = body.Result.ReadyzOK && body.Result.LocalOK && body.Result.ExternalOK && updateDigest.MatchString(body.Result.BackendDigest) && updateDigest.MatchString(body.Result.WebDigest)
	}
	if !valid {
		writeError(w, 400, "required update evidence missing")
		return
	}
	repairs, retries := job.RepairCount, job.RetryCount
	if body.Phase == "repairing" {
		repairs++
	}
	if job.Phase == "repairing" && body.Phase == "preparing" {
		retries++
	}
	step, _ := json.Marshal(body.Step)
	result, _ := json.Marshal(body.Result)
	advanced, err := h.Queries.AdvanceSelfUpdateJob(r.Context(), db.AdvanceSelfUpdateJobParams{ID: id, ExpectedPhase: job.Phase, ExpectedRepairCount: job.RepairCount, ExpectedRetryCount: job.RetryCount, Phase: body.Phase, StagingBranch: body.StagingBranch, CandidateSha: body.CandidateSHA, ApprovedSha: body.ApprovedSHA, PushedCustomSha: body.PushedCustomSHA, Step: step, Result: result, RepairCount: repairs, RetryCount: retries})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "stale update transition")
		return
	}
	if err != nil {
		writeError(w, 500, "unable to advance update job")
		return
	}
	writeJSON(w, 200, safeUpdateJob(advanced))
}
