package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/spf13/cobra"
)

func TestSyncHelpExplainsTrashDeletion(t *testing.T) {
	var out bytes.Buffer
	cmd := &cobra.Command{Use: syncCmd.Use, Short: syncCmd.Short, Long: syncCmd.Long}
	cmd.SetArgs([]string{"--help"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute: %v", err)
	}
	if !strings.Contains(out.String(), "Deleted browser chats and projects are moved to the system Trash.") {
		t.Fatalf("sync help does not explain Trash behavior: %s", out.String())
	}
}

func newSyncTestClient(serverURL string) *cli.APIClient {
	return cli.NewAPIClient(serverURL, "workspace-1", "test-token")
}

func TestReconcileLocalProjectDirectoryCreatesResource(t *testing.T) {
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/projects/project-1/resources":
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]any{"resources": []any{}})
			case http.MethodPost:
				if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
					t.Fatal(err)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "resource-1"})
			default:
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newSyncTestClient(server.URL)
	if err := reconcileLocalProjectDirectory(
		context.Background(), client, "project-1", "产品设计",
		"/Users/example/Documents/multica/产品设计", "daemon-1", nil,
	); err != nil {
		t.Fatalf("reconcileLocalProjectDirectory: %v", err)
	}
	ref, ok := created["resource_ref"].(map[string]any)
	if !ok {
		t.Fatalf("resource_ref = %#v", created["resource_ref"])
	}
	if ref["local_path"] != "/Users/example/Documents/multica/产品设计" ||
		ref["daemon_id"] != "daemon-1" || ref["execution_mode"] != "in_place" {
		t.Fatalf("resource_ref = %#v", ref)
	}
}

func TestSyncChatsMirrorsBrowserProjectAndTranscript(t *testing.T) {
	root := t.TempDir()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/chat/sessions":
			if got := r.URL.Query().Get("status"); got != "all" {
				t.Errorf("status query = %q, want all", got)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": "chat-1", "title": "第一句话", "project_id": "project-1",
			}})
		case "/api/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]any{{
				"id": "project-1", "title": "产品设计",
			}}})
		case "/api/projects/project-1/resources":
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]any{"resources": []any{}})
			case http.MethodPost:
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "resource-1"})
			default:
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			}
		case "/api/chat/sessions/chat-1/messages":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"role": "user", "content": "第一句话"},
				{"role": "assistant", "content": "收到"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("MULTICA_SERVER_URL", server.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	if err := syncChats(testCmd(), root); err != nil {
		t.Fatalf("syncChats: %v", err)
	}
	transcript, err := os.ReadFile(filepath.Join(root, "产品设计", "第一句话", "conversation.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(transcript); !strings.Contains(got, "## User\n\n第一句话") || !strings.Contains(got, "## Assistant\n\n收到") {
		t.Fatalf("unexpected transcript: %s", got)
	}
	if len(requests) != 5 {
		t.Fatalf("requests = %#v, want browser reads plus local directory resource reconciliation", requests)
	}
}

func TestSyncRootRequiresAbsolutePath(t *testing.T) {
	cmd := testCmd()
	cmd.Flags().String("root", "relative", "")
	if _, err := syncRoot(cmd); err == nil {
		t.Fatal("syncRoot accepted a relative path")
	}
}

func TestSyncChatsLeavesUserPickedDirectoryAlone(t *testing.T) {
	root := t.TempDir()
	// A folder the user picked in the desktop app — outside the sync root.
	external := t.TempDir()
	// The mock resource must be bound to the daemon id syncChats resolves, so
	// resolve it the same way (idempotent read of the machine's daemon.id).
	daemonID, err := daemon.EnsureDaemonID(resolveProfile(testCmd()))
	if err != nil {
		t.Fatal(err)
	}
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/chat/sessions":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": "chat-1", "title": "第一句话", "project_id": "project-1",
			}})
		case "/api/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]any{{
				"id": "project-1", "title": "产品设计",
			}}})
		case "/api/projects/project-1/resources":
			if r.Method != http.MethodGet {
				http.Error(w, "user-picked directory must not be written", http.StatusConflict)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resources": []map[string]any{{
				"id":            "resource-1",
				"resource_type": "local_directory",
				"resource_ref": map[string]any{
					"local_path":     external,
					"daemon_id":      daemonID,
					"label":          "我的应用",
					"execution_mode": "in_place",
				},
			}}})
		case "/api/chat/sessions/chat-1/messages":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"role": "user", "content": "第一句话"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("MULTICA_SERVER_URL", server.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	if err := syncChats(testCmd(), root); err != nil {
		t.Fatalf("syncChats: %v", err)
	}

	// Chat transcripts are still mirrored under the sync root…
	transcript := filepath.Join(root, "产品设计", "第一句话", "conversation.md")
	if _, err := os.Stat(transcript); err != nil {
		t.Fatalf("transcript not mirrored: %v", err)
	}
	// …but the sync process never reaches into the user's folder: claiming a
	// project means writing .multica-project.json into its directory.
	if _, err := os.Stat(filepath.Join(external, ".multica-project.json")); !os.IsNotExist(err) {
		t.Fatalf("sync process claimed the user-picked directory: %v", err)
	}
	for _, request := range requests {
		if strings.HasPrefix(request, "POST") || strings.HasPrefix(request, "PUT") {
			t.Errorf("resource must stay untouched, got %s", request)
		}
	}
}

