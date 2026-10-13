package localsync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTrashRecordingExporter(root string) (*Exporter, *[]string) {
	targets := []string{}
	exporter := NewExporter(root)
	exporter.moveToTrash = func(path string) error {
		targets = append(targets, path)
		return nil
	}
	return exporter, &targets
}

func newTrashMovingExporter(t *testing.T, root string) *Exporter {
	t.Helper()
	exporter := NewExporter(root)
	trash := t.TempDir()
	exporter.moveToTrash = func(path string) error {
		return os.Rename(path, filepath.Join(trash, filepath.Base(path)))
	}
	return exporter
}

func TestDeleteMovesVerifiedChatDirectoryToTrash(t *testing.T) {
	e, targets := newTrashRecordingExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "project", ChatSessionID: "c", ChatTitle: "chat"}); err != nil {
		t.Fatal(err)
	}
	chat, err := e.find("p", "c")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Delete("other", "c"); err != nil {
		t.Fatal(err)
	}
	if len(*targets) != 0 {
		t.Fatalf("mismatched metadata moved to Trash: %v", *targets)
	}
	if err := e.Delete("p", "c"); err != nil {
		t.Fatal(err)
	}
	if len(*targets) != 1 || (*targets)[0] != chat {
		t.Fatalf("Trash targets = %v, want [%s]", *targets, chat)
	}
}

func TestPruneMovesMissingChatDirectoryToTrash(t *testing.T) {
	e, targets := newTrashRecordingExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "project", ChatSessionID: "gone", ChatTitle: "chat"}); err != nil {
		t.Fatal(err)
	}
	chat, err := e.find("p", "gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Prune(map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if len(*targets) != 1 || (*targets)[0] != chat {
		t.Fatalf("Trash targets = %v, want [%s]", *targets, chat)
	}
}

func TestPruneProjectsMovesCompleteDeletedProjectToTrash(t *testing.T) {
	e, targets := newTrashRecordingExporter(t.TempDir())
	project, err := e.EnsureProjectPath("gone", "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "agent-output.txt"), []byte("keep recoverable"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := e.PruneProjects(map[string]bool{}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if len(*targets) != 1 || (*targets)[0] != project {
		t.Fatalf("Trash targets = %v, want [%s]", *targets, project)
	}
}

func TestTrashFailureLeavesChatDirectoryForRetry(t *testing.T) {
	e := NewExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "project", ChatSessionID: "c", ChatTitle: "chat"}); err != nil {
		t.Fatal(err)
	}
	chat, err := e.find("p", "c")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("offline")
	e.moveToTrash = func(string) error { return failure }
	if err := e.Delete("p", "c"); !errors.Is(err, failure) {
		t.Fatalf("Delete error = %v, want %v", err, failure)
	}
	if _, err := os.Stat(chat); err != nil {
		t.Fatalf("chat unavailable for retry: %v", err)
	}
}

func TestPruneProjectsRetriesFailedEmptyParentCleanup(t *testing.T) {
	for _, operation := range []string{"Delete", "Prune"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			e := NewExporter(root)
			if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "project", ChatSessionID: "c", ChatTitle: "chat"}); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(root, "project")
			chat := filepath.Join(project, "chat")
			trash := t.TempDir()
			failure := errors.New("parent Trash unavailable")
			e.moveToTrash = func(path string) error {
				if path == project {
					return failure
				}
				return os.Rename(path, filepath.Join(trash, filepath.Base(path)))
			}
			var err error
			if operation == "Delete" {
				err = e.Delete("p", "c")
			} else {
				err = e.Prune(map[string]bool{})
			}
			if !errors.Is(err, failure) {
				t.Fatalf("cleanup error = %v, want %v", err, failure)
			}
			if _, err := os.Stat(chat); !os.IsNotExist(err) {
				t.Fatalf("chat remains at source: %v", err)
			}
			if _, err := os.Stat(filepath.Join(project, projectMetadataFile)); err != nil {
				t.Fatalf("failed parent cleanup lost metadata: %v", err)
			}
			// A new exporter and project reconciliation match the next sync cycle.
			e = newTrashMovingExporter(t, root)
			if err := e.EnsureProject("p", "project"); err != nil {
				t.Fatal(err)
			}
			moveToTrash := e.moveToTrash
			e.moveToTrash = func(string) error { return failure }
			if err := e.PruneProjects(map[string]bool{"p": true}, map[string]bool{}); !errors.Is(err, failure) {
				t.Fatalf("retry error = %v, want %v", err, failure)
			}
			if _, err := os.Stat(filepath.Join(project, projectMetadataFile)); err != nil {
				t.Fatalf("failed retry lost parent metadata: %v", err)
			}
			e.moveToTrash = moveToTrash
			if err := e.Prune(map[string]bool{}); err != nil {
				t.Fatal(err)
			}
			if err := e.PruneProjects(map[string]bool{"p": true}, map[string]bool{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(project); !os.IsNotExist(err) {
				t.Fatalf("retained metadata-only parent was not retried: %v", err)
			}
		})
	}
}

