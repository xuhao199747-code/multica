package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

var projectWorkflowCmd = &cobra.Command{Use: "workflow", Short: "Run project workflow actions"}
var projectWorkflowConfirmCmd = &cobra.Command{Use: "confirm <project-id>", Short: "Freeze a confirmed PRD execution snapshot", Args: exactArgs(1), RunE: runProjectWorkflowConfirm}
var projectWorkflowDispatchCmd = &cobra.Command{Use: "dispatch <project-id>", Short: "Dispatch a workflow issue to a project agent", Args: exactArgs(1), RunE: runProjectWorkflowDispatch}
var projectWorkflowDeliveryCmd = &cobra.Command{Use: "delivery <project-id>", Short: "Submit code delivery evidence for review", Args: exactArgs(1), RunE: runProjectWorkflowDelivery}
var projectWorkflowReviewCmd = &cobra.Command{Use: "review <project-id>", Short: "Record a workflow review result", Args: exactArgs(1), RunE: runProjectWorkflowReview}
var projectWorkflowCompleteCmd = &cobra.Command{Use: "complete <project-id>", Short: "Record completed project delivery evidence", Args: exactArgs(1), RunE: runProjectWorkflowComplete}

func init() {
	projectCmd.AddCommand(projectWorkflowCmd)
	projectWorkflowCmd.AddCommand(projectWorkflowConfirmCmd, projectWorkflowDispatchCmd, projectWorkflowDeliveryCmd, projectWorkflowReviewCmd, projectWorkflowCompleteCmd)
	projectWorkflowConfirmCmd.Flags().String("source-chat", "", "Project PRD chat session ID")
	projectWorkflowConfirmCmd.Flags().String("source-agent", "", "PRD agent ID")
	projectWorkflowConfirmCmd.Flags().String("snapshot", "", "JSON file containing goal, scope, acceptance_criteria, and risks")
	projectWorkflowDispatchCmd.Flags().String("execution", "", "Execution ID")
	projectWorkflowDispatchCmd.Flags().String("agent", "", "Assigned project agent ID")
	projectWorkflowDispatchCmd.Flags().String("role", "", "development, review, or repair")
	projectWorkflowDispatchCmd.Flags().String("title", "", "Task title")
	projectWorkflowDeliveryCmd.Flags().String("execution", "", "Execution ID")
	projectWorkflowDeliveryCmd.Flags().String("issue", "", "Linked development or repair issue ID")
	projectWorkflowDeliveryCmd.Flags().String("commit", "", "Commit or pull request reference")
	projectWorkflowDeliveryCmd.Flags().String("tests", "", "Test evidence")
	projectWorkflowDeliveryCmd.Flags().String("summary", "", "Change summary")
	projectWorkflowReviewCmd.Flags().String("execution", "", "Execution ID")
	projectWorkflowReviewCmd.Flags().String("issue", "", "Linked review issue ID")
	projectWorkflowReviewCmd.Flags().String("outcome", "", "passed, needs_changes, or needs_member_decision")
	projectWorkflowCompleteCmd.Flags().String("execution", "", "Execution ID")
	projectWorkflowCompleteCmd.Flags().String("evidence", "", "JSON file containing delivery evidence")
}

func workflowProject(ctx context.Context, cmd *cobra.Command, input string) (*cli.APIClient, string, error) {
	c, err := newAPIClient(cmd)
	if err != nil {
		return nil, "", err
	}
	project, err := resolveProjectID(ctx, c, input)
	if err != nil {
		return nil, "", fmt.Errorf("resolve project: %w", err)
	}
	return c, project.ID, nil
}

func runProjectWorkflowConfirm(cmd *cobra.Command, args []string) error {
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	c, projectID, err := workflowProject(ctx, cmd, args[0])
	if err != nil {
		return err
	}
	sourceChat, _ := cmd.Flags().GetString("source-chat")
	sourceAgent, _ := cmd.Flags().GetString("source-agent")
	file, _ := cmd.Flags().GetString("snapshot")
	if sourceChat == "" || sourceAgent == "" || file == "" {
		return fmt.Errorf("--source-chat, --source-agent, and --snapshot are required")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	var result map[string]any
	if err := c.PostJSON(ctx, "/api/projects/"+projectID+"/workflow/executions", map[string]any{"source_chat_session_id": sourceChat, "source_agent_id": sourceAgent, "snapshot": snapshot}, &result); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runProjectWorkflowDispatch(cmd *cobra.Command, args []string) error {
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	c, projectID, err := workflowProject(ctx, cmd, args[0])
	if err != nil {
		return err
	}
	execution, _ := cmd.Flags().GetString("execution")
	agent, _ := cmd.Flags().GetString("agent")
	role, _ := cmd.Flags().GetString("role")
	title, _ := cmd.Flags().GetString("title")
	if execution == "" || agent == "" || role == "" || title == "" {
		return fmt.Errorf("--execution, --agent, --role, and --title are required")
	}
	var result map[string]any
	if err := c.PostJSON(ctx, "/api/projects/"+projectID+"/workflow/executions/dispatch", map[string]any{"execution_id": execution, "agent_id": agent, "role": role, "title": title}, &result); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runProjectWorkflowDelivery(cmd *cobra.Command, args []string) error {
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	c, projectID, err := workflowProject(ctx, cmd, args[0])
	if err != nil {
		return err
	}
	execution, _ := cmd.Flags().GetString("execution")
	issue, _ := cmd.Flags().GetString("issue")
	commit, _ := cmd.Flags().GetString("commit")
	tests, _ := cmd.Flags().GetString("tests")
	summary, _ := cmd.Flags().GetString("summary")
	if execution == "" || issue == "" || commit == "" || tests == "" || summary == "" {
		return fmt.Errorf("--execution, --issue, --commit, --tests, and --summary are required")
	}
	var result map[string]any
	if err := c.PostJSON(ctx, "/api/projects/"+projectID+"/workflow/executions/delivery", map[string]any{"execution_id": execution, "issue_id": issue, "commit": commit, "tests": tests, "summary": summary}, &result); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runProjectWorkflowReview(cmd *cobra.Command, args []string) error {
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	c, projectID, err := workflowProject(ctx, cmd, args[0])
	if err != nil {
		return err
	}
	execution, _ := cmd.Flags().GetString("execution")
	issue, _ := cmd.Flags().GetString("issue")
	outcome, _ := cmd.Flags().GetString("outcome")
	if execution == "" || issue == "" || outcome == "" {
		return fmt.Errorf("--execution, --issue, and --outcome are required")
	}
	var result map[string]any
	if err := c.PostJSON(ctx, "/api/projects/"+projectID+"/workflow/executions/review", map[string]any{"execution_id": execution, "issue_id": issue, "outcome": outcome}, &result); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runProjectWorkflowComplete(cmd *cobra.Command, args []string) error {
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	c, projectID, err := workflowProject(ctx, cmd, args[0])
	if err != nil {
		return err
	}
	execution, _ := cmd.Flags().GetString("execution")
	file, _ := cmd.Flags().GetString("evidence")
	if execution == "" || file == "" {
		return fmt.Errorf("--execution and --evidence are required")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read evidence: %w", err)
	}
	var evidence map[string]any
	if err := json.Unmarshal(data, &evidence); err != nil || len(evidence) == 0 {
		return fmt.Errorf("--evidence must contain a non-empty JSON object")
	}
	var result map[string]any
	if err := c.PostJSON(ctx, "/api/projects/"+projectID+"/workflow/executions/delivery/complete", map[string]any{"execution_id": execution, "evidence": evidence}, &result); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}
