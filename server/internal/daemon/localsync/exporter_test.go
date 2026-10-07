package localsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncCreatesProjectAndConversationTranscript(t *testing.T) {
	root := t.TempDir()
	exporter := NewExporter(root)
	err := exporter.Sync(Snapshot{
		ProjectID: "project-123456", ProjectTitle: "Multica 改造",
		ChatSessionID: "chat-abcdef", ChatTitle: "第一句话",
		Messages: []Message{{Role: "user", Content: "第一句话"}, {Role: "assistant", Content: "收到"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "Multica 改造"))
	if err != nil {
		t.Fatal(err)
	}
	var chatDir string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == "第一句话" {
			chatDir = entry.Name()
		}
	}
	if chatDir == "" {
		t.Fatalf("unexpected conversation directory: %#v", entries)
	}
	transcript, err := os.ReadFile(filepath.Join(root, "Multica 改造", chatDir, "conversation.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(transcript), "## User\n\n第一句话") || !strings.Contains(string(transcript), "## Assistant\n\n收到") {
		t.Fatalf("unexpected transcript: %s", transcript)
	}
}

func TestSyncRenamesConversationAndDeleteOnlyMatchesMetadata(t *testing.T) {
	root := t.TempDir()
	e := NewExporter(root)
	s := Snapshot{ProjectID: "p1", ProjectTitle: "项目", ChatSessionID: "c1", ChatTitle: "旧名"}
	if err := e.Sync(s); err != nil {
		t.Fatal(err)
	}
	s.ChatTitle = "新名"
	if err := e.Sync(s); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "项目")
	if _, err := os.Stat(filepath.Join(project, "旧名")); !os.IsNotExist(err) {
		t.Fatalf("old directory remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "新名", "metadata.json")); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete("p1", "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, "新名")); !os.IsNotExist(err) {
		t.Fatalf("chat was not deleted: %v", err)
	}
}

func TestPruneRemovesChatsNoLongerReturnedByBrowser(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "gone", ChatTitle: "会话"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Prune(map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.find("p", "gone"); !os.IsNotExist(err) {
		t.Fatalf("deleted chat remains: %v", err)
	}
}

func TestSyncMovesUnassignedChatToUncategorized(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c", ChatTitle: "会话"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(Snapshot{ChatSessionID: "c", ChatTitle: "会话"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.find("p", "c"); !os.IsNotExist(err) {
		t.Fatalf("old project mapping remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "未归类", "会话")); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureProjectCreatesEmptyProjectFolder(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.EnsureProject("p", "新项目"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "新项目")); err != nil {
		t.Fatal(err)
	}
}

func TestSyncUsesFirstUserMessageWhenChatTitleIsEmpty(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.Sync(Snapshot{
		ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c", Messages: []Message{
			{Role: "assistant", Content: "欢迎"},
			{Role: "user", Content: "  用第一句话做标题  "},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "项目", "用第一句话做标题")); err != nil {
		t.Fatalf("first user message was not used as the title: %v", err)
	}
}

func TestSyncKeepsExistingConversationWhenRenameTargetCollides(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c1", ChatTitle: "旧名"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c2", ChatTitle: "新名"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c1", ChatTitle: "新名"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.find("p", "c1"); err != nil {
		t.Fatalf("renamed chat was lost: %v", err)
	}
	if _, err := e.find("p", "c2"); err != nil {
		t.Fatalf("existing chat was overwritten: %v", err)
	}
}

func TestMovingChatRemovesEmptyOldProjectFolder(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c", ChatTitle: "会话"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(Snapshot{ChatSessionID: "c", ChatTitle: "会话"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "项目")); !os.IsNotExist(err) {
		t.Fatalf("empty old project folder remains: %v", err)
	}
}

func TestPruneAllowsNewRoot(t *testing.T) {
	e := NewExporter(filepath.Join(t.TempDir(), "not-created-yet"))
	if err := e.Prune(map[string]bool{}); err != nil {
		t.Fatalf("pruning a missing root should be a no-op: %v", err)
	}
}

func TestEnsureProjectRenamesOwnedProjectDirectory(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.EnsureProject("p", "旧项目"); err != nil {
		t.Fatal(err)
	}
	if err := e.EnsureProject("p", "新项目"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "旧项目")); !os.IsNotExist(err) {
		t.Fatalf("old project directory remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "新项目", projectMetadataFile)); err != nil {
		t.Fatal(err)
	}
}

func TestPruneProjectsRemovesOnlyEmptyOwnedDeletedProjects(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.EnsureProject("gone", "已删除"); err != nil {
		t.Fatal(err)
	}
	if err := e.EnsureProject("keep", "保留"); err != nil {
		t.Fatal(err)
	}
	if err := e.PruneProjects(map[string]bool{"keep": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "已删除")); !os.IsNotExist(err) {
		t.Fatalf("deleted empty project remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "保留")); err != nil {
		t.Fatal(err)
	}
}

func TestSyncMigratesLegacyIDDecoratedDirectoriesToReadableNames(t *testing.T) {
	e := NewExporter(t.TempDir())
	legacy := filepath.Join(e.root, "项目--p", "旧名--c")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomically(filepath.Join(filepath.Dir(legacy), projectMetadataFile), struct {
		ProjectID string `json:"project_id"`
	}{ProjectID: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomically(filepath.Join(legacy, "metadata.json"), struct {
		ProjectID     string `json:"project_id"`
		ChatSessionID string `json:"chat_session_id"`
	}{ProjectID: "p", ChatSessionID: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c", ChatTitle: "新名"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "项目", "新名", "metadata.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "项目--p")); !os.IsNotExist(err) {
		t.Fatalf("legacy project directory remains: %v", err)
	}
}

func TestSyncUsesFriendlySuffixForDuplicateChatTitles(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c1", ChatTitle: "同名"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "项目", ChatSessionID: "c2", ChatTitle: "同名"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(e.root, "项目", "同名", "metadata.json"),
		filepath.Join(e.root, "项目", "同名（2）", "metadata.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected collision-safe readable name at %s: %v", path, err)
		}
	}
	if _, err := e.find("p", "c1"); err != nil {
		t.Fatalf("first chat was overwritten: %v", err)
	}
}
