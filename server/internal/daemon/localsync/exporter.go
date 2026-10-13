package localsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

type Message struct{ Role, Content string }
type Snapshot struct {
	ProjectID, ProjectTitle  string
	ChatSessionID, ChatTitle string
	Messages                 []Message
}
type Exporter struct {
	root        string
	moveToTrash func(string) error
}

const projectMetadataFile = ".multica-project.json"

type projectMetadata struct {
	ProjectID      string `json:"project_id"`
	CleanupPending bool   `json:"cleanup_pending,omitempty"`
}

func NewExporter(root string) *Exporter {
	return &Exporter{root: root, moveToTrash: defaultMoveToTrash}
}

// EnsureProject mirrors an empty browser project before it has any chats.
func (e *Exporter) EnsureProject(projectID, title string) error {
	_, err := e.EnsureProjectPath(projectID, title)
	return err
}

// EnsureProjectPath mirrors a browser project and returns its canonical local
// directory. Callers that need an agent working directory must use this value
// rather than reconstructing a Finder name: ensureProject can disambiguate
// duplicate titles and can rename an existing exporter-owned directory.
func (e *Exporter) EnsureProjectPath(projectID, title string) (string, error) {
	return e.ensureProject(projectID, title)
}

func (e *Exporter) ensureProject(projectID, title string) (string, error) {
	if projectID == "" {
		return "", nil
	}
	if err := os.MkdirAll(e.root, 0755); err != nil {
		return "", err
	}
	old, findErr := e.findProject(projectID)
	if findErr != nil && !os.IsNotExist(findErr) {
		return "", findErr
	}
	dir, err := e.availableDir(e.root, named(title, projectID), old)
	if err != nil {
		return "", err
	}
	if findErr == nil && old != dir {
		if err := os.Rename(old, dir); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	metadata := projectMetadata{ProjectID: projectID}
	if findErr == nil {
		data, err := os.ReadFile(filepath.Join(dir, projectMetadataFile))
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(data, &metadata); err != nil {
			return "", err
		}
	}
	if err := writeJSONAtomically(filepath.Join(dir, projectMetadataFile), metadata); err != nil {
		return "", err
	}
	return dir, nil
}

var unsafe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

func safeName(s string) string {
	s = strings.TrimSpace(unsafe.ReplaceAllString(s, "-"))
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "未命名"
	}
	return s
}

// named is deliberately human-readable. Stable IDs stay in the hidden
// metadata files so Finder shows projects and chats exactly as their titles.
func named(title, _ string) string { return safeName(title) }

