package selfupdate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type CommandRunner interface {
	Run(context.Context, string, []string, string, []string) (CommandResult, error)
	RunToFile(context.Context, string, []string, string, []string, string) error
}
type CommandResult struct {
	Stdout, Stderr string
	ExitCode       int
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, dir string, env []string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = -1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}, err
}
func (ExecRunner) RunToFile(ctx context.Context, name string, args []string, dir string, env []string, path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = file
	if err = cmd.Run(); err != nil {
		return err
	}
	return file.Sync()
}

type Job struct {
	ID            string `json:"id"`
	SourceSHA     string `json:"source_sha"`
	TargetSHA     string `json:"target_sha"`
	TargetTag     string `json:"target_tag"`
	Phase         string `json:"phase"`
	CandidateSHA  string `json:"candidate_sha"`
	StagingBranch string `json:"staging_branch"`
	RepairCount   int    `json:"repair_count"`
	RetryCount    int    `json:"retry_count"`
}
type Candidate struct{ JobID, BaseSHA, ReleaseSHA, CandidateSHA, StagingBranch, WorktreePath string }
type CandidateError struct {
	Step       string
	Repairable bool
}

func (e *CandidateError) Error() string { return "candidate preparation failed: " + e.Step }

// PrepareCandidate never operates in the operator's source checkout. All Git
// state belongs to a dedicated bare repository and a job-specific worktree.
func PrepareCandidate(ctx context.Context, runner CommandRunner, cfg WorkerConfig, job Job) (Candidate, error) {
	var candidate Candidate
	if err := cfg.Validate(); err != nil {
		return candidate, err
	}
	if !jobID.MatchString(job.ID) || !commitSHA.MatchString(job.SourceSHA) || !commitSHA.MatchString(job.TargetSHA) || !validRef(job.TargetTag) || job.Phase != "preparing" {
		return candidate, fmt.Errorf("invalid claimed job")
	}
	candidate = Candidate{JobID: job.ID, BaseSHA: job.SourceSHA, ReleaseSHA: job.TargetSHA, StagingBranch: "self-update/" + job.ID, WorktreePath: filepath.Join(cfg.WorktreeRoot, job.ID)}
	if _, err := os.Lstat(filepath.Join(cfg.StateDir, job.ID)); !os.IsNotExist(err) {
		return candidate, fmt.Errorf("existing journal requires reconciliation")
	}
	if err := privateDir(cfg.StateDir); err != nil {
		return candidate, err
	}
	if err := privateDir(cfg.WorktreeRoot); err != nil {
		return candidate, err
	}
	journal := Journal{Job: job, Candidate: candidate, Phase: "preparing"}
	if err := SaveJournal(cfg.StateDir, journal); err != nil {
		return candidate, err
	}
	env := commandEnvironment()
	gitPrefix := []string{"--git-dir=" + cfg.RepositoryDir, "-c", "core.hooksPath=/dev/null", "-c", "protocol.file.allow=never"}
	run := func(name string, args []string, dir string) (CommandResult, error) {
		result, err := runner.Run(ctx, name, args, dir, env)
		code := result.ExitCode
		if err != nil && code == 0 {
			code = -1
		}
		safeArgs := make([]string, len(args))
		for i, arg := range args {
			safeArgs[i] = redactArgument(arg, cfg, candidate)
		}
		journal.Outcomes = append(journal.Outcomes, CommandOutcome{name, safeArgs, code})
		journal.Candidate = candidate
		if saveErr := SaveJournal(cfg.StateDir, journal); saveErr != nil {
			return result, saveErr
		}
		if err != nil || code != 0 {
			return result, fmt.Errorf("command failed")
		}
		return result, nil
	}
	git := func(args ...string) (CommandResult, error) {
		return run("git", append(append([]string(nil), gitPrefix...), args...), cfg.StateDir)
	}
	fail := func(step string, repair bool) (Candidate, error) {
		return candidate, &CandidateError{Step: step, Repairable: repair}
	}
	if _, err := os.Lstat(cfg.RepositoryDir); os.IsNotExist(err) {
		if _, err = run("git", []string{"init", "--bare", cfg.RepositoryDir}, cfg.StateDir); err != nil {
			return fail("repository", false)
		}
	} else if err != nil {
		return fail("repository", false)
	}
	bare, err := git("rev-parse", "--is-bare-repository")
	if err != nil || strings.TrimSpace(bare.Stdout) != "true" {
		return fail("repository", false)
	}
	baseRef := "refs/self-update/base/" + job.ID
	releaseRef := "refs/self-update/release/" + job.ID
	if _, err = git("fetch", "--no-tags", cfg.CustomRemote, "refs/heads/"+cfg.CustomBranch+":"+baseRef); err != nil {
		return fail("fetch_base", false)
	}
	base, err := git("rev-parse", baseRef+"^{commit}")
	if err != nil || strings.TrimSpace(base.Stdout) != job.SourceSHA {
		return fail("moved_branch", false)
	}
	if _, err = git("fetch", "--no-tags", "https://github.com/multica-ai/multica.git", "refs/tags/"+job.TargetTag+":"+releaseRef); err != nil {
		return fail("fetch_release", false)
	}
	release, err := git("rev-parse", releaseRef+"^{commit}")
	if err != nil || strings.TrimSpace(release.Stdout) != job.TargetSHA {
		return fail("moved_tag", false)
	}
	remote, err := git("ls-remote", "--heads", cfg.CustomRemote, "refs/heads/"+cfg.CustomBranch)
	if err != nil || !remoteEquals(remote.Stdout, job.SourceSHA, "refs/heads/"+cfg.CustomBranch) {
		return fail("moved_branch", false)
	}
	staging, err := git("ls-remote", "--heads", cfg.CustomRemote, "refs/heads/"+candidate.StagingBranch)
	if err != nil || strings.TrimSpace(staging.Stdout) != "" {
		return fail("existing_staging", false)
	}
	if _, err = git("branch", candidate.StagingBranch, job.SourceSHA); err != nil {
		return fail("existing_staging", false)
	}
	if !cfg.DryRun {
		if _, err = git("push", cfg.CustomRemote, job.SourceSHA+":refs/heads/"+candidate.StagingBranch); err != nil {
			return fail("staging_push", false)
		}
	}
	if _, err = os.Lstat(candidate.WorktreePath); !os.IsNotExist(err) {
		return fail("existing_worktree", false)
	}
	if _, err = git("worktree", "add", "--detach", candidate.WorktreePath, job.SourceSHA); err != nil {
		return fail("worktree", false)
	}
	if _, err = os.Lstat(filepath.Join(candidate.WorktreePath, ".env")); !os.IsNotExist(err) {
		return fail("copied_env", false)
	}
	// Use -C rather than the bare --git-dir so merge changes only this worktree.
	workGit := func(args ...string) (CommandResult, error) {
		return run("git", append([]string{"-C", candidate.WorktreePath, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Multica updater", "-c", "user.email=self-update@localhost"}, args...), candidate.WorktreePath)
	}
	if _, err = workGit("merge", "--no-ff", "--no-edit", job.TargetSHA); err != nil {
		return fail("merge_conflict", true)
	}
	head, err := workGit("rev-parse", "HEAD")
	if err != nil || !commitSHA.MatchString(strings.TrimSpace(head.Stdout)) {
		return fail("candidate_sha", false)
	}
	candidate.CandidateSHA = strings.TrimSpace(head.Stdout)
	if _, err = git("update-ref", "refs/heads/"+candidate.StagingBranch, candidate.CandidateSHA, job.SourceSHA); err != nil {
		return fail("staging_ref", false)
	}
	if !cfg.DryRun {
		if _, err = git("push", cfg.CustomRemote, candidate.CandidateSHA+":refs/heads/"+candidate.StagingBranch); err != nil {
			return fail("staging_push", false)
		}
	}
	// An older candidate helper ignores the no-start flag. Refuse it before any
	// setup/check target can reach an unconditional Compose up.
	helperPath := filepath.Join(candidate.WorktreePath, "scripts", "ensure-postgres.sh")
	if err = noSymlinks(helperPath); err != nil {
		return fail("unsafe_postgres_helper", false)
	}
	helper, err := os.ReadFile(helperPath)
	if err != nil || !strings.Contains(string(helper), "MULTICA_SELF_UPDATE_NO_CONTAINER_START") {
		return fail("unsafe_postgres_helper", false)
	}
	makeArgs := func(target string) []string {
		return []string{target, "ENV_FILE=.env.worktree", "WORKTREE_ENV_FILE=.env.worktree"}
	}
	if _, err = run("make", makeArgs("worktree-env"), candidate.WorktreePath); err != nil {
		return fail("candidate_env", false)
	}
	if err = candidateEnvironment(candidate.WorktreePath, cfg.ProductionDatabase); err != nil {
		return fail("candidate_env", false)
	}
	if _, err = run("make", makeArgs("setup-worktree"), candidate.WorktreePath); err != nil {
		return fail("setup", false)
	}
	if err = candidateEnvironment(candidate.WorktreePath, cfg.ProductionDatabase); err != nil {
		return fail("candidate_env", false)
	}
	if _, err = run("make", makeArgs("check-worktree"), candidate.WorktreePath); err != nil {
		return fail("checks", true)
	}
	for _, image := range []struct{ name, file string }{{"backend", "Dockerfile"}, {"web", "Dockerfile.web"}} {
		if _, err = run("docker", []string{"build", "--file", image.file, "--tag", "multica-" + image.name + ":" + candidate.CandidateSHA, "--build-arg", "COMMIT=" + candidate.CandidateSHA, "."}, candidate.WorktreePath); err != nil {
			return fail("build", true)
		}
	}
	journal.Candidate = candidate
	journal.Phase = "prepared"
	if err = SaveJournal(cfg.StateDir, journal); err != nil {
		return candidate, err
	}
	return candidate, nil
}
func remoteEquals(output, sha, ref string) bool {
	fields := strings.Fields(output)
	return len(fields) == 2 && fields[0] == sha && fields[1] == ref
}
func commandEnvironment() []string {
	env := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "MAKEFLAGS=", "MULTICA_SELF_UPDATE_NO_CONTAINER_START=1"}
	// Preserve proxy variables so child git/docker invocations reach the
	// network when the host requires an HTTP(S)/SOCKS proxy. Without these,
	// git ls-remote/fetch to GitHub hang or fail on hosts whose direct
	// egress is blocked but whose system proxy works.
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "SSH_AUTH_SOCK", "LANG", "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}
func redactArgument(arg string, cfg WorkerConfig, c Candidate) string {
	for _, item := range []struct{ value, label string }{{cfg.WorkerToken, "[token]"}, {cfg.ProductionEnvFile, "[production-env]"}, {c.WorktreePath, "[worktree]"}, {cfg.RepositoryDir, "[repository]"}, {cfg.StateDir, "[state]"}, {cfg.CustomRemote, "[custom-remote]"}} {
		if item.value != "" {
			arg = strings.ReplaceAll(arg, item.value, item.label)
		}
	}
	return arg
}
