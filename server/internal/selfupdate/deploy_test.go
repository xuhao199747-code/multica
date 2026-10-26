package selfupdate

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type bootstrapRunner struct {
	deployRunner
	moved, unrelated bool
	tree             string
	copiedEnv        bool
	notBare          bool
}

func (f *bootstrapRunner) Run(ctx context.Context, name string, args []string, dir string, env []string) (CommandResult, error) {
	s := strings.Join(args, " ")
	if strings.Contains(s, "--is-bare-repository") {
		if f.notBare {
			return CommandResult{Stdout: "false"}, nil
		}
		return CommandResult{Stdout: "true"}, nil
	}
	if strings.Contains(s, "ls-remote") {
		f.calls = append(f.calls, recordedCommand{name, args, dir, env})
		sha := mergedSHA
		if f.moved {
			sha = baseSHA
		}
		return CommandResult{Stdout: sha + "\trefs/heads/custom"}, nil
	}
	if strings.Contains(s, "rev-parse refs/self-update/bootstrap-release") {
		return CommandResult{Stdout: releaseSHA}, nil
	}
	if strings.Contains(s, "rev-parse refs/self-update/bootstrap-source") {
		return CommandResult{Stdout: mergedSHA}, nil
	}
	if strings.Contains(s, "merge-base --is-ancestor") && f.unrelated {
		return CommandResult{ExitCode: 1}, nil
	}
	if strings.Contains(s, "worktree add") {
		f.tree = dirForWorktree(args)
		if err := os.MkdirAll(filepath.Join(f.tree, "scripts"), 0700); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(filepath.Join(f.tree, "scripts/ensure-postgres.sh"), []byte("MULTICA_SELF_UPDATE_NO_CONTAINER_START"), 0600); err != nil {
			return CommandResult{}, err
		}
		if f.copiedEnv {
			if err := os.WriteFile(filepath.Join(f.tree, ".env"), []byte("POSTGRES_DB=production"), 0600); err != nil {
				return CommandResult{}, err
			}
		}
	}
	if name == "make" && args[0] == "worktree-env" {
		if err := os.WriteFile(filepath.Join(dir, ".env.worktree"), []byte("POSTGRES_DB=candidate\nDATABASE_URL=postgres://localhost/candidate\n"), 0600); err != nil {
			return CommandResult{}, err
		}
	}
	return f.deployRunner.Run(ctx, name, args, dir, env)
}
func TestBootstrap(t *testing.T) {
	for _, mode := range []string{"dry", "apply", "moved", "unrelated", "health failure", "register failure", "malformed registration", "copied env", "incomplete preflight", "registered", "active job", "nonbare repository"} {
		t.Run(mode, func(t *testing.T) {
			cfg, _, base := deploymentFixture(t)
			f := &bootstrapRunner{deployRunner: *base, moved: mode == "moved", unrelated: mode == "unrelated"}
			f.copiedEnv = mode == "copied env"
			f.notBare = mode == "nonbare repository"
			registrations := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/self-update/worker/bootstrap" {
					if r.Method == http.MethodGet {
						if mode == "incomplete preflight" {
							w.Write([]byte(`{}`))
							return
						}
						if mode == "registered" {
							w.Write([]byte(`{"registered":true,"active_job":false}`))
							return
						}
						if mode == "active job" {
							w.Write([]byte(`{"registered":false,"active_job":true}`))
							return
						}
						w.Write([]byte(`{"registered":false,"active_job":false}`))
						return
					}
					registrations++
					if r.Header.Get("Authorization") != "Bearer test-secret" {
						t.Error("missing bearer")
					}
					if mode == "register failure" {
						w.WriteHeader(503)
						return
					}
					if mode == "malformed registration" {
						w.Write([]byte(`{}`))
						return
					}
					w.Write([]byte(`{"registered":true}`))
					return
				}
				if r.URL.Path == "/readyz" {
					if mode == "health failure" {
						w.WriteHeader(503)
						return
					}
					w.Write([]byte(`{"checks":{"db":"ok","migrations":"ok"}}`))
				}
			}))
			defer s.Close()
			cfg.ServerURL = s.URL
			// Route all test health requests through the local fake HTTP service.
			client := s.Client()
			client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				clone := req.Clone(req.Context())
				u, _ := url.Parse(s.URL)
				clone.URL.Scheme = u.Scheme
				clone.URL.Host = u.Host
				return http.DefaultTransport.RoundTrip(clone)
			})
			var report bytes.Buffer
			err := Bootstrap(context.Background(), f, cfg, mergedSHA, "v1.2.3", mode != "dry", &report, client)
			wantErr := mode != "dry" && mode != "apply"
			if (err != nil) != wantErr {
				t.Fatalf("mode=%s err=%v report=%s", mode, err, report.String())
			}
			if mode == "dry" {
				if !strings.Contains(report.String(), "local_frontend=http://127.0.0.1:3000") || !strings.Contains(report.String(), "backend_ready=http://127.0.0.1:8080/readyz") {
					t.Fatal("dry run omits exact planned URLs")
				}
				if registrations != 0 || f.tree != "" {
					t.Fatal("dry run deployed")
				}
				for _, call := range f.calls {
					if call.Name == "docker" || call.Name == "make" {
						t.Fatal("dry run runs deployment")
					}
				}
				if !strings.Contains(report.String(), mergedSHA) || !strings.Contains(report.String(), cfg.ProductionDatabase) || !strings.Contains(report.String(), cfg.ProductionEnvFile) {
					t.Fatal("incomplete dry run report")
				}
			}
			if mode == "health failure" && registrations != 0 {
				t.Fatal("registered unhealthy deployment")
			}
			if mode == "copied env" {
				for _, call := range f.calls {
					if call.Name == "make" {
						t.Fatal("copied env reached Make")
					}
				}
			}
			if mode == "apply" && registrations != 1 {
				t.Fatal("missing registration")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeployCandidate(t *testing.T) {
	for _, mode := range []string{"success", "build failure", "check failure", "dump failure", "empty backup", "bad port", "remote port", "changed head", "wrong deployed image", "dirty candidate"} {
		t.Run(mode, func(t *testing.T) {
			cfg, c, f := deploymentFixture(t)
			switch mode {
			case "dirty candidate":
				f.fail = "diff --quiet"
			case "wrong deployed image":
				f.wrongImage = true
			case "build failure":
				f.fail = "Dockerfile.web"
			case "check failure":
				f.fail = "check-worktree"
			case "dump failure":
				f.fail = "pg_dump"
			case "empty backup":
				f.emptyDump = true
			case "bad port":
				f.port = "not-a-port"
			case "remote port":
				f.port = "0.0.0.0:12345"
			case "changed head":
				c.CandidateSHA = baseSHA
			}
			evidence, err := DeployCandidate(context.Background(), f, cfg, c)
			if mode == "success" {
				data, e := os.ReadFile(filepath.Join(cfg.StateDir, "deployments", c.JobID+".json"))
				if e != nil || !bytes.Contains(data, []byte(evidence.PreviousBackendDigest)) {
					t.Fatalf("old images not durably preserved: %v", e)
				}
			}
			if (err != nil) != (mode != "success") {
				t.Fatalf("mode=%s evidence=%+v err=%v", mode, evidence, err)
			}
			dumpAt, upAt := -1, -1
			for i, call := range f.calls {
				s := strings.Join(call.Args, " ")
				for _, arg := range call.Args {
					if arg == "rm" || arg == "destroy" || arg == "down" {
						t.Fatalf("destructive command: %s", s)
					}
				}
				if strings.Contains(s, "pg_dump") {
					dumpAt = i
				}
				if strings.Contains(s, " up ") {
					upAt = i
					if !strings.HasSuffix(s, "up -d --no-deps backend frontend") || !strings.Contains(s, "--env-file "+cfg.ProductionEnvFile) {
						t.Fatalf("unsafe compose up: %s", s)
					}
				}
			}
			if upAt >= 0 && (dumpAt < 0 || dumpAt > upAt) {
				t.Fatal("up before backup")
			}
			if (mode == "build failure" || mode == "check failure" || mode == "dump failure" || mode == "empty backup" || mode == "changed head") && upAt >= 0 {
				t.Fatal("failed preflight deployed")
			}
			if mode == "success" && (evidence.PreviousBackendDigest == "" || evidence.PreviousWebDigest == "" || evidence.DatabaseBackup == "" || evidence.ReadyURL != "http://127.0.0.1:12345/readyz" || evidence.ExternalURL != "https://site.example") {
				t.Fatalf("missing evidence: %+v", evidence)
			}
		})
	}
}
