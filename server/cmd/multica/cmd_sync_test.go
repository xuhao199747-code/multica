package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	transcript, err := os.ReadFile(filepath.Join(root, "产品设计--project-1", "第一句话--chat-1", "conversation.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(transcript); !strings.Contains(got, "## User\n\n第一句话") || !strings.Contains(got, "## Assistant\n\n收到") {
		t.Fatalf("unexpected transcript: %s", got)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %#v, want three browser API reads", requests)
	}
}

func TestSyncRootRequiresAbsolutePath(t *testing.T) {
	cmd := testCmd()
	cmd.Flags().String("root", "relative", "")
	if _, err := syncRoot(cmd); err == nil {
		t.Fatal("syncRoot accepted a relative path")
	}
}
