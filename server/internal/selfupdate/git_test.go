package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const baseSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const releaseSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const mergedSHA = "cccccccccccccccccccccccccccccccccccccccc"
const testJobID = "11111111-1111-4111-8111-111111111111"

type recordedCommand struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}
type fakeRunner struct {
	calls                                                    []recordedCommand
	fail                                                     string
	movedBranch, movedTag, existing, copiedEnv, productionDB bool
	oldHelper                                                bool
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, dir string, env []string) (CommandResult, error) {
	f.calls = append(f.calls, recordedCommand{name, append([]string(nil), args...), dir, env})
	cmd := strings.Join(args, " ")
	if strings.Contains(cmd, "--is-bare-repository") {
		return CommandResult{Stdout: "true\n"}, nil
	}
	if f.fail != "" && strings.Contains(cmd, f.fail) {
		return CommandResult{ExitCode: 1, Stderr: "secret must not escape"}, errors.New("failed")
	}
	if strings.Contains(cmd, "ls-remote") {
		if strings.Contains(cmd, "refs/heads/self-update/") {
			if f.existing {
				return CommandResult{Stdout: mergedSHA + "\trefs/heads/self-update/" + testJobID + "\n"}, nil
			}
			return CommandResult{}, nil
		}
		sha := baseSHA
		if f.movedBranch {
			sha = mergedSHA
		}
		return CommandResult{Stdout: sha + "\trefs/heads/custom\n"}, nil
	}
	if strings.Contains(cmd, "rev-parse refs/self-update/base") {
		return CommandResult{Stdout: baseSHA + "\n"}, nil
	}
	if strings.Contains(cmd, "rev-parse refs/self-update/release") {
		sha := releaseSHA
		if f.movedTag {
			sha = mergedSHA
		}
		return CommandResult{Stdout: sha + "\n"}, nil
	}
	if strings.Contains(cmd, "rev-parse HEAD") {
		return CommandResult{Stdout: mergedSHA + "\n"}, nil
	}
	if strings.Contains(cmd, "worktree add") {
		if err := os.MkdirAll(dirForWorktree(args), 0700); err != nil {
			return CommandResult{}, err
		}
		if f.copiedEnv {
			_ = os.WriteFile(filepath.Join(dirForWorktree(args), ".env"), []byte("POSTGRES_DB=production"), 0600)
		}
		scripts := filepath.Join(dirForWorktree(args), "scripts")
		if err := os.MkdirAll(scripts, 0700); err != nil {
			return CommandResult{}, err
		}
		helper := "MULTICA_SELF_UPDATE_NO_CONTAINER_START"
		if f.oldHelper {
			helper = "old helper"
		}
		if err := os.WriteFile(filepath.Join(scripts, "ensure-postgres.sh"), []byte(helper), 0600); err != nil {
			return CommandResult{}, err
		}
	}
	if name == "make" && args[0] == "worktree-env" {
		db := "candidate_db"
		if f.productionDB {
			db = "production"
		}
		return CommandResult{}, os.WriteFile(filepath.Join(dir, ".env.worktree"), []byte("POSTGRES_DB="+db+"\nDATABASE_URL=postgres://user:password@localhost/"+db+"?sslmode=disable\n"), 0600)
	}
	return CommandResult{}, nil
}
func dirForWorktree(args []string) string {
	for i, v := range args {
		if v == "--detach" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func (f *fakeRunner) RunToFile(context.Context, string, []string, string, []string, string) error {
	return errors.New("unexpected file command")
}
func candidateFixture(t *testing.T) (WorkerConfig, Job) {
	t.Helper()
	root := canonicalTempDir(t)
	env := filepath.Join(root, "production.env")
	if err := os.WriteFile(env, []byte("POSTGRES_DB=production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return WorkerConfig{RepositoryDir: filepath.Join(root, "repo.git"), WorktreeRoot: filepath.Join(root, "worktrees"), StateDir: filepath.Join(root, "state"), ProductionEnvFile: env, ProductionDatabase: "production", CustomRemote: "https://github.com/example/multica.git", CustomBranch: "custom", ServerURL: "http://localhost:8080", WorkerToken: "test-secret"}, Job{ID: testJobID, SourceSHA: baseSHA, TargetSHA: releaseSHA, TargetTag: "v1.2.3", Phase: "preparing"}
}
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestPrepareCandidate(t *testing.T) {
	for _, tc := range []struct {
		name, fail                          string
		branch, tag, existing, env, db, dry bool
		oldHelper                           bool
		wantErr, repair                     bool
	}{
		{name: "success"}, {name: "dry run", dry: true}, {name: "moved branch", branch: true, wantErr: true}, {name: "moved tag", tag: true, wantErr: true},
		{name: "conflicting staging ref", existing: true, wantErr: true}, {name: "copied env", env: true, wantErr: true}, {name: "production database", db: true, wantErr: true},
		{name: "old postgres helper", oldHelper: true, wantErr: true},
		{name: "merge conflict", fail: "merge --no-ff", wantErr: true, repair: true}, {name: "failed checks", fail: "check-worktree", wantErr: true, repair: true}, {name: "failed image", fail: "Dockerfile.web", wantErr: true, repair: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, job := candidateFixture(t)
			cfg.DryRun = tc.dry
			fake := &fakeRunner{fail: tc.fail, movedBranch: tc.branch, movedTag: tc.tag, existing: tc.existing, copiedEnv: tc.env, productionDB: tc.db, oldHelper: tc.oldHelper}
			candidate, err := PrepareCandidate(context.Background(), fake, cfg, job)
			if (err != nil) != tc.wantErr {
				t.Fatalf("candidate=%+v error=%v", candidate, err)
			}
			var repair *CandidateError
			if tc.repair && (!errors.As(err, &repair) || !repair.Repairable) {
				t.Fatalf("failure must preserve repair candidate: %v", err)
			}
			if !tc.wantErr && candidate.CandidateSHA != mergedSHA {
				t.Fatalf("unpinned candidate: %+v", candidate)
			}
			mergeAt, pushBase, pushMerged, checkAt := -1, -1, -1, -1
			for i, call := range fake.calls {
				if call.Name == "make" && !containsString(call.Env, "MULTICA_SELF_UPDATE_NO_CONTAINER_START=1") {
					t.Fatal("candidate make may start postgres containers")
				}
				cmd := strings.Join(call.Args, " ")
				if strings.Contains(cmd, "--force") || call.Name == "sh" {
					t.Fatalf("unsafe command: %+v", call)
				}
				if strings.Contains(cmd, "merge --no-ff") {
					mergeAt = i
				}
				if strings.Contains(cmd, "push") {
					if tc.dry {
						t.Fatal("dry run pushed")
					}
					if !strings.Contains(cmd, ":refs/heads/self-update/"+job.ID) {
						t.Fatalf("custom branch pushed before review: %s", cmd)
					}
					if strings.Contains(cmd, baseSHA+":") {
						pushBase = i
					}
					if strings.Contains(cmd, mergedSHA+":") {
						pushMerged = i
					}
				}
				if call.Name == "make" && call.Args[0] == "check-worktree" {
					checkAt = i
				}
				for _, v := range call.Env {
					if strings.HasPrefix(v, "DATABASE_URL=") || strings.Contains(v, cfg.ProductionEnvFile) {
						t.Fatalf("production environment leaked: %s", v)
					}
				}
				if call.Name == "docker" && !strings.Contains(cmd, "build ") {
					t.Fatalf("container operation: %s", cmd)
				}
			}
			if mergeAt >= 0 && !tc.dry && (pushBase < 0 || pushBase > mergeAt) {
				t.Fatal("base staging ref must be pushed before merge")
			}
			if checkAt >= 0 && !tc.dry && (pushMerged < 0 || pushMerged > checkAt) {
				t.Fatal("merged staging ref must be pushed before checks")
			}
			if tc.env || tc.db {
				if checkAt >= 0 {
					t.Fatal("unsafe candidate reached checks")
				}
			}
			if mergeAt >= 0 {
				if _, statErr := os.Stat(candidate.WorktreePath); statErr != nil {
					t.Fatal("candidate worktree was removed")
				}
				if tc.fail == "merge --no-ff" && (candidate.CandidateSHA != "" || pushMerged >= 0) {
					t.Fatal("conflict must preserve staging at base")
				}
				if (tc.fail == "check-worktree" || tc.fail == "Dockerfile.web") && candidate.CandidateSHA != mergedSHA {
					t.Fatal("failed checks lost merged repair candidate")
				}
			}
			if tc.existing && mergeAt >= 0 {
				t.Fatal("conflicting ref reached merge")
			}
		})
	}
}

func TestExecRunnerPreservesArgumentsAndExclusiveOutput(t *testing.T) {
	root := canonicalTempDir(t)
	tool := filepath.Join(root, "fake-command")
	script := "#!/bin/bash\nprintf '%s' \"$1\"\nprintf 'diagnostic' >&2\nexit \"${2:-0}\"\n"
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := ExecRunner{}
	literal := "$(touch must-not-exist); `touch also-missing`"
	result, err := runner.Run(context.Background(), tool, []string{literal, "7"}, root, commandEnvironment())
	if err == nil || result.ExitCode != 7 || result.Stdout != literal || result.Stderr != "diagnostic" {
		t.Fatalf("lost exact command result: %+v %v", result, err)
	}
	for _, name := range []string{"must-not-exist", "also-missing"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("shell evaluated arguments")
		}
	}
	output := filepath.Join(root, "output")
	if err := runner.RunToFile(context.Background(), tool, []string{"backup"}, root, commandEnvironment(), output); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(output)
	info, _ := os.Stat(output)
	if string(data) != "backup" || info.Mode().Perm() != 0600 {
		t.Fatal("output not private or incorrect")
	}
	if err := runner.RunToFile(context.Background(), tool, []string{"replacement"}, root, commandEnvironment(), output); err == nil {
		t.Fatal("existing output overwritten")
	}
}
func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestCommandEnvironmentPreservesProxyVariables(t *testing.T) {
	root := canonicalTempDir(t)
	tool := filepath.Join(root, "env-dump")
	script := "#!/bin/bash\nenv | sort\n"
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	keys := []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"}
	for _, key := range keys {
		t.Setenv(key, "proxy-value-for-"+key)
	}
	result, err := ExecRunner{}.Run(context.Background(), tool, nil, root, commandEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	stdout := result.Stdout
	for _, key := range keys {
		if !strings.Contains(stdout, key+"=proxy-value-for-"+key) {
			t.Fatalf("missing %s in child env:\n%s", key, stdout)
		}
	}
}
