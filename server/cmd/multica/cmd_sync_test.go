package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

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
		"/Users/example/Documents/multica/产品设计", "daemon-1",
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
