//go:build agentintegration

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealReviewRepairSmoke checks real agent behavior in a disposable directory.
// It does not exercise the server dispatcher or modify a user repository.
func TestRealReviewRepairSmoke(t *testing.T) {
	requireRealAgentSmoke(t)
	kimiPath, err := exec.LookPath("kimi")
	if err != nil {
		t.Fatal("kimi executable unavailable")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal("reviewer executable unavailable")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "calculator.py")
	if err := os.WriteFile(file, []byte("def add(a, b):\n    return a - b\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test_calculator.py"), []byte("from calculator import add\nassert add(2, 3) == 5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worker, err := New("kimi", Config{ExecutablePath: kimiPath, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := New("codex", Config{ExecutablePath: codexPath, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	run := func(backend Backend, model, prompt string) Result {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		session, err := backend.Execute(ctx, prompt, ExecOptions{Cwd: dir, Model: model, Timeout: 3 * time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for range session.Messages {
			}
		}()
		result := <-session.Result
		if result.Status != "completed" {
			t.Fatalf("agent run ended: status=%s error=%s", result.Status, result.Error)
		}
		return result
	}
	check := func(result Result, expected string) string {
		t.Helper()
		var verdict struct {
			Verdict  string `json:"verdict"`
			Findings string `json:"findings"`
		}
		if err := json.Unmarshal([]byte(result.Output), &verdict); err != nil || verdict.Verdict != expected {
			t.Fatalf("review verdict = %q, expected %s (parse error: %v)", result.Output, expected, err)
		}
		return verdict.Findings
	}
	findings := check(run(reviewer, "gpt-5.6-luna", `Inspect calculator.py and test_calculator.py. Review the code and tests. Your final output must be exactly one JSON object with exactly two fields, for example {"verdict":"needs_changes","findings":"Describe the evidence and required changes here."}. verdict must be pass, needs_changes, or needs_decision. findings must be one JSON string, never an array or object. Do not include markdown or other text. Do not modify files.`), "needs_changes")
	if strings.TrimSpace(findings) == "" {
		t.Fatal("review supplied no findings")
	}
	run(worker, "kimi-code/kimi-for-coding", "Fix the review findings, run relevant tests, and report changes, test results and blockers. Do not merge or deploy. Findings: "+findings)
	output, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "return a - b") {
		t.Fatal("worker did not repair the original bug")
	}
	if output, err := exec.Command("python3", filepath.Join(dir, "test_calculator.py")).CombinedOutput(); err != nil {
		t.Fatalf("test still fails: %v %s", err, output)
	}
	check(run(reviewer, "gpt-5.6-luna", `Review the repair and its tests in this directory. Return exactly one JSON object with exactly two fields, for example {"verdict":"pass","findings":"Evidence and test results."}. verdict must be pass, needs_changes, or needs_decision. findings must be one JSON string, never an array or object. Do not include markdown or other text. Do not modify files.`), "pass")
}

// TestRealManualReviewReportSmoke checks the structured report contract with a
// real reviewer and disposable files, without contacting the user's workspace.
func TestRealManualReviewReportSmoke(t *testing.T) {
	requireRealAgentSmoke(t)
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "calculator.py"), []byte("def add(a, b):\n    return a - b\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reviewer, err := New("codex", Config{ExecutablePath: codexPath, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	prompt := `Review calculator.py for issue 01900000-0000-7000-8000-000000000001. It should add two numbers. Do not modify files. At the end of your reply include an HTML comment exactly in this format: <!-- multica-review {"reviews":[{"issue_id":"ISSUE_UUID","verdict":"needs_changes","findings":"specific issue"}]} -->. Use the actual issue UUID. Include only issues needing code changes.`
	session, err := reviewer.Execute(ctx, prompt, ExecOptions{Cwd: dir, Model: "gpt-5.6-luna", Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("review failed: %s %s", result.Status, result.Error)
	}
	start := strings.Index(result.Output, "<!-- multica-review ")
	if start < 0 {
		t.Fatalf("structured marker missing from reviewer output: %s", result.Output)
	}
	data := result.Output[start+len("<!-- multica-review "):]
	end := strings.Index(data, "-->")
	if end < 0 {
		t.Fatal("unterminated marker")
	}
	var report struct {
		Reviews []struct {
			IssueID  string `json:"issue_id"`
			Verdict  string `json:"verdict"`
			Findings string `json:"findings"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal([]byte(data[:end]), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Reviews) != 1 || report.Reviews[0].IssueID != "01900000-0000-7000-8000-000000000001" || report.Reviews[0].Verdict != "needs_changes" || strings.TrimSpace(report.Reviews[0].Findings) == "" {
		t.Fatalf("unexpected report: %+v", report)
	}
}
