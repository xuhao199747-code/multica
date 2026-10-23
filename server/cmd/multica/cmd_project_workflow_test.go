package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestWorkflowConfirmDoesNotRequireGuessedSourceAgent(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MULTICA_AGENT_ID", "")
	t.Setenv("MULTICA_TASK_ID", "")
	const projectID = "01900000-0000-7000-8000-000000000001"
	posted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/projects/"+projectID+"/workflow/executions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["source_chat_session_id"] != "source-chat" {
			t.Errorf("source chat: %v", body)
		}
		if agent, ok := body["source_agent_id"]; ok && agent != "" {
			t.Errorf("CLI invented identity: %v", body)
		}
		posted = true
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "execution", "source_agent_id": "actual-chat-agent"})
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	file := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(file, []byte(`{"goal":"Direct Mica handoff","scope":[],"acceptance_criteria":[],"risks":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{Use: "confirm"}
	cmd.Flags().String("source-chat", "source-chat", "")
	cmd.Flags().String("source-agent", "", "")
	cmd.Flags().String("snapshot", file, "")
	cmd.Flags().String("profile", "", "")
	_, err := captureStdout(t, func() error { return runProjectWorkflowConfirm(cmd, []string{projectID}) })
	if err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("confirmation was not posted")
	}
}
