package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type DeployEvidence struct {
	BackendDigest, WebDigest, DatabaseBackup, LocalURL, ExternalURL string
	PreviousBackendDigest, PreviousWebDigest, ReadyURL              string
}

// Bootstrap establishes provenance only from an explicitly supplied source SHA.
// A dry run checks Git ancestry and prints the exact deployment inputs, but never
// creates a source worktree, runs checks/builds, dumps a database or starts Docker.
func Bootstrap(ctx context.Context, r CommandRunner, cfg WorkerConfig, source, tag string, apply bool, report io.Writer, client *http.Client) error {
	values, err := productionValues(cfg)
	if err != nil {
		return err
	}
	if !commitSHA.MatchString(source) || !regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(tag) {
		return fmt.Errorf("bootstrap requires exact SHA and stable release tag")
	}
	external, err := externalURL(values)
	if err != nil {
		return err
	}
	if err = privateDir(cfg.StateDir); err != nil {
		return err
	}
	lockPath := filepath.Join(cfg.StateDir, "worker.lock")
	if err = noSymlinks(lockPath); err != nil {
		return err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("host worker is already running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	env := commandEnvironment()
	git := func(args ...string) (CommandResult, error) {
		return checkedRun(ctx, r, "git", append([]string{"--git-dir=" + cfg.RepositoryDir, "-c", "core.hooksPath=/dev/null", "-c", "protocol.file.allow=never"}, args...), cfg.StateDir, env)
	}
	if _, err = os.Lstat(cfg.RepositoryDir); os.IsNotExist(err) {
		if _, err = checkedRun(ctx, r, "git", []string{"init", "--bare", cfg.RepositoryDir}, cfg.StateDir, env); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	bare, err := git("rev-parse", "--is-bare-repository")
	if err != nil || strings.TrimSpace(bare.Stdout) != "true" {
		return fmt.Errorf("bootstrap requires dedicated bare repository")
	}
	remote, err := git("ls-remote", "--heads", cfg.CustomRemote, "refs/heads/"+cfg.CustomBranch)
	if err != nil || !remoteEquals(remote.Stdout, source, "refs/heads/"+cfg.CustomBranch) {
		return fmt.Errorf("bootstrap custom branch differs from exact source SHA")
	}
	baseRef := "refs/self-update/bootstrap-source/" + source
	releaseRef := "refs/self-update/bootstrap-release/" + tag
	if _, err = git("fetch", "--no-tags", cfg.CustomRemote, "refs/heads/"+cfg.CustomBranch+":"+baseRef); err != nil {
		return err
	}
	fetched, err := git("rev-parse", baseRef+"^{commit}")
	if err != nil || strings.TrimSpace(fetched.Stdout) != source {
		return fmt.Errorf("bootstrap source moved during fetch")
	}
	if _, err = git("fetch", "--no-tags", "https://github.com/multica-ai/multica.git", "refs/tags/"+tag+":"+releaseRef); err != nil {
		return err
	}
	release, err := git("rev-parse", releaseRef+"^{commit}")
	if err != nil || !commitSHA.MatchString(strings.TrimSpace(release.Stdout)) {
		return fmt.Errorf("bootstrap release commit unavailable")
	}
	releaseSHA := strings.TrimSpace(release.Stdout)
	if _, err = git("merge-base", "--is-ancestor", releaseSHA, source); err != nil {
		return fmt.Errorf("official release is not an ancestor of bootstrap source")
	}
	frontPort := values["FRONTEND_PORT"]
	if frontPort == "" {
		frontPort = "3000"
	}
	backPort := "8080"
	for _, key := range []string{"PORT", "SERVER_PORT", "API_PORT", "BACKEND_PORT"} {
		if values[key] != "" {
			backPort = values[key]
		}
	}
	local, err := publishedURL("127.0.0.1:" + frontPort)
	if err != nil {
		return err
	}
	ready, err := publishedURL("127.0.0.1:" + backPort)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(report, "source_sha=%s\nrelease_tag=%s\nrelease_sha=%s\ncustom_branch=%s\nproduction_env=%s\ncompose_project=multica\ncompose_files=docker-compose.selfhost.yml,docker-compose.selfhost.build.yml\ndatabase=%s\nlocal_frontend=%s\nbackend_ready=%s/readyz\nexternal_url=%s\napply=%t\n", source, tag, releaseSHA, cfg.CustomBranch, cfg.ProductionEnvFile, cfg.ProductionDatabase, local, ready, external, apply); err != nil {
		return err
	}
	if !apply {
		return nil
	}
	// The control plane has a dedicated bearer endpoint; browser admin sessions
	// cannot establish a baseline. The host retains evidence if registration fails.
	worker := Worker{Config: cfg, Client: client}
	var status struct {
		Registered *bool `json:"registered"`
		ActiveJob  *bool `json:"active_job"`
	}
	if err = worker.request(ctx, http.MethodGet, "/api/self-update/worker/bootstrap", nil, &status); err != nil {
		return err
	}
	if status.Registered == nil || status.ActiveJob == nil {
		return fmt.Errorf("incomplete bootstrap preflight response")
	}
	if *status.Registered || *status.ActiveJob {
		return fmt.Errorf("bootstrap requires an empty baseline and no active update")
	}
	id := uuid.NewString()
	candidate := Candidate{JobID: id, BaseSHA: source, ReleaseSHA: releaseSHA, CandidateSHA: source, WorktreePath: filepath.Join(cfg.WorktreeRoot, id)}
	if err = privateDir(cfg.WorktreeRoot); err != nil {
		return err
	}
	if _, err = git("worktree", "add", "--detach", candidate.WorktreePath, source); err != nil {
		return err
	}
	if _, err = os.Lstat(filepath.Join(candidate.WorktreePath, ".env")); !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap candidate .env is forbidden")
	}
	if err = noSymlinks(filepath.Join(candidate.WorktreePath, "scripts/ensure-postgres.sh")); err != nil {
		return err
	}
	helper, err := os.ReadFile(filepath.Join(candidate.WorktreePath, "scripts/ensure-postgres.sh"))
	if err != nil || !strings.Contains(string(helper), "MULTICA_SELF_UPDATE_NO_CONTAINER_START") {
		return fmt.Errorf("bootstrap source lacks guarded PostgreSQL helper")
	}
	for _, target := range []string{"worktree-env", "setup-worktree"} {
		if _, err = checkedRun(ctx, r, "make", []string{target, "ENV_FILE=.env.worktree", "WORKTREE_ENV_FILE=.env.worktree"}, candidate.WorktreePath, env); err != nil {
			return err
		}
		if err = candidateEnvironment(candidate.WorktreePath, cfg.ProductionDatabase); err != nil {
			return err
		}
	}
	evidence, err := DeployCandidate(ctx, r, cfg, candidate)
	record := struct {
		SourceSHA  string         `json:"source_sha"`
		ReleaseTag string         `json:"release_tag"`
		Evidence   DeployEvidence `json:"evidence"`
		Verified   bool           `json:"verified"`
	}{source, tag, evidence, false}
	// Local recovery evidence is private and includes old image IDs even on error.
	persist := func() error {
		data, e := json.Marshal(record)
		if e != nil {
			return e
		}
		path := filepath.Join(cfg.StateDir, "bootstrap-"+id+".json")
		file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer file.Close()
		if _, e = file.Write(data); e != nil {
			return e
		}
		return file.Sync()
	}
	if err != nil {
		_ = persist()
		return err
	}
	if err = worker.VerifyDeployment(ctx, evidence); err != nil {
		_ = persist()
		return err
	}
	record.Verified = true
	if err = persist(); err != nil {
		return err
	}
	payload := struct {
		SourceSHA      string `json:"source_sha"`
		ReleaseTag     string `json:"release_tag"`
		BackendDigest  string `json:"backend_digest"`
		WebDigest      string `json:"web_digest"`
		BackupVerified bool   `json:"backup_verified"`
		ReadyzOK       bool   `json:"readyz_ok"`
		LocalOK        bool   `json:"local_ok"`
		ExternalOK     bool   `json:"external_ok"`
	}{source, tag, evidence.BackendDigest, evidence.WebDigest, true, true, true, true}
	var registered struct {
		Registered *bool `json:"registered"`
	}
	if err = worker.request(ctx, http.MethodPost, "/api/self-update/worker/bootstrap", payload, &registered); err != nil {
		return err
	}
	if registered.Registered == nil || !*registered.Registered {
		return fmt.Errorf("bootstrap registration was not acknowledged")
	}
	return nil
}

var imageDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func publishedURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	host, port, err := net.SplitHostPort(value)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || n < 1 || n > 65535 || (host != "127.0.0.1" && host != "::1") {
		return "", fmt.Errorf("invalid loopback published port")
	}
	return "http://" + net.JoinHostPort(host, port), nil
}
func externalURL(values map[string]string) (string, error) {
	value := values["MULTICA_APP_URL"]
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("explicit external frontend URL required")
	}
	return value, nil
}