func TestSyncChatsStillManagesProjectWithSyncRootDirectory(t *testing.T) {
	root := t.TempDir()
	daemonID, err := daemon.EnsureDaemonID(resolveProfile(testCmd()))
	if err != nil {
		t.Fatal(err)
	}
	var putRef map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/chat/sessions":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case "/api/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]any{{
				"id": "project-1", "title": "产品设计",
			}}})
		case "/api/projects/project-1/resources":
			if r.Method != http.MethodGet {
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				return
			}
			// Stale label: reconciliation should converge it to the
			// current project title, proving management was NOT skipped.
			_ = json.NewEncoder(w).Encode(map[string]any{"resources": []map[string]any{{
				"id":            "resource-1",
				"resource_type": "local_directory",
				"resource_ref": map[string]any{
					"local_path":     filepath.Join(root, "产品设计"),
					"daemon_id":      daemonID,
					"label":          "旧名称",
					"execution_mode": "in_place",
				},
			}}})
		case "/api/projects/project-1/resources/resource-1":
			if r.Method != http.MethodPut {
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			putRef, _ = body["resource_ref"].(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "resource-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("MULTICA_SERVER_URL", server.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	if err := syncChats(testCmd(), root); err != nil {
		t.Fatalf("syncChats: %v", err)
	}
	if putRef == nil {
		t.Fatal("sync-root resource was not reconciled")
	}
	if putRef["label"] != "产品设计" || putRef["local_path"] != filepath.Join(root, "产品设计") {
		t.Fatalf("reconciled ref = %#v", putRef)
	}
}

func TestHasUserManagedLocalDirectory(t *testing.T) {
	root := "/Users/example/Documents/multica"
	localRef := func(path, daemon string) syncProjectResource {
		b, err := json.Marshal(map[string]any{"local_path": path, "daemon_id": daemon})
		if err != nil {
			t.Fatal(err)
		}
		return syncProjectResource{ID: "r1", ResourceType: "local_directory", ResourceRef: b}
	}
	cases := []struct {
		name      string
		resources []syncProjectResource
		want      bool
	}{
		{"outside root is user-managed", []syncProjectResource{localRef("/Users/example/Projects/app", "daemon-1")}, true},
		{"inside root stays managed", []syncProjectResource{localRef(filepath.Join(root, "产品设计"), "daemon-1")}, false},
		{"other daemon does not skip this machine", []syncProjectResource{localRef("/Users/example/Projects/app", "daemon-2")}, false},
		{"github repo ignored", []syncProjectResource{{ID: "r2", ResourceType: "github_repo", ResourceRef: json.RawMessage(`{"url":"https://github.com/a/b"}`)}}, false},
		{"malformed ref ignored", []syncProjectResource{{ID: "r3", ResourceType: "local_directory", ResourceRef: json.RawMessage(`oops`)}}, false},
		{"no resources", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasUserManagedLocalDirectory(tc.resources, "daemon-1", root); got != tc.want {
				t.Fatalf("hasUserManagedLocalDirectory = %v, want %v", got, tc.want)
			}
		})
	}
}
