package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerConfig(t *testing.T) {
	cfg, _ := candidateFixture(t)
	path := filepath.Join(canonicalTempDir(t), "config.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkerConfig(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkerConfig(path); err == nil {
		t.Fatal("world-readable config accepted")
	}
	if _, err := LoadWorkerConfig("relative.json"); err == nil {
		t.Fatal("relative config accepted")
	}
	for _, change := range []func(*WorkerConfig){func(c *WorkerConfig) { c.CustomBranch = "--help" }, func(c *WorkerConfig) { c.CustomRemote = "https://secret:token@github.com/example/repo" }, func(c *WorkerConfig) { c.ProductionEnvFile = filepath.Join(c.WorktreeRoot, "production.env") }, func(c *WorkerConfig) { c.ProductionDatabase = "" }, func(c *WorkerConfig) { c.RepositoryDir = c.WorktreeRoot }, func(c *WorkerConfig) { c.WorkerToken = "" }, func(c *WorkerConfig) { c.ServerURL = "https://token@host/path" }} {
		bad := cfg
		change(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("unsafe configuration accepted: %+v", bad)
		}
	}
	if err := os.Chmod(cfg.ProductionEnvFile, 0644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("public production env accepted")
	}
}

func TestCandidateEnvironmentRejectsOverrides(t *testing.T) {
	for _, extra := range []string{"ENV_FILE=/protected/production.env\n", "MULTICA_SELF_UPDATE_NO_CONTAINER_START=0\n", "export MULTICA_SELF_UPDATE_NO_CONTAINER_START=0\n", "BASH_ENV=/tmp/inject\n", "JWT_SECRET=x; touch /tmp/inject\n", "DATABASE_URL=postgres://localhost/production\n"} {
		t.Run(extra, func(t *testing.T) {
			root := canonicalTempDir(t)
			data := "POSTGRES_DB=candidate\nDATABASE_URL=postgres://localhost/candidate\n" + extra
			if err := os.WriteFile(filepath.Join(root, ".env.worktree"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := candidateEnvironment(root, "production"); err == nil {
				t.Fatal("accepted unsafe environment override")
			}
		})
	}
}

func TestProductionEnvOutsideCheckout(t *testing.T) {
	cfg, _ := candidateFixture(t)
	checkout := filepath.Join(canonicalTempDir(t), "checkout")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg.ProductionEnvFile = filepath.Join(checkout, "production.env")
	if err := os.WriteFile(cfg.ProductionEnvFile, []byte("POSTGRES_DB=production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted production env inside a source checkout")
	}
}

func TestCandidateEnvironmentEffectiveDatabase(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		wantErr     bool
	}{
		{name: "plain candidate"},
		{name: "candidate dbname", query: "?dbname=candidate"},
		{name: "candidate database", query: "?database=candidate"},
		{name: "dbname overrides path", query: "?dbname=production", wantErr: true},
		{name: "database overrides path", query: "?database=production", wantErr: true},
		{name: "encoded dbname key", query: "?%64bname=production", wantErr: true},
		{name: "encoded database key", query: "?data%62ase=production", wantErr: true},
		{name: "encoded dbname value", query: "?dbname=%70roduction", wantErr: true},
		{name: "encoded database value", query: "?database=%70roduction", wantErr: true},
		{name: "encoded key and value", query: "?%64%61%74%61%62%61%73%65=%70%72%6f%64%75%63%74%69%6f%6e", wantErr: true},
		{name: "different nonproduction override", query: "?dbname=other_database", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := canonicalTempDir(t)
			data := "POSTGRES_DB=candidate\nDATABASE_URL=postgres://test_user:test_password@localhost/candidate" + tc.query + "\n"
			if err := os.WriteFile(filepath.Join(root, ".env.worktree"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := candidateEnvironment(root, "production"); (err != nil) != tc.wantErr {
				t.Fatalf("effective database validation error=%v, want rejection=%v", err, tc.wantErr)
			}
		})
	}
}