// DeployCandidate is a guarded primitive. The caller must supply the approved
// (or explicitly confirmed bootstrap) SHA; coordination owns the review gate.
func DeployCandidate(ctx context.Context, r CommandRunner, cfg WorkerConfig, c Candidate) (DeployEvidence, error) {
	var out DeployEvidence
	values, err := productionValues(cfg)
	if err != nil {
		return out, err
	}
	if cfg.DryRun || !commitSHA.MatchString(c.CandidateSHA) || !jobID.MatchString(c.JobID) || c.WorktreePath != filepath.Join(cfg.WorktreeRoot, c.JobID) {
		return out, fmt.Errorf("invalid deployment candidate")
	}
	if err = noSymlinks(c.WorktreePath); err != nil {
		return out, err
	}
	out.ExternalURL, err = externalURL(values)
	if err != nil {
		return out, err
	}
	env := append(commandEnvironment(), "MULTICA_CUSTOM_IMAGE_TAG="+c.CandidateSHA, "VERSION="+c.CandidateSHA, "COMMIT="+c.CandidateSHA, "DATE="+time.Now().UTC().Format(time.RFC3339), "NEXT_PUBLIC_APP_VERSION="+c.CandidateSHA)
	run := func(name string, args ...string) (CommandResult, error) {
		return checkedRun(ctx, r, name, args, c.WorktreePath, env)
	}
	head, err := run("git", "-C", c.WorktreePath, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head.Stdout) != c.CandidateSHA {
		return out, fmt.Errorf("deployment HEAD differs from approved SHA")
	}
	clean := func() error {
		if _, e := run("git", "-C", c.WorktreePath, "diff", "--quiet", "HEAD", "--"); e != nil {
			return fmt.Errorf("deployment candidate has uncommitted changes")
		}
		status, e := run("git", "-C", c.WorktreePath, "status", "--porcelain", "--untracked-files=normal")
		if e != nil || strings.TrimSpace(status.Stdout) != "" {
			return fmt.Errorf("deployment candidate has uncommitted files")
		}
		return nil
	}
	if err = clean(); err != nil {
		return out, err
	}
	if err = candidateEnvironment(c.WorktreePath, cfg.ProductionDatabase); err != nil {
		return out, err
	}
	helperPath := filepath.Join(c.WorktreePath, "scripts", "ensure-postgres.sh")
	if err = noSymlinks(helperPath); err != nil {
		return out, err
	}
	helper, err := os.ReadFile(helperPath)
	if err != nil || !strings.Contains(string(helper), "MULTICA_SELF_UPDATE_NO_CONTAINER_START") {
		return out, fmt.Errorf("unsafe candidate PostgreSQL helper")
	}
	if _, err = run("make", "check-worktree", "ENV_FILE=.env.worktree", "WORKTREE_ENV_FILE=.env.worktree"); err != nil {
		return out, err
	}
	if err = candidateEnvironment(c.WorktreePath, cfg.ProductionDatabase); err != nil {
		return out, err
	}
	for _, image := range []struct{ name, file string }{{"backend", "Dockerfile"}, {"web", "Dockerfile.web"}} {
		args := []string{"build", "--file", image.file, "--tag", "multica-" + image.name + ":" + c.CandidateSHA}
		for _, arg := range []string{"VERSION=" + c.CandidateSHA, "COMMIT=" + c.CandidateSHA, "DATE=" + time.Now().UTC().Format(time.RFC3339), "NEXT_PUBLIC_APP_VERSION=" + c.CandidateSHA} {
			args = append(args, "--build-arg", arg)
		}
		if _, err = run("docker", append(args, ".")...); err != nil {
			return out, err
		}
	}
	if err = clean(); err != nil {
		return out, err
	}
	head, err = run("git", "-C", c.WorktreePath, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head.Stdout) != c.CandidateSHA {
		return out, fmt.Errorf("candidate changed during deployment checks")
	}
	for _, item := range []struct {
		service  string
		old, new *string
	}{{"backend", &out.PreviousBackendDigest, &out.BackendDigest}, {"frontend", &out.PreviousWebDigest, &out.WebDigest}} {
		id, e := run("docker", composeArgs(cfg, "ps", "-q", item.service)...)
		if e != nil || strings.TrimSpace(id.Stdout) == "" {
			return out, fmt.Errorf("running service identity unavailable")
		}
		old, e := run("docker", "inspect", "--format", "{{.Image}}", strings.TrimSpace(id.Stdout))
		if e != nil || !imageDigest.MatchString(strings.TrimSpace(old.Stdout)) {
			return out, fmt.Errorf("old image identity unavailable")
		}
		*item.old = strings.TrimSpace(old.Stdout)
		image := "backend"
		if item.service == "frontend" {
			image = "web"
		}
		next, e := run("docker", "image", "inspect", "--format", "{{.Id}}", "multica-"+image+":"+c.CandidateSHA)
		if e != nil || !imageDigest.MatchString(strings.TrimSpace(next.Stdout)) {
			return out, fmt.Errorf("candidate image identity unavailable")
		}
		*item.new = strings.TrimSpace(next.Stdout)
	}
	out.DatabaseBackup, err = BackupDatabase(ctx, r, cfg, c.WorktreePath)
	if err != nil {
		return out, err
	}
	// Discover bindings before replacement so an invalid/remote binding fails closed.
	for _, item := range []struct {
		service, port string
		dest          *string
	}{{"backend", "8080", &out.ReadyURL}, {"frontend", "3000", &out.LocalURL}} {
		result, e := run("docker", composeArgs(cfg, "port", item.service, item.port)...)
		if e != nil {
			return out, e
		}
		*item.dest, e = publishedURL(result.Stdout)
		if e != nil {
			return out, e
		}
	}
	out.ReadyURL += "/readyz"
	if err = preserveDeployment(cfg, c, out); err != nil {
		return out, err
	}
	if _, err = run("docker", composeArgs(cfg, "up", "-d", "--no-deps", "backend", "frontend")...); err != nil {
		return out, err
	}
	for _, item := range []struct{ service, digest string }{{"backend", out.BackendDigest}, {"frontend", out.WebDigest}} {
		id, e := run("docker", composeArgs(cfg, "ps", "-q", item.service)...)
		if e != nil || strings.TrimSpace(id.Stdout) == "" {
			return out, fmt.Errorf("deployed service unavailable")
		}
		image, e := run("docker", "inspect", "--format", "{{.Image}}", strings.TrimSpace(id.Stdout))
		if e != nil || strings.TrimSpace(image.Stdout) != item.digest {
			return out, fmt.Errorf("deployed image differs from built candidate")
		}
	}
	if err = runningDatabase(ctx, r, cfg, c.WorktreePath, env); err != nil {
		return out, err
	}
	// Read published bindings again after replacement; preflight bindings alone
	// cannot prove the new service is reachable on the recorded URL.
	for _, item := range []struct {
		service, port string
		dest          *string
	}{{"backend", "8080", &out.ReadyURL}, {"frontend", "3000", &out.LocalURL}} {
		result, e := run("docker", composeArgs(cfg, "port", item.service, item.port)...)
		if e != nil {
			return out, e
		}
		*item.dest, e = publishedURL(result.Stdout)
		if e != nil {
			return out, e
		}
	}
	out.ReadyURL += "/readyz"
	return out, nil
}

func preserveDeployment(cfg WorkerConfig, c Candidate, e DeployEvidence) error {
	root := filepath.Join(cfg.StateDir, "deployments")
	if err := privateDir(root); err != nil {
		return err
	}
	path := filepath.Join(root, c.JobID+".json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err = json.NewEncoder(file).Encode(struct {
		Candidate Candidate
		Evidence  DeployEvidence
	}{c, e}); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