func TestPruneProjectsKeepsActiveEmptyProjectWithoutPendingCleanup(t *testing.T) {
	e, targets := newTrashRecordingExporter(t.TempDir())
	if err := e.EnsureProject("p", "project"); err != nil {
		t.Fatal(err)
	}
	if err := e.PruneProjects(map[string]bool{"p": true}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if len(*targets) != 0 {
		t.Fatalf("active empty project moved to Trash: %v", *targets)
	}
}

func TestPruneProjectsPendingCleanupPreservesActiveChatsAndUserFiles(t *testing.T) {
	for _, content := range []string{"active chat", "user file"} {
		t.Run(content, func(t *testing.T) {
			e, targets := newTrashRecordingExporter(t.TempDir())
			project, err := e.EnsureProjectPath("p", "project")
			if err != nil {
				t.Fatal(err)
			}
			if err := writeJSONAtomically(filepath.Join(project, projectMetadataFile), projectMetadata{ProjectID: "p", CleanupPending: true}); err != nil {
				t.Fatal(err)
			}
			if content == "active chat" {
				if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "project", ChatSessionID: "c", ChatTitle: "chat"}); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(project, "notes.txt"), []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := e.PruneProjects(map[string]bool{"p": true}, map[string]bool{"c": true}); err != nil {
				t.Fatal(err)
			}
			if len(*targets) != 0 {
				t.Fatalf("pending project with %s moved to Trash: %v", content, *targets)
			}
		})
	}
}

func TestPruneProjectsKeepsDeletedProjectWithActiveChat(t *testing.T) {
	e, targets := newTrashRecordingExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "gone", ProjectTitle: "project", ChatSessionID: "active", ChatTitle: "chat"}); err != nil {
		t.Fatal(err)
	}
	if err := e.PruneProjects(map[string]bool{}, map[string]bool{"active": true}); err != nil {
		t.Fatal(err)
	}
	if len(*targets) != 0 {
		t.Fatalf("project with active chat moved to Trash: %v", *targets)
	}
	if err := e.PruneProjects(map[string]bool{}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(e.root, "project")
	if len(*targets) != 1 || (*targets)[0] != project {
		t.Fatalf("inactive project's Trash targets = %v, want [%s]", *targets, project)
	}
}

func TestPruneProjectsPreservesUnownedAndMalformedProjects(t *testing.T) {
	e, targets := newTrashRecordingExporter(t.TempDir())
	for _, name := range []string{"unowned", "malformed", "empty-id"} {
		dir := filepath.Join(e.root, name)
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if name != "unowned" {
			data := []byte("invalid")
			if name == "empty-id" {
				data = []byte(`{"project_id":""}`)
			}
			if err := os.WriteFile(filepath.Join(dir, projectMetadataFile), data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := e.PruneProjects(map[string]bool{}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if len(*targets) != 0 {
		t.Fatalf("unverified projects moved to Trash: %v", *targets)
	}
}

func TestMovingChatTrashesEmptyOwnedProject(t *testing.T) {
	e, targets := newTrashRecordingExporter(t.TempDir())
	if err := e.Sync(Snapshot{ProjectID: "p", ProjectTitle: "project", ChatSessionID: "c", ChatTitle: "chat"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(Snapshot{ChatSessionID: "c", ChatTitle: "chat"}); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(e.root, "project")
	if len(*targets) != 1 || (*targets)[0] != project {
		t.Fatalf("empty project's Trash targets = %v, want [%s]", *targets, project)
	}
}

func TestTrashRejectsRootAndOutsidePaths(t *testing.T) {
	root := t.TempDir()
	e, targets := newTrashRecordingExporter(root)
	for _, path := range []string{root, filepath.Dir(root), filepath.Join(root, "..", "outside")} {
		if err := e.trash(path); err == nil {
			t.Fatalf("trash(%q) accepted path outside sync content", path)
		}
	}
	if len(*targets) != 0 {
		t.Fatalf("rejected paths reached Trash adapter: %v", *targets)
	}
}

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
	e := newTrashMovingExporter(t, root)
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
	e := newTrashMovingExporter(t, t.TempDir())
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
	e := newTrashMovingExporter(t, t.TempDir())
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

func TestEnsureProjectPathReturnsRenamedOwnedDirectory(t *testing.T) {
	e := NewExporter(t.TempDir())
	if _, err := e.EnsureProjectPath("p", "旧项目"); err != nil {
		t.Fatal(err)
	}
	path, err := e.EnsureProjectPath("p", "新项目")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "新项目" {
		t.Fatalf("project path = %q, want final renamed directory", path)
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
	e := newTrashMovingExporter(t, t.TempDir())
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

func TestPruneProjectsRemovesOnlyOwnedDeletedProjects(t *testing.T) {
	e := newTrashMovingExporter(t, t.TempDir())
	if err := e.EnsureProject("gone", "已删除"); err != nil {
		t.Fatal(err)
	}
	if err := e.EnsureProject("keep", "保留"); err != nil {
		t.Fatal(err)
	}
	if err := e.PruneProjects(map[string]bool{"keep": true}, map[string]bool{}); err != nil {
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
	e := newTrashMovingExporter(t, t.TempDir())
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
