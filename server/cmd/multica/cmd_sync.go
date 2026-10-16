package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/multica-ai/multica/server/internal/daemon/localsync"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Mirror browser chat transcripts to a local folder",
	Long:  "Mirror browser chat transcripts to a local folder. Deleted browser chats and projects are moved to the system Trash.",
}
var syncOnceCmd = &cobra.Command{Use: "once", Short: "Synchronize chats once", RunE: runSyncOnce}
var syncStartCmd = &cobra.Command{Use: "start", Short: "Synchronize chats continuously", RunE: runSyncStart}

func init() {
	for _, c := range []*cobra.Command{syncOnceCmd, syncStartCmd} {
		c.Flags().String("root", "", "Absolute local export directory (required)")
	}
	syncStartCmd.Flags().Duration("interval", 15*time.Second, "Polling interval")
	syncCmd.AddCommand(syncOnceCmd, syncStartCmd)
}

func syncRoot(cmd *cobra.Command) (string, error) {
	root, _ := cmd.Flags().GetString("root")
	if root == "" {
		return "", fmt.Errorf("--root is required")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("--root must be an absolute path")
	}
	return filepath.Clean(root), nil
}
func runSyncOnce(cmd *cobra.Command, _ []string) error {
	root, err := syncRoot(cmd)
	if err != nil {
		return err
	}
	return syncChats(cmd, root)
}
func runSyncStart(cmd *cobra.Command, _ []string) error {
	root, err := syncRoot(cmd)
	if err != nil {
		return err
	}
	interval, _ := cmd.Flags().GetDuration("interval")
	if interval <= 0 {
		return fmt.Errorf("--interval must be greater than zero")
	}
	for {
		if err := syncChats(cmd, root); err != nil {
			fmt.Fprintln(os.Stderr, "sync:", err)
		}
		select {
		case <-cmd.Context().Done():
			return cmd.Context().Err()
		case <-time.After(interval):
		}
	}
}
func syncChats(cmd *cobra.Command, root string) error {
	c, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var chats []struct {
		ID        string  `json:"id"`
		Title     string  `json:"title"`
		ProjectID *string `json:"project_id"`
	}
	if err := c.GetJSON(ctx, "/api/chat/sessions?status=all", &chats); err != nil {
		return err
	}
	projects := map[string]string{}
	var projectResponse struct {
		Projects []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"projects"`
	}
	if err := c.GetJSON(ctx, "/api/projects", &projectResponse); err != nil {
		return err
	}
	for _, p := range projectResponse.Projects {
		projects[p.ID] = p.Title
	}
	e := localsync.NewExporter(root)
	daemonID, err := daemon.EnsureDaemonID(resolveProfile(cmd))
	if err != nil {
		return fmt.Errorf("resolve local daemon id: %w", err)
	}
	activeProjects := make(map[string]bool, len(projects))
	for id, title := range projects {
		activeProjects[id] = true
		resources, err := listProjectResources(ctx, c, id)
		if err != nil {
			return err
		}
		if hasUserManagedLocalDirectory(resources, daemonID, root) {
			// The project is bound to a folder the user picked on this machine
			// that lives outside the sync root. That directory is theirs: the
			// sync process must not create, rename, trash, or repoint it, and
			// must not "correct" the resource back to a sync-root directory.
			// Chat transcripts are still mirrored under the root by e.Sync.
			continue
		}
		path, err := e.EnsureProjectPath(id, title)
		if err != nil {
			return err
		}
		if err := reconcileLocalProjectDirectory(ctx, c, id, title, path, daemonID, resources); err != nil {
			return fmt.Errorf("sync project local directory %s: %w", id, err)
		}
	}
	active := make(map[string]bool, len(chats))
	for _, chat := range chats {
		active[chat.ID] = true
		var ms []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := c.GetJSON(ctx, "/api/chat/sessions/"+chat.ID+"/messages", &ms); err != nil {
			return err
		}
		messages := make([]localsync.Message, len(ms))
		for i, m := range ms {
			messages[i] = localsync.Message{Role: m.Role, Content: m.Content}
		}
		projectID, projectTitle := "", ""
		if chat.ProjectID != nil {
			projectID, projectTitle = *chat.ProjectID, projects[*chat.ProjectID]
		}
		if err := e.Sync(localsync.Snapshot{ProjectID: projectID, ProjectTitle: projectTitle, ChatSessionID: chat.ID, ChatTitle: chat.Title, Messages: messages}); err != nil {
			return err
		}
	}
	if err := e.Prune(active); err != nil {
		return err
	}
	return e.PruneProjects(activeProjects, active)
}

type syncProjectResource struct {
	ID           string          `json:"id"`
	ResourceType string          `json:"resource_type"`
	ResourceRef  json.RawMessage `json:"resource_ref"`
}

func listProjectResources(ctx context.Context, c *cli.APIClient, projectID string) ([]syncProjectResource, error) {
	var response struct {
		Resources []syncProjectResource `json:"resources"`
	}
	if err := c.GetJSON(ctx, "/api/projects/"+projectID+"/resources", &response); err != nil {
		return nil, fmt.Errorf("list project resources: %w", err)
	}
	return response.Resources, nil
}

// hasUserManagedLocalDirectory reports whether the project is bound to a
// local_directory resource for this daemon whose path lies OUTSIDE the sync
// root — a folder the user picked themselves (for example when the desktop
// app attaches an existing folder while creating the project). The sync
// process manages only directories it created inside the root; a user-picked
// folder must be left untouched, otherwise the next poll would overwrite the
// resource's local_path and orphan the user's choice.
func hasUserManagedLocalDirectory(resources []syncProjectResource, daemonID, root string) bool {
	for _, resource := range resources {
		if resource.ResourceType != "local_directory" {
			continue
		}
		var ref map[string]any
		if json.Unmarshal(resource.ResourceRef, &ref) != nil || ref["daemon_id"] != daemonID {
			continue
		}
		localPath, _ := ref["local_path"].(string)
		if localPath != "" && !pathWithinRoot(root, localPath) {
			return true
		}
	}
	return false
}

// pathWithinRoot reports whether path lies inside root (or equals it). Unlike
// the exporter's trash guard, the paths may not exist on disk yet.
func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// reconcileLocalProjectDirectory makes the directory created by the local
// exporter available to this machine's daemon. It deliberately matches only
// local_directory resources assigned to this exact daemon, leaving resources
// belonging to other devices untouched.
func reconcileLocalProjectDirectory(
	ctx context.Context,
	c *cli.APIClient,
	projectID, projectTitle, localPath, daemonID string,
	resources []syncProjectResource,
) error {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(daemonID) == "" {
		return fmt.Errorf("project id and daemon id are required")
	}

	desired := map[string]any{
		"local_path":     localPath,
		"daemon_id":      daemonID,
		"label":          projectTitle,
		"execution_mode": "in_place",
	}
	basePath := "/api/projects/" + projectID + "/resources"
	for _, resource := range resources {
		if resource.ResourceType != "local_directory" {
			continue
		}
		var ref map[string]any
		if json.Unmarshal(resource.ResourceRef, &ref) != nil || ref["daemon_id"] != daemonID {
			continue
		}
		changed := false
		for key, value := range desired {
			if ref[key] != value {
				ref[key] = value
				changed = true
			}
		}
		if !changed {
			return nil
		}
		if err := c.PutJSON(ctx, basePath+"/"+resource.ID, map[string]any{
			"resource_ref": ref,
		}, &syncProjectResource{}); err != nil {
			return fmt.Errorf("update local directory resource: %w", err)
		}
		return nil
	}

	if err := c.PostJSON(ctx, basePath, map[string]any{
		"resource_type": "local_directory",
		"resource_ref":  desired,
		"label":         projectTitle,
	}, &syncProjectResource{}); err != nil {
		return fmt.Errorf("create local directory resource: %w", err)
	}
	return nil
}
