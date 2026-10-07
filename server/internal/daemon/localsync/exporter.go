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
type Exporter struct{ root string }

const projectMetadataFile = ".multica-project.json"

func NewExporter(root string) *Exporter { return &Exporter{root: root} }

// EnsureProject mirrors an empty browser project before it has any chats.
func (e *Exporter) EnsureProject(projectID, title string) error {
	_, err := e.ensureProject(projectID, title)
	return err
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
	if err := writeJSONAtomically(filepath.Join(dir, projectMetadataFile), struct {
		ProjectID string `json:"project_id"`
	}{ProjectID: projectID}); err != nil {
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
	if err := os.RemoveAll(dir); err != nil {
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
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if err := e.removeEmptyParents(filepath.Dir(dir)); err != nil {
			return err
		}
	}
	return nil
}

// PruneProjects removes only empty, exporter-owned project directories that
// no longer exist in the browser snapshot. Chats are moved before this runs.
func (e *Exporter) PruneProjects(active map[string]bool) error {
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
		var metadata struct {
			ProjectID string `json:"project_id"`
		}
		if json.Unmarshal(data, &metadata) != nil || metadata.ProjectID == "" || active[metadata.ProjectID] {
			continue
		}
		children, readErr := os.ReadDir(dir)
		if readErr != nil {
			return readErr
		}
		if len(children) == 1 && children[0].Name() == projectMetadataFile {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
	}
	return nil
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
				removed, cleanupErr := removeOwnedEmptyProject(current)
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

func removeOwnedEmptyProject(dir string) (bool, error) {
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
	var metadata struct {
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.ProjectID == "" {
		return false, nil
	}
	return true, os.RemoveAll(dir)
}

func isNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}
