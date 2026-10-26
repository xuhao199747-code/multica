package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type deployRunner struct {
	calls                   []recordedCommand
	fail, databaseURL, port string
	emptyDump               bool
	upDone, wrongImage      bool
}

func (f *deployRunner) Run(_ context.Context, name string, args []string, dir string, env []string) (CommandResult, error) {
	f.calls = append(f.calls, recordedCommand{name, args, dir, env})
	s := strings.Join(args, " ")
	if strings.Contains(s, " up ") {
		f.upDone = true
	}
	if f.fail != "" && strings.Contains(s, f.fail) {
		return CommandResult{ExitCode: 1}, errors.New("failed")
	}
	switch {
	case strings.Contains(s, "printenv DATABASE_URL"):
		return CommandResult{Stdout: f.databaseURL}, nil
	case strings.Contains(s, "inspect --format {{.Image}}"):
		if f.upDone && !f.wrongImage {
			return CommandResult{Stdout: "sha256:" + strings.Repeat("b", 64)}, nil
		}
		return CommandResult{Stdout: "sha256:" + strings.Repeat("a", 64)}, nil
	case strings.Contains(s, "image inspect"):
		return CommandResult{Stdout: "sha256:" + strings.Repeat("b", 64)}, nil
	case strings.Contains(s, "ps -q"):
		return CommandResult{Stdout: "container-id"}, nil
	case strings.Contains(s, " port "):
		if f.port != "" {
			return CommandResult{Stdout: f.port}, nil
		}
		return CommandResult{Stdout: "127.0.0.1:12345\n"}, nil
	case strings.Contains(s, "rev-parse HEAD"):
		return CommandResult{Stdout: mergedSHA}, nil
	}
	return CommandResult{}, nil
}
func (f *deployRunner) RunToFile(_ context.Context, name string, args []string, dir string, env []string, path string) error {
	f.calls = append(f.calls, recordedCommand{name, args, dir, env})
	if f.fail == "pg_dump" {
		return errors.New("dump failed")
	}
	data := []byte("backup")
	if f.emptyDump {
		data = nil
	}
	return os.WriteFile(path, data, 0600)
}
func deploymentFixture(t *testing.T) (WorkerConfig, Candidate, *deployRunner) {
	cfg, _ := candidateFixture(t)
	c := Candidate{JobID: testJobID, CandidateSHA: mergedSHA, BaseSHA: baseSHA, ReleaseSHA: releaseSHA, WorktreePath: filepath.Join(cfg.WorktreeRoot, testJobID)}
	if err := os.MkdirAll(filepath.Join(c.WorktreePath, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{cfg.ProductionEnvFile: "POSTGRES_DB=production\nPOSTGRES_USER=multica\nMULTICA_APP_URL=https://site.example\n", filepath.Join(c.WorktreePath, ".env.worktree"): "POSTGRES_DB=candidate\nDATABASE_URL=postgres://localhost/candidate\n", filepath.Join(c.WorktreePath, "scripts/ensure-postgres.sh"): "MULTICA_SELF_UPDATE_NO_CONTAINER_START"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, c, &deployRunner{databaseURL: "postgres://user:secret@postgres/production"}
}
func TestBackupDatabase(t *testing.T) {
	for _, mode := range []string{"success", "mismatch", "override", "encoded override", "missing env", "dump failed", "empty"} {
		t.Run(mode, func(t *testing.T) {
			cfg, c, f := deploymentFixture(t)
			switch mode {
			case "mismatch":
				f.databaseURL = "postgres://localhost/other"
			case "override":
				f.databaseURL += "?dbname=other"
			case "encoded override":
				f.databaseURL += "?%64atabase=%6fther"
			case "missing env":
				cfg.ProductionEnvFile += ".missing"
			case "dump failed":
				f.fail = "pg_dump"
			case "empty":
				f.emptyDump = true
			}
			path, err := BackupDatabase(context.Background(), f, cfg, c.WorktreePath)
			if mode != "success" {
				if err == nil {
					t.Fatal("unsafe backup accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Size() == 0 || info.Mode().Perm() != 0600 {
				t.Fatalf("unsafe backup: %v %v", info, err)
			}
			for _, call := range f.calls {
				if strings.Contains(strings.Join(call.Args, " "), " up ") {
					t.Fatal("backup starts containers")
				}
			}
		})
	}
}
