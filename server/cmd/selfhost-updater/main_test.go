package main

import (
	"context"
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/selfupdate"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type neverRunner struct{ t *testing.T }

func (r neverRunner) Run(context.Context, string, []string, string, []string) (selfupdate.CommandResult, error) {
	r.t.Fatal("unexpected host command")
	return selfupdate.CommandResult{}, nil
}
func (r neverRunner) RunToFile(context.Context, string, []string, string, []string, string) error {
	r.t.Fatal("unexpected file command")
	return nil
}
func TestServeCLI(t *testing.T) {
	for _, args := range [][]string{nil, {"bootstrap"}, {"serve"}, {"serve", "--config", "relative.json"}, {"serve", "--config", "/missing", "extra"}} {
		if err := run(context.Background(), args, neverRunner{t}); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(root, "production.env")
	if err = os.WriteFile(env, []byte("POSTGRES_DB=production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	claims := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/self-update/worker/active-job" {
			w.WriteHeader(204)
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("missing worker token")
		}
		if r.URL.Path == "/api/self-update/worker/claim" {
			claims++
			w.WriteHeader(204)
			cancel()
		}
	}))
	defer server.Close()
	cfg := selfupdate.WorkerConfig{RepositoryDir: filepath.Join(root, "repo.git"), WorktreeRoot: filepath.Join(root, "trees"), StateDir: filepath.Join(root, "state"), ProductionEnvFile: env, ProductionDatabase: "production", CustomRemote: "https://github.com/example/multica.git", CustomBranch: "custom", ServerURL: server.URL, WorkerToken: "private-token"}
	bytes, _ := json.Marshal(cfg)
	path := filepath.Join(root, "config.json")
	if err = os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	_ = run(ctx, []string{"serve", "--config", path, "--dry-run"}, neverRunner{t})
	if claims != 1 {
		t.Fatalf("serve did not poll: %d", claims)
	}
}