// availableDir keeps Finder names readable. The ID stays hidden in metadata;
// duplicate titles receive a familiar numbered suffix instead of an opaque ID.
func (e *Exporter) availableDir(parent, title, current string) (string, error) {
	for index := 1; ; index++ {
		name := title
		if index > 1 {
			name = fmt.Sprintf("%s（%d）", title, index)
		}
		candidate := filepath.Join(parent, name)
		if candidate == current {
			return candidate, nil
		}
		_, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func (e *Exporter) Sync(s Snapshot) error {
	if s.ProjectID == "" {
		s.ProjectID, s.ProjectTitle = "unassigned", "未归类"
	}
	if strings.TrimSpace(s.ChatTitle) == "" {
		s.ChatTitle = firstUserMessage(s.Messages)
	}
	project, err := e.ensureProject(s.ProjectID, s.ProjectTitle)
	if err != nil {
		return err
	}
	old, findErr := e.findByChat(s.ChatSessionID)
	if findErr != nil && !os.IsNotExist(findErr) {
		return findErr
	}
	dir, err := e.availableDir(project, named(s.ChatTitle, s.ChatSessionID), old)
	if err != nil {
		return err
	}
	if findErr == nil && old != dir {
		if err := os.Rename(old, dir); err != nil {
			return err
		} else if err := e.removeEmptyParents(filepath.Dir(old)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var b strings.Builder
	for _, m := range s.Messages {
		role := "User"
		if m.Role == "assistant" {
			role = "Assistant"
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", role, m.Content)
	}
	tmp := filepath.Join(dir, ".conversation.tmp")
	if err := os.WriteFile(tmp, []byte(b.String()), 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "conversation.md")); err != nil {
		return err
	}
	return writeJSONAtomically(filepath.Join(dir, "metadata.json"), struct {
		ProjectID     string `json:"project_id"`
		ChatSessionID string `json:"chat_session_id"`
	}{ProjectID: s.ProjectID, ChatSessionID: s.ChatSessionID})
}

func writeJSONAtomically(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func firstUserMessage(messages []Message) string {
	for _, message := range messages {
		if message.Role == "user" && strings.TrimSpace(message.Content) != "" {
			return strings.TrimSpace(message.Content)
		}
	}
	return "未命名"
}

func (e *Exporter) find(projectID, chatID string) (string, error) {
	var result string
	err := filepath.WalkDir(e.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "metadata.json" {
			return err
		}
		var m struct {
			ProjectID     string `json:"project_id"`
			ChatSessionID string `json:"chat_session_id"`
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if json.Unmarshal(b, &m) == nil && m.ProjectID == projectID && m.ChatSessionID == chatID {
			result = filepath.Dir(path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if result == "" {
		return "", os.ErrNotExist
	}
	return result, nil
}

func (e *Exporter) findByChat(chatID string) (string, error) {
	var result string
	err := filepath.WalkDir(e.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "metadata.json" {
			return err
		}
		var m struct {
			ChatSessionID string `json:"chat_session_id"`
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if json.Unmarshal(b, &m) == nil && m.ChatSessionID == chatID {
			result = filepath.Dir(path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if result == "" {
		return "", os.ErrNotExist
	}
	return result, nil
}

func (e *Exporter) findProject(projectID string) (string, error) {
	entries, err := os.ReadDir(e.root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(e.root, entry.Name(), projectMetadataFile)
		var metadata struct {
			ProjectID string `json:"project_id"`
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && json.Unmarshal(data, &metadata) == nil && metadata.ProjectID == projectID {
			return filepath.Dir(path), nil
		}
	}
	return "", os.ErrNotExist
}

func (e *Exporter) Delete(projectID, chatID string) error {
	dir, err := e.find(projectID, chatID)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	if err := e.trash(dir); err != nil {
		return err
	}
	return e.removeEmptyParents(parent)
}

// Prune removes only directories created by this exporter whose chat session is
// no longer present in the user's browser-visible session list.
func (e *Exporter) Prune(active map[string]bool) error {
	var remove []string
	err := filepath.WalkDir(e.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "metadata.json" {
			return err
		}
		var m struct {
			ChatSessionID string `json:"chat_session_id"`
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if json.Unmarshal(b, &m) == nil && m.ChatSessionID != "" && !active[m.ChatSessionID] {
			remove = append(remove, filepath.Dir(path))
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, dir := range remove {
		if err := e.trash(dir); err != nil {
			return err
		}
		if err := e.removeEmptyParents(filepath.Dir(dir)); err != nil {
			return err
		}
	}
	return nil
}

// PruneProjects moves complete exporter-owned projects absent from the browser
// snapshot to Trash once they contain no active chats. It also retries failed
// metadata-only parent cleanup, including projects still in the snapshot.
func (e *Exporter) PruneProjects(active, activeChats map[string]bool) error {
	entries, err := os.ReadDir(e.root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(e.root, entry.Name())
		data, readErr := os.ReadFile(filepath.Join(dir, projectMetadataFile))
		if readErr != nil {
			continue
		}
		var metadata projectMetadata
		if json.Unmarshal(data, &metadata) != nil || metadata.ProjectID == "" {
			continue
		}
		if active[metadata.ProjectID] {
			if metadata.CleanupPending {
				if _, err := e.removeOwnedEmptyProject(dir); err != nil {
					return err
				}
			}
			continue
		}
		hasActiveChat := false
		if err := filepath.WalkDir(dir, func(path string, child os.DirEntry, err error) error {
			if err != nil || child.IsDir() || child.Name() != "metadata.json" {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var chat struct {
				ChatSessionID string `json:"chat_session_id"`
			}
			if json.Unmarshal(data, &chat) == nil && chat.ChatSessionID != "" && activeChats[chat.ChatSessionID] {
				hasActiveChat = true
			}
			return nil
		}); err != nil {
			return err
		}
		if hasActiveChat {
			continue
		}
		if err := e.trash(dir); err != nil {
			return err
		}
	}
	return nil
}

func (e *Exporter) trash(path string) error {
	root, err := filepath.Abs(e.root)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to move path outside sync root to Trash: %s", target)
	}
	return e.moveToTrash(target)
}

// removeEmptyParents removes only empty directories below the configured root.
func (e *Exporter) removeEmptyParents(dir string) error {
	root, err := filepath.Abs(e.root)
	if err != nil {
		return err
	}
	for {
		current, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		if current == root {
			return nil
		}
		rel, err := filepath.Rel(root, current)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("refusing to clean directory outside sync root: %s", current)
		}
		if err := os.Remove(current); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			if isNotEmpty(err) {
				removed, cleanupErr := e.removeOwnedEmptyProject(current)
				if cleanupErr != nil {
					return cleanupErr
				}
				if !removed {
					return nil
				}
				dir = filepath.Dir(current)
				continue
			}
			return err
		}
		dir = filepath.Dir(current)
	}
}

func (e *Exporter) removeOwnedEmptyProject(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	if len(entries) != 1 || entries[0].Name() != projectMetadataFile {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, projectMetadataFile))
	if err != nil {
		return false, err
	}
	var metadata projectMetadata
	if json.Unmarshal(data, &metadata) != nil || metadata.ProjectID == "" {
		return false, nil
	}
	if err := e.trash(dir); err != nil {
		metadata.CleanupPending = true
		if recordErr := writeJSONAtomically(filepath.Join(dir, projectMetadataFile), metadata); recordErr != nil {
			return false, errors.Join(err, fmt.Errorf("record pending project cleanup: %w", recordErr))
		}
		return false, err
	}
	return true, nil
}

func isNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}
