package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon/localsync"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{Use: "sync", Short: "Mirror browser chat transcripts to a local folder"}
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
	activeProjects := make(map[string]bool, len(projects))
	for id, title := range projects {
		activeProjects[id] = true
		if err := e.EnsureProject(id, title); err != nil {
			return err
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
	return e.PruneProjects(activeProjects)
}
